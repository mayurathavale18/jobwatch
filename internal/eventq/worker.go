// Package eventq runs handlers for events queued in the store's events
// table: Telegram updates enqueued by the long-poll listener, and actions
// the dashboard enqueues (tailor-one, outreach, cron run-now).
package eventq

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"jobwatch/internal/store"
)

// Handler processes one event's payload. A returned error marks the event
// failed; there is no automatic retry, since handlers already report
// user-facing failures themselves (e.g. a "Fix failed" Telegram reply) and
// a blind retry would repeat those side effects.
type Handler func(ctx context.Context, payload string) error

type Worker struct {
	Store    *store.Store
	Handlers map[string]Handler

	// Concurrency is how many events run at once, so a 90s resume fix
	// doesn't hold up an instant status reply behind it.
	// ponytail: one shared pool; long cron run-nows can occupy slots.
	// Split into per-kind lanes if Telegram replies ever queue behind them.
	Concurrency int

	// PollInterval bounds pickup latency for events enqueued by other
	// processes (the dashboard pod); in-process enqueues call Wake instead.
	PollInterval time.Duration

	// MaxAttempts caps how many times an event interrupted by a worker
	// restart is re-queued before it's failed.
	MaxAttempts int

	wake chan struct{}
	once sync.Once
}

func (w *Worker) init() {
	w.once.Do(func() { w.wake = make(chan struct{}, 1) })
}

// Wake nudges idle workers to check the queue now. Never blocks.
func (w *Worker) Wake() {
	w.init()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run recovers interrupted events, then processes the queue until ctx is
// cancelled. In-flight handlers run on a context that is NOT cancelled with
// ctx, so a deploy's SIGTERM lets them finish (within the pod's grace
// period) instead of killing a half-done resume rebuild; if the process is
// killed anyway, the event stays running and is recovered on next start.
func (w *Worker) Run(ctx context.Context) error {
	w.init()
	requeued, failed, err := w.Store.RecoverRunningEvents(ctx, w.MaxAttempts)
	if err != nil {
		return fmt.Errorf("recovering running events: %w", err)
	}
	if requeued > 0 || failed > 0 {
		slog.Warn("eventq: recovered interrupted events", "requeued", requeued, "failed", failed)
	}

	handlerCtx := context.WithoutCancel(ctx)
	var wg sync.WaitGroup
	for i := 0; i < w.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx, handlerCtx)
		}()
	}
	wg.Wait()
	return nil
}

func (w *Worker) loop(ctx, handlerCtx context.Context) {
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		ev, err := w.Store.ClaimEvent(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("eventq: claiming event", "error", err)
			}
		}
		if ev == nil {
			select {
			case <-ctx.Done():
				return
			case <-w.wake:
			case <-ticker.C:
			}
			continue
		}
		w.handle(handlerCtx, ev)
		// Another event may be waiting; let a sibling worker pick it up too.
		w.Wake()
	}
}

func (w *Worker) handle(ctx context.Context, ev *store.Event) {
	start := time.Now()
	errMsg := ""
	func() {
		defer func() {
			if r := recover(); r != nil {
				errMsg = fmt.Sprintf("panic: %v", r)
			}
		}()
		h, ok := w.Handlers[ev.Kind]
		if !ok {
			errMsg = "no handler for kind " + ev.Kind
			return
		}
		if err := h(ctx, ev.Payload); err != nil {
			errMsg = err.Error()
		}
	}()

	if err := w.Store.FinishEvent(ctx, ev.ID, errMsg); err != nil {
		slog.Error("eventq: finishing event", "id", ev.ID, "error", err)
	}
	if errMsg != "" {
		slog.Error("eventq: event failed", "id", ev.ID, "kind", ev.Kind, "error", errMsg, "duration", time.Since(start))
	} else {
		slog.Info("eventq: event done", "id", ev.ID, "kind", ev.Kind, "duration", time.Since(start))
	}
}
