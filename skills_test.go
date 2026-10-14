package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	talkie "github.com/androiddrew/talkie-core"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// writeSkill makes a skill directory under dir with a SKILL.md and scripts.
func writeSkill(t *testing.T, dir, name, skillMD string, scripts map[string]string) string {
	t.Helper()
	sd := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(sd, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if skillMD != "" {
		if err := os.WriteFile(filepath.Join(sd, skillFile), []byte(skillMD), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for file, body := range scripts {
		if err := os.WriteFile(filepath.Join(sd, "scripts", file), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return sd
}

const lightsMD = "---\nname: test-lights\ndescription: Turn the test lights on or off.\n---\nRun lights.sh with [\"on\"] or [\"off\"].\n"

// lightsScript records its arguments in calls, next to the skill.
const lightsScript = "#!/bin/sh\necho \"$@\" >> calls\n"

func TestSkillDir(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "test-lights", lightsMD, nil)
	writeSkill(t, dir, "weather", "---\ndescription: Say the weather.\n---\nBody.", nil) // name from the directory
	writeSkill(t, dir, "broken", "no front matter", nil)
	writeSkill(t, dir, "no-skill-md", "", nil)
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("not a skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := skillDir{dir: dir, log: slog.New(slog.DiscardHandler)}
	skills, err := d.load()
	if err != nil {
		t.Fatal(err)
	}
	want := []agentSkill{
		{Name: "test-lights", Description: "Turn the test lights on or off.", Content: `Run lights.sh with ["on"] or ["off"].`, Dir: filepath.Join(dir, "test-lights")},
		{Name: "weather", Description: "Say the weather.", Content: "Body.", Dir: filepath.Join(dir, "weather")},
	}
	if len(skills) != len(want) || skills[0] != want[0] || skills[1] != want[1] {
		t.Errorf("skills\n%+v\nwant\n%+v", skills, want)
	}
	if _, err := d.get("broken"); err == nil {
		t.Error("got a broken skill")
	}
	if skills, err := (skillDir{dir: filepath.Join(dir, "missing")}).load(); err != nil || len(skills) != 0 {
		t.Errorf("a missing directory gave %v, %v", skills, err)
	}
}

func TestSkillTool(t *testing.T) {
	dir := t.TempDir()
	sd := writeSkill(t, dir, "test-lights", lightsMD, map[string]string{
		"lights.sh": lightsScript,
		"fail.sh":   "#!/bin/sh\necho 'Error: no token' >&2\nexit 3\n",
		"slow.sh":   "#!/bin/sh\nsleep 10\n",
	})
	if err := os.WriteFile(filepath.Join(sd, "scripts", "data.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "secret.sh"), []byte("#!/bin/sh\necho no\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := readSkill(sd)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.scripts(), ","); got != "fail.sh,lights.sh,slow.sh" {
		t.Errorf("scripts %s", got)
	}
	st := &skillTool{skill: s, timeout: 200 * time.Millisecond}
	info, err := st.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "test-lights" || !strings.HasPrefix(info.Desc, "Turn the test lights on or off.") {
		t.Errorf("info %+v", info)
	}
	run := func(args string) (string, error) { return st.InvokableRun(context.Background(), args) }

	out, err := run(`{"script":"lights.sh","args":["on","now"]}`)
	if err != nil || out != "exit code 0\n(no output)" {
		t.Errorf("lights.sh: %q, %v", out, err)
	}
	if calls, _ := os.ReadFile(filepath.Join(sd, "calls")); string(calls) != "on now\n" {
		t.Errorf("the script got %q", calls)
	}
	if out, err := run(`{"script":"fail.sh"}`); err != nil || out != "exit code 3\nError: no token" {
		t.Errorf("fail.sh: %q, %v", out, err)
	}
	if _, err := run(`{"script":"slow.sh"}`); err == nil || !strings.Contains(err.Error(), "didn't finish") {
		t.Errorf("slow.sh: %v", err)
	}
	for _, args := range []string{
		`{"args":["on"]}`, // which script?
		`{"script":"../secret.sh"}`,
		`{"script":"/bin/sh","args":["-c","echo hi"]}`,
		`{"script":".."}`,
		`{"script":"missing.sh"}`,
		`{"script":"data.txt"}`,
		`not json`,
	} {
		if out, err := run(args); err == nil {
			t.Errorf("%s ran: %q", args, out)
		}
	}
}

// TestSkillToolOneScript checks that a skill with one script takes only its
// arguments.
func TestSkillToolOneScript(t *testing.T) {
	sd := writeSkill(t, t.TempDir(), "test-lights", lightsMD, map[string]string{"lights.sh": lightsScript})
	s, err := readSkill(sd)
	if err != nil {
		t.Fatal(err)
	}
	st := &skillTool{skill: s}
	info, err := st.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := js.Properties.Get("script"); ok {
		t.Error("a one-script skill asks for the script")
	}
	if out, err := st.InvokableRun(context.Background(), `{"args":["off"]}`); err != nil || !strings.HasPrefix(out, "exit code 0") {
		t.Errorf("%q, %v", out, err)
	}
	if calls, _ := os.ReadFile(filepath.Join(sd, "calls")); string(calls) != "off\n" {
		t.Errorf("the script got %q", calls)
	}
}

func TestSkillNames(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "a", "---\nname: office lights\ndescription: x\n---\n", nil)
	writeSkill(t, dir, "b", "---\nname: dup\ndescription: x\n---\n", nil)
	writeSkill(t, dir, "c", "---\nname: dup\ndescription: y\n---\n", nil)
	skills, err := (skillDir{dir: dir, log: slog.New(slog.DiscardHandler)}).load()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "dup" || skills[0].Description != "x" {
		t.Errorf("skills %+v", skills)
	}
}

func toolCall(id, name, args string) *schema.Message {
	i := 0
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
		Index: &i, ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args},
	}}}
}

// TestSkillTurn runs "turn the lights on" through a model that calls the
// skill's tool, then answers.
func TestSkillTurn(t *testing.T) {
	dir := t.TempDir()
	sd := writeSkill(t, dir, "test-lights", lightsMD, map[string]string{"lights.sh": lightsScript})
	m := &fakeModel{turns: [][]*schema.Message{
		{content("Okay. "), toolCall("call_1", "test-lights", `{"args":["on"]}`)},
		{content("The lights "), content("are on.")},
	}}
	runner, err := newRunner(context.Background(), m, "Be brief.", dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a := NewAgent(runner, slog.New(slog.DiscardHandler))
	out := &recorder{ctx: context.Background()}
	if err := a.respond(context.Background(), talkie.Turn{Session: "s1", Text: "Turn the lights on."}, out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.text(), "Okay.\nThe lights are on."; got != want {
		t.Errorf("spoke %q, want %q", got, want)
	}
	if calls, _ := os.ReadFile(filepath.Join(sd, "calls")); string(calls) != "on\n" {
		t.Errorf("the script got %q", calls)
	}
	if m.calls() != 2 {
		t.Fatalf("%d model calls", m.calls())
	}
	system := m.input(0)[0].Content
	for _, want := range []string{"Be brief.", "- test-lights: Turn the test lights on or off.", "## Skill test-lights", `Run lights.sh with ["on"]`} {
		if !strings.Contains(system, want) {
			t.Errorf("the system prompt lacks %q:\n%s", want, system)
		}
	}
	if last := m.input(1)[len(m.input(1))-1]; last.Role != schema.Tool || !strings.HasPrefix(last.Content, "exit code 0") {
		t.Errorf("tool result %+v", last)
	}
	// The history keeps the tool calls and results, then what was said.
	if err := a.respond(context.Background(), talkie.Turn{Session: "s1", Text: "Thanks."}, &recorder{ctx: context.Background()}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, msg := range m.input(2)[1:] {
		line := string(msg.Role) + ": " + msg.Content
		for _, c := range msg.ToolCalls {
			line += " [" + c.ID + " " + c.Function.Name + "]"
		}
		if msg.Role == schema.Tool {
			line = "tool " + msg.ToolCallID
		}
		got = append(got, line)
	}
	want := []string{
		"user: Turn the lights on.",
		"assistant:  [call_1 test-lights]", "tool call_1",
		"assistant: Okay.\nThe lights are on.",
		"user: Thanks.",
	}
	if !equal(got, want) {
		t.Errorf("history\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestNoSpeechAfterToolCall drops what a model says after a tool call in
// the same message: it can't know the result yet.
func TestNoSpeechAfterToolCall(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "test-lights", lightsMD, map[string]string{"lights.sh": lightsScript})
	m := &fakeModel{turns: [][]*schema.Message{
		{content("Okay. "), toolCall("call_1", "test-lights", `{"args":["on"]}`), content("\nThe lights are on, I bet.")},
		{content("The lights are on.")},
	}}
	runner, err := newRunner(context.Background(), m, "Be brief.", dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	out := &recorder{ctx: context.Background()}
	if err := NewAgent(runner, slog.New(slog.DiscardHandler)).respond(context.Background(), talkie.Turn{Session: "s1", Text: "Lights on."}, out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.text(), "Okay.\nThe lights are on."; got != want {
		t.Errorf("spoke %q, want %q", got, want)
	}
}

// TestSkillToolErrorsReachTheModel checks that a failing tool call becomes a
// result the model answers from, not a failed turn.
func TestSkillToolErrorsReachTheModel(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "test-lights", lightsMD, nil)
	m := &fakeModel{turns: [][]*schema.Message{
		{toolCall("call_1", "test-lights", `{"script":"gone.sh"}`)},
		{toolCall("call_2", "made_up_tool", `{}`)},
		{content("Sorry, that didn't work.")},
	}}
	runner, err := newRunner(context.Background(), m, "Be brief.", dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	out := &recorder{ctx: context.Background()}
	if err := NewAgent(runner, slog.New(slog.DiscardHandler)).respond(context.Background(), talkie.Turn{Session: "s1", Text: "Lights on."}, out); err != nil {
		t.Fatal(err)
	}
	if out.text() != "Sorry, that didn't work." {
		t.Errorf("spoke %q", out.text())
	}
	var errs []string
	for _, msg := range m.input(2) {
		if msg.Role == schema.Tool {
			errs = append(errs, msg.Content)
		}
	}
	if len(errs) != 2 || !strings.Contains(errs[0], `error: the skill has no script "gone.sh"`) || !strings.Contains(errs[1], "made_up_tool") {
		t.Errorf("tool results %q", errs)
	}
}

func TestNoSkillsNoPrompt(t *testing.T) {
	m := &fakeModel{chunks: []*schema.Message{content("Hi.")}}
	runner, err := newRunner(context.Background(), m, "Be brief.", t.TempDir(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := NewAgent(runner, slog.New(slog.DiscardHandler)).respond(context.Background(), talkie.Turn{Session: "s1", Text: "Hi."}, &recorder{ctx: context.Background()}); err != nil {
		t.Fatal(err)
	}
	if system := m.input(0)[0].Content; system != "Be brief." {
		t.Errorf("system prompt %q", system)
	}
}

// fakeToolModel records the tools it is given with each call.
type fakeToolModel struct {
	*fakeModel
	mu    sync.Mutex
	tools [][]string
}

func (f *fakeToolModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	var names []string
	for _, ti := range model.GetCommonOptions(nil, opts...).Tools {
		names = append(names, ti.Name)
	}
	f.mu.Lock()
	f.tools = append(f.tools, names)
	f.mu.Unlock()
	return f.fakeModel.Stream(ctx, input, opts...)
}

func (f *fakeToolModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}

// TestSkillsReadEachTurn adds a skill to an empty skills directory between
// turns: the next turn has it, without a restart.
func TestSkillsReadEachTurn(t *testing.T) {
	dir := t.TempDir()
	m := &fakeToolModel{fakeModel: &fakeModel{chunks: []*schema.Message{content("Hi.")}}}
	runner, err := newRunner(context.Background(), m, "Be brief.", dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a := NewAgent(runner, slog.New(slog.DiscardHandler))
	say := func(text string) {
		t.Helper()
		if err := a.respond(context.Background(), talkie.Turn{Session: "s1", Text: text}, &recorder{ctx: context.Background()}); err != nil {
			t.Fatal(err)
		}
	}
	say("Hi.")
	writeSkill(t, dir, "test-lights", lightsMD, nil)
	say("And now?")
	if system := m.input(0)[0].Content; system != "Be brief." {
		t.Errorf("with no skills, the system prompt is %q", system)
	}

	if system := m.input(1)[0].Content; !strings.Contains(system, "## Skill test-lights") {
		t.Errorf("the new skill isn't in the system prompt:\n%s", system)
	}
	if len(m.tools[0]) != 0 {
		t.Errorf("with no skills, the tools are %v", m.tools[0])
	}
	if strings.Join(m.tools[1], ",") != "test-lights" {
		t.Errorf("with a skill, the tools are %v", m.tools[1])
	}
}
