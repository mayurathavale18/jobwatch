package web

import (
	"errors"
	"mime"
	"net/http"

	"jobwatch/internal/profile"
	"jobwatch/internal/store"
)

func (s *Server) handleAPIProfileGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	snapshot, err := s.store.GetProfile(r.Context())
	if errors.Is(err, store.ErrProfileNotFound) {
		http.Error(w, "No profile saved yet.", http.StatusNotFound)
		return
	}
	if err != nil {
		// Stored profile errors can include candidate data. Keep them out of logs.
		http.Error(w, "Could not load the profile. Try again.", http.StatusInternalServerError)
		return
	}
	writeJSON(w, snapshot)
}

func (s *Server) handleAPIProfileSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Expected application/json.", http.StatusUnsupportedMediaType)
		return
	}
	// The CLI and dashboard use the same strict decoder and semantic validation.
	snapshot, err := profile.Decode(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	saved, err := s.store.SaveProfile(r.Context(), snapshot.Profile, snapshot.Revision)
	if errors.Is(err, store.ErrProfileConflict) {
		http.Error(w, "The profile changed since you loaded it. Your edits have not been saved. Download your edits before loading the latest profile.", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Could not save the profile. Your edits are still here; try again.", http.StatusInternalServerError)
		return
	}
	writeJSON(w, saved)
}
