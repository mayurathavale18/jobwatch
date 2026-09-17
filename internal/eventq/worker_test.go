package eventq

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"jobwatch/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func waitForStatuses(t *testing.T, st *store.Store, want map[int64]string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		events, err := st.RecentEvents(context.Background(), 50)
		if err != nil {
			t.Fatal(err)
		}
		got := map[int64]string{}
		for _, e := range events {
			got[e.ID] = e.Status
		}
		ok := true
		for id, s := range want {
			if got[id] != s {
				ok = false
			}
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("statuses = %v, want %v", got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWorkerRunsHandlersConcurrentlyAndRecordsOutcomes(t *testing.T) {
	st := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slowStarted := make(chan struct{})
	release := make(chan struct{})
	w := &Worker{
		Store:        st,
		Concurrency:  2,
		PollInterval: time.Hour, // only Wake/initial claim may drive pickup
		MaxAttempts:  3,
		Handlers: map[string]Handler{
			"slow": func(ctx context.Context, payload string) error {
				close(slowStarted)
				<-release
				return nil
			},
			"fast": func(ctx context.Context, payload string) error { return nil },
			"bad":  func(ctx context.Context, payload string) error { return errors.New("nope") },
			"panicky": func(ctx context.Context, payload string) error {
				panic("kaboom")
			},
		},
	}

	slow, _ := st.EnqueueEvent(ctx, "slow", "")
	go w.Run(ctx)
	<-slowStarted

	// While "slow" holds one slot, the other worker must still process new
	// events as soon as it's woken.
	fast, _ := st.EnqueueEvent(ctx, "fast", "")
	bad, _ := st.EnqueueEvent(ctx, "bad", "")
	panicky, _ := st.EnqueueEvent(ctx, "panicky", "")
	unknown, _ := st.EnqueueEvent(ctx, "mystery", "")
	w.Wake()

	waitForStatuses(t, st, map[int64]string{
		slow: store.EventRunning, fast: store.EventDone, bad: store.EventFailed,
		panicky: store.EventFailed, unknown: store.EventFailed,
	})
	close(release)
	waitForStatuses(t, st, map[int64]string{slow: store.EventDone})
}

func TestWorkerLetsInFlightHandlerFinishAfterCancel(t *testing.T) {
	st := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	var handlerCtxErr error
	var mu sync.Mutex
	w := &Worker{
		Store: st, Concurrency: 1, PollInterval: 10 * time.Millisecond, MaxAttempts: 3,
		Handlers: map[string]Handler{
			"job": func(hctx context.Context, payload string) error {
				close(started)
				time.Sleep(100 * time.Millisecond)
				mu.Lock()
				handlerCtxErr = hctx.Err()
				mu.Unlock()
				return nil
			},
		},
	}
	id, _ := st.EnqueueEvent(context.Background(), "job", "")

	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	<-started
	cancel() // SIGTERM mid-handler

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	mu.Lock()
	defer mu.Unlock()
	if handlerCtxErr != nil {
		t.Errorf("handler context was cancelled (%v); in-flight work must be allowed to finish", handlerCtxErr)
	}
	waitForStatuses(t, st, map[int64]string{id: store.EventDone})
}
