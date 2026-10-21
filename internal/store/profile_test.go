package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jobwatch/internal/profile"
)

func testProfile(t *testing.T) profile.Profile {
	t.Helper()
	file, err := os.Open("../../examples/profile.example.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	snapshot, err := profile.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Profile
}

func TestProfilePersistenceAndRevisionGuards(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profile.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProfile(ctx); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("missing profile: %v", err)
	}
	p := testProfile(t)
	first, err := s.SaveProfile(ctx, p, 0)
	if err != nil || first.Revision != 1 || first.UpdatedAt.IsZero() {
		t.Fatalf("create: %+v %v", first, err)
	}
	if _, err := s.SaveProfile(ctx, p, 0); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("duplicate create: %v", err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loaded, err := s.GetProfile(ctx)
	if err != nil || loaded.Revision != 1 || loaded.Profile.Identity.Name != p.Identity.Name || !loaded.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("reopen: %+v %v", loaded, err)
	}
	p.Identity.Name = "Another Example"
	second, err := s.SaveProfile(ctx, p, loaded.Revision)
	if err != nil || second.Revision != 2 {
		t.Fatalf("update: %+v %v", second, err)
	}
	if _, err := s.SaveProfile(ctx, first.Profile, first.Revision); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("stale save: %v", err)
	}
	p.SchemaVersion = 999
	if _, err := s.SaveProfile(ctx, p, 2); err == nil {
		t.Fatal("invalid direct save accepted")
	}
	p.SchemaVersion = 1
	p.Outreach.Instructions = strings.Repeat("x", profile.MaxImportBytes)
	if _, err := s.SaveProfile(ctx, p, 2); err == nil {
		t.Fatal("oversized direct save accepted")
	}
	after, err := s.GetProfile(ctx)
	if err != nil || after.Revision != 2 || after.Profile.Identity.Name != "Another Example" {
		t.Fatalf("failed save changed stored profile: %+v %v", after, err)
	}
	// Export is an importable snapshot, including revision and timestamp.
	data, _ := json.Marshal(after)
	decoded, err := profile.Decode(strings.NewReader(string(data)))
	if err != nil || decoded.Revision != after.Revision || !decoded.UpdatedAt.Equal(after.UpdatedAt) {
		t.Fatalf("export round trip: %+v %v", decoded, err)
	}
}

func TestConcurrentProfileSavesAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, expected := range []int64{0, 1} {
		p := testProfile(t)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, st := range []*Store{first, second} {
			wg.Add(1)
			go func(st *Store) {
				defer wg.Done()
				<-start
				_, err := st.SaveProfile(context.Background(), p, expected)
				results <- err
			}(st)
		}
		close(start)
		wg.Wait()
		close(results)
		wins, conflicts := 0, 0
		for err := range results {
			if err == nil {
				wins++
			} else if errors.Is(err, ErrProfileConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if wins != 1 || conflicts != 1 {
			t.Fatalf("expected one winner and one conflict, got %d/%d", wins, conflicts)
		}
	}
}

func TestProfileMigrationLeavesJobDataAlone(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	job := sampleJob()
	tx, _ := s.BeginTx(ctx)
	id, err := s.InsertJob(ctx, tx, job, StatusApplied)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE candidate_profile`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	before := time.Now()
	if _, err := s.SaveProfile(ctx, testProfile(t), 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(ctx, id)
	if err != nil || got.Status != StatusApplied || got.Title != job.Title {
		t.Fatalf("profile migration changed job: %+v %v", got, err)
	}
	snapshot, _ := s.GetProfile(ctx)
	if snapshot.UpdatedAt.Before(before) {
		t.Fatal("write timestamp must be generated locally")
	}
}
