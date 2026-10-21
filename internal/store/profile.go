package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"jobwatch/internal/profile"
)

var ErrProfileNotFound = errors.New("no candidate profile stored; import one first")
var ErrProfileConflict = errors.New("profile revision conflict; export the latest profile before editing")

func (s *Store) GetProfile(ctx context.Context) (profile.Snapshot, error) {
	var snapshot profile.Snapshot
	var document, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT revision, document, updated_at FROM candidate_profile WHERE id = 1`).
		Scan(&snapshot.Revision, &document, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, ErrProfileNotFound
	}
	if err != nil {
		return snapshot, err
	}
	if err := json.Unmarshal([]byte(document), &snapshot.Profile); err != nil {
		return snapshot, fmt.Errorf("reading stored profile: %w", err)
	}
	if err := snapshot.Profile.Validate(); err != nil {
		return snapshot, fmt.Errorf("stored profile: %w", err)
	}
	snapshot.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	return snapshot, err
}

// SaveProfile is an atomic compare-and-swap across independent CLI/server processes.
// The existing row and its revision are untouched on invalid or stale writes.
func (s *Store) SaveProfile(ctx context.Context, p profile.Profile, expectedRevision int64) (profile.Snapshot, error) {
	var snapshot profile.Snapshot
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return snapshot, fmt.Errorf("invalid expected profile revision")
	}
	snapshot = profile.Snapshot{Profile: p, Revision: expectedRevision + 1, UpdatedAt: time.Now().UTC()}
	if err := snapshot.Validate(); err != nil {
		return profile.Snapshot{}, err
	}
	document, err := json.Marshal(p)
	if err != nil {
		return profile.Snapshot{}, err
	}
	var result sql.Result
	if expectedRevision == 0 {
		result, err = s.db.ExecContext(ctx, `INSERT INTO candidate_profile(id, revision, document, updated_at)
			VALUES (1, 1, ?, ?) ON CONFLICT(id) DO NOTHING`, string(document), snapshot.UpdatedAt.Format(time.RFC3339Nano))
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE candidate_profile SET document = ?, revision = revision + 1, updated_at = ?
			WHERE id = 1 AND revision = ?`, string(document), snapshot.UpdatedAt.Format(time.RFC3339Nano), expectedRevision)
	}
	if err != nil {
		return profile.Snapshot{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return profile.Snapshot{}, err
	}
	if changed != 1 {
		return profile.Snapshot{}, ErrProfileConflict
	}
	return snapshot, nil
}
