package cfcommon

import (
	"fmt"
	"sync"
)

type EventKind string

const (
	KindLog          EventKind = "log"
	KindProgress     EventKind = "progress"
	KindProgressDone EventKind = "progress_done"
	KindHit          EventKind = "hit"
	KindResult       EventKind = "result"
	KindFile         EventKind = "file"
	KindStage        EventKind = "stage"
	KindWarn         EventKind = "warn"
	KindError        EventKind = "error"
	KindDone         EventKind = "done"
)

type Event struct {
	Stage   string    `json:"stage"`
	Kind    EventKind `json:"kind"`
	Message string    `json:"message"`
	Done    int64     `json:"done,omitempty"`
	Total   int64     `json:"total,omitempty"`
	Hit     int64     `json:"hit,omitempty"`
	Path    string    `json:"path,omitempty"`
	IP      string    `json:"ip,omitempty"`
	Extra   any       `json:"extra,omitempty"`
}

type Reporter struct {
	emit func(Event)
}

func NewReporter(fn func(Event)) Reporter {
	if fn == nil {
		fn = func(Event) {}
	}
	return Reporter{emit: fn}
}

func StdoutReporter() Reporter {
	return NewReporter(func(e Event) {
		switch e.Kind {
		case KindProgress:
			if e.Message != "" {
				fmt.Printf("\r进度: %d/%d | 命中: %d | %s\033[K", e.Done, e.Total, e.Hit, e.Message)
			} else {
				fmt.Printf("\r进度: %d/%d | 命中: %d\033[K", e.Done, e.Total, e.Hit)
			}
		case KindProgressDone:
			fmt.Println()
		case KindError:
			fmt.Printf("[错误] %s\n", e.Message)
		case KindWarn:
			fmt.Printf("[警告] %s\n", e.Message)
		default:
			if e.Message != "" {
				fmt.Println(e.Message)
			}
		}
	})
}

func (r Reporter) Emit(e Event) {
	if r.emit != nil {
		r.emit(e)
	}
}

func (r Reporter) Log(stage, format string, args ...any) {
	r.Emit(Event{Stage: stage, Kind: KindLog, Message: fmt.Sprintf(format, args...)})
}

func (r Reporter) Warn(stage, format string, args ...any) {
	r.Emit(Event{Stage: stage, Kind: KindWarn, Message: fmt.Sprintf(format, args...)})
}

func (r Reporter) Error(stage, format string, args ...any) {
	r.Emit(Event{Stage: stage, Kind: KindError, Message: fmt.Sprintf(format, args...)})
}

func (r Reporter) Stage(stage, format string, args ...any) {
	r.Emit(Event{Stage: stage, Kind: KindStage, Message: fmt.Sprintf(format, args...)})
}

func (r Reporter) File(stage, path, format string, args ...any) {
	r.Emit(Event{Stage: stage, Kind: KindFile, Path: path, Message: fmt.Sprintf(format, args...)})
}

func (r Reporter) ProgressHandler(stage string) func(done, total, hit int64, latest string) {
	return func(done, total, hit int64, latest string) {
		r.Emit(Event{
			Stage:   stage,
			Kind:    KindProgress,
			Message: latest,
			Done:    done,
			Total:   total,
			Hit:     hit,
		})
	}
}

type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
	log  []Event
}

func NewHub() *Hub {
	return &Hub{subs: make(map[chan Event]struct{})}
}

func (h *Hub) Reset() {
	h.mu.Lock()
	h.log = nil
	h.mu.Unlock()
}

func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	if e.Kind != KindProgress {
		h.log = append(h.log, e)
		if len(h.log) > 4000 {
			h.log = h.log[len(h.log)-3000:]
		}
	}
	targets := make([]chan Event, 0, len(h.subs))
	for ch := range h.subs {
		targets = append(targets, ch)
	}
	h.mu.Unlock()
	for _, ch := range targets {
		select {
		case ch <- e:
		default:
		}
	}
}

func (h *Hub) Subscribe() (<-chan Event, []Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	snapshot := append([]Event(nil), h.log...)
	h.mu.Unlock()
	return ch, snapshot, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *Hub) History() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Event(nil), h.log...)
}

func (h *Hub) Reporter() Reporter {
	return NewReporter(h.Publish)
}
