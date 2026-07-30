package web

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"jobwatch/internal/jobsubmit"
	"jobwatch/internal/store"
)

type apiJob struct {
	ID                int64  `json:"id"`
	CompanySlug       string `json:"companySlug"`
	CompanyName       string `json:"companyName"`
	Title             string `json:"title"`
	Location          string `json:"location"`
	URL               string `json:"url"`
	PostedAt          string `json:"postedAt"`
	FirstSeenAt       string `json:"firstSeenAt"`
	Status            string `json:"status"`
	Notes             string `json:"notes"`
	OutreachStatus    string `json:"outreachStatus"`
	FounderName       string `json:"founderName"`
	FounderEmail      string `json:"founderEmail"`
	OutreachDraftedAt string `json:"outreachDraftedAt"`
}

func toAPIJob(j store.JobRow) apiJob {
	return apiJob{
		ID:                j.ID,
		CompanySlug:       j.CompanySlug,
		CompanyName:       j.CompanyName,
		Title:             j.Title,
		Location:          j.Location,
		URL:               j.URL,
		PostedAt:          j.PostedAt.String,
		FirstSeenAt:       j.FirstSeenAt,
		Status:            j.Status,
		Notes:             j.Notes,
		OutreachStatus:    j.OutreachStatus,
		FounderName:       j.FounderName,
		FounderEmail:      j.FounderEmail,
		OutreachDraftedAt: j.OutreachDraftedAt,
	}
}

type apiPollRun struct {
	FinishedAt      string `json:"finishedAt"`
	CompaniesOK     int    `json:"companiesOK"`
	CompaniesFailed int    `json:"companiesFailed"`
	NewJobs         int    `json:"newJobs"`
	Errors          string `json:"errors"`
}

type apiJobsResponse struct {
	Jobs          []apiJob       `json:"jobs"`
	Companies     []string       `json:"companies"`
	Providers     []string       `json:"providers"`
	StatusCounts  map[string]int `json:"statusCounts"`
	TotalJobs     int            `json:"totalJobs"`
	LastPoll      *apiPollRun    `json:"lastPoll"`
	Statuses      []string       `json:"statuses"`
	Page          int            `json:"page"`
	PageSize      int            `json:"pageSize"`
	TotalFiltered int            `json:"totalFiltered"`
}

const jobsPageSize = 25

func (s *Server) handleAPIJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}

	filter := store.JobFilter{
		Provider: q.Get("provider"),
		Company:  q.Get("company"),
		Search:   q.Get("q"),
		Limit:    jobsPageSize,
		Offset:   (page - 1) * jobsPageSize,
	}
	if statusParam := q.Get("status"); statusParam != "" {
		filter.Statuses = strings.Split(statusParam, ",")
	}
	if days, err := strconv.Atoi(q.Get("days")); err == nil && days > 0 {
		filter.Since = time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
	}

	jobs, err := s.store.ListJobs(ctx, filter)
	if err != nil {
		httpError(w, "listing jobs", err)
		return
	}

	totalFiltered, err := s.store.CountJobs(ctx, filter)
	if err != nil {
		httpError(w, "counting filtered jobs", err)
		return
	}

	companies, err := s.store.CompanyNames(ctx)
	if err != nil {
		httpError(w, "listing companies", err)
		return
	}

	providersList, err := s.store.Providers(ctx)
	if err != nil {
		httpError(w, "listing providers", err)
		return
	}

	counts, err := s.store.StatusCounts(ctx)
	if err != nil {
		httpError(w, "counting statuses", err)
		return
	}

	total := 0
	for _, n := range counts {
		total += n
	}

	lastPoll, err := s.store.LastPollRun(ctx)
	if err != nil {
		httpError(w, "loading last poll run", err)
		return
	}

	apiJobs := make([]apiJob, len(jobs))
	for i, j := range jobs {
		apiJobs[i] = toAPIJob(j)
	}

	var lp *apiPollRun
	if lastPoll != nil {
		lp = &apiPollRun{
			FinishedAt:      lastPoll.FinishedAt.String,
			CompaniesOK:     lastPoll.CompaniesOK,
			CompaniesFailed: lastPoll.CompaniesFailed,
			NewJobs:         lastPoll.NewJobs,
			Errors:          lastPoll.Errors,
		}
	}

	writeJSON(w, apiJobsResponse{
		Jobs:          apiJobs,
		Companies:     companies,
		Providers:     providersList,
		StatusCounts:  counts,
		TotalJobs:     total,
		LastPoll:      lp,
		Statuses:      store.ValidStatuses,
		Page:          page,
		PageSize:      jobsPageSize,
		TotalFiltered: totalFiltered,
	})
}

type patchJobRequest struct {
	Status *string `json:"status"`
	Notes  *string `json:"notes"`
}

