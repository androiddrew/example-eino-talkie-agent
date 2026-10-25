package main

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestXDGDirs: the XDG variables choose the directories, and relative ones
// are ignored, as the spec says.
func TestXDGDirs(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	for _, c := range []struct {
		config, data             string
		wantConfig, wantData     string
		wantSkills, wantConfFile string
	}{
		{"", "", "/home/u/.config/eino-talkie-agent", "/home/u/.local/share/eino-talkie-agent",
			"/home/u/.config/eino-talkie-agent/skills", "/home/u/.config/eino-talkie-agent/config.yaml"},
		{"/etc/x", "/data", "/etc/x/eino-talkie-agent", "/data/eino-talkie-agent",
			"/etc/x/eino-talkie-agent/skills", "/etc/x/eino-talkie-agent/config.yaml"},
		{"rel", "rel", "/home/u/.config/eino-talkie-agent", "/home/u/.local/share/eino-talkie-agent",
			"/home/u/.config/eino-talkie-agent/skills", "/home/u/.config/eino-talkie-agent/config.yaml"},
	} {
		t.Setenv("XDG_CONFIG_HOME", c.config)
		t.Setenv("XDG_DATA_HOME", c.data)
		if got := configDir(); got != c.wantConfig {
			t.Errorf("XDG_CONFIG_HOME=%q: config %q, want %q", c.config, got, c.wantConfig)
		}
		if got := dataDir(); got != c.wantData {
			t.Errorf("XDG_DATA_HOME=%q: data %q, want %q", c.data, got, c.wantData)
		}
		if got := defaultSkillsDir(); got != c.wantSkills {
			t.Errorf("skills %q, want %q", got, c.wantSkills)
		}
		if got := defaultConfigFile(); got != c.wantConfFile {
			t.Errorf("config file %q, want %q", got, c.wantConfFile)
		}
	}
}

// testFlags is a flag set like main's, for applyConfig.
func testFlags(t *testing.T, args ...string) (*flag.FlagSet, map[string]any) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	values := map[string]any{
		"base-url":    fs.String("base-url", "http://default", ""),
		"model":       fs.String("model", "default-model", ""),
		"data":        fs.String("data", "/default/data", ""),
		"int8":        fs.Bool("int8", false, ""),
		"latency":     fs.Duration("latency", 0, ""),
		"temperature": fs.Float64("temperature", 0.2, ""),
		"wake":        fs.String("wake", "hey computer", ""),
		"config":      fs.String("config", "", ""),
	}
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return fs, values
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestApplyConfig: config.yaml sets any flag by name, below the command
// line and the environment.
func TestApplyConfig(t *testing.T) {
	path := writeConfig(t, `
base-url: http://from-file
model: file-model
data: /data/talkie
int8: true
latency: 150ms
temperature: 0
wake: ""
`)
	t.Setenv("OPENAI_BASE_URL", "http://from-env")
	fs, v := testFlags(t, "-model", "cli-model")
	if err := applyConfig(fs, path, true); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]any{
		"base-url":    "http://default", // the environment wins; its value is in the flag's default
		"model":       "cli-model",
		"data":        "/data/talkie",
		"int8":        true,
		"latency":     150 * time.Millisecond,
		"temperature": 0.0,
		"wake":        "",
	} {
		var got any
		switch p := v[name].(type) {
		case *string:
			got = *p
		case *bool:
			got = *p
		case *time.Duration:
			got = *p
		case *float64:
			got = *p
		}
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
}

func TestApplyConfigErrors(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{"modle: x\n", `"modle" isn't a setting`},
		{"config: other.yaml\n", `"config" isn't a setting`},
		{"model: [a, b]\n", "must be a single value"},
		{"int8: maybe\n", "int8"},
		{"model: x\n  bad indent: y\n", "yaml"},
	} {
		fs, _ := testFlags(t)
		err := applyConfig(fs, writeConfig(t, c.body), true)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %v, want one with %q", c.body, err, c.want)
		}
	}
	fs, _ := testFlags(t)
	missing := filepath.Join(t.TempDir(), "config.yaml")
	if err := applyConfig(fs, missing, false); err != nil {
		t.Errorf("missing default config: %v", err)
	}
	if err := applyConfig(fs, missing, true); err == nil {
		t.Error("missing -config: no error")
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fs, v := testFlags(t, "-data", "~/models", "-config", "/abs/config.yaml")
	if err := expandHome(fs); err != nil {
		t.Fatal(err)
	}
	if got := *v["data"].(*string); got != filepath.Join(home, "models") {
		t.Errorf("data %q", got)
	}
	if got := *v["config"].(*string); got != "/abs/config.yaml" {
		t.Errorf("config %q", got)
	}
}

// writeAssets lays out dir as get-assets.sh does, with empty files.
func writeAssets(t *testing.T, dir string, files ...string) {
	t.Helper()
	for _, f := range files {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var (
	kokoroFiles = []string{"kokoro/kokoro-v1.0.onnx", "kokoro/voices-v1.0.bin"}
	int8Files   = []string{"parakeet/nemo128.onnx", "parakeet/encoder-model.int8.onnx", "parakeet/decoder_joint-model.int8.onnx"}
	fp32Files   = []string{"parakeet/nemo128.onnx", "parakeet/encoder-model.onnx", "parakeet/encoder-model.onnx.data", "parakeet/decoder_joint-model.onnx"}
)

func TestResolveAssets(t *testing.T) {
	data := t.TempDir()
	writeAssets(t, data, slices.Concat([]string{"onnxruntime/lib/libonnxruntime.so"}, int8Files, kokoroFiles)...)
	a, err := resolveAssets(data, assets{})
	if err != nil {
		t.Fatal(err)
	}
	want := assets{ORT: filepath.Join(data, "onnxruntime/lib/libonnxruntime.so"), Parakeet: filepath.Join(data, "parakeet"), Kokoro: filepath.Join(data, "kokoro"), INT8: true}
	if a != want {
		t.Errorf("got %+v\nwant %+v", a, want)
	}

	// FP32 files, and ONNX Runtime installed system-wide.
	fp32 := t.TempDir()
	writeAssets(t, fp32, slices.Concat(fp32Files, kokoroFiles)...)
	if a, err := resolveAssets(fp32, assets{}); err != nil || a.INT8 || a.ORT != "libonnxruntime.so" {
		t.Errorf("FP32: %+v, %v", a, err)
	}

	// Each flag overrides its part of the data directory.
	other := t.TempDir()
	writeAssets(t, other, slices.Concat(int8Files, kokoroFiles)...)
	a, err = resolveAssets(data, assets{ORT: "/opt/ort/libonnxruntime.so", Parakeet: filepath.Join(other, "parakeet"), Kokoro: filepath.Join(other, "kokoro")})
	if err != nil || a.ORT != "/opt/ort/libonnxruntime.so" || a.Parakeet != filepath.Join(other, "parakeet") || a.Kokoro != filepath.Join(other, "kokoro") {
		t.Errorf("overrides: %+v, %v", a, err)
	}

	// Missing models say where they were expected and how to get them.
	_, err = resolveAssets(t.TempDir(), assets{})
	if err == nil || !strings.Contains(err.Error(), "nemo128.onnx is missing") || !strings.Contains(err.Error(), "get-assets.sh") {
		t.Errorf("missing models: %v", err)
	}
}
