package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	talkie "github.com/androiddrew/talkie-core"
)

// lockedBuffer is a buffer the joiner's goroutine and the test can share.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func (b *lockedBuffer) Len() int { return len(b.String()) }

// latencyLog collects the turn_latency lines a latencyJoin logs.
func latencyLog() (*latencyJoin, *lockedBuffer, *lockedBuffer) {
	var logs, status lockedBuffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
	return newLatencyJoin(log, &status), &logs, &status
}

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

func event(ev talkie.Event) latencyRecord { return latencyRecord{event: &ev} }
func timed(t agentTiming) latencyRecord   { return latencyRecord{timing: &t} }
func wantAttrs(t *testing.T, line string, attrs ...string) {
	t.Helper()
	for _, a := range attrs {
		if !strings.Contains(line, " "+a) {
			t.Errorf("%q lacks %s", line, a)
		}
	}
}

// The user stops talking at 0 ms; the transcript is ready at 300 ms.
var (
	transcript = event(talkie.Event{Type: talkie.EventTranscript, Time: at(300), Session: "s1", Text: "Tallest mountain?", STT: 300 * time.Millisecond})
	agentDone  = timed(agentTiming{Session: "s1", Reply: "reply_1", Text: "Tallest mountain?",
		Respond: at(310), FirstToken: at(810), FirstSentence: at(1010), Done: at(1500), Chars: 60})
	replyStarted  = event(talkie.Event{Type: talkie.EventReplyStarted, Time: at(1210), Session: "s1", Reply: "reply_1"})
	replyFinished = event(talkie.Event{Type: talkie.EventReplyFinished, Time: at(4310), Session: "s1", Reply: "reply_1"})
)

func TestLatencyJoin(t *testing.T) {
	j, logs, status := latencyLog()
	j.add(event(talkie.Event{Type: talkie.EventState, State: talkie.StateThinking}))
	for _, r := range []latencyRecord{transcript, agentDone, replyStarted} {
		j.add(r)
	}
	if logs.Len() > 0 {
		t.Fatalf("logged before the reply finished: %s", logs)
	}
	j.add(replyFinished)
	wantAttrs(t, logs.String(), "msg=turn_latency", "reply=reply_1", "stt=300ms", "first_token=500ms",
		"first_sentence=700ms", "first_audio=400ms", "end_to_end=1.21s", "chars=60", "total=4s")
	if strings.Contains(logs.String(), "interrupted_by") {
		t.Error("an uninterrupted reply has interrupted_by")
	}
	if status.String() != "[thinking]\n" {
		t.Errorf("status %q", status)
	}
	if len(j.replies) != 0 || len(j.transcripts) != 0 {
		t.Errorf("left %d replies and %d transcripts", len(j.replies), len(j.transcripts))
	}
}

// TestLatencyJoinOutOfOrder feeds the records in the worst order: the
// channel interleaves two goroutines, so the agent's timing can come last.
func TestLatencyJoinOutOfOrder(t *testing.T) {
	j, logs, _ := latencyLog()
	for _, r := range []latencyRecord{replyFinished, replyStarted, agentDone, transcript} {
		j.add(r)
	}
	wantAttrs(t, logs.String(), "stt=300ms", "first_token=500ms", "first_audio=400ms", "end_to_end=1.21s", "total=4s")
	if n := strings.Count(logs.String(), "turn_latency"); n != 1 {
		t.Errorf("logged %d lines", n)
	}
}

func TestLatencyJoinInterrupted(t *testing.T) {
	j, logs, _ := latencyLog()
	for _, r := range []latencyRecord{transcript, replyStarted,
		event(talkie.Event{Type: talkie.EventInterrupted, Time: at(2000), Session: "s1", Reply: "reply_1", InterruptedBy: talkie.InterruptedByWake}),
		event(talkie.Event{Type: talkie.EventReplyStopped, Time: at(2010), Session: "s1", Reply: "reply_1"}),
	} {
		j.add(r)
	}
	if logs.Len() > 0 {
		t.Fatal("logged before the agent returned")
	}
	done := *agentDone.timing
	done.Done, done.Err = at(2020), context.Canceled
	j.add(timed(done))
	wantAttrs(t, logs.String(), "interrupted_by=wake_word", "total=1.7s")
	if strings.Contains(logs.String(), "err=") {
		t.Error("a cancel is logged as an error")
	}
}

func TestLatencyJoinInterruptedWhileThinking(t *testing.T) {
	j, logs, _ := latencyLog()
	j.add(transcript)
	// Interrupted before any audio: the event has no reply ID, and the
	// agent returns after it.
	j.add(event(talkie.Event{Type: talkie.EventInterrupted, Time: at(900), Session: "s1", InterruptedBy: talkie.InterruptedByApp}))
	done := *agentDone.timing
	done.FirstToken, done.FirstSentence, done.Chars, done.Done = time.Time{}, time.Time{}, 0, at(910)
	j.add(timed(done))
	wantAttrs(t, logs.String(), "reply=reply_1", "interrupted_by=app", "chars=0")
	if strings.Contains(logs.String(), "first_token") {
		t.Error("first_token without a token")
	}
	if len(j.thinking) != 0 {
		t.Error("the interruption wasn't claimed")
	}
}

func TestLatencyJoinSweep(t *testing.T) {
	j, logs, _ := latencyLog()
	j.add(transcript)
	j.add(timed(agentTiming{Session: "s1", Reply: "reply_9", Text: "Other", Respond: at(0), Done: at(10)}))
	j.add(event(talkie.Event{Type: talkie.EventReplyStarted, Time: at(5), Reply: "reply_8"})) // no agent record
	j.sweep(at(2 * 60 * 1000))
	wantAttrs(t, logs.String(), "reply=reply_9")
	if len(j.replies) != 0 || len(j.transcripts) != 0 {
		t.Errorf("left %d replies and %d transcripts", len(j.replies), len(j.transcripts))
	}
}

// TestLatencyNeverBlocks fills the channel with no joiner running: OnEvent
// and Timed must return at once, dropping and counting what doesn't fit.
func TestLatencyNeverBlocks(t *testing.T) {
	j, logs, _ := latencyLog()
	l := &latency{records: make(chan latencyRecord, 4), join: j}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			l.OnEvent(talkie.Event{Type: talkie.EventState, State: talkie.StateListening})
		}
		l.Timed(*agentDone.timing)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OnEvent blocked on a full channel")
	}
	if got := l.dropped.Load(); got != 97 {
		t.Errorf("dropped %d, want 97", got)
	}

	// The joiner reports the drops.
	ctx, cancel := context.WithCancel(context.Background())
	go l.Run(ctx)
	defer cancel()
	for _, r := range []latencyRecord{transcript, agentDone, replyStarted, replyFinished} {
		l.records <- r
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(logs.String(), "turn_latency") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	wantAttrs(t, logs.String(), "dropped_records=97")
}
