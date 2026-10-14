package main

import (
	"context"
	"strings"
	"sync"

	talkie "github.com/androiddrew/talkie-core"
	"github.com/cloudwego/eino/schema"
)

// history is the conversation so far, for one talkie session at a time. A
// new session starts it empty.
//
// Turns take it in order: talkie can start a turn while the one it cancelled
// is still returning, so each turn waits for the previous one to record its
// reply before reading the history.
type history struct {
	mu      sync.Mutex
	session string
	msgs    []*schema.Message
	// last is the index of the previous turn's reply, provisional until the
	// next turn says whether it was interrupted; -1 when there is none.
	last int
	done chan struct{} // closed when the latest turn has recorded its reply
}

func newHistory() *history {
	done := make(chan struct{})
	close(done)
	return &history{last: -1, done: done}
}

// begin waits for the previous turn, applies turn to the history and returns
// the messages for the model, with record, which the turn must call exactly
// once with the reply it wrote.
func (h *history) begin(ctx context.Context, turn talkie.Turn) (msgs []*schema.Message, record func(reply string), err error) {
	h.mu.Lock()
	prev, done := h.done, make(chan struct{})
	h.done = done
	h.mu.Unlock()
	select {
	case <-prev:
	case <-ctx.Done():
		go func() { <-prev; close(done) }() // keep the next turn in order
		return nil, nil, ctx.Err()
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if turn.Session != h.session {
		h.session, h.msgs, h.last = turn.Session, nil, -1
	}
	if in := turn.Interrupted; in != nil && h.last >= 0 {
		if heard := heardReply(in); heard == "" {
			h.msgs = append(h.msgs[:h.last], h.msgs[h.last+1:]...)
		} else {
			h.msgs[h.last] = schema.AssistantMessage(heard, nil)
		}
	}
	h.last = -1
	h.msgs = append(h.msgs, schema.UserMessage(turn.Text))
	msgs = append([]*schema.Message(nil), h.msgs...)
	session := turn.Session
	return msgs, func(reply string) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if reply = strings.TrimSpace(reply); reply != "" && h.session == session {
			h.msgs = append(h.msgs, schema.AssistantMessage(reply, nil))
			h.last = len(h.msgs) - 1
		}
		close(done)
	}, nil
}

// heardReply is what the user heard of an interrupted reply, marked so the
// model knows it was cut off; empty when the user spoke before it played.
func heardReply(in *talkie.Interruption) string {
	if in.Played == 0 {
		return ""
	}
	s := strings.TrimSpace(in.Heard)
	if cut := strings.TrimSpace(in.Cut); cut != "" {
		s += " [cut off while saying: " + cut + "]"
	}
	return strings.TrimSpace(s + " [interrupted]")
}
