package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	talkie "github.com/androiddrew/talkie-core"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// liveModel connects to the server in EINO_TALKIE_TEST_LLM, such as
// http://orin.runcible.io:11434/v1, or skips the test.
func liveModel(t *testing.T, reasoning string) *watchedModel {
	t.Helper()
	baseURL := os.Getenv("EINO_TALKIE_TEST_LLM")
	if baseURL == "" {
		t.Skip("set EINO_TALKIE_TEST_LLM to an OpenAI-compatible base URL to run against a real model")
	}
	cm, err := newChatModel(context.Background(), baseURL, env("OPENAI_API_MODEL", "qwen3.5:9b"), env("OPENAI_API_KEY", "ollama"), reasoning)
	if err != nil {
		t.Fatal(err)
	}
	return &watchedModel{ToolCallingChatModel: cm}
}

// watchedModel notes what the model streams before the adapter sees it.
type watchedModel struct {
	model.ToolCallingChatModel
	mu        sync.Mutex
	reasoning strings.Builder
}

func (w *watchedModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := w.ToolCallingChatModel.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderWithConvert(sr, func(m *schema.Message) (*schema.Message, error) {
		w.mu.Lock()
		w.reasoning.WriteString(m.ReasoningContent)
		w.mu.Unlock()
		return m, nil
	}), nil
}

func (w *watchedModel) reasoned() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.reasoning.String()
}

func liveAgent(t *testing.T, m model.ToolCallingChatModel) *Agent {
	t.Helper()
	runner, err := newRunner(context.Background(), m, voicePrompt)
	if err != nil {
		t.Fatal(err)
	}
	return NewAgent(runner, slog.New(slog.DiscardHandler))
}

func TestLiveTurn(t *testing.T) {
	m := liveModel(t, "none")
	a := liveAgent(t, m)
	var timing agentTiming
	a.timed = func(t agentTiming) { timing = t }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out := &recorder{ctx: ctx}
	if err := a.respond(ctx, talkie.Turn{Session: "live", Reply: "reply_1", Text: "What is the tallest mountain in Africa?"}, out); err != nil {
		t.Fatal(err)
	}
	answer := out.text()
	t.Logf("answer: %q", answer)
	t.Logf("first_token=%v first_sentence=%v total=%v", timing.FirstToken.Sub(timing.Respond), timing.FirstSentence.Sub(timing.Respond), timing.Done.Sub(timing.Respond))
	if strings.TrimSpace(answer) == "" {
		t.Error("the answer is empty")
	}
	if r := m.reasoned(); r != "" {
		t.Errorf("the model reasoned with thinking off: %q", r)
	}
	if strings.Contains(answer, "<think>") {
		t.Error("the answer contains a think block")
	}
}

func TestLiveCancel(t *testing.T) {
	a := liveAgent(t, liveModel(t, "none"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out := &recorder{ctx: ctx, wrote: make(chan struct{}, 1)}
	errc := make(chan error, 1)
	go func() {
		errc <- a.respond(ctx, talkie.Turn{Session: "live", Reply: "reply_1", Text: "Tell me about the history of Rome in detail."}, out)
	}()
	select {
	case <-out.wrote:
	case err := <-errc:
		t.Fatalf("Respond returned %v before writing", err)
	}
	cancel()
	start := time.Now()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Respond returned %v", err)
		}
		t.Logf("returned %v after the cancel, having said %q", time.Since(start), out.text())
	case <-time.After(time.Second):
		t.Fatal("Respond didn't return within 1s of the cancel")
	}
}

// TestLiveThinkingFirstToken measures how long the model takes to start
// answering with thinking on, the reason -reasoning defaults to none. It runs
// only with EINO_TALKIE_TEST_THINKING=1, since it can take minutes.
func TestLiveThinkingFirstToken(t *testing.T) {
	if os.Getenv("EINO_TALKIE_TEST_THINKING") == "" {
		t.Skip("set EINO_TALKIE_TEST_THINKING=1 to measure the model with thinking on")
	}
	m := liveModel(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	msgs := []*schema.Message{schema.SystemMessage(voicePrompt), schema.UserMessage("What is the tallest mountain in Africa?")}
	start := time.Now()
	sr, err := m.Stream(ctx, msgs)
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	var firstReasoning, firstContent time.Duration
	for {
		frame, err := sr.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if frame.ReasoningContent != "" && firstReasoning == 0 {
			firstReasoning = time.Since(start)
		}
		if frame.Content != "" && firstContent == 0 {
			firstContent = time.Since(start)
		}
	}
	t.Logf("thinking on: first reasoning %v, first answer token %v, total %v, %d reasoning chars",
		firstReasoning, firstContent, time.Since(start), len(m.reasoned()))
}
