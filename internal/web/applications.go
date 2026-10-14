package web

import (
	"encoding/json"
	"io"
	"net/http"

	"jobwatch/internal/store"
)

func (s *Server) handleAPIApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.Applications(r.Context(), r.URL.Query().Get("platform"), 0)
	if err != nil {
		httpError(w, "listing applications", err)
		return
	}
	writeJSON(w, map[string]any{"applications": apps})
}

func (s *Server) handleAPIApplicationsImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Applications []store.PlatformApplication `json:"applications"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 10<<20))
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid application JSON", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "expected one JSON object", http.StatusBadRequest)
		return
	}
	if len(req.Applications) == 0 || len(req.Applications) > 1000 {
		http.Error(w, "import 1 to 1000 applications", http.StatusBadRequest)
		return
	}
	for _, a := range req.Applications {
		if err := a.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := s.store.ImportApplications(r.Context(), req.Applications); err != nil {
		httpError(w, "importing applications", err)
		return
	}
	writeJSON(w, map[string]any{"imported": len(req.Applications)})
}

func (s *Server) handleAPIApplicationDrafts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform string `json:"platform"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil || req.Platform != "instahyre" {
		http.Error(w, "platform must be instahyre", http.StatusBadRequest)
		return
	}
	n, err := s.store.QueueApplicationDrafts(r.Context(), req.Platform)
	if err != nil {
		httpError(w, "queueing application drafts", err)
		return
	}
	writeJSON(w, map[string]any{"queued": n})
}
