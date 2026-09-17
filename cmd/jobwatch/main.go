// Command jobwatch polls job-board APIs, tracks new postings in SQLite,
// notifies over Telegram, and serves a local dashboard.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"jobwatch/internal/config"
	"jobwatch/internal/eventq"
	"jobwatch/internal/notify"
	"jobwatch/internal/poller"
	"jobwatch/internal/providers"
	"jobwatch/internal/store"
	"jobwatch/internal/tgsync"
	"jobwatch/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "poll":
		err = runPoll(args, false)
	case "backfill":
		err = runPoll(args, true)
	case "serve":
		err = runServe(args)
	case "test-notify":
		err = runTestNotify(args)
	case "tg-sync":
		err = runTgSync(args)
	case "refilter":
		err = runRefilter(args)
	case "worker":
		err = runWorker(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `jobwatch — job-opening tracker

Usage:
  jobwatch poll        [-config config.yaml]   run one polling cycle, notify on new matches, then exit
  jobwatch backfill    [-config config.yaml]   poll and store everything as seen, without notifying
  jobwatch serve       [-config config.yaml]   run the dashboard web server
  jobwatch test-notify [-config config.yaml]   send a test Telegram message and exit
  jobwatch worker      [-config config.yaml]   long-poll Telegram and run queued events (replies + dashboard actions) until stopped
  jobwatch tg-sync     [-config config.yaml]   one-shot Telegram drain for local dev; never run while a worker is running
  jobwatch refilter    [-config config.yaml]   re-apply current filters to status=new jobs, marking failures ignored`)
}

func loadConfigFlag(fs *flag.FlagSet, args []string) (*config.Config, error) {
	path := fs.String("config", "config.yaml", "path to config.yaml")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return config.Load(*path)
}