func (s *Server) handleAPIPatchJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	var req patchJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}

	if req.Status != nil {
		if !store.IsValidStatus(*req.Status) {
			http.Error(w, "invalid status", http.StatusBadRequest)
			return
		}
		if err := s.store.UpdateStatus(ctx, id, *req.Status); err != nil {
			httpError(w, "updating status", err)
			return
		}
	}

	if req.Notes != nil {
		if err := s.store.UpdateNotes(ctx, id, *req.Notes); err != nil {
			httpError(w, "updating notes", err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleAPICron(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.loadCronStatuses())
}

// handleAPICronRun triggers one cron job's wrapper script immediately,
// outside its normal schedule (e.g. tg-sync outside its 07:00-24:00 IST
// window). The script runs detached -- this only waits long enough to
// confirm it started, since some jobs (tailor-resume, weekly-backup) can
// take minutes. Progress shows up the same way the cron-scheduled run
// would: logs/cron.log and the job's own status.json.
func (s *Server) handleAPICronRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	job, ok := findCronJob(name)
	if !ok {
		http.Error(w, "unknown cron job", http.StatusNotFound)
		return
	}

	logFile, err := os.OpenFile(filepath.Join(s.logsDir, "cron.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		httpError(w, "opening cron.log", err)
		return
	}

	cmd := exec.Command("bash", job.Script)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		httpError(w, "starting cron job", err)
		return
	}

	go func() {
		defer logFile.Close()
		if err := cmd.Wait(); err != nil {
			slog.Error("manually triggered cron job exited non-zero", "job", name, "error", err)
		}
	}()

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{"triggered": true, "name": name})
}

type apiManualJobRequest struct {
	URL    string `json:"url"`
	JDText string `json:"jdText"`
}

type apiManualJobResponse struct {
	ID             int64  `json:"id"`
	AlreadyExisted bool   `json:"alreadyExisted"`
	Company        string `json:"company"`
	Title          string `json:"title"`
}

// tailorOneScript is the wrapper spawned for a single freshly-submitted
// job, relative to the process's cwd -- same convention as cronJobDefs'
// Script paths (see cron.go), always run from the repo root.
const tailorOneScript = "scripts/tailor-one.sh"

// handleAPIJobsManual inserts a job from an arbitrary URL (dashboard's
// "add job link" input) and spawns a detached one-off tailoring run for
// it, so the tailored resume + verdict reaches Telegram in roughly
// 10-30s instead of waiting for the next tailor-resume cron tick. Mirrors
// handleAPICronRun's detached-exec pattern: the HTTP response doesn't
// wait for tailoring to finish, since the result arrives via Telegram the
// same way every other job notification already does.
func (s *Server) handleAPIJobsManual(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req apiManualJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" || (!strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://")) {
		http.Error(w, "url must be a non-empty http(s) URL", http.StatusBadRequest)
		return
	}
	req.JDText = strings.TrimSpace(req.JDText)

	id, existed, company, title, err := jobsubmit.InsertManualJob(ctx, s.store, req.URL, req.JDText, s.pages)
	if err != nil {
		httpError(w, "inserting manual job", err)
		return
	}

	resp := apiManualJobResponse{ID: id, AlreadyExisted: existed, Company: company, Title: title}

	if existed {
		w.WriteHeader(http.StatusOK)
		writeJSON(w, resp)
		return
	}

	logFile, err := os.OpenFile(filepath.Join(s.logsDir, "cron.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		httpError(w, "opening cron.log", err)
		return
	}

	cmd := exec.Command("bash", tailorOneScript, strconv.FormatInt(id, 10))
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		httpError(w, "starting tailor-one", err)
		return
	}

	go func() {
		defer logFile.Close()
		if err := cmd.Wait(); err != nil {
			slog.Error("tailor-one exited non-zero", "job_id", id, "error", err)
		}
	}()

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, resp)
}

// outreachOneScript is the wrapper spawned for a manual outreach trigger
// (dashboard button or Telegram "outreach" reply), relative to the
// process's cwd -- same convention as tailorOneScript.
const outreachOneScript = "scripts/outreach-one.sh"

type apiOutreachRequest struct {
	FounderEmail string `json:"founderEmail"`
}

// handleAPIJobsOutreach triggers a one-off founder-outreach attempt for
// an existing job from the dashboard. Mirrors handleAPIJobsManual's
// detached-exec pattern -- the HTTP response doesn't wait for the
// lookup/draft to finish; the result surfaces via Telegram (see
// run_outreach_step's own notification) or a later dashboard refresh of
// outreachStatus.
func (s *Server) handleAPIJobsOutreach(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	var req apiOutreachRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req) // optional body; malformed/empty is fine, just no override
	}

	if _, err := s.store.GetJob(ctx, id); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		httpError(w, "loading job", err)
		return
	}

	logFile, err := os.OpenFile(filepath.Join(s.logsDir, "cron.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		httpError(w, "opening cron.log", err)
		return
	}

	args := []string{outreachOneScript, strconv.FormatInt(id, 10)}
	if req.FounderEmail != "" {
		args = append(args, req.FounderEmail)
	}
	cmd := exec.Command("bash", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		httpError(w, "starting outreach-one", err)
		return
	}

	go func() {
		defer logFile.Close()
		if err := cmd.Wait(); err != nil {
			slog.Error("outreach-one exited non-zero", "job_id", id, "error", err)
		}
	}()

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{"id": id})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
