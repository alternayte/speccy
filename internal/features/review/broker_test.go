package review

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// The broker drops the events of a run a while after it ends, so a long-lived hosted process
// does not keep every run in memory.
func TestBrokerForgetsEndedRuns(t *testing.T) {
	b := NewBroker()
	b.Keep = 20 * time.Millisecond
	id := uuid.New()
	b.Publish(id, Event{Type: "stage", Stage: "lint"})
	b.Publish(id, Event{Type: "done"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		n := len(b.runs)
		b.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the broker still holds the ended run")
}
