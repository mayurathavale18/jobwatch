package store

import (
	"context"
	"testing"
)

func TestEventLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if ev, err := st.ClaimEvent(ctx); err != nil || ev != nil {
		t.Fatalf("empty queue: got %+v, %v", ev, err)
	}

	id1, _ := st.EnqueueEvent(ctx, EventCronRun, `{"name":"poll"}`)
	id2, _ := st.EnqueueEvent(ctx, EventOutreach, `{"job_id":1}`)

	ev, err := st.ClaimEvent(ctx)
	if err != nil || ev == nil || ev.ID != id1 || ev.Status != EventRunning || ev.Attempts != 1 {
		t.Fatalf("first claim = %+v, %v; want oldest event running with 1 attempt", ev, err)
	}
	ev2, _ := st.ClaimEvent(ctx)
	if ev2 == nil || ev2.ID != id2 {
		t.Fatalf("second claim = %+v, want event %d (never the already-running one)", ev2, id2)
	}
	if ev3, _ := st.ClaimEvent(ctx); ev3 != nil {
		t.Fatalf("third claim = %+v, want nil", ev3)
	}

	if err := st.FinishEvent(ctx, id1, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishEvent(ctx, id2, "boom"); err != nil {
		t.Fatal(err)
	}
	events, _ := st.RecentEvents(ctx, 10)
	got := map[int64]string{}
	for _, e := range events {
		got[e.ID] = e.Status + ":" + e.Error
	}
	if got[id1] != "done:" || got[id2] != "failed:boom" {
		t.Errorf("statuses = %v", got)
	}
}

func TestRecoverRunningEvents(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// An event interrupted mid-run is re-queued, keeping its attempt count.
	id, _ := st.EnqueueEvent(ctx, EventCronRun, `{}`)
	for attempt := 1; attempt <= 2; attempt++ {
		ev, _ := st.ClaimEvent(ctx)
		if ev == nil || ev.ID != id || ev.Attempts != attempt {
			t.Fatalf("claim %d = %+v, want id %d attempts %d", attempt, ev, id, attempt)
		}
		requeued, failed, err := st.RecoverRunningEvents(ctx, 3)
		if err != nil || requeued != 1 || failed != 0 {
			t.Fatalf("recover after attempt %d: requeued=%d failed=%d err=%v, want 1/0", attempt, requeued, failed, err)
		}
	}

	// On its third interruption it has hit maxAttempts and is failed instead.
	st.ClaimEvent(ctx)
	requeued, failed, err := st.RecoverRunningEvents(ctx, 3)
	if err != nil || requeued != 0 || failed != 1 {
		t.Fatalf("final recover: requeued=%d failed=%d err=%v, want 0/1", requeued, failed, err)
	}
	if ev, _ := st.ClaimEvent(ctx); ev != nil {
		t.Fatalf("claim after fail = %+v, want nil", ev)
	}
}

func TestEnqueueEventsSetKVIsAtomic(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.EnqueueEventsSetKV(ctx, EventTelegramUpdate, []string{`{"update_id":1}`, `{"update_id":2}`}, "tg_offset", "3"); err != nil {
		t.Fatal(err)
	}
	v, ok, _ := st.GetKV(ctx, "tg_offset")
	events, _ := st.RecentEvents(ctx, 10)
	if !ok || v != "3" || len(events) != 2 {
		t.Fatalf("offset=%q ok=%v events=%d, want 3/true/2", v, ok, len(events))
	}
}
