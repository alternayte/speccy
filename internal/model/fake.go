package model

import (
	"context"
	"fmt"
	"sync"
)

// Fake answers from a script (BUILD §5). Tests never call a real model.
//
// A script maps a call fingerprint (role, prompt version, content hash) to the answers for
// successive calls; the last answer repeats. Special answers act out failures:
//
//	"!timeout"  wait until the call's deadline
//	"!429"      HTTP 429
//	"!500"      HTTP 500
//	"!invalid"  text that is not JSON
type Fake struct {
	mu      sync.Mutex
	scripts map[string][]string
	calls   []Call
}

// NewFake returns a fake backend with scripts.
func NewFake(scripts map[string][]string) *Fake {
	s := make(map[string][]string, len(scripts))
	for k, v := range scripts {
		s[k] = append([]string(nil), v...)
	}
	return &Fake{scripts: s}
}

// Calls returns the calls the fake received, in order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Call implements Backend.
func (f *Fake) Call(ctx context.Context, _ string, c Call) (Raw, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	fp := Fingerprint(c)
	queue, ok := f.scripts[fp]
	if !ok || len(queue) == 0 {
		f.mu.Unlock()
		return Raw{}, fmt.Errorf("fake backend: no script for fingerprint %s (role %s). Add it to the test script", fp, c.Role)
	}
	answer := queue[0]
	if len(queue) > 1 {
		f.scripts[fp] = queue[1:]
	}
	f.mu.Unlock()

	switch answer {
	case "!timeout":
		<-ctx.Done()
		return Raw{}, ctx.Err()
	case "!429":
		return Raw{}, &StatusError{Status: 429, Message: "rate limited (fake)"}
	case "!500":
		return Raw{}, &StatusError{Status: 500, Message: "server error (fake)"}
	case "!invalid":
		answer = "This is not JSON {"
	}
	return Raw{Text: answer, TokensIn: estimateTokens(c.System + c.Prompt), TokensOut: estimateTokens(answer)}, nil
}
