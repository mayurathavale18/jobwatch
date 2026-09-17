package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
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
	FitScore          *int64 `json:"fitScore"`
	WorkMode          string `json:"workMode"`
	EmailDraftID      string `json:"emailDraftId"`
	EmailTo           string `json:"emailTo"`
	EmailSentAt       string `json:"emailSentAt"`
}

func toAPIJob(j store.JobRow) apiJob {
	out := apiJob{
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
		WorkMode:          j.WorkMode,
		EmailDraftID:      j.EmailDraftID,
		EmailTo:           j.EmailTo,
		EmailSentAt:       j.EmailSentAt,
	}
	if j.FitScore.Valid {
		out.FitScore = &j.FitScore.Int64
	}
	return out
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

// handleAPICronRun queues one cron job's wrapper script to run now, outside
// its normal schedule. The event worker (jobwatch worker) runs it; progress
// shows up the same way the scheduled run would (logs/cron.log, the job's
// status.json) plus the event's own row in /api/events.
func (s *Server) handleAPICronRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := findCronJob(name); !ok {
		http.Error(w, "unknown cron job", http.StatusNotFound)
		return
	}
	id, err := s.enqueue(r.Context(), store.EventCronRun, map[string]any{"name": name})
	if err != nil {
		httpError(w, "queueing cron run", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{"triggered": true, "name": name, "eventId": id})
}

// enqueue writes an event for the worker process to pick up (within its
// poll interval, ~2s). Durable: survives a dashboard or worker restart.
func (s *Server) enqueue(ctx context.Context, kind string, payload any) (int64, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	return s.store.EnqueueEvent(ctx, kind, string(b))
}

type apiManualJobRequest struct {
	URL                 string `json:"url"`
	JDText              string `json:"jdText"`
	OutreachInstruction string `json:"outreachInstruction"`
}

type apiManualJobResponse struct {
	ID             int64  `json:"id"`
	AlreadyExisted bool   `json:"alreadyExisted"`
	Company        string `json:"company"`
	Title          string `json:"title"`
}

// handleAPIJobsManual inserts a job from an arbitrary URL (dashboard's
// "add job link" input) and queues a one-off tailoring run for it, so the
// tailored resume + verdict reaches Telegram in roughly 10-30s instead of
// waiting for the next tailor-resume cron tick. The HTTP response doesn't
// wait for tailoring; the result arrives via Telegram like every other job
// notification.
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
	req.OutreachInstruction = strings.TrimSpace(req.OutreachInstruction)

	id, existed, company, title, err := jobsubmit.InsertManualJob(ctx, s.store, req.URL, req.JDText, req.OutreachInstruction, s.pages)
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

	if _, err := s.enqueue(ctx, store.EventTailorOne, map[string]any{"job_id": id}); err != nil {
		httpError(w, "queueing tailor-one", err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, resp)
}

type apiOutreachRequest struct {
	FounderEmail string `json:"founderEmail"`
}

// handleAPIJobsOutreach queues a one-off founder-outreach attempt for an
// existing job from the dashboard. The HTTP response doesn't wait for the
// lookup/draft; the result surfaces via Telegram (run_outreach_step's own
// notification) or a later dashboard refresh of outreachStatus.
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

	if _, err := s.enqueue(ctx, store.EventOutreach, map[string]any{"job_id": id, "founder_email": req.FounderEmail}); err != nil {
		httpError(w, "queueing outreach", err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{"id": id})
}

type apiEvent struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Payload   string `json:"payload"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	Error     string `json:"error"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// handleAPIEvents lists the most recent queued/processed events.
func (s *Server) handleAPIEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.RecentEvents(r.Context(), 30)
	if err != nil {
		httpError(w, "listing events", err)
		return
	}
	out := make([]apiEvent, len(events))
	for i, e := range events {
		out[i] = apiEvent{e.ID, e.Kind, e.Payload, e.Status, e.Attempts, e.Error, e.CreatedAt, e.UpdatedAt}
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
