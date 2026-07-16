package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// cronJob describes one cron-scheduled wrapper this dashboard reports on.
// Schedule/Interval are descriptive metadata matching the crontab installed
// alongside these scripts (see scripts/*-wrapper.sh) -- they aren't read
// from `crontab -l` live, since that would mean shelling out from the web
// server for a purely informational page.
type cronJob struct {
	Name     string
	Schedule string
	Interval time.Duration // expected time between runs, used to flag staleness
}

var cronJobDefs = []cronJob{
	{"poll", "every 15 min", 15 * time.Minute},
	{"tg-sync", "every 5 min, 07:00–24:00 IST", 5 * time.Minute},
	{"tailor-resume", "every 30 min", 30 * time.Minute},
	{"dashboard-watchdog", "every 10 min", 10 * time.Minute},
	{"daily-summary", "23:50 IST daily", 24 * time.Hour},
	{"weekly-backup", "Sunday 02:00 IST", 7 * 24 * time.Hour},
}

// CronJobStatus is one job's status as shown on the Cron tab.
type CronJobStatus struct {
	Name     string
	Schedule string
	HasRun   bool
	LastRun  time.Time
	Status   string // OK, FAIL, ALERT, SKIP
	Detail   string
	Stale    bool // last run is overdue by more than 3x its expected interval
}

type statusFile struct {
	LastRun string `json:"last_run"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
}

func (s *Server) loadCronStatuses() []CronJobStatus {
	out := make([]CronJobStatus, 0, len(cronJobDefs))
	now := time.Now()

	for _, job := range cronJobDefs {
		st := CronJobStatus{Name: job.Name, Schedule: job.Schedule}

		path := filepath.Join(s.logsDir, job.Name+".status.json")
		data, err := os.ReadFile(path)
		if err != nil {
			out = append(out, st) // never run (or logs dir not found) -- HasRun stays false
			continue
		}

		var parsed statusFile
		if err := json.Unmarshal(data, &parsed); err != nil {
			out = append(out, st)
			continue
		}

		st.Status = parsed.Status
		st.Detail = parsed.Detail
		if t, err := time.Parse(time.RFC3339, parsed.LastRun); err == nil {
			st.LastRun = t
			st.HasRun = true
			st.Stale = job.Interval > 0 && now.Sub(t) > 3*job.Interval
		}

		out = append(out, st)
	}

	return out
}

type cronData struct {
	Jobs []CronJobStatus
}

func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	data := cronData{Jobs: s.loadCronStatuses()}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "cron.html", data); err != nil {
		httpError(w, "rendering cron template", err)
	}
}
