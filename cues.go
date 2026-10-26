package main

import (
	"fmt"
	"os"

	talkie "github.com/androiddrew/talkie-core"
)

// cueDefault selects talkie's built-in sound for a cue.
const cueDefault = "default"

// loadCues turns the -cue-* settings into talkie's cues. Each is empty for
// none, "default" for talkie's built-in sound, or a WAV file.
func loadCues(listening, thinking, idle string) (talkie.Cues, error) {
	builtin := talkie.DefaultCues()
	var cues talkie.Cues
	for _, c := range []struct {
		flag, value string
		builtin     *talkie.Sound
		into        **talkie.Sound
	}{
		{"cue-listening", listening, builtin.Listening, &cues.Listening},
		{"cue-thinking", thinking, builtin.Thinking, &cues.Thinking},
		{"cue-idle", idle, builtin.Idle, &cues.Idle},
	} {
		switch c.value {
		case "":
		case cueDefault:
			*c.into = c.builtin
		default:
			s, err := readSound(c.value)
			if err != nil {
				return cues, fmt.Errorf("-%s: %w", c.flag, err)
			}
			*c.into = s
		}
	}
	return cues, nil
}

func readSound(path string) (*talkie.Sound, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := talkie.ReadWAV(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}
