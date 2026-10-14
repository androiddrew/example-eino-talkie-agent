package main

import (
	"strings"
	"testing"
)

func TestSpeechFilter(t *testing.T) {
	for _, c := range []struct {
		name   string
		deltas []string
		want   string
	}{
		{"plain", []string{"The capital ", "is Paris. ", "It is lovely."}, "The capital is Paris. It is lovely."},
		{"markup left to Kokoro", []string{"**Paris** is the `capital`."}, "**Paris** is the `capital`."},
		{"whitespace", []string{"Paris   is\t\tlovely.  ", "\n\n\nRome too."}, "Paris is lovely.\nRome too."},
		{"numbered list", []string{"Cities:\n1. Paris\n2. Rome\n10) Oslo"}, "Cities:\nParis\nRome\nOslo"},
		{"bulleted list", []string{"Cities:\n- Paris\n* Rome\n• Oslo"}, "Cities:\nParis\nRome\nOslo"},
		{"list number split", []string{"Cities:\n", "1", ". Pa", "ris"}, "Cities:\nParis"},
		{"numbers mid-line stay", []string{"Pick 1. Or 2 - maybe."}, "Pick 1. Or 2 - maybe."},
		{"inline list after a line break", []string{"Two:", " - one"}, "Two: - one"},
		{"url", []string{"See https://example.com/a?b=c for more."}, "See for more."},
		{"url keeps the sentence end", []string{"See www.example.com. Then go."}, "See . Then go."},
		{"url split across deltas", []string{"See htt", "ps://exa", "mple.com/x now"}, "See now"},
		{"markdown link keeps its text", []string{"Read [the docs](https://go.dev/doc) today."}, "Read the docs today."},
		{"link split", []string{"Read [the do", "cs](https://go", ".dev) today."}, "Read the docs today."},
		{"code block", []string{"Run this:\n```go\nfmt.Println(1)\n", "x := 2\n```\nDone."}, "Run this:\nDone."},
		{"think block", []string{"<think>\nThe user ", "wants Paris.\n</think>\n\nParis."}, "Paris."},
		{"think split in a word", []string{"<thi", "nk>hmm</th", "ink>Paris."}, "Paris."},
		{"unicode space", []string{"Paris is lovely."}, "Paris is lovely."},
	} {
		t.Run(c.name, func(t *testing.T) {
			var f speechFilter
			var out strings.Builder
			for _, d := range c.deltas {
				out.WriteString(f.Write(d))
			}
			out.WriteString(f.Flush())
			if out.String() != c.want {
				t.Errorf("%q\nwant %q", out.String(), c.want)
			}
		})
	}
}

// TestSpeechFilterStreams checks that text is released as soon as a word is
// complete, so filtering doesn't delay speech.
func TestSpeechFilterStreams(t *testing.T) {
	var f speechFilter
	if got := f.Write("The capital is Par"); got != "The capital is" {
		t.Errorf("first delta released %q", got)
	}
	if got := f.Write("is. It"); got != " Paris." {
		t.Errorf("second delta released %q", got)
	}
	if got := f.Flush(); got != " It" {
		t.Errorf("flush released %q", got)
	}
}
