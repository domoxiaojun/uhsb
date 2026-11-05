package cfcommon

import (
	"strings"
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

func TestHubResetDisconnectsFullSubscriber(t *testing.T) {
	h := NewHub()
	ch, _, unsub := h.Subscribe()
	defer unsub()
	// 直接填满订阅缓冲，不走 Publish（Publish 自己有断开逻辑，会掩盖 Reset 的行为）。
	h.mu.Lock()
	for c := range h.subs {
		for i := 0; i < cap(c); i++ {
			c <- Event{Kind: KindLog, Message: "filler"}
		}
	}
	h.mu.Unlock()
	h.Reset()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // 已断开，浏览器会重连并拿到 Reset + 新快照
			}
		case <-deadline:
			t.Fatal("Reset 在 channel 满时静默丢弃，未断开订阅者")
		}
	}
}

func TestHubPublishTrimAddsNotice(t *testing.T) {
	h := NewHub()
	for i := 0; i < hubMaxHistory+1; i++ {
		h.Publish(Event{Kind: KindLog, Message: "x"})
	}
	got := h.History()
	if len(got) != hubKeepHistory+1 {
		t.Fatalf("history len = %d, want %d", len(got), hubKeepHistory+1)
	}
	for _, e := range got {
		if e.Kind == KindWarn && strings.Contains(e.Message, "已裁剪") {
			return
		}
	}
	t.Fatal("裁剪后未插入提示事件，重连用户会误以为日志从第一句开始")
}
