package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
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
	defer func() {
		record(said.String())
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
	go func() { errc <- readAssistant(runCtx, a.runner.Run(runCtx, msgs), deltas) }()
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
// It returns when the agent finishes or ctx is done.
func readAssistant(ctx context.Context, events *adk.AsyncIterator[*adk.AgentEvent], out chan<- string) error {
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
		if mv.Role != schema.Assistant {
			continue
		}
		if !mv.IsStreaming {
			if mv.Message != nil {
				if err := send(mv.Message.Content); err != nil {
					return err
				}
			}
			continue
		}
		if err := readStream(ctx, mv.MessageStream, send); err != nil {
			return err
		}
	}
}

// readStream sends each frame's content, skipping reasoning and tool calls,
// and closes the stream, which stops the model's HTTP stream.
func readStream(ctx context.Context, stream adk.MessageStream, send func(string) error) error {
	defer stream.Close()
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return ctx.Err() // a cancelled model can end its stream early
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if frame != nil {
			if err := send(frame.Content); err != nil {
				return err
			}
		}
	}
}

var goodbyes = regexp.MustCompile(`(?i)\b(good ?bye|bye|that'?s all|stop listening)\b`)

// isGoodbye reports whether the user is ending the conversation.
func isGoodbye(text string) bool {
	return goodbyes.MatchString(text)
}
