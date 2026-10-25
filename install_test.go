package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallSkills: the bundled skills install with runnable scripts, load,
// and a second install keeps what is there.
func TestInstallSkills(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	var out strings.Builder
	if err := installSkills(dir, &out); err != nil {
		t.Fatal(err)
	}
	skills, err := skillDir{dir: dir}.load()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range skills {
		names = append(names, s.Name)
		if len(s.scripts()) == 0 {
			t.Errorf("%s: no executable scripts", s.Name)
		}
	}
	if got := strings.Join(names, ","); got != "kids-story,office-lights,web-search" {
		t.Errorf("installed %s", got)
	}
	if strings.Count(out.String(), "installed ") != 3 {
		t.Errorf("output %q", out.String())
	}
	info, err := os.Stat(filepath.Join(dir, "web-search", skillFile))
	if err != nil || info.Mode().Perm()&0o111 != 0 {
		t.Errorf("SKILL.md: %v, %v", info.Mode(), err)
	}

	edited := filepath.Join(dir, "office-lights", skillFile)
	if err := os.WriteFile(edited, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "web-search")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := installSkills(dir, &out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(edited); string(b) != "mine" {
		t.Errorf("the edited skill was replaced: %q", b)
	}
	if !strings.Contains(out.String(), "installed "+filepath.Join(dir, "web-search")) || strings.Count(out.String(), "kept ") != 2 {
		t.Errorf("second install: %q", out.String())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("left %s behind", e.Name())
		}
	}
}
