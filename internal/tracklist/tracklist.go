// Package tracklist reads and writes musickit's plain-text track list format.
//
//	One track per line: "Artist - Title"
//	An optional version hint after a pipe biases the match:
//	  Moloko - Sing It Back | Boris Dlugosch
//	Blank lines and lines starting with # are skipped.
//
// The format is the same everywhere: files, stdin and command-line arguments
// all go through Parse, so anything you can put in a file you can also pipe or
// type, and `playlist export` round-trips into `playlist add`.
package tracklist

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Entry is one parsed line.
type Entry struct {
	// Line is the input, verbatim, for reporting.
	Line string
	// Num is the 1-based line number within the source.
	Num int
	// Artist and Title are the split halves; Title holds the whole line when
	// Unparsed is set.
	Artist string
	Title  string
	// Hint is the optional text after the pipe.
	Hint string
	// Unparsed marks a line with no " - " separator.
	Unparsed bool
}

// String renders an entry back into the input format.
func (e Entry) String() string {
	var b strings.Builder
	if e.Artist != "" {
		b.WriteString(e.Artist)
		b.WriteString(" - ")
	}
	b.WriteString(e.Title)
	if e.Hint != "" {
		b.WriteString(" | ")
		b.WriteString(e.Hint)
	}
	return b.String()
}

// Format renders an artist and title as a source line.
func Format(artist, title string) string {
	return Entry{Artist: artist, Title: title}.String()
}

// Parse reads a track list. Errors come from the reader only: a line that does
// not parse is returned with Unparsed set, so one bad line does not sink the
// batch.
func Parse(r io.Reader) ([]Entry, error) {
	var entries []Entry
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for num := 1; scanner.Scan(); num++ {
		entry, ok := ParseLine(scanner.Text())
		if !ok {
			continue
		}
		entry.Num = num
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// ParseLine parses one line. ok is false for blank lines and comments.
func ParseLine(line string) (entry Entry, ok bool) {
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if line == "" || strings.HasPrefix(line, "#") {
		return Entry{}, false
	}

	body, hint := line, ""
	if before, after, found := strings.Cut(line, "|"); found {
		body = strings.TrimSpace(before)
		hint = strings.TrimSpace(after)
	}

	artist, title, found := strings.Cut(body, " - ")
	if !found {
		return Entry{Line: line, Title: body, Hint: hint, Unparsed: true}, true
	}
	return Entry{
		Line:   line,
		Artist: strings.TrimSpace(artist),
		Title:  strings.TrimSpace(title),
		Hint:   hint,
	}, true
}

// ParseArgs parses command-line arguments as source lines.
func ParseArgs(args []string) []Entry {
	var entries []Entry
	for i, arg := range args {
		entry, ok := ParseLine(arg)
		if !ok {
			continue
		}
		entry.Num = i + 1
		entries = append(entries, entry)
	}
	return entries
}

// Write renders entries back to the source format, one per line.
func Write(w io.Writer, entries []Entry) error {
	bw := bufio.NewWriter(w)
	for _, e := range entries {
		if _, err := fmt.Fprintln(bw, e.String()); err != nil {
			return err
		}
	}
	return bw.Flush()
}
