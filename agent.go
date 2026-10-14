package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	talkie "github.com/androiddrew/talkie-core"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// Agent answers talkie's turns with an eino agent, speaking its reply as it
// streams in. It keeps the conversation's history for the session.
type Agent struct {
	runner  *adk.Runner
	history *history
	log     *slog.Logger
	// timed, if set, receives each reply's timings when Respond returns.
	timed func(agentTiming)
}

// NewAgent returns an Agent around runner, which must stream.
func NewAgent(runner *adk.Runner, log *slog.Logger) *Agent {
	return &Agent{runner: runner, history: newHistory(), log: log}
}

// agentTiming is when an agent reply reached each stage.
type agentTiming struct {
	Session, Reply, Text string
	Respond              time.Time // Respond was called
	FirstToken           time.Time // the first content delta; zero if none
	FirstSentence        time.Time // the first delta that completed a sentence
	Done                 time.Time // Respond returned
	Chars                int       // characters written to the reply
	Err                  error
}

// speaker is where a reply is written: *talkie.Reply, or a fake in tests.
type speaker interface {
	WriteString(s string) (int, error)
	Then(talkie.Then)
}

// couldNotReach is said when the model call fails, since talkie says nothing
// for an error.
const couldNotReach = "Sorry, I couldn't reach the language model."

// Respond answers one turn; see talkie.Agent.
func (a *Agent) Respond(ctx context.Context, turn talkie.Turn, reply *talkie.Reply) error {
	return a.respond(ctx, turn, reply)
}

func (a *Agent) respond(ctx context.Context, turn talkie.Turn, out speaker) (err error) {
	timing := agentTiming{Session: turn.Session, Reply: turn.Reply, Text: turn.Text, Respond: time.Now()}
	msgs, record, err := a.history.begin(ctx, turn)
	if err != nil {
		return err
	}
	var said strings.Builder
	var tools toolLog
	defer func() {
		record(said.String(), tools.take())
		timing.Done, timing.Chars, timing.Err = time.Now(), said.Len(), err
		if a.timed != nil {
			a.timed(timing)
		}
	}()
	if isGoodbye(turn.Text) {
		out.Then(talkie.ThenEnd)
	}
	write := func(s string) error {
		if s == "" {
			return nil
		}
		if _, err := out.WriteString(s); err != nil {
			if ctx.Err() != nil {
				return ctx.Err() // interrupted
			}
			return err
		}
		now := time.Now()
		if timing.FirstToken.IsZero() {
			timing.FirstToken = now
		}
		said.WriteString(s)
		if timing.FirstSentence.IsZero() && sentenceEnd.MatchString(said.String()) {
			timing.FirstSentence = now
		}
		return nil
	}

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	deltas, errc := make(chan string), make(chan error, 1)
	go func() { errc <- readAssistant(runCtx, a.runner.Run(runCtx, msgs), deltas, &tools) }()
	var filter speechFilter
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d := <-deltas:
			if err := write(filter.Write(d)); err != nil {
				return err
			}
		case err := <-errc:
			if err == nil {
				return write(filter.Flush())
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if said.Len() == 0 {
				if _, werr := out.WriteString(couldNotReach); werr != nil && ctx.Err() != nil {
					return ctx.Err()
				}
			}
			return fmt.Errorf("model: %w", err)
		}
	}
}

// sentenceEnd matches a complete sentence, as talkie splits them.
var sentenceEnd = regexp.MustCompile(`[.!?]["')\]”’]*\s|\n`)

// readAssistant sends the content of the assistant's messages in events to
// out, a delta at a time, and closes each message stream when done with it.
// It adds tool calls and their results to tools. It returns when the agent
// finishes or ctx is done.
func readAssistant(ctx context.Context, events *adk.AsyncIterator[*adk.AgentEvent], out chan<- string, tools *toolLog) error {
	send := func(s string) error {
		if s == "" {
			return nil
		}
		select {
		case out <- s:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for {
		ev, ok := events.Next()
		if !ok {
			return nil
		}
		if ev.Err != nil {
			return ev.Err
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		mv := ev.Output.MessageOutput
		if mv.Role == schema.Tool {
			if msg, err := mv.GetMessage(); err == nil && msg != nil {
				tools.add(msg)
			}
			continue
		}
		if mv.Role != schema.Assistant {
			continue
		}
		// A message, such as one before a tool call, ends with a sentence.
		sent := false
		sendMsg := func(s string) error {
			sent = sent || s != ""
			return send(s)
		}
		msg := mv.Message
		if !mv.IsStreaming {
			if msg != nil {
				if err := sendMsg(msg.Content); err != nil {
					return err
				}
			}
		} else {
			var err error
			if msg, err = readStream(ctx, mv.MessageStream, sendMsg); err != nil {
				return err
			}
		}
		if msg != nil && len(msg.ToolCalls) > 0 {
			// Its content is in what was said.
			tools.add(&schema.Message{Role: schema.Assistant, ToolCalls: msg.ToolCalls})
		}
		if sent {
			if err := send("\n"); err != nil {
				return err
			}
		}
	}
}

// readStream sends each frame's content, skipping reasoning and tool calls,
// and closes the stream, which stops the model's HTTP stream. It returns the
// whole message.
//
// Content after a tool call isn't sent: a model can go on to say the call
// worked before it has run, then say so again from the result.
func readStream(ctx context.Context, stream adk.MessageStream, send func(string) error) (*schema.Message, error) {
	defer stream.Close()
	var frames []*schema.Message
	called := false
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if err := ctx.Err(); err != nil {
				return nil, err // a cancelled model can end its stream early
			}
			if len(frames) == 0 {
				return nil, nil
			}
			return schema.ConcatMessages(frames)
		}
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if frame != nil {
			frames = append(frames, frame)
			if !called {
				if err := send(frame.Content); err != nil {
					return nil, err
				}
			}
			called = called || len(frame.ToolCalls) > 0
		}
	}
}

// toolLog collects a turn's tool calls and results, for the history. The
// reader adds to it while Respond, once interrupted, may take it.
type toolLog struct {
	mu   sync.Mutex
	msgs []*schema.Message
}

func (l *toolLog) add(m *schema.Message) {
	l.mu.Lock()
	l.msgs = append(l.msgs, m)
	l.mu.Unlock()
}

// take returns the tool calls that have all their results, with the results:
// a call cut off by an interruption is left out, since a model API rejects a
// call without a result.
func (l *toolLog) take() []*schema.Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	answered := map[string]bool{}
	for _, m := range l.msgs {
		if m.Role == schema.Tool {
			answered[m.ToolCallID] = true
		}
	}
	var out []*schema.Message
	kept := map[string]bool{}
	for _, m := range l.msgs {
		switch m.Role {
		case schema.Assistant:
			complete := true
			for _, c := range m.ToolCalls {
				complete = complete && answered[c.ID]
			}
			if complete {
				out = append(out, m)
				for _, c := range m.ToolCalls {
					kept[c.ID] = true
				}
			}
		case schema.Tool:
			if kept[m.ToolCallID] {
				out = append(out, m)
			}
		}
	}
	return out
}

var goodbyes = regexp.MustCompile(`(?i)\b(good ?bye|bye|that'?s all|stop listening)\b`)

// isGoodbye reports whether the user is ending the conversation.
func isGoodbye(text string) bool {
	return goodbyes.MatchString(text)
}
