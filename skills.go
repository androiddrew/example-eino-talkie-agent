package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"gopkg.in/yaml.v3"
)

// Skills are directories in the skills directory, each with a SKILL.md: YAML
// front matter (name, description) and instructions for the model, as in the
// Agent Skills format. A skill's executables go in its scripts directory.
//
// Before each turn, the agent reads the skills and puts their instructions in
// the system prompt, so edits apply on the next turn without a restart. Each
// skill is also a tool, named after it, that runs one of the executables in
// its scripts directory with arguments, without a shell.
//
// eino's skill middleware loads a skill's instructions with a tool call
// instead, when the model decides it needs them. With qwen3.5:9b that cost a
// model round (about 2 s) and sometimes derailed it: having loaded the
// skill, it said it had done the task without running the script. Skills
// for a voice assistant are few and short, so they are all in the prompt.

// defaultSkillsDir is $XDG_CONFIG_HOME/eino-talkie-agent/skills, or
// ~/.config/eino-talkie-agent/skills.
func defaultSkillsDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "eino-talkie-agent", "skills")
}

const skillFile = "SKILL.md"

// agentSkill is a skill read from its directory.
type agentSkill struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Content     string `yaml:"-"` // the instructions after the front matter
	Dir         string `yaml:"-"`
}

// skillDir reads the skills in a directory.
type skillDir struct {
	dir string
	log *slog.Logger
}

