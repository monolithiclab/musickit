package match

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/monolithiclab/musickit/internal/applemusic"
	"github.com/monolithiclab/musickit/internal/tracklist"
)

func track(artist, title, album string) applemusic.Track {
	return applemusic.Track{
		ID:   "id-" + title,
		Type: "songs",
		Attributes: applemusic.TrackAttributes{
			Name:       title,
			ArtistName: artist,
			AlbumName:  album,
		},
	}
}

func want(artist, title, hint string) tracklist.Entry {
	return tracklist.Entry{Artist: artist, Title: title, Hint: hint, Line: artist + " - " + title}
}

func TestNormalise(t *testing.T) {
	cases := map[string]string{
		"L'Impératrice":        "l imperatrice",
		"L Imperatrice":        "l imperatrice",
		"Sigur Rós":            "sigur ros",
		"Simon & Garfunkel":    "simon and garfunkel",
		"  Björk  ":            "bjork",
		"Motörhead (Remaster)": "motorhead remaster",
		"Blue Öyster Cult":     "blue oyster cult",
		"Æther/Œuvre":          "aether oeuvre",
	}
	for in, expected := range cases {
		if got := Normalise(in); got != expected {
			t.Errorf("Normalise(%q) = %q, want %q", in, got, expected)
		}
	}
}

func TestScoreRanking(t *testing.T) {
	asked := want("Danger", "4:30", "")
	studio := track("Danger", "4:30", "09/14 2007")
	live := track("Danger", "4:30 (Live)", "Live in Paris")
	karaoke := track("Karaoke Crew", "4:30 (In the Style of Danger)", "Karaoke Hits")

	studioScore, liveScore, karaokeScore := Score(studio, asked), Score(live, asked), Score(karaoke, asked)

	if studioScore < MinScore {
		t.Errorf("the studio take scored %d, below MinScore %d", studioScore, MinScore)
	}
	if liveScore >= studioScore {
		t.Errorf("live (%d) outranked the studio take (%d)", liveScore, studioScore)
	}
	if karaokeScore >= MinScore {
		t.Errorf("karaoke scored %d, at or above MinScore %d — it would be added silently", karaokeScore, MinScore)
	}
}

func TestScoreHonoursHint(t *testing.T) {
	asked := want("Moloko", "Sing It Back", "Boris Dlugosch")
	album := track("Moloko", "Sing It Back", "Things to Make and Do")
	hinted := track("Moloko", "Sing It Back", "Boris Dlugosch Mix")

	if Score(hinted, asked) <= Score(album, asked) {
		t.Errorf("the hinted version (%d) did not beat the album version (%d)",
			Score(hinted, asked), Score(album, asked))
	}
}

func TestScoreFoldsAccents(t *testing.T) {
	asked := want("L'Imperatrice", "Peur des Filles", "")
	accented := track("L'Impératrice", "Peur des Filles", "Matahari")
	if got := Score(accented, asked); got < MinScore {
		t.Errorf("accented artist scored %d, below MinScore %d", got, MinScore)
	}
}

func TestScoreRejectsWrongArtist(t *testing.T) {
	asked := want("Phoenix", "If I Ever Feel Better", "")
	impostor := track("The Karaoke Channel", "If I Ever Feel Better", "Sing Along")
	if got := Score(impostor, asked); got >= MinScore {
		t.Errorf("impostor scored %d, at or above MinScore %d", got, MinScore)
	}
}

// TestScoreAcceptsReorderedCredits is drawn from the real list: Apple files
// "Lindstrøm & Christabelle" under "Christabelle & Lindstrøm".
func TestScoreAcceptsReorderedCredits(t *testing.T) {
	asked := want("Lindstrøm & Christabelle", "Baby Can't Stop", "")
	reversed := track("Christabelle & Lindstrøm", "Baby Can't Stop", "Real Life Is No Cool")
	if got := Score(reversed, asked); got < MinScore {
		t.Errorf("reordered credit scored %d, below MinScore %d", got, MinScore)
	}
}

func TestScoreAcceptsExtendedCredits(t *testing.T) {
	asked := want("Daft Punk", "Get Lucky", "")
	credited := track("Daft Punk, Pharrell Williams & Nile Rodgers", "Get Lucky", "Random Access Memories")
	if got := Score(credited, asked); got < MinScore {
		t.Errorf("extended credit scored %d, below MinScore %d", got, MinScore)
	}
}