func runPoll(args []string, backfill bool) error {
	name := "poll"
	if backfill {
		name = "backfill"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	cfg, err := loadConfigFlag(fs, args)
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()

	var notifier poller.Notifier
	if !backfill {
		tg, err := notify.New(cfg.BotToken(), cfg.ChatID())
		if err != nil {
			slog.Warn("telegram notifications disabled", "reason", err.Error())
		} else {
			notifier = tg
		}
	}

	p := poller.New(st, cfg, notifier)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	start := time.Now()
	result, err := p.Run(ctx, backfill)
	if err != nil {
		return fmt.Errorf("poll run: %w", err)
	}

	slog.Info("poll cycle complete",
		"mode", name,
		"duration", time.Since(start).String(),
		"companies_ok", result.CompaniesOK,
		"companies_failed", result.CompaniesFailed,
		"new_jobs", result.NewJobs,
		"errors", result.Errors,
	)
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfg, err := loadConfigFlag(fs, args)
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return web.Serve(ctx, cfg.Dashboard.Addr, st)
}

// runRefilter re-applies the current config filters to every job still in
// status=new. Filters are only evaluated at insert time, so without this a
// filter fix leaves stale matches queued for (budget-limited) tailoring.
func runRefilter(args []string) error {
	fs := flag.NewFlagSet("refilter", flag.ExitOnError)
	cfg, err := loadConfigFlag(fs, args)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()

	ctx := context.Background()
	jobs, err := st.ListJobs(ctx, store.JobFilter{Statuses: []string{"new"}})
	if err != nil {
		return err
	}
	ignored := 0
	for _, j := range jobs {
		if poller.Passes(providers.Job{Title: j.Title, Location: j.Location}, cfg.Filters) {
			continue
		}
		if err := st.UpdateStatus(ctx, j.ID, "ignored"); err != nil {
			return err
		}
		ignored++
	}
	slog.Info("refilter complete", "checked", len(jobs), "ignored", ignored)
	return nil
}

func runTestNotify(args []string) error {
	fs := flag.NewFlagSet("test-notify", flag.ExitOnError)
	cfg, err := loadConfigFlag(fs, args)
	if err != nil {
		return err
	}

	tg, err := notify.New(cfg.BotToken(), cfg.ChatID())
	if err != nil {
		return fmt.Errorf("telegram not configured: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	msg := "<b>jobwatch</b>\nTest notification — if you see this, Telegram is wired up correctly."
	if err := tg.Send(ctx, msg); err != nil {
		return fmt.Errorf("sending test message: %w", err)
	}

	slog.Info("test notification sent")
	return nil
}

func runTgSync(args []string) error {
	fs := flag.NewFlagSet("tg-sync", flag.ExitOnError)
	fixScript := fs.String("fix-script", "scripts/resume-fix.sh",
		"path to the script invoked when a user replies \"fix\" to a job notification (empty disables the feature)")
	outreachScript := fs.String("outreach-script", "scripts/outreach-one.sh",
		"path to the script invoked when a user replies \"outreach\" to a job notification (empty disables the feature)")
	cfg, err := loadConfigFlag(fs, args)
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()

	tg, err := notify.New(cfg.BotToken(), cfg.ChatID())
	if err != nil {
		return fmt.Errorf("telegram not configured: %w", err)
	}

	chatID, err := strconv.ParseInt(cfg.ChatID(), 10, 64)
	if err != nil {
		return fmt.Errorf("telegram chat id %q is not numeric: %w", cfg.ChatID(), err)
	}

	syncer := tgsync.New(st, tg, chatID, *fixScript, *outreachScript)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	result, err := syncer.Run(ctx)
	if err != nil {
		return fmt.Errorf("tg-sync: %w", err)
	}

	slog.Info("tg-sync complete",
		"processed", result.Processed,
		"status_changes", result.StatusChanges,
		"errors", result.Errors,
	)
	return nil
}

// runWorker is the long-lived background process: it long-polls Telegram
// (replies land in about a second instead of on a 5-minute cron) and runs
// every queued event -- those Telegram updates plus dashboard-triggered
// tailor-one / outreach / cron run-now actions. It is the bot's only
// getUpdates consumer, so exactly one worker may run per bot.
func runWorker(args []string) error {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	fixScript := fs.String("fix-script", "scripts/resume-fix.sh", "script run for a \"fix\"/\"update\" reply")
	outreachScript := fs.String("outreach-script", "scripts/outreach-one.sh", "script run for an \"outreach\" reply or dashboard outreach")
	tailorOneScript := fs.String("tailor-one-script", "scripts/tailor-one.sh", "script run for a dashboard manual job submit")
	logsDir := fs.String("logs-dir", "logs", "directory for cron.log (script output)")
	concurrency := fs.Int("concurrency", 4, "events processed at once")
	cfg, err := loadConfigFlag(fs, args)
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	runScript := func(ctx context.Context, script string, scriptArgs ...string) error {
		return runLoggedScript(ctx, *logsDir, script, scriptArgs...)
	}

	w := &eventq.Worker{
		Store:        st,
		Concurrency:  *concurrency,
		PollInterval: 2 * time.Second,
		MaxAttempts:  3,
		Handlers: map[string]eventq.Handler{
			store.EventTailorOne: func(ctx context.Context, payload string) error {
				var p struct {
					JobID int64 `json:"job_id"`
				}
				if err := json.Unmarshal([]byte(payload), &p); err != nil {
					return err
				}
				return runScript(ctx, *tailorOneScript, strconv.FormatInt(p.JobID, 10))
			},
			store.EventOutreach: func(ctx context.Context, payload string) error {
				var p struct {
					JobID        int64  `json:"job_id"`
					FounderEmail string `json:"founder_email"`
				}
				if err := json.Unmarshal([]byte(payload), &p); err != nil {
					return err
				}
				a := []string{strconv.FormatInt(p.JobID, 10)}
				if p.FounderEmail != "" {
					a = append(a, p.FounderEmail)
				}
				return runScript(ctx, *outreachScript, a...)
			},
			store.EventCronRun: func(ctx context.Context, payload string) error {
				var p struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal([]byte(payload), &p); err != nil {
					return err
				}
				script, ok := web.CronScript(p.Name)
				if !ok {
					return fmt.Errorf("unknown cron job %q", p.Name)
				}
				return runScript(ctx, script)
			},
		},
	}

	tg, tgErr := notify.New(cfg.BotToken(), cfg.ChatID())
	chatID, idErr := strconv.ParseInt(cfg.ChatID(), 10, 64)
	if tgErr != nil || idErr != nil {
		// Still useful without Telegram: dashboard-queued events run.
		slog.Warn("worker: telegram not configured, listener disabled", "error", errors.Join(tgErr, idErr))
		return w.Run(ctx)
	}
	tg.LongPollSeconds = 50
	tg.Client = &http.Client{Timeout: 70 * time.Second}

	syncer := tgsync.New(st, tg, chatID, *fixScript, *outreachScript)
	w.Handlers[store.EventTelegramUpdate] = syncer.HandleEvent

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- syncer.Listen(ctx, w.Wake)
	}()

	slog.Info("worker started", "concurrency", *concurrency)
	if err := w.Run(ctx); err != nil {
		return err
	}
	return <-listenErr
}

// runLoggedScript runs a wrapper script, appending its output to
// logs/cron.log (same place the old detached dashboard spawns logged), and
// returns an error carrying the output's last line on failure.
// ponytail: 20-minute cap covers tailor-one's 5-minute lock wait plus a run;
// weekly-backup has no cap of its own, raise this if it ever gets killed.
func runLoggedScript(ctx context.Context, logsDir, script string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "bash", append([]string{script}, args...)...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()

	if err := os.MkdirAll(logsDir, 0o755); err == nil {
		if f, err := os.OpenFile(filepath.Join(logsDir, "cron.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.Write(out.Bytes())
			_ = f.Close()
		}
	}

	if runErr != nil {
		last := strings.TrimSpace(out.String())
		if i := strings.LastIndexByte(last, '\n'); i >= 0 {
			last = last[i+1:]
		}
		return fmt.Errorf("%s: %w: %s", filepath.Base(script), runErr, last)
	}
	return nil
}
