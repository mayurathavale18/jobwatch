// Command jobwatch polls job-board APIs, tracks new postings in SQLite,
// notifies over Telegram, and serves a local dashboard.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"jobwatch/internal/config"
	"jobwatch/internal/notify"
	"jobwatch/internal/poller"
	"jobwatch/internal/store"
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
  jobwatch test-notify [-config config.yaml]   send a test Telegram message and exit`)
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
