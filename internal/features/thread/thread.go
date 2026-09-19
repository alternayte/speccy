// Package thread is the thread aggregate (SDD §6.9, DEC-008): a discussion anchored to text, a
// section, a finding, or a profile check, addressed to humans or to the AI. decide and evolve
// are pure.
package thread

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/speccy/internal/es"
	"github.com/alternayte/speccy/internal/kernel"
)

// StreamType is the stream type of a thread.
const StreamType = "thread"

// Event types (SDD §11.3).
const (
	Opened           = "ThreadOpened"
	MessagePosted    = "MessagePosted"
	DecisionRecorded = "DecisionRecorded"
	DecisionReversed = "DecisionReversed"
	MarkedBlocking   = "ThreadMarkedBlocking"
	Resolved         = "ThreadResolved"
	Reopened         = "ThreadReopened"
)

// Anchor kinds (REQ-087).
const (
	AnchorText    = "text"
	AnchorSection = "section"
	AnchorFinding = "finding"
	AnchorCheck   = "check" // a profile check: a rubric suggestion (REQ-015)
)

// Audiences.
const (
	Humans = "humans"
	AI     = "ai"
)

// MaxMessage is the longest message.
const MaxMessage = 20_000

// Author is who posted a message.
type Author struct {
	Kind string `json:"kind"` // user | guest | ai
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Message is one message.
type Message struct {
	ID       uuid.UUID `json:"id"`
	Seq      int       `json:"seq"`
	Author   Author    `json:"author"`
	Body     string    `json:"body"`
	Sources  []string  `json:"sources,omitempty"`
	Decision string    `json:"decision,omitempty"` // "" | decision | reversal
	At       time.Time `json:"at"`
}

// State is a thread.
type State struct {
	ID          uuid.UUID       `json:"id"`
	BundleID    *uuid.UUID      `json:"bundle_id,omitempty"`
	ProfileKey  string          `json:"profile_key,omitempty"`
	AnchorKind  string          `json:"anchor_kind"`
	Anchor      json.RawMessage `json:"anchor"`
	AddressedTo string          `json:"addressed_to"`
	Title       string          `json:"title"`
	Blocking    bool            `json:"blocking"`
	Open        bool            `json:"open"`
	CreatedBy   Author          `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
	Messages    []Message       `json:"messages"`
	// Decision is the ID of the message that holds the thread's current decision.
	Decision *uuid.UUID `json:"decision,omitempty"`
}

// Open is the command to open a thread with its first message.
type Open struct {
	ID          uuid.UUID
	BundleID    *uuid.UUID
	ProfileKey  string
	AnchorKind  string
	Anchor      json.RawMessage
	AddressedTo string
	Title       string
	Blocking    bool
	By          Author
	Body        string
	MessageID   uuid.UUID
	At          time.Time
}

type openedV1 struct {
	V int `json:"v"`
	Open
}

type postedV1 struct {
	V int `json:"v"`
	Message
}

type decisionV1 struct {
	V       int       `json:"v"`
	Message uuid.UUID `json:"message"`
	By      string    `json:"by"`
}

type flagV1 struct {
	V     int    `json:"v"`
	By    string `json:"by"`
	Value bool   `json:"value,omitempty"`
}

func checkBody(body string) error {
	b := strings.TrimSpace(body)
	if b == "" {
		return kernel.Invalid("empty_message", "Write a message.")
	}
	if len(b) > MaxMessage {
		return kernel.Invalid("message_too_long", "A message is at most %d characters.", MaxMessage)
	}
	return nil
}

// DecideOpen opens a thread. A guest can open a thread only for humans (REQ-086).
func DecideOpen(s State, c Open) ([]es.Event, error) {
	if s.ID != uuid.Nil {
		return nil, kernel.Conflict("thread_exists", "This thread exists already.")
	}
	switch c.AnchorKind {
	case AnchorText, AnchorSection, AnchorFinding, AnchorCheck:
	default:
		return nil, kernel.Invalid("bad_anchor", "A thread anchors to text, a section, a finding, or a profile check.")
	}
	if c.AddressedTo != Humans && c.AddressedTo != AI {
		return nil, kernel.Invalid("bad_audience", "A thread is for humans or for the AI.")
	}
	if c.By.Kind == "guest" && (c.AddressedTo == AI || c.Blocking) {
		return nil, kernel.Forbidden("guest_not_allowed", "A guest can start a thread for humans only, and cannot mark it blocking.")
	}
	if err := checkBody(c.Body); err != nil {
		return nil, err
	}
	c.Body = strings.TrimSpace(c.Body)
	c.Title = strings.TrimSpace(c.Title)
	return []es.Event{es.NewEvent(Opened, openedV1{V: 1, Open: c})}, nil
}

// DecidePost adds a message. A resolved thread takes no message until it is reopened. A guest
// cannot post in a thread for the AI.
func DecidePost(s State, m Message) ([]es.Event, error) {
	if s.ID == uuid.Nil {
		return nil, kernel.NotFound("thread_not_found", "No thread has this ID.")
	}
	if !s.Open {
		return nil, kernel.Conflict("thread_resolved", "This thread is resolved. Reopen it to write in it.")
	}
	if m.Author.Kind == "guest" && s.AddressedTo == AI {
		return nil, kernel.Forbidden("guest_not_allowed", "A guest cannot ask the AI.")
	}
	if err := checkBody(m.Body); err != nil {
		return nil, err
	}
	m.Body = strings.TrimSpace(m.Body)
	m.Seq = len(s.Messages) + 1
	return []es.Event{es.NewEvent(MessagePosted, postedV1{V: 1, Message: m})}, nil
}

// DecideDecision marks a message as the thread's decision (REQ-089). A later decision that
// replaces an earlier one is recorded as a reversal.
func DecideDecision(s State, message uuid.UUID, by Author) ([]es.Event, error) {
	if by.Kind != "user" {
		return nil, kernel.Forbidden("guest_not_allowed", "Only a member can record a decision.")
	}
	found := false
	for _, m := range s.Messages {
		if m.ID == message {
			found = true
		}
	}
	if !found {
		return nil, kernel.NotFound("message_not_found", "The thread has no such message.")
	}
	if s.Decision != nil && *s.Decision == message {
		return nil, nil
	}
	t := DecisionRecorded
	if s.Decision != nil {
		t = DecisionReversed
	}
	return []es.Event{es.NewEvent(t, decisionV1{V: 1, Message: message, By: by.ID})}, nil
}

// DecideBlocking marks the thread blocking or not (REQ-089). Members only.
func DecideBlocking(s State, blocking bool, by Author) ([]es.Event, error) {
	if by.Kind != "user" {
		return nil, kernel.Forbidden("guest_not_allowed", "Only a member can mark a thread blocking.")
	}
	if s.Blocking == blocking {
		return nil, nil
	}
	return []es.Event{es.NewEvent(MarkedBlocking, flagV1{V: 1, By: by.ID, Value: blocking})}, nil
}

// DecideResolve resolves or reopens the thread. Members only.
func DecideResolve(s State, open bool, by Author) ([]es.Event, error) {
	if by.Kind != "user" {
		return nil, kernel.Forbidden("guest_not_allowed", "Only a member can resolve or reopen a thread.")
	}
	if s.Open == open {
		return nil, nil
	}
	t := Resolved
	if open {
		t = Reopened
	}
	return []es.Event{es.NewEvent(t, flagV1{V: 1, By: by.ID})}, nil
}

// Evolve applies one event.
func Evolve(s State, e es.Event) State {
	switch e.Type {
	case Opened:
		var p openedV1
		_ = json.Unmarshal(e.Payload, &p)
		c := p.Open
		return State{ID: c.ID, BundleID: c.BundleID, ProfileKey: c.ProfileKey, AnchorKind: c.AnchorKind, Anchor: c.Anchor,
			AddressedTo: c.AddressedTo, Title: c.Title, Blocking: c.Blocking, Open: true, CreatedBy: c.By, CreatedAt: c.At,
			Messages: []Message{{ID: c.MessageID, Seq: 1, Author: c.By, Body: c.Body, At: c.At}}}
	case MessagePosted:
		var p postedV1
		_ = json.Unmarshal(e.Payload, &p)
		s.Messages = append(append([]Message{}, s.Messages...), p.Message)
	case DecisionRecorded, DecisionReversed:
		var p decisionV1
		_ = json.Unmarshal(e.Payload, &p)
		msgs := append([]Message{}, s.Messages...)
		for i := range msgs {
			if msgs[i].ID == p.Message {
				msgs[i].Decision = "decision"
				if e.Type == DecisionReversed {
					msgs[i].Decision = "reversal"
				}
			}
		}
		s.Messages = msgs
		id := p.Message
		s.Decision = &id
	case MarkedBlocking:
		var p flagV1
		_ = json.Unmarshal(e.Payload, &p)
		s.Blocking = p.Value
	case Resolved:
		s.Open = false
	case Reopened:
		s.Open = true
	}
	return s
}
