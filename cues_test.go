package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	talkie "github.com/androiddrew/talkie-core"
)

// writeWAV writes 16-bit mono PCM.
func writeWAV(t *testing.T, path string, rate int, samples []int16) {
	t.Helper()
	data := make([]byte, 44+2*len(samples))
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+2*len(samples)))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(2*rate))
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(2*len(samples)))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(s))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCues(t *testing.T) {
	off, err := loadCues("", "", "")
	if err != nil || off.Listening != nil || off.Thinking != nil || off.Idle != nil {
		t.Errorf("all off: %+v, %v", off, err)
	}

	dir := t.TempDir()
	mine := filepath.Join(dir, "thinking.wav")
	writeWAV(t, mine, 22050, []int16{0, 16384, -16384})
	c, err := loadCues("default", mine, "")
	if err != nil {
		t.Fatal(err)
	}
	builtin := talkie.DefaultCues()
	if c.Listening == nil || !slices.Equal(c.Listening.Samples, builtin.Listening.Samples) {
		t.Error("cue-listening default isn't the built-in sound")
	}
	if c.Thinking == nil || c.Thinking.Rate != 22050 || !slices.Equal(c.Thinking.Samples, []float32{0, 0.5, -0.5}) {
		t.Errorf("cue-thinking from a file: %+v", c.Thinking)
	}
	if c.Idle != nil {
		t.Error("cue-idle should be off")
	}

	bad := filepath.Join(dir, "bad.wav")
	if err := os.WriteFile(bad, []byte("not audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ idle, want string }{
		{filepath.Join(dir, "missing.wav"), "-cue-idle: open"},
		{bad, "bad.wav: talkie: not a WAV file"},
	} {
		if _, err := loadCues("", "", c.idle); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.idle, err, c.want)
		}
	}
}
