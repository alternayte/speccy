package review

import (
	"sync"

	"github.com/google/uuid"
)

// Event is one progress event of a run (REQ-026).
type Event struct {
	Type      string `json:"type"` // stage, progress, done, failed
	Stage     string `json:"stage"`
	Message   string `json:"message,omitempty"`
	Done      int    `json:"done,omitempty"`
	Total     int    `json:"total,omitempty"`
	CacheHits int    `json:"cache_hits,omitempty"`
}

// Broker fans run events out to live listeners. It keeps each run's events, so a listener that
// joins late gets the history first. It lives in one process (SDD §19 Q10: one replica).
type Broker struct {
	mu   sync.Mutex
	runs map[uuid.UUID]*runEvents
}

type runEvents struct {
	history []Event
	subs    map[chan Event]struct{}
	closed  bool
}

// NewBroker returns an empty broker.
func NewBroker() *Broker { return &Broker{runs: map[uuid.UUID]*runEvents{}} }

func (b *Broker) run(id uuid.UUID) *runEvents {
	r, ok := b.runs[id]
	if !ok {
		r = &runEvents{subs: map[chan Event]struct{}{}}
		b.runs[id] = r
	}
	return r
}

// Publish sends an event to every listener of the run. A done or failed event ends the run.
func (b *Broker) Publish(id uuid.UUID, e Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.run(id)
	if r.closed {
		return
	}
	r.history = append(r.history, e)
	for ch := range r.subs {
		select {
		case ch <- e:
		default: // a slow listener misses a progress event, not the final one
		}
	}
	if e.Type == "done" || e.Type == "failed" {
		r.closed = true
		for ch := range r.subs {
			close(ch)
		}
		r.subs = nil
	}
}

// Subscribe returns the run's events so far, and a channel of the next ones. The channel
// closes when the run ends. cancel stops the subscription.
func (b *Broker) Subscribe(id uuid.UUID) (history []Event, next <-chan Event, cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.run(id)
	ch := make(chan Event, 64)
	history = append([]Event(nil), r.history...)
	if r.closed {
		close(ch)
		return history, ch, func() {}
	}
	r.subs[ch] = struct{}{}
	return history, ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if r.subs != nil {
			if _, ok := r.subs[ch]; ok {
				delete(r.subs, ch)
				close(ch)
			}
		}
	}
}
