package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	talkie "github.com/androiddrew/talkie-core"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeModel streams scripted chunks, a delay apart. Its first blocks streams
// then block until their context is done, as a model still generating would.
type fakeModel struct {
	chunks []*schema.Message
	delay  time.Duration
	blocks int
	err    error // returned by Stream

	mu     sync.Mutex
	inputs [][]*schema.Message
	exited chan struct{} // closed when the latest stream's producer stops
}

func content(s string) *schema.Message { return &schema.Message{Role: schema.Assistant, Content: s} }
func reasoning(s string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, ReasoningContent: s}
}

func (f *fakeModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return nil, errors.New("fakeModel: Generate is not used")
}

func (f *fakeModel) Stream(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	exited := make(chan struct{})
	f.mu.Lock()
	f.inputs = append(f.inputs, append([]*schema.Message(nil), input...))
	f.exited = exited
	block := len(f.inputs) <= f.blocks
	f.mu.Unlock()
	if f.err != nil {
		close(exited)
		return nil, f.err
	}
	sr, sw := schema.Pipe[*schema.Message](0)
	go func() {
		defer close(exited)
		defer sw.Close()
		for _, c := range f.chunks {
			select {
			case <-time.After(f.delay):
			case <-ctx.Done():
				return
			}
			if sw.Send(c, nil) {
				return // the reader closed the stream
			}
		}
		if block {
			<-ctx.Done()
		}
	}()
	return sr, nil
}

func (f *fakeModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return f, nil }

func (f *fakeModel) input(i int) []*schema.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inputs[i]
}

func (f *fakeModel) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.inputs)
}

func (f *fakeModel) streamExited() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exited
}

// recorder is a speaker that records what is written. Like talkie.Reply,
// its writes fail once ctx is cancelled; hold, if set, blocks each write
// until it is closed.
type recorder struct {
	ctx  context.Context
	hold chan struct{}

	mu     sync.Mutex
	calls  int // WriteString calls, including held ones
	writes []string
	then   talkie.Then
	wrote  chan struct{} // receives after each write, if set
}

func (r *recorder) WriteString(s string) (int, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if r.hold != nil {
		<-r.hold
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	r.writes = append(r.writes, s)
	r.mu.Unlock()
	if r.wrote != nil {
		select {
		case r.wrote <- struct{}{}:
		default:
		}
	}
	return len(s), nil
}

func (r *recorder) Then(t talkie.Then) {
	r.mu.Lock()
	r.then = t
	r.mu.Unlock()
}

func (r *recorder) called() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.writes, "")
}

func newTestAgent(t *testing.T, m *fakeModel) *Agent {
	t.Helper()
	runner, err := newRunner(context.Background(), m, "Be brief.")
	if err != nil {
		t.Fatal(err)
	}
	return NewAgent(runner, slog.New(slog.DiscardHandler))
}

// conversation returns the user and assistant messages of a model input.
func conversation(msgs []*schema.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == schema.User || m.Role == schema.Assistant {
			out = append(out, string(m.Role)+": "+m.Content)
		}
	}
	return out
}

func equal(a, b []string) bool { return strings.Join(a, "\n") == strings.Join(b, "\n") }

