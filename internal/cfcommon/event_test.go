package cfcommon

import (
	"testing"
	"time"
)

func TestHubSkipsProgressInHistory(t *testing.T) {
	h := NewHub()
	h.Publish(Event{Kind: KindLog, Message: "keep"})
	h.Publish(Event{Kind: KindProgress, Message: "skip"})
	h.Publish(Event{Kind: KindHit, IP: "1.1.1.1"})
	got := h.History()
	if len(got) != 2 {
		t.Fatalf("history len = %d, want 2", len(got))
	}
	if got[0].Kind != KindLog || got[1].Kind != KindHit {
		t.Fatalf("history = %#v", got)
	}
}

func TestHubReset(t *testing.T) {
	h := NewHub()
	h.Publish(Event{Kind: KindLog, Message: "old"})
	h.Reset()
	if len(h.History()) != 0 {
		t.Fatalf("history after reset = %#v", h.History())
	}
}

func TestHubResetNotifiesSubscribers(t *testing.T) {
	h := NewHub()
	ch, _, unsub := h.Subscribe()
	defer unsub()
	h.Reset()
	select {
	case e := <-ch:
		if e.Kind != KindReset {
			t.Fatalf("kind = %v, want reset", e.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting reset event")
	}
}

func TestHubClosesSlowSubscriberOnCriticalOverflow(t *testing.T) {
	h := NewHub()
	ch, _, unsub := h.Subscribe()
	defer unsub()
	for i := 0; i < 300; i++ {
		h.Publish(Event{Kind: KindLog, Message: "x"})
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // 已被关闭，符合预期
			}
		case <-deadline:
			t.Fatal("subscriber channel was not closed after overflow")
		}
	}
}
