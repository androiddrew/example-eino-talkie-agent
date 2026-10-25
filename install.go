package main

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// bundled holds the repository's skills, so an installed binary can write
// them out with -install-skills.
//
//go:embed skills
var bundled embed.FS

// installSkills copies the bundled skills into dir, reporting each to w. A
// skill already in dir is kept as it is, edits included. Embedded files have
// no modes, so files in a skill's scripts directory are made executable.
func installSkills(dir string, w io.Writer) error {
	entries, err := fs.ReadDir(bundled, "skills")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		dst := filepath.Join(dir, e.Name())
		if exists(dst) {
			fmt.Fprintf(w, "kept      %s (already there)\n", dst)
			continue
		}
		// Write into a temporary directory first, so an interrupted
		// install doesn't leave half a skill that the next one keeps.
		tmp, err := os.MkdirTemp(dir, "."+e.Name()+"-")
		if err != nil {
			return err
		}
		if err := copySkill(path.Join("skills", e.Name()), tmp); err != nil {
			return errors.Join(err, os.RemoveAll(tmp))
		}
		if err := os.Chmod(tmp, 0o755); err != nil {
			return errors.Join(err, os.RemoveAll(tmp))
		}
		if err := os.Rename(tmp, dst); err != nil {
			return errors.Join(err, os.RemoveAll(tmp))
		}
		fmt.Fprintf(w, "installed %s\n", dst)
	}
	return nil
}

// copySkill writes the bundled skill at src into dst.
func copySkill(src, dst string) error {
	return fs.WalkDir(bundled, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, src), "/")
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := bundled.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if path.Base(path.Dir(p)) == "scripts" {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
}
