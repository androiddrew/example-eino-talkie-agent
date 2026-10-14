package main

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// speechFilter turns a model's streamed text into text Kokoro reads well.
//
// talkie speaks with go-kokoro's text front end, which already reads
// markdown as prose (emphasis, headings, inline code, links and tables) and
// normalizes numbers, money and dates. What it reads badly, the filter
// drops:
//   - URLs, which the normalizer spells out letter by letter;
//   - fenced code blocks, whose lines talkie would speak one by one;
//   - list numbers such as "1." at the start of a line, which talkie's
//     sentence splitter would cut off as sentences of their own;
//   - <think> blocks, which some models write into the content.
//
// It also collapses runs of whitespace. Text is judged a word at a time, so
// the word after the last space of a delta waits for the next delta or Flush.
type speechFilter struct {
	pending string // the text after the last whitespace, not yet judged
	started bool   // a word has been written
	midLine bool   // a word has been seen since the last line break
	sep     string // the separator owed before the next word: "", " " or "\n"
	inCode  bool   // inside a fenced code block
	inThink bool   // inside a <think> block
}

// Write takes a delta and returns the text to speak now.
func (f *speechFilter) Write(delta string) string {
	text := f.pending + delta
	i := strings.LastIndexFunc(text, unicode.IsSpace)
	if i < 0 {
		f.pending = text
		return ""
	}
	_, size := utf8.DecodeRuneInString(text[i:])
	end := i + size
	f.pending = text[end:]
	return f.judge(text[:end])
}

// Flush returns what is left at the end of the reply.
func (f *speechFilter) Flush() string {
	text := f.pending
	f.pending = ""
	return f.judge(text)
}

// judge filters text that ends on a word boundary.
func (f *speechFilter) judge(text string) string {
	var out strings.Builder
	for len(text) > 0 {
		space := strings.IndexFunc(text, func(r rune) bool { return !unicode.IsSpace(r) })
		if space < 0 {
			space = len(text)
		}
		if gap := text[:space]; gap != "" {
			if strings.ContainsRune(gap, '\n') {
				f.sep, f.midLine = "\n", false
			} else if f.sep == "" {
				f.sep = " "
			}
		}
		text = text[space:]
		word := strings.IndexFunc(text, unicode.IsSpace)
		if word < 0 {
			word = len(text)
		}
		if w := f.word(text[:word]); w != "" {
			if f.started {
				out.WriteString(f.sep)
			}
			out.WriteString(w)
			f.started, f.sep = true, ""
		}
		text = text[word:]
	}
	return out.String()
}

var (
	listNumber = regexp.MustCompile(`^(\d+[.)]|[-*+•])$`)
	bareURL    = regexp.MustCompile(`(?i)^[(<"']*(https?://|www\.)\S*?([.,;:!?]*)$`)
	linkTarget = regexp.MustCompile(`\]\([^)\s]*\)?`)
)

// word filters one whitespace-delimited word.
func (f *speechFilter) word(w string) string {
	if w == "" {
		return ""
	}
	lineStart := !f.midLine
	f.midLine = true
	w = f.think(w)
	if lineStart && strings.HasPrefix(w, "```") {
		f.inCode = !f.inCode
		return ""
	}
	if f.inCode || w == "" {
		return ""
	}
	if lineStart && listNumber.MatchString(w) {
		f.sep = "\n" // the item is a sentence of its own
		return ""
	}
	// A markdown link, perhaps split over words: keep its text.
	w = strings.NewReplacer("[", "", "]", "").Replace(linkTarget.ReplaceAllString(w, ""))
	if m := bareURL.FindStringSubmatch(w); m != nil {
		return m[2] // keep a sentence end
	}
	return w
}

// think drops the parts of w inside <think> blocks.
func (f *speechFilter) think(w string) string {
	var out strings.Builder
	for w != "" {
		if f.inThink {
			i := strings.Index(w, "</think>")
			if i < 0 {
				return out.String()
			}
			f.inThink, w = false, w[i+len("</think>"):]
			continue
		}
		i := strings.Index(w, "<think>")
		if i < 0 {
			out.WriteString(w)
			break
		}
		out.WriteString(w[:i])
		f.inThink, w = true, w[i+len("<think>"):]
	}
	return out.String()
}
