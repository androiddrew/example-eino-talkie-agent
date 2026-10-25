package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	talkie "github.com/androiddrew/talkie-core"
)

// trainRoom measures the room through cfg's devices, as talkie-demo -train
// does, and saves the profile where talkie.Run looks for it: the background
// noise, the speaker's echo, the echo canceller while Kokoro speaks, and the
// wake word in the user's voice. Each step waits for Enter on in.
func trainRoom(ctx context.Context, cfg talkie.Config, in io.Reader, out io.Writer) error {
	lines := bufio.NewScanner(in)
	p, err := talkie.Train(ctx, cfg, func(ctx context.Context, s talkie.Step) error {
		fmt.Fprintf(out, "\n%s\nPress Enter when ready. ", s.Instruction)
		if !lines.Scan() {
			return errors.New("training needs a terminal to press Enter in: run it interactively (docker run -it)")
		}
		return ctx.Err()
	})
	if err != nil {
		return err
	}
	dir, err := talkie.DefaultProfileDir()
	if err != nil {
		return err
	}
	path := talkie.ProfilePath(dir, p.Input, p.Output)
	if err := talkie.SaveProfile(path, p); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nMicrophone: %s\nSpeaker:    %s\n", p.Input, p.Output)
	fmt.Fprintf(out, "Room noise: %.0f dBFS\n", p.NoiseDBFS)
	if p.Echo.Heard {
		fmt.Fprintf(out, "Latency:    %v (measured %v to %v; talkie will assume %v)\n",
			p.Latency.Median.Round(time.Millisecond), p.Latency.Min.Round(time.Millisecond), p.Latency.Max.Round(time.Millisecond), p.Latency.Assumed().Round(time.Millisecond))
		fmt.Fprintf(out, "Echo:       %+.1f dB\n", p.Echo.GainDB)
	}
	if p.Canceller != nil {
		fmt.Fprintf(out, "Canceller:  learned the room, %.1f dB of echo removed\n", p.Canceller.ERLEDB)
	}
	if w := p.Wake; w != nil {
		scores := make([]string, len(w.Positives))
		for i, v := range w.Positives {
			scores[i] = fmt.Sprintf("%.2f", v)
		}
		fmt.Fprintf(out, "Wake word:  %q scored %s; your other speech at most %.2f\n", w.Keyword, strings.Join(scores, ", "), w.NegativeMax)
		if w.Threshold > 0 {
			fmt.Fprintf(out, "            talkie will wake at %.2f\n", w.Threshold)
		}
		if w.Advice != "" {
			fmt.Fprintf(out, "\nAdvice: %s\n", w.Advice)
		}
	}
	if p.Echo.Advice != "" {
		fmt.Fprintf(out, "\nAdvice: %s\n", p.Echo.Advice)
	}
	fmt.Fprintf(out, "\nSaved %s\n", path)
	return nil
}
