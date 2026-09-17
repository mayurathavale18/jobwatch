package web

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"jobwatch/internal/store"
)

// parseLinkedInConnectionsCSV parses the "Connections.csv" from a LinkedIn
// data export. The file starts with a "Notes:" preamble of variable length
// before the real header row (First Name,Last Name,URL,Email Address,
// Company,Position,Connected On), so scan forward to the header first.
func parseLinkedInConnectionsCSV(data []byte) ([]store.Connection, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.ToLower(l), "first name,") {
			start = i
			break
		}
	}
	if start == -1 {
		return nil, nil
	}

	r := csv.NewReader(strings.NewReader(strings.Join(lines[start:], "\n")))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	get := func(rec []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	var out []store.Connection
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		c := store.Connection{
			FirstName:   get(rec, "first name"),
			LastName:    get(rec, "last name"),
			Email:       get(rec, "email address"),
			Company:     get(rec, "company"),
			Position:    get(rec, "position"),
			LinkedInURL: get(rec, "url"),
		}
		if c.FirstName == "" && c.LastName == "" {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// handleAPIConnectionsUpload replaces the imported LinkedIn connections
// with the posted Connections.csv (raw CSV body).
func (s *Server) handleAPIConnectionsUpload(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 20<<20))
	if err != nil {
		httpError(w, "reading upload", err)
		return
	}
	conns, err := parseLinkedInConnectionsCSV(body)
	if err != nil {
		http.Error(w, "could not parse CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(conns) == 0 {
		http.Error(w, "no connections found — is this a LinkedIn Connections.csv export?", http.StatusBadRequest)
		return
	}
	if err := s.store.ReplaceConnections(r.Context(), conns); err != nil {
		httpError(w, "storing connections", err)
		return
	}
	writeJSON(w, map[string]any{"imported": len(conns)})
}

func (s *Server) handleAPIConnections(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountConnections(r.Context())
	if err != nil {
		httpError(w, "counting connections", err)
		return
	}
	writeJSON(w, map[string]any{"count": n})
}

type apiConnection struct {
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	Email       string `json:"email"`
	Company     string `json:"company"`
	Position    string `json:"position"`
	LinkedInURL string `json:"linkedinUrl"`
}

// handleAPIJobReferrals lists imported connections whose company matches
// the job's company -- potential referral routes.
func (s *Server) handleAPIJobReferrals(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}
	job, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		httpError(w, "loading job", err)
		return
	}
	conns, err := s.store.MatchConnections(r.Context(), job.CompanyName)
	if err != nil {
		httpError(w, "matching connections", err)
		return
	}
	out := make([]apiConnection, len(conns))
	for i, c := range conns {
		out[i] = apiConnection{
			FirstName: c.FirstName, LastName: c.LastName, Email: c.Email,
			Company: c.Company, Position: c.Position, LinkedInURL: c.LinkedInURL,
		}
	}
	writeJSON(w, map[string]any{"referrals": out})
}

// handleAPIJobGet returns one job -- the email compose page loads by id.
func (s *Server) handleAPIJobGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}
	job, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		httpError(w, "loading job", err)
		return
	}
	writeJSON(w, toAPIJob(job))
}

// emailToolScript mirrors tailorOneScript's repo-root-relative convention.
const emailToolScript = "scripts/email_tool.py"

type apiEmailRequest struct {
	Action      string `json:"action"` // generate | revise | save_draft | send
	To          string `json:"to"`
	ToName      string `json:"toName"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	Instruction string `json:"instruction"`
	DraftID     string `json:"draftId"`
}

// handleAPIJobEmail proxies the compose UI's actions to email_tool.py,
// synchronously -- unlike the fire-and-forget outreach flow, the UI is
// waiting for the generated/revised text or the draft id. LLM actions can
// take ~45s, hence the long exec timeout.
func (s *Server) handleAPIJobEmail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}
	job, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		httpError(w, "loading job", err)
		return
	}

	var req apiEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	switch req.Action {
	case "generate", "revise", "save_draft", "send":
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"action":      req.Action,
		"job_id":      id,
		"to":          req.To,
		"to_name":     req.ToName,
		"subject":     req.Subject,
		"body":        req.Body,
		"instruction": req.Instruction,
		"draft_id":    req.DraftID,
		"company":     job.CompanyName,
		"title":       job.Title,
	})

	cmd := exec.Command("python3", emailToolScript)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		httpError(w, "starting email tool", err)
		return
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(90 * time.Second):
		_ = cmd.Process.Kill()
		http.Error(w, "email tool timed out", http.StatusGatewayTimeout)
		return
	}
	if err != nil {
		httpError(w, "email tool failed: "+stderr.String(), err)
		return
	}

	var resp map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		httpError(w, "email tool returned unparseable output", err)
		return
	}
	writeJSON(w, resp)
}
