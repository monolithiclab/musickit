package tracklist

import (
	"bytes"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	source := `# a comment
Phoenix - If I Ever Feel Better

Moloko - Sing It Back | Boris Dlugosch
   Blur - Girls and Boys
just a title
Underworld - Two Months Off|A Hundred Days Off
`
	entries, err := Parse(strings.NewReader(source))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := []Entry{
		{Line: "Phoenix - If I Ever Feel Better", Num: 2, Artist: "Phoenix", Title: "If I Ever Feel Better"},
		{Line: "Moloko - Sing It Back | Boris Dlugosch", Num: 4, Artist: "Moloko", Title: "Sing It Back", Hint: "Boris Dlugosch"},
		{Line: "Blur - Girls and Boys", Num: 5, Artist: "Blur", Title: "Girls and Boys"},
		{Line: "just a title", Num: 6, Title: "just a title", Unparsed: true},
		{Line: "Underworld - Two Months Off|A Hundred Days Off", Num: 7, Artist: "Underworld", Title: "Two Months Off", Hint: "A Hundred Days Off"},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i, w := range want {
		if entries[i] != w {
			t.Errorf("entry %d:\n got %+v\nwant %+v", i, entries[i], w)
		}
	}
}

func TestParseSkipsBlankAndComments(t *testing.T) {
	entries, err := Parse(strings.NewReader("\n\n#only comments\n   \n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want none: %+v", len(entries), entries)
	}
}

func TestParseHandlesCRLF(t *testing.T) {
	entries, err := Parse(strings.NewReader("Blur - Song 2\r\nPhoenix - 1901\r\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Title != "Song 2" {
		t.Errorf("title = %q, want %q — the \\r leaked through", entries[0].Title, "Song 2")
	}
}

func TestEntryStringRoundTrips(t *testing.T) {
	for _, line := range []string{
		"Phoenix - If I Ever Feel Better",
		"Moloko - Sing It Back | Boris Dlugosch",
	} {
		entry, ok := ParseLine(line)
		if !ok {
			t.Fatalf("ParseLine(%q) skipped the line", line)
		}
		if got := entry.String(); got != line {
			t.Errorf("round trip: got %q, want %q", got, line)
		}
	}
}

func TestParseArgsNumbersFromOne(t *testing.T) {
	entries := ParseArgs([]string{"Blur - Song 2", "#skipped", "Air - La Femme d'Argent"})
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Num != 1 || entries[1].Num != 3 {
		t.Errorf("line numbers = %d, %d; want 1, 3", entries[0].Num, entries[1].Num)
	}
}

func TestWriteProducesParsableOutput(t *testing.T) {
	entries := []Entry{
		{Artist: "Daft Punk", Title: "Get Lucky"},
		{Artist: "Air", Title: "Sexy Boy"},
	}
	var buf bytes.Buffer
	if err := Write(&buf, entries); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != "Daft Punk - Get Lucky\nAir - Sexy Boy\n" {
		t.Fatalf("Write produced %q", got)
	}

	// The point of the format: export output is import input.
	back, err := Parse(&buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(back) != 2 || back[0].Artist != "Daft Punk" || back[1].Title != "Sexy Boy" {
		t.Errorf("round trip lost data: %+v", back)
	}
}

func TestFormat(t *testing.T) {
	if got := Format("Blur", "Song 2"); got != "Blur - Song 2" {
		t.Errorf("Format = %q", got)
	}
}