// load reads every skill, sorted by name. A skill that can't be read is
// skipped with a warning, so one bad SKILL.md doesn't hide the rest.
func (d skillDir) load() ([]agentSkill, error) {
	entries, err := os.ReadDir(d.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var skills []agentSkill
	seen := map[string]bool{}
	for _, e := range entries {
		dir := filepath.Join(d.dir, e.Name())
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		s, err := readSkill(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil && !validName.MatchString(s.Name) {
			err = fmt.Errorf("name %q isn't letters, digits, - and _", s.Name)
		}
		if err == nil && seen[s.Name] {
			err = fmt.Errorf("another skill is named %q", s.Name)
		}
		if err != nil {
			if d.log != nil {
				d.log.Warn("skipping skill", "dir", dir, "err", err)
			}
			continue
		}
		seen[s.Name] = true
		skills = append(skills, s)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

// get reads the skill called name.
func (d skillDir) get(name string) (agentSkill, error) {
	skills, err := d.load()
	if err != nil {
		return agentSkill{}, err
	}
	for _, s := range skills {
		if s.Name == name {
			return s, nil
		}
	}
	return agentSkill{}, fmt.Errorf("no skill named %q", name)
}

// readSkill reads dir/SKILL.md. The name defaults to the directory's.
func readSkill(dir string) (agentSkill, error) {
	data, err := os.ReadFile(filepath.Join(dir, skillFile))
	if err != nil {
		return agentSkill{}, err
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return agentSkill{}, errors.New("SKILL.md doesn't start with --- front matter")
	}
	front, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return agentSkill{}, errors.New("SKILL.md's front matter has no closing ---")
	}
	var s agentSkill
	if err := yaml.Unmarshal([]byte(front), &s); err != nil {
		return agentSkill{}, fmt.Errorf("front matter: %w", err)
	}
	if s.Name == "" {
		s.Name = filepath.Base(dir)
	}
	if s.Description = strings.TrimSpace(s.Description); s.Description == "" {
		return agentSkill{}, errors.New("front matter has no description")
	}
	s.Content, s.Dir = strings.TrimSpace(body), dir
	return s, nil
}

// skillsPrompt is added to the system prompt: the skills and their
// instructions.
func skillsPrompt(skills []agentSkill) string {
	var b strings.Builder
	b.WriteString("\n\nYou have these skills:\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
	}
	b.WriteString("\nWhen the user asks what you can do or which skills you have, name them from this list in plain words. " +
		"For a request that needs a skill, call the tool named after the skill, as its instructions below say, before saying anything: " +
		"the user hears what you say before the tool runs. " +
		"Call it for every request, even one you did earlier in the conversation: saying it is done does nothing. " +
		"Say a task is done only when the tool succeeded in this turn, in one short sentence; if it failed, say so briefly.\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "\n## Skill %s\n\n%s\n", s.Name, s.Content)
	}
	return b.String()
}

// skillsHandler reads the skills in a directory before each turn and gives
// them to the agent: their instructions in the system prompt, and a tool for
// each, replacing the agent's tools.
type skillsHandler struct {
	*adk.BaseChatModelAgentMiddleware
	skills skillDir
}

func (h skillsHandler) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	skills, err := h.skills.load()
	if err != nil {
		return ctx, runCtx, fmt.Errorf("skills: %w", err)
	}
	runCtx.Tools = skillTools(skills)
	if len(skills) > 0 {
		runCtx.Instruction += skillsPrompt(skills)
	}
	return ctx, runCtx, nil
}

// skillTools returns a tool for each skill.
func skillTools(skills []agentSkill) []tool.BaseTool {
	tools := make([]tool.BaseTool, len(skills))
	for i, s := range skills {
		tools[i] = &skillTool{skill: s}
	}
	return tools
}

// validName is a tool name model APIs accept.
var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// skillTool runs a skill's scripts. Its name and description are the
// skill's.
type skillTool struct {
	skill   agentSkill
	timeout time.Duration // zero selects 30 s
}

func (t *skillTool) Info(context.Context) (*schema.ToolInfo, error) {
	params := map[string]*schema.ParameterInfo{
		"args": {Type: schema.Array, Desc: "the script's arguments, as the skill's instructions say", ElemInfo: &schema.ParameterInfo{Type: schema.String}},
	}
	if scripts := t.skill.scripts(); len(scripts) != 1 {
		params["script"] = &schema.ParameterInfo{Type: schema.String, Desc: "the script to run", Enum: scripts, Required: true}
	}
	return &schema.ToolInfo{
		Name:        t.skill.Name,
		Desc:        t.skill.Description + " Runs the skill's script and returns its exit code and output.",
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

// scripts returns the executables in the skill's scripts directory.
func (s agentSkill) scripts() []string {
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "scripts"))
	var names []string
	for _, e := range entries {
		if _, err := skillScript(s.Dir, e.Name()); err == nil {
			names = append(names, e.Name())
		}
	}
	return names
}

// maxOutput limits the output returned to the model.
const maxOutput = 2000

func (t *skillTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var in struct {
		Script string   `json:"script"`
		Args   []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &in); err != nil {
		return "", fmt.Errorf("arguments: %w", err)
	}
	s := t.skill
	if scripts := s.scripts(); in.Script == "" && len(scripts) == 1 {
		in.Script = scripts[0]
	}
	path, err := skillScript(s.Dir, in.Script)
	if err != nil {
		return "", err
	}
	timeout := t.timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, in.Args...)
	cmd.Dir = s.Dir
	// On a timeout or an interruption, kill the script and anything it
	// started, which could otherwise hold its output open.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		return "", err
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("%s didn't finish within %v", in.Script, timeout)
	}
	output := strings.TrimSpace(out.String())
	if len(output) > maxOutput {
		output = output[:maxOutput] + " [truncated]"
	}
	if output == "" {
		output = "(no output)"
	}
	return fmt.Sprintf("exit code %d\n%s", code, output), nil
}

// skillScript resolves name to an executable in dir's scripts directory.
func skillScript(dir, name string) (string, error) {
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", fmt.Errorf("script %q must be a file name in the skill's scripts directory", name)
	}
	path := filepath.Join(dir, "scripts", name)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("the skill has no script %q", name)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("script %q isn't an executable file", name)
	}
	return path, nil
}

// toolErrors turns a tool's error into a result the model can read and tell
// the user about; otherwise the error ends the agent's run. Cancellation
// still ends it.
type toolErrors struct {
	*adk.BaseChatModelAgentMiddleware
	log *slog.Logger
}

func (h toolErrors) WrapInvokableToolCall(_ context.Context, call adk.InvokableToolCallEndpoint, tc *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		h.log.Info("tool call", "tool", tc.Name, "args", args)
		result, err := call(ctx, args, opts...)
		if err != nil {
			if ctx.Err() != nil {
				return "", err
			}
			h.log.Warn("tool failed", "tool", tc.Name, "err", err)
			return "error: " + err.Error(), nil
		}
		h.log.Debug("tool result", "tool", tc.Name, "result", result)
		return result, nil
	}, nil
}