// TestScoreIgnoresJoiningWords: "The" alone must not make two acts look alike.
func TestScoreIgnoresJoiningWords(t *testing.T) {
	asked := want("The Beatles", "Yesterday", "")
	other := track("The Rolling Stones", "Yesterday", "Some Compilation")
	if got := Score(other, asked); got >= MinScore {
		t.Errorf("a different band sharing only \"The\" scored %d, at or above MinScore %d", got, MinScore)
	}
}

func TestArtistOverlap(t *testing.T) {
	cases := []struct {
		want, got      string
		share          float64
		wantComparable bool
	}{
		{"lindstrom and christabelle", "christabelle and lindstrom", 1, true},
		{"daft punk", "daft punk pharrell williams and nile rodgers", 1, true},
		{"simon and garfunkel", "paul simon", 0.5, true},
		{"the beatles", "the rolling stones", 0, true},
		{"the", "the the", 0, false},
	}
	for _, c := range cases {
		share, known := artistOverlap(c.want, c.got)
		if share != c.share || known != c.wantComparable {
			t.Errorf("artistOverlap(%q, %q) = %v, %v; want %v, %v",
				c.want, c.got, share, known, c.share, c.wantComparable)
		}
	}
}

// stubSearcher answers from a fixed table and counts calls.
type stubSearcher struct {
	byTerm map[string][]applemusic.Track
	err    error
	calls  atomic.Int64
}

func (s *stubSearcher) SearchSongs(_ context.Context, term string, _ int) ([]applemusic.Track, error) {
	s.calls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return s.byTerm[term], nil
}

func TestResolvePreservesOrder(t *testing.T) {
	entries := []tracklist.Entry{
		want("Blur", "Girls and Boys", ""),
		want("Nobody", "Nothing at All", ""),
		want("Air", "Sexy Boy", ""),
		{Line: "no separator here", Title: "no separator here", Unparsed: true},
	}
	searcher := &stubSearcher{byTerm: map[string][]applemusic.Track{
		"Blur Girls and Boys": {track("Blur", "Girls and Boys", "Parklife")},
		"Air Sexy Boy":        {track("Air", "Sexy Boy", "Moon Safari")},
	}}

	results := Resolve(context.Background(), searcher, entries, 3)
	if len(results) != len(entries) {
		t.Fatalf("got %d results, want %d", len(results), len(entries))
	}
	if !results[0].Found || results[0].Track.Attributes.Name != "Girls and Boys" {
		t.Errorf("result 0 = %+v, want the Blur track", results[0])
	}
	if results[1].Found {
		t.Errorf("result 1 matched something: %+v", results[1])
	}
	if !results[2].Found || results[2].Track.Attributes.Name != "Sexy Boy" {
		t.Errorf("result 2 = %+v, want the Air track", results[2])
	}
	if results[3].Err == nil {
		t.Error("an unparsed line should carry an error, not search")
	}
	// The unparsed line must not cost a search.
	if got := searcher.calls.Load(); got != 3 {
		t.Errorf("made %d searches, want 3", got)
	}
}

func TestResolveReportsSearchErrors(t *testing.T) {
	searcher := &stubSearcher{err: errors.New("network down")}
	results := Resolve(context.Background(), searcher, []tracklist.Entry{want("Blur", "Song 2", "")}, 1)
	if len(results) != 1 || results[0].Found {
		t.Fatalf("unexpected results: %+v", results)
	}
	if results[0].Err == nil {
		t.Error("the search error was swallowed")
	}
}

func TestResolveStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	entries := make([]tracklist.Entry, 20)
	for i := range entries {
		entries[i] = want("Artist", "Title", "")
	}
	results := Resolve(ctx, &stubSearcher{}, entries, 2)
	if len(results) != len(entries) {
		t.Fatalf("got %d results, want %d", len(results), len(entries))
	}
	if results[len(results)-1].Err == nil {
		t.Error("the last entry should carry the cancellation")
	}
}

func TestEqual(t *testing.T) {
	if !Equal("L'Impératrice", "Peur des Filles", "L Imperatrice", "peur des filles") {
		t.Error("Equal should fold case and accents")
	}
	if Equal("Blur", "Song 2", "Blur", "Girls and Boys") {
		t.Error("Equal matched two different songs")
	}
}
