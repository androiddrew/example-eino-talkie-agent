package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	talkie "github.com/androiddrew/talkie-core"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// liveModel connects to the server in EINO_TALKIE_TEST_LLM, such as
// http://localhost:11434/v1, or skips the test.
func liveModel(t *testing.T, reasoning string) *watchedModel {
	t.Helper()
	baseURL := os.Getenv("EINO_TALKIE_TEST_LLM")
	if baseURL == "" {
		t.Skip("set EINO_TALKIE_TEST_LLM to an OpenAI-compatible base URL to run against a real model")
	}
	cm, err := newChatModel(context.Background(), baseURL, env("OPENAI_API_MODEL", defaultModel), env("OPENAI_API_KEY", "ollama"), reasoning, liveTemperature(t))
	if err != nil {
		t.Fatal(err)
	}
	return &watchedModel{ToolCallingChatModel: cm}
}

// liveTemperature is EINO_TALKIE_TEST_TEMP, negative for the server's
// default, or -temperature's default.
func liveTemperature(t *testing.T) float64 {
	v := os.Getenv("EINO_TALKIE_TEST_TEMP")
	if v == "" {
		return 0.2
	}
	temp, err := strconv.ParseFloat(v, 64)
	if err != nil {
		t.Fatalf("EINO_TALKIE_TEST_TEMP: %v", err)
	}
	return temp
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
	runner, err := newRunner(context.Background(), m, voicePrompt, "", slog.New(slog.DiscardHandler))
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

// TestLiveSkills asks a real model what skills it has, then gives it light
// requests in different words, with the repository's office-lights skill and
// a script that only records its arguments.
func TestLiveSkills(t *testing.T) {
	m := liveModel(t, "none")
	skillMD, err := os.ReadFile("skills/office-lights/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sd := writeSkill(t, dir, "office-lights", string(skillMD), map[string]string{"office-lights.sh": lightsScript})
	runner, err := newRunner(context.Background(), m, voicePrompt, dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a := NewAgent(runner, slog.New(slog.DiscardHandler))
	session := 0
	turn := func(text string) string {
		t.Helper()
		var timing agentTiming
		a.timed = func(t agentTiming) { timing = t }
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		out := &recorder{ctx: ctx}
		if err := a.respond(ctx, talkie.Turn{Session: fmt.Sprint("live_", session), Text: text}, out); err != nil {
			t.Fatal(err)
		}
		t.Logf("%q -> %q (first_token=%v total=%v)", text, out.text(), sinceOrZero(timing.Respond, timing.FirstToken), timing.Done.Sub(timing.Respond).Round(time.Millisecond))
		return out.text()
	}
	calls := func() string {
		b, _ := os.ReadFile(filepath.Join(sd, "calls"))
		_ = os.Remove(filepath.Join(sd, "calls"))
		return strings.TrimSpace(string(b))
	}

	if answer := turn("What skills do you have?"); !strings.Contains(strings.ToLower(answer), "light") {
		t.Errorf("the answer doesn't mention the lights skill")
	}
	if c := calls(); c != "" {
		t.Errorf("asking about skills ran the script: %q", c)
	}
	// One session, so later requests follow earlier ones done the same way.
	for _, c := range []struct{ say, want string }{
		{"Turn the lights on.", "on"},
		{"Now turn them off.", "off"},
		{"Lights on please.", "on"},
		{"Can you switch off the office lights?", "off"},
		{"Make the lights warm.", "morning"},
		{"It's too bright in here, dim them.", "half"},
		{"Give me cool daylight.", "day"},
		{"Full brightness, please.", "full"},
		{"Turn the lights on and make them warm.", "morning\non"}, // sorted
	} {
		turn(c.say)
		got := calls()
		if lines := strings.Split(got, "\n"); len(lines) == 2 { // either order
			sort.Strings(lines)
			got = strings.Join(lines, "\n")
		}
		if got != c.want {
			t.Errorf("%q ran the script with %q, want %q", c.say, got, c.want)
		}
	}
}

// TestLiveWebSearch asks a real model questions with the repository's
// web-search skill, searching through SearXNG, and the office-lights skill
// with a recording script.
func TestLiveWebSearch(t *testing.T) {
	m := liveModel(t, "none")
	searxng := env("SEARXNG_URL", "http://127.0.0.1:8888")
	if resp, err := http.Get(searxng + "/healthz"); err != nil {
		t.Skipf("SearXNG isn't running at %s: %v", searxng, err)
	} else {
		resp.Body.Close()
	}
	search, err := filepath.Abs("skills/web-search/scripts/web-search")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, s := range []struct{ name, script, body string }{
		{"web-search", "web-search", "#!/bin/sh\necho \"$*\" >> calls\nexec " + search + " \"$@\"\n"},
		{"office-lights", "office-lights.sh", lightsScript},
	} {
		md, err := os.ReadFile(filepath.Join("skills", s.name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		writeSkill(t, dir, s.name, string(md), map[string]string{s.script: s.body})
	}
	runner, err := newRunner(context.Background(), m, voicePrompt, dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a := NewAgent(runner, slog.New(slog.DiscardHandler))
	calls := func(skill string) string {
		path := filepath.Join(dir, skill, "calls")
		b, _ := os.ReadFile(path)
		_ = os.Remove(path)
		return strings.TrimSpace(string(b))
	}
	for i, c := range []struct {
		say    string
		search bool   // a web search is expected
		lights string // the lights script's expected arguments
		answer string // a word the answer must contain, ignoring case
	}{
		{say: "Who won the 2026 World Cup?", search: true, answer: "spain"},
		{say: "What's the tallest mountain in Africa?", search: true, answer: "kilimanjaro"},
		{say: "How are you doing today?"},
		{say: "Turn the lights on.", lights: "on"},
	} {
		var timing agentTiming
		a.timed = func(t agentTiming) { timing = t }
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		out := &recorder{ctx: ctx}
		err := a.respond(ctx, talkie.Turn{Session: fmt.Sprint("search_", i), Text: c.say}, out)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		queries := calls("web-search")
		t.Logf("%q -> %q (searched %q, first_token=%v total=%v)", c.say, out.text(), queries,
			sinceOrZero(timing.Respond, timing.FirstToken), timing.Done.Sub(timing.Respond).Round(time.Millisecond))
		if (queries != "") != c.search {
			t.Errorf("%q: searched %q, want a search %v", c.say, queries, c.search)
		}
		if got := calls("office-lights"); got != c.lights {
			t.Errorf("%q: the lights script got %q, want %q", c.say, got, c.lights)
		}
		if !strings.Contains(strings.ToLower(out.text()), c.answer) {
			t.Errorf("%q: the answer lacks %q", c.say, c.answer)
		}
		if strings.Contains(out.text(), "http") || strings.Contains(out.text(), ".com") {
			t.Errorf("%q: the answer reads out a web address", c.say)
		}
	}
}

// TestLiveKidsStory asks a real model for stories of different kinds with the
// repository's kids-story skill, and the office-lights skill with a recording
// script.
func TestLiveKidsStory(t *testing.T) {
	m := liveModel(t, "none")
	story, err := filepath.Abs("skills/kids-story/scripts/kids-story")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, s := range []struct{ name, script, body string }{
		{"kids-story", "kids-story", "#!/bin/sh\necho \"$*\" >> calls\nexec " + story + " \"$@\"\n"},
		{"office-lights", "office-lights.sh", lightsScript},
	} {
		md, err := os.ReadFile(filepath.Join("skills", s.name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		writeSkill(t, dir, s.name, string(md), map[string]string{s.script: s.body})
	}
	runner, err := newRunner(context.Background(), m, voicePrompt, dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a := NewAgent(runner, slog.New(slog.DiscardHandler))
	calls := func(skill string) string {
		path := filepath.Join(dir, skill, "calls")
		b, _ := os.ReadFile(path)
		_ = os.Remove(path)
		return strings.TrimSpace(string(b))
	}
	for i, c := range []struct {
		say    string
		story  string // a word the story request must contain, or "" for no story
		lights string // the lights script's expected arguments
	}{
		{say: "Tell me a funny story about a pirate cat.", story: "pirate"},
		{say: "Can you tell Mia a short bedtime story?", story: "bedtime"},
		{say: "Tell me a story about sharing for a five year old.", story: "shar"},
		{say: "Tell me a spooky story about a ghost.", story: "ghost"},
		{say: "How are you doing today?"},
		{say: "Turn the lights on.", lights: "on"},
	} {
		var timing agentTiming
		a.timed = func(t agentTiming) { timing = t }
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		out := &recorder{ctx: ctx}
		err := a.respond(ctx, talkie.Turn{Session: fmt.Sprint("story_", i), Text: c.say}, out)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		requests := calls("kids-story")
		t.Logf("%q -> %q (requested %q, first_token=%v total=%v)", c.say, out.text(), requests,
			sinceOrZero(timing.Respond, timing.FirstToken), timing.Done.Sub(timing.Respond).Round(time.Millisecond))
		if c.story == "" {
			if requests != "" {
				t.Errorf("%q: requested a story %q", c.say, requests)
			}
		} else {
			if !strings.Contains(strings.ToLower(requests), c.story) {
				t.Errorf("%q: requested a story %q, want one with %q", c.say, requests, c.story)
			}
			if n := strings.Count(out.text(), ". "); n < 6 {
				t.Errorf("%q: the story has only about %d sentences", c.say, n+1)
			}
			if strings.ContainsAny(out.text(), "*#") {
				t.Errorf("%q: the story has markdown", c.say)
			}
		}
		if got := calls("office-lights"); got != c.lights {
			t.Errorf("%q: the lights script got %q, want %q", c.say, got, c.lights)
		}
	}
}
