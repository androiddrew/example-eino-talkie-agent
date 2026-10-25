package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Where things are, following the XDG Base Directory spec:
//
//	$XDG_CONFIG_HOME/eino-talkie-agent/config.yaml  settings (-config)
//	$XDG_CONFIG_HOME/eino-talkie-agent/skills/      skills (-skills)
//	$XDG_DATA_HOME/eino-talkie-agent/               ONNX Runtime and the models (-data)
//
// The data directory has the layout talkie-core's get-assets.sh writes
// (onnxruntime/lib, parakeet, kokoro), and -ort, -parakeet and -kokoro
// override each part. A setting comes from, in order: its flag, its
// environment variable (EINO_TALKIE_ and the flag's name, such as
// EINO_TALKIE_CUDA), config.yaml, then its default.

const appName = "eino-talkie-agent"

// xdgDir is the XDG base directory in env, or home/fallback when it is unset
// or relative, as the spec says to ignore relative paths.
func xdgDir(env, fallback string) string {
	if dir := os.Getenv(env); filepath.IsAbs(dir) {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, fallback)
}

func configDir() string { return appDir("XDG_CONFIG_HOME", ".config") }
func dataDir() string   { return appDir("XDG_DATA_HOME", ".local/share") }

func appDir(env, fallback string) string {
	dir := xdgDir(env, fallback)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, appName)
}

// defaultConfigFile is config.yaml in the config directory.
func defaultConfigFile() string {
	if dir := configDir(); dir != "" {
		return filepath.Join(dir, "config.yaml")
	}
	return ""
}

// defaultSkillsDir is skills in the config directory.
func defaultSkillsDir() string {
	if dir := configDir(); dir != "" {
		return filepath.Join(dir, "skills")
	}
	return ""
}

// envAliases are older environment variables for some flags, read after
// their EINO_TALKIE_ ones.
var envAliases = map[string]string{
	"base-url": "OPENAI_BASE_URL",
	"model":    "OPENAI_API_MODEL",
	"api-key":  "OPENAI_API_KEY",
	"ort":      "TALKIE_ORT",
}

// envNames are the environment variables for a flag: EINO_TALKIE_ and its
// name in upper case with _ for -, then its alias.
func envNames(name string) []string {
	names := []string{"EINO_TALKIE_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))}
	if alias := envAliases[name]; alias != "" {
		names = append(names, alias)
	}
	return names
}

// applyEnv sets the flags not set on the command line from their environment
// variables, and returns the names of the flags set either way, which
// config.yaml then leaves alone.
func applyEnv(flags *flag.FlagSet) (map[string]bool, error) {
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var err error
	flags.VisitAll(func(f *flag.Flag) {
		if set[f.Name] || err != nil || f.Name == "install-skills" || f.Name == "train" {
			return
		}
		for _, env := range envNames(f.Name) {
			if v := os.Getenv(env); v != "" {
				if e := f.Value.Set(v); e != nil {
					err = fmt.Errorf("%s: %w", env, e)
				}
				set[f.Name] = true
				return
			}
		}
	})
	return set, err
}

// pathFlags are the flags whose values may start with ~/.
var pathFlags = []string{"config", "data", "skills", "ort", "parakeet", "kokoro", "key"}

// applyConfig sets flags from the YAML file at path, whose keys are flag
// names, except those already set, on the command line or by their
// environment variables. A missing file is an error only when explicit, since
// the default one is optional.
func applyConfig(flags *flag.FlagSet, path string, explicit bool, set map[string]bool) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return nil
	}
	if err != nil {
		return err
	}
	var settings map[string]any
	if err := yaml.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for name, value := range settings {
		if flags.Lookup(name) == nil || name == "config" || name == "install-skills" || name == "train" {
			return fmt.Errorf("%s: %q isn't a setting (see -h)", path, name)
		}
		if set[name] {
			continue
		}
		switch value.(type) {
		case map[string]any, []any:
			return fmt.Errorf("%s: %s must be a single value", path, name)
		case nil:
			value = ""
		}
		if err := flags.Set(name, fmt.Sprint(value)); err != nil {
			return fmt.Errorf("%s: %s: %w", path, name, err)
		}
	}
	return nil
}

// expandHome replaces a leading ~/ in the path flags with the home directory.
func expandHome(flags *flag.FlagSet) error {
	home, err := os.UserHomeDir()
	for _, name := range pathFlags {
		f := flags.Lookup(name)
		if f == nil {
			continue
		}
		rest, ok := strings.CutPrefix(f.Value.String(), "~/")
		if !ok {
			continue
		}
		if err != nil {
			return fmt.Errorf("-%s: %w", name, err)
		}
		if err := f.Value.Set(filepath.Join(home, rest)); err != nil {
			return err
		}
	}
	return nil
}

// assets are the paths talkie needs.
type assets struct {
	ORT, Parakeet, Kokoro string
	INT8                  bool
}

// resolveAssets fills in what the flags leave empty from the data
// directory. ONNX Runtime falls back to the OS loader's libonnxruntime.so,
// for one installed system-wide. Parakeet uses its INT8 files when the FP32
// ones aren't there, since get-assets.sh downloads only INT8 by default.
func resolveAssets(data string, a assets) (assets, error) {
	if a.ORT == "" {
		a.ORT = "libonnxruntime.so"
		if lib := filepath.Join(data, "onnxruntime", "lib", "libonnxruntime.so"); data != "" && exists(lib) {
			a.ORT = lib
		}
	}
	if a.Parakeet == "" && data != "" {
		a.Parakeet = filepath.Join(data, "parakeet")
	}
	if a.Kokoro == "" && data != "" {
		a.Kokoro = filepath.Join(data, "kokoro")
	}
	if !a.INT8 && !exists(filepath.Join(a.Parakeet, "encoder-model.onnx")) && exists(filepath.Join(a.Parakeet, "encoder-model.int8.onnx")) {
		a.INT8 = true
	}
	for _, need := range []string{
		filepath.Join(a.Parakeet, "nemo128.onnx"),
		filepath.Join(a.Kokoro, "kokoro-v1.0.onnx"),
		filepath.Join(a.Kokoro, "voices-v1.0.bin"),
	} {
		if !exists(need) {
			return a, fmt.Errorf("%s is missing: download the models into %s with talkie-core's get-assets.sh -d (see the README), or set -data, -parakeet and -kokoro", need, data)
		}
	}
	return a, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
}
