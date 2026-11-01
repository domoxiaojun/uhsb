package cfcommon

import "testing"

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
