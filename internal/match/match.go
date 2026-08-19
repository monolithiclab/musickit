// Package match resolves "Artist - Title" lines to catalogue tracks.
//
// The scoring is deliberately conservative: it reports a miss rather than
// adding the wrong track, because a wrong track lands silently in someone's
// playlist while a miss is printed and can be fixed by hand.
package match

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"

	"musickit/internal/applemusic"
	"musickit/internal/tracklist"
)

// MinScore is the score a candidate must reach to be accepted.
const MinScore = 45

// Candidates is how many search results are ranked per line.
const Candidates = 15

// DefaultWorkers is the number of concurrent searches.
const DefaultWorkers = 4

// Searcher is the slice of the Apple Music client this package needs.
type Searcher interface {
	SearchSongs(ctx context.Context, term string, limit int) ([]applemusic.Track, error)
}

// Result is the outcome for one source line.
type Result struct {
	Entry tracklist.Entry
	Track applemusic.Track
	Score int
	Found bool
	Err   error
}

// Resolve searches for every entry, preserving input order. Entries that fail
// to parse or search come back with Found false and, where relevant, Err set.
func Resolve(ctx context.Context, s Searcher, entries []tracklist.Entry, workers int) []Result {
	if workers <= 0 {
		workers = DefaultWorkers
	}
	results := make([]Result, len(entries))
	jobs := make(chan int)

	var wg sync.WaitGroup
	for range min(workers, max(len(entries), 1)) {
		wg.Go(func() {
			for i := range jobs {
				results[i] = Find(ctx, s, entries[i])
			}
		})
	}
	for i := range entries {
		select {
		case <-ctx.Done():
			// Leave the remainder as zero-value misses; the caller reports
			// ctx.Err() itself.
			close(jobs)
			wg.Wait()
			for j := i; j < len(results); j++ {
				results[j] = Result{Entry: entries[j], Err: ctx.Err()}
			}
			return results
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	return results
}

// Find searches for a single entry and returns the best candidate that clears
// MinScore.
func Find(ctx context.Context, s Searcher, e tracklist.Entry) Result {
	if e.Unparsed {
		return Result{Entry: e, Err: errUnparsed}
	}
	term := strings.TrimSpace(strings.Join([]string{e.Artist, e.Title, e.Hint}, " "))
	tracks, err := s.SearchSongs(ctx, term, Candidates)
	if err != nil {
		return Result{Entry: e, Err: err}
	}

	ranked := make([]Result, 0, len(tracks))
	for _, t := range tracks {
		ranked = append(ranked, Result{Entry: e, Track: t, Score: Score(t, e), Found: true})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })

	if len(ranked) > 0 && ranked[0].Score >= MinScore {
		return ranked[0]
	}
	return Result{Entry: e}
}

type unparsedError struct{}

func (unparsedError) Error() string { return `expected "Artist - Title"` }

var errUnparsed = unparsedError{}

var (
	nonAlnum  = regexp.MustCompile(`[^a-z0-9]+`)
	liveRe    = regexp.MustCompile(`\blive\b`)
	variantRe = regexp.MustCompile(`\b(remix|edit|version|mix|rework|dub)\b`)
	oddballRe = regexp.MustCompile(`\b(instrumental|acoustic|demo|commentary|sped up|slowed)\b`)
	knockoffs = []string{"karaoke", "tribute", "made famous by", "made popular by", "in the style of", "cover version"}
	foldTable = map[rune]string{
		'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
		'ç': "c", 'ć': "c", 'č': "c",
		'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ė': "e", 'ę': "e", 'ě': "e",
		'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ī': "i", 'į': "i",
		'ñ': "n", 'ń': "n", 'ň': "n",
		'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ő': "o",
		'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ů': "u", 'ű': "u",
		'ý': "y", 'ÿ': "y",
		'š': "s", 'ś': "s", 'ß': "ss", 'ž': "z", 'ź': "z", 'ż': "z",
		'æ': "ae", 'œ': "oe", 'ð': "d", 'đ': "d", 'þ': "th", 'ł': "l",
	}
)

// Normalise folds case, diacritics and punctuation so "L'Impératrice" and
// "L Imperatrice" compare equal. Hand-rolled to keep the module free of
// golang.org/x/text; add runes to foldTable rather than the dependency.
func Normalise(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "&", " and ")
	var b strings.Builder
	for _, r := range s {
		if repl, ok := foldTable[r]; ok {
			b.WriteString(repl)
		} else {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(nonAlnum.ReplaceAllString(b.String(), " "))
}

// Score rates a candidate against a wanted entry. A change to these weights
// needs a row in TestScoreRanking.
func Score(t applemusic.Track, want tracklist.Entry) int {
	name := Normalise(t.Attributes.Name)
	artist := Normalise(t.Attributes.ArtistName)
	album := Normalise(t.Attributes.AlbumName)
	wantTitle := Normalise(want.Title)
	wantArtist := Normalise(want.Artist)
	hint := Normalise(want.Hint)
	score := 0

	switch {
	case name == wantTitle:
		score += 50
	case strings.Contains(name, wantTitle):
		score += 30
	case strings.Contains(wantTitle, name):
		score += 15
	}

	switch {
	case wantArtist == "":
		// Nothing asked for, nothing to credit.
	case artist == wantArtist:
		score += 40
	case strings.Contains(artist, wantArtist), strings.Contains(wantArtist, artist):
		score += 22
	default:
		overlap, known := artistOverlap(wantArtist, artist)
		switch {
		case !known:
			// Nothing but joining words to go on.
		case overlap == 1:
			score += 34
		case overlap > 0:
			// Part of the credit matches: enough not to punish, not enough
			// to reward.
		default:
			// A different artist entirely. Without this an exact title alone
			// clears MinScore, and every "Song 2" in the catalogue is a match.
			score -= 15
		}
	}

	haystack := name + " " + album
	// Knockoff labels usually give themselves away in the artist ("The Karaoke
	// Channel"), so that field joins the search for them — but only for them:
	// the live and variant checks below must not fire on the band Live.
	for _, k := range knockoffs {
		if strings.Contains(haystack+" "+artist, k) {
			score -= 200
			break
		}
	}

	asked := wantTitle + " " + hint
	if liveRe.MatchString(haystack) && !liveRe.MatchString(asked) {
		score -= 35 // don't drift onto a live take
	}
	if variantRe.MatchString(name) && !variantRe.MatchString(asked) {
		score -= 10 // mild: sometimes the only pressing is an "edit"
	}
	if oddballRe.MatchString(name) {
		score -= 25
	}

	if hint != "" {
		if strings.Contains(haystack, hint) {
			score += 45
		} else {
			score -= 5
		}
	}
	return score
}

// joiners are the words that turn up in every second artist credit and so say
// nothing about whether two credits name the same act.
var joiners = map[string]bool{
	"the": true, "and": true, "a": true, "of": true,
	"feat": true, "featuring": true, "ft": true, "with": true, "vs": true,
}

// artistOverlap is the share of the wanted artist's words that appear in a
// candidate's credit, and whether there was anything to compare.
//
// Credits get reordered ("Christabelle & Lindstrøm" is filed under both orders)
// and extended ("Daft Punk, Pharrell Williams & Nile Rodgers"), which neither
// equality nor substring catches, so the words are compared as a set.
func artistOverlap(want, got string) (share float64, known bool) {
	wanted := artistWords(want)
	if len(wanted) == 0 {
		return 0, false
	}
	have := make(map[string]bool)
	for _, w := range artistWords(got) {
		have[w] = true
	}
	hits := 0
	for _, w := range wanted {
		if have[w] {
			hits++
		}
	}
	return float64(hits) / float64(len(wanted)), true
}

func artistWords(s string) []string {
	var out []string
	for w := range strings.FieldsSeq(s) {
		if !joiners[w] {
			out = append(out, w)
		}
	}
	return out
}

// Equal reports whether two artist/title pairs name the same track once
// normalised. Used to line library tracks up against a source list.
func Equal(artistA, titleA, artistB, titleB string) bool {
	return Normalise(artistA) == Normalise(artistB) && Normalise(titleA) == Normalise(titleB)
}