func TestContentInOrderWithoutReasoning(t *testing.T) {
	m := &fakeModel{chunks: []*schema.Message{
		reasoning("The user wants a city. "), content("The capital "), reasoning("Paris it is."),
		content("of France "), content("is Paris. "), content("It is lovely."),
	}, delay: time.Millisecond}
	a := newTestAgent(t, m)
	var timing agentTiming
	a.timed = func(t agentTiming) { timing = t }
	out := &recorder{ctx: context.Background()}
	if err := a.respond(context.Background(), talkie.Turn{Session: "s1", Reply: "reply_1", Text: "Capital of France?"}, out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.text(), "The capital of France is Paris. It is lovely."; got != want {
		t.Errorf("spoke %q, want %q", got, want)
	}
	if strings.Contains(out.text(), "Paris it is") || strings.Contains(out.text(), "user wants") {
		t.Error("reasoning was spoken")
	}
	if out.then != "" {
		t.Errorf("then %q; only a goodbye sets it", out.then)
	}
	if timing.Reply != "reply_1" || timing.FirstToken.IsZero() || timing.FirstSentence.Before(timing.FirstToken) || timing.Chars != len(out.text()) {
		t.Errorf("timing %+v", timing)
	}
	if got := m.input(0); got[0].Role != schema.System || got[0].Content != "Be brief." {
		t.Errorf("the instruction isn't the system message: %v", got[0])
	}
}

func TestCancelStopsTheStream(t *testing.T) {
	m := &fakeModel{chunks: []*schema.Message{content("Rome was founded "), content("in 753 BC. ")}, blocks: 1}
	a := newTestAgent(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &recorder{ctx: ctx, wrote: make(chan struct{}, 1)}
	errc := make(chan error, 1)
	go func() {
		errc <- a.respond(ctx, talkie.Turn{Session: "s1", Reply: "reply_1", Text: "Tell me about Rome."}, out)
	}()
	select {
	case <-out.wrote:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was written")
	}
	cancel()
	start := time.Now()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Respond returned %v, want context.Canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Respond didn't return within 100ms of the cancel")
	}
	select {
	case <-m.streamExited():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("the model's stream is still open")
	}
	t.Logf("returned in %v", time.Since(start))
}

func TestInterruptionFixUp(t *testing.T) {
	m := &fakeModel{chunks: []*schema.Message{content("Rome was founded in 753 BC. "), content("It grew into an empire.")}}
	a := newTestAgent(t, m)
	ctx := context.Background()
	if err := a.respond(ctx, talkie.Turn{Session: "s1", Text: "Tell me about Rome."}, &recorder{ctx: ctx}); err != nil {
		t.Fatal(err)
	}
	in := &talkie.Interruption{Heard: "Rome was founded in 753 BC.", Cut: "It grew into an empire.", Played: 3 * time.Second}
	if err := a.respond(ctx, talkie.Turn{Session: "s1", Text: "What were you saying?", Interrupted: in}, &recorder{ctx: ctx}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"user: Tell me about Rome.",
		"assistant: Rome was founded in 753 BC. [cut off while saying: It grew into an empire.] [interrupted]",
		"user: What were you saying?",
	}
	if got := conversation(m.input(1)); !equal(got, want) {
		t.Errorf("history\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Interrupted while thinking: the reply never played, so it is dropped.
	in = &talkie.Interruption{Unspoken: "Rome was founded in 753 BC. It grew into an empire."}
	if err := a.respond(ctx, talkie.Turn{Session: "s1", Text: "Never mind.", Interrupted: in}, &recorder{ctx: ctx}); err != nil {
		t.Fatal(err)
	}
	want = append(want, "user: Never mind.")
	if got := conversation(m.input(2)); !equal(got, want) {
		t.Errorf("history\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestNewSessionStartsEmpty(t *testing.T) {
	m := &fakeModel{chunks: []*schema.Message{content("Paris.")}}
	a := newTestAgent(t, m)
	ctx := context.Background()
	for _, turn := range []talkie.Turn{{Session: "s1", Text: "Capital of France?"}, {Session: "s1", Text: "And Spain?"}, {Session: "s2", Text: "Hello."}} {
		if err := a.respond(ctx, turn, &recorder{ctx: ctx}); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := conversation(m.input(1)), []string{"user: Capital of France?", "assistant: Paris.", "user: And Spain?"}; !equal(got, want) {
		t.Errorf("second turn's history %q", got)
	}
	if got, want := conversation(m.input(2)), []string{"user: Hello."}; !equal(got, want) {
		t.Errorf("new session's history %q", got)
	}
}

// TestNextTurnWaitsForCancelledTurn starts a turn while the cancelled one is
// still writing: it must see the cancelled turn's reply, fixed up.
func TestNextTurnWaitsForCancelledTurn(t *testing.T) {
	m := &fakeModel{chunks: []*schema.Message{content("Rome was founded in 753 BC. "), content("It grew.")}, blocks: 1}
	a := newTestAgent(t, m)
	ctx1, cancel1 := context.WithCancel(context.Background())
	hold := make(chan struct{})
	first := &recorder{ctx: ctx1, hold: hold}
	done1 := make(chan error, 1)
	go func() { done1 <- a.respond(ctx1, talkie.Turn{Session: "s1", Text: "Tell me about Rome."}, first) }()

	// Let the first write in, then hold the second one, then cancel.
	hold <- struct{}{}
	for first.called() < 2 {
		time.Sleep(time.Millisecond)
	}
	cancel1()

	ctx2 := context.Background()
	done2 := make(chan error, 1)
	in := &talkie.Interruption{Heard: "Rome was founded in 753 BC.", Played: time.Second}
	go func() {
		done2 <- a.respond(ctx2, talkie.Turn{Session: "s1", Text: "Go on.", Interrupted: in}, &recorder{ctx: ctx2})
	}()
	time.Sleep(50 * time.Millisecond)
	if n := m.calls(); n != 1 {
		t.Fatalf("the second turn called the model (%d calls) before the first returned", n)
	}
	close(hold)
	if err := <-done1; !errors.Is(err, context.Canceled) {
		t.Errorf("first turn returned %v", err)
	}
	if err := <-done2; err != nil {
		t.Fatal(err)
	}
	want := []string{"user: Tell me about Rome.", "assistant: Rome was founded in 753 BC. [interrupted]", "user: Go on."}
	if got := conversation(m.input(1)); !equal(got, want) {
		t.Errorf("history %q, want %q", got, want)
	}
}

func TestGoodbyeEndsSession(t *testing.T) {
	for text, end := range map[string]bool{
		"Goodbye.": true, "Okay, bye!": true, "That's all, thanks.": true, "Stop listening.": true, "Good bye": true,
		"What's the best buy?": false, "Tell me about Rome.": false,
	} {
		m := &fakeModel{chunks: []*schema.Message{content("Goodbye!")}}
		out := &recorder{ctx: context.Background()}
		if err := newTestAgent(t, m).respond(context.Background(), talkie.Turn{Session: "s1", Text: text}, out); err != nil {
			t.Fatal(err)
		}
		if got := out.then == talkie.ThenEnd; got != end {
			t.Errorf("%q: ThenEnd %v, want %v", text, got, end)
		}
	}
}

func TestModelErrorIsSpoken(t *testing.T) {
	m := &fakeModel{err: errors.New("connection refused")}
	out := &recorder{ctx: context.Background()}
	err := newTestAgent(t, m).respond(context.Background(), talkie.Turn{Session: "s1", Text: "Hello?"}, out)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("Respond returned %v", err)
	}
	if out.text() != couldNotReach {
		t.Errorf("spoke %q", out.text())
	}
}

func TestMarkdownIsFiltered(t *testing.T) {
	m := &fakeModel{chunks: []*schema.Message{content("Top cities:\n1"), content(". Paris, see https://"), content("paris.fr.\n2. Rome")}}
	out := &recorder{ctx: context.Background()}
	if err := newTestAgent(t, m).respond(context.Background(), talkie.Turn{Session: "s1", Text: "Cities?"}, out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.text(), "Top cities:\nParis, see .\nRome"; got != want {
		t.Errorf("spoke %q, want %q", got, want)
	}
}
