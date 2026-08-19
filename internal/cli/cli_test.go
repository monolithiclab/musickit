package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"musickit/internal/applemusic"
	"musickit/internal/musicapp"
)

// ===== fakes =====

// fakeAPI is a stand-in for Apple's server, holding a library in memory. It
// answers only the handful of routes musickit calls.
type fakeAPI struct {
	mu sync.Mutex

	// songs maps a search term to the results for it. A term with no entry
	// returns nothing, which is how a "missing" line is arranged.
	songs map[string][]applemusic.Track

	playlists []applemusic.Playlist
	tracks    map[string][]applemusic.Track

	created []struct {
		Name        string
		Description string
		Tracks      []string
	}
	added map[string][]string

	// fail, when set, is consulted before every request.
	fail func(r *http.Request) (int, string)

	requests []string
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		songs:  map[string][]applemusic.Track{},
		tracks: map[string][]applemusic.Track{},
		added:  map[string][]string{},
	}
}

func song(id, artist, title, album string) applemusic.Track {
	return applemusic.Track{
		ID:   id,
		Type: "songs",
		Attributes: applemusic.TrackAttributes{
			Name: title, ArtistName: artist, AlbumName: album,
			DurationInMillis: 195000, ReleaseDate: "1997-04-07",
		},
	}
}

func (f *fakeAPI) addPlaylist(id, name string, tracks ...applemusic.Track) {
	f.playlists = append(f.playlists, applemusic.Playlist{
		ID:         id,
		Type:       "library-playlists",
		Attributes: applemusic.PlaylistAttributes{Name: name, CanEdit: true},
	})
	f.tracks[id] = tracks
}

// answer registers a search result for the term musickit will build from an
// "Artist - Title" line.
func (f *fakeAPI) answer(term string, tracks ...applemusic.Track) {
	f.songs[term] = tracks
}

func (f *fakeAPI) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)

	if f.fail != nil {
		if status, body := f.fail(r); status != 0 {
			http.Error(w, body, status)
			return
		}
	}

	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/v1/catalog/") && strings.HasSuffix(path, "/search"):
		writeJSON(w, map[string]any{"results": map[string]any{
			"songs": map[string]any{"data": f.songs[r.URL.Query().Get("term")]},
		}})

	case path == "/v1/me/storefront":
		writeJSON(w, map[string]any{"data": []map[string]string{{"id": "fr"}}})

	case path == "/v1/me/library/playlists" && r.Method == http.MethodGet:
		writeJSON(w, map[string]any{"data": f.playlists})

	case path == "/v1/me/library/playlists" && r.Method == http.MethodPost:
		f.create(w, r)

	case strings.HasSuffix(path, "/tracks") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/me/library/playlists/"), "/tracks")
		tracks, ok := f.tracks[id]
		if !ok || len(tracks) == 0 {
			// Apple answers 404 for an empty playlist.
			http.Error(w, `{"errors":[]}`, http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"data": tracks})

	case strings.HasSuffix(path, "/tracks") && r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/me/library/playlists/"), "/tracks")
		var body struct {
			Data []applemusic.TrackRef `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, ref := range body.Data {
			f.added[id] = append(f.added[id], ref.ID)
		}
		w.WriteHeader(http.StatusNoContent)

	case strings.HasPrefix(path, "/v1/me/library/playlists/"):
		id := strings.TrimPrefix(path, "/v1/me/library/playlists/")
		for _, p := range f.playlists {
			if p.ID == id {
				writeJSON(w, map[string]any{"data": []applemusic.Playlist{p}})
				return
			}
		}
		http.Error(w, `{"errors":[]}`, http.StatusNotFound)

	default:
		http.Error(w, "unexpected route "+path, http.StatusNotImplemented)
	}
}

func (f *fakeAPI) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Attributes struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"attributes"`
		Relationships *struct {
			Tracks struct {
				Data []applemusic.TrackRef `json:"data"`
			} `json:"tracks"`
		} `json:"relationships"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rec := struct {
		Name        string
		Description string
		Tracks      []string
	}{Name: body.Attributes.Name, Description: body.Attributes.Description}
	if body.Relationships != nil {
		for _, ref := range body.Relationships.Tracks.Data {
			rec.Tracks = append(rec.Tracks, ref.ID)
		}
	}
	f.created = append(f.created, rec)

	id := fmt.Sprintf("p.new%d", len(f.created))
	f.playlists = append(f.playlists, applemusic.Playlist{
		ID: id, Attributes: applemusic.PlaylistAttributes{Name: rec.Name, CanEdit: true},
	})
	writeJSON(w, map[string]any{"data": []applemusic.Playlist{f.playlists[len(f.playlists)-1]}})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// fakeMusic stands in for the Music app.
type fakeMusic struct {
	playlists []musicapp.Playlist
	tracks    map[string][]musicapp.Track
	removed   map[string][]musicapp.Track
	renamed   [2]string
	deleted   string
	created   string

	// skip is a set of persistent IDs Remove pretends it could not delete,
	// standing in for a playlist that changed under us.
	skip map[string]bool
	err  error
}

func newFakeMusic() *fakeMusic {
	return &fakeMusic{
		tracks:  map[string][]musicapp.Track{},
		removed: map[string][]musicapp.Track{},
		skip:    map[string]bool{},
	}
}

func (m *fakeMusic) add(name string, tracks ...musicapp.Track) {
	for i := range tracks {
		tracks[i].Index = i + 1
	}
	m.playlists = append(m.playlists, musicapp.Playlist{Name: name, ID: "PID-" + name, Count: len(tracks)})
	m.tracks[name] = tracks
}

func (m *fakeMusic) Playlists(context.Context) ([]musicapp.Playlist, error) {
	return m.playlists, m.err
}

func (m *fakeMusic) Tracks(_ context.Context, playlist string) ([]musicapp.Track, error) {
	return m.tracks[playlist], m.err
}

func (m *fakeMusic) Remove(_ context.Context, playlist string, tracks []musicapp.Track) ([]musicapp.Track, error) {
	if m.err != nil {
		return nil, m.err
	}
	var removed []musicapp.Track
	for _, t := range tracks {
		if m.skip[t.ID] {
			continue
		}
		removed = append(removed, t)
	}
	m.removed[playlist] = append(m.removed[playlist], removed...)
	return removed, nil
}

func (m *fakeMusic) Rename(_ context.Context, playlist, newName string) error {
	m.renamed = [2]string{playlist, newName}
	return m.err
}

func (m *fakeMusic) Delete(_ context.Context, playlist string) error {
	m.deleted = playlist
	return m.err
}

func (m *fakeMusic) Create(_ context.Context, name string) error {
	m.created = name
	return m.err
}

// localTrack builds a track as the Music app reports it.
func localTrack(id, artist, title string) musicapp.Track {
	return musicapp.Track{ID: id, Artist: artist, Title: title, Album: "An Album"}
}

// ===== harness =====

type result struct {
	code int
	out  string
	err  string
}

// stdout returns the data lines, which is what a pipe would see.
func (r result) lines() []string {
	trimmed := strings.TrimRight(r.out, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

type harness struct {
	api   *fakeAPI
	music *fakeMusic
	stdin string
}

func newHarness() *harness {
	return &harness{api: newFakeAPI(), music: newFakeMusic()}
}

// run executes musickit as the shell would, with the network and the Music app
// replaced.
func (h *harness) run(t *testing.T, argv ...string) result {
	t.Helper()
	srv := h.api.serve(t)

	var out, errOut bytes.Buffer
	stdio := IO{In: strings.NewReader(h.stdin), Out: &out, Err: &errOut}

	code := Run(context.Background(), argv, stdio, "test", func(rt *Runtime) {
		rt.NewAPI = func(context.Context, bool) (*applemusic.Client, error) {
			return &applemusic.Client{
				BaseURL:    srv.URL,
				HTTP:       srv.Client(),
				DevToken:   "dev",
				UserToken:  "user",
				Storefront: "fr",
			}, nil
		}
		rt.NewMusic = func() (MusicApp, error) { return h.music, nil }
	})
	return result{code: code, out: out.String(), err: errOut.String()}
}

// ===== parsing and conventions =====

func TestHelpAndVersionSucceed(t *testing.T) {
	for _, argv := range [][]string{{"--help"}, {"--version"}, {"playlist", "--help"}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			res := newHarness().run(t, argv...)
			if res.code != ExitOK {
				t.Errorf("exit code = %d, want %d\n%s", res.code, ExitOK, res.err)
			}
			if res.out == "" {
				t.Error("help and version belong on stdout")
			}
		})
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	for _, argv := range [][]string{
		{"no-such-command"},
		{"playlist", "rename", "only-one-argument"},
		{"--nonsense"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			res := newHarness().run(t, argv...)
			if res.code != ExitUsage {
				t.Errorf("exit code = %d, want %d (usage)\n%s", res.code, ExitUsage, res.err)
			}
			if res.err == "" {
				t.Error("a usage error belongs on stderr")
			}
		})
	}
}

func TestPlaylistAliasIsAccepted(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026")
	res := h.run(t, "pl", "ls")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if !strings.Contains(res.out, "Summer 2026") {
		t.Errorf("stdout = %q", res.out)
	}
}

// TestQuietSilencesNarrationOnly pins the stdout/stderr split: -q must not cost
// a single data line.
func TestQuietSilencesNarrationOnly(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026")

	loud := h.run(t, "playlist", "list")
	quiet := h.run(t, "-q", "playlist", "list")

	if loud.out != quiet.out {
		t.Errorf("--quiet changed stdout:\n loud %q\nquiet %q", loud.out, quiet.out)
	}
	if loud.err == "" {
		t.Error("the summary should be narrated on stderr")
	}
	if quiet.err != "" {
		t.Errorf("--quiet left %q on stderr", quiet.err)
	}
}

func TestSearchOutput(t *testing.T) {
	h := newHarness()
	h.api.answer("blur song 2", song("1", "Blur", "Song 2", "Blur"))

	res := h.run(t, "search", "blur", "song", "2")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	fields := strings.Split(strings.TrimRight(res.out, "\n"), "\t")
	want := []string{"1", "Blur", "Song 2", "Blur", "1997-04-07", "3:15"}
	if !slices.Equal(fields, want) {
		t.Errorf("row = %q, want %q", fields, want)
	}
}

func TestJSONOutputIsNDJSON(t *testing.T) {
	h := newHarness()
	h.api.answer("blur song 2", song("1", "Blur", "Song 2", "Blur"), song("2", "Blur", "Song 2", "Live"))

	res := h.run(t, "--json", "search", "blur", "song", "2")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	lines := res.lines()
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want one object per track: %q", len(lines), res.out)
	}
	for _, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %q is not JSON: %v", line, err)
		}
	}
}

// ===== input plumbing =====

func TestAddReadsTracksFromArguments(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026", song("0", "Air", "Sexy Boy", "Moon Safari"))
	h.api.answer("Blur Song 2", song("1", "Blur", "Song 2", "Blur"))

	res := h.run(t, "playlist", "add", "Summer 2026", "Blur - Song 2")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if got := h.api.added["p.1"]; !slices.Equal(got, []string{"1"}) {
		t.Errorf("added %q, want the matched catalogue id", got)
	}
	if !strings.HasPrefix(res.out, "added\t") {
		t.Errorf("stdout = %q, want an added record", res.out)
	}
}

func TestAddReadsTracksFromStdin(t *testing.T) {
	h := newHarness()
	h.stdin = "# from a pipe\nBlur - Song 2\nAir - Sexy Boy\n"
	h.api.addPlaylist("p.1", "Summer 2026", song("0", "Nobody", "Nothing", "Nowhere"))
	h.api.answer("Blur Song 2", song("1", "Blur", "Song 2", "Blur"))
	h.api.answer("Air Sexy Boy", song("2", "Air", "Sexy Boy", "Moon Safari"))

	res := h.run(t, "playlist", "add", "Summer 2026")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if got := h.api.added["p.1"]; !slices.Equal(got, []string{"1", "2"}) {
		t.Errorf("added %q, want both ids in input order", got)
	}
}

func TestAddReadsTracksFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "list.txt")
	if err := os.WriteFile(path, []byte("Blur - Song 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026", song("0", "Nobody", "Nothing", "Nowhere"))
	h.api.answer("Blur Song 2", song("1", "Blur", "Song 2", "Blur"))

	res := h.run(t, "playlist", "add", "Summer 2026", "--from", path)
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if got := h.api.added["p.1"]; !slices.Equal(got, []string{"1"}) {
		t.Errorf("added %q", got)
	}
}

func TestFromDashIsStdin(t *testing.T) {
	h := newHarness()
	h.stdin = "Blur - Song 2\n"
	h.api.addPlaylist("p.1", "Summer 2026", song("0", "Nobody", "Nothing", "Nowhere"))
	h.api.answer("Blur Song 2", song("1", "Blur", "Song 2", "Blur"))

	if res := h.run(t, "playlist", "add", "Summer 2026", "-f", "-"); res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if got := h.api.added["p.1"]; !slices.Equal(got, []string{"1"}) {
		t.Errorf("added %q", got)
	}
}

func TestArgumentsAndFromTogetherAreRejected(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026")
	res := h.run(t, "playlist", "add", "Summer 2026", "Blur - Song 2", "--from", "/dev/null")
	if res.code != ExitError {
		t.Errorf("exit code = %d, want %d", res.code, ExitError)
	}
	if !strings.Contains(res.err, "not both") {
		t.Errorf("stderr = %q", res.err)
	}
}

func TestMissingFileIsReported(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026")
	res := h.run(t, "playlist", "add", "Summer 2026", "--from", filepath.Join(t.TempDir(), "absent.txt"))
	if res.code != ExitError {
		t.Errorf("exit code = %d, want %d", res.code, ExitError)
	}
	if !strings.Contains(res.err, "absent.txt") {
		t.Errorf("stderr %q does not name the file", res.err)
	}
}

// TestExportPipesIntoAdd is the round trip the shared track-list format exists
// for: `playlist export A | playlist add B`.
func TestExportPipesIntoAdd(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.src", "Source",
		song("1", "Blur", "Song 2", "Blur"),
		song("2", "Air", "Sexy Boy", "Moon Safari"))
	h.api.addPlaylist("p.dst", "Destination", song("0", "Nobody", "Nothing", "Nowhere"))
	h.api.answer("Blur Song 2", song("1", "Blur", "Song 2", "Blur"))
	h.api.answer("Air Sexy Boy", song("2", "Air", "Sexy Boy", "Moon Safari"))

	export := h.run(t, "playlist", "export", "Source")
	if export.code != ExitOK {
		t.Fatalf("export exit code = %d\n%s", export.code, export.err)
	}
	if export.out != "Blur - Song 2\nAir - Sexy Boy\n" {
		t.Fatalf("export produced %q", export.out)
	}

	h.stdin = export.out
	add := h.run(t, "playlist", "add", "Destination")
	if add.code != ExitOK {
		t.Fatalf("add exit code = %d\n%s", add.code, add.err)
	}
	if got := h.api.added["p.dst"]; !slices.Equal(got, []string{"1", "2"}) {
		t.Errorf("added %q, want both tracks", got)
	}
}

// ===== playlist resolution =====

func TestPlaylistResolvesByNameIDAndFolding(t *testing.T) {
	for _, ref := range []string{"p.1", "Été 2026", "ete 2026", "ETE 2026"} {
		t.Run(ref, func(t *testing.T) {
			h := newHarness()
			h.api.addPlaylist("p.1", "Été 2026", song("1", "Air", "Sexy Boy", "Moon Safari"))
			res := h.run(t, "playlist", "show", ref)
			if res.code != ExitOK {
				t.Fatalf("exit code = %d\n%s", res.code, res.err)
			}
			if !strings.Contains(res.out, "Sexy Boy") {
				t.Errorf("stdout = %q", res.out)
			}
		})
	}
}

func TestAmbiguousPlaylistNameListsCandidates(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Ete 2026", song("1", "Air", "Sexy Boy", "x"))
	h.api.addPlaylist("p.2", "Été 2026", song("2", "Blur", "Song 2", "y"))

	res := h.run(t, "playlist", "show", "ete 2026")
	if res.code != ExitError {
		t.Fatalf("exit code = %d, want %d", res.code, ExitError)
	}
	for _, want := range []string{"p.1", "p.2", "use an id"} {
		if !strings.Contains(res.err, want) {
			t.Errorf("stderr %q does not mention %q", res.err, want)
		}
	}
}

func TestUnknownPlaylistPointsAtList(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026")
	res := h.run(t, "playlist", "show", "Winter")
	if res.code != ExitError {
		t.Fatalf("exit code = %d", res.code)
	}
	if !strings.Contains(res.err, "musickit playlist list") {
		t.Errorf("stderr %q does not suggest the next command", res.err)
	}
}

func TestShowEmptyPlaylist(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Empty")
	res := h.run(t, "playlist", "show", "Empty")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s; Apple's 404 for an empty playlist must not surface", res.code, res.err)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want nothing", res.out)
	}
}

// ===== create =====

func TestCreateFromStdin(t *testing.T) {
	h := newHarness()
	h.stdin = "Blur - Song 2\nAir - Sexy Boy\n"
	h.api.answer("Blur Song 2", song("1", "Blur", "Song 2", "Blur"))
	h.api.answer("Air Sexy Boy", song("2", "Air", "Sexy Boy", "Moon Safari"))

	res := h.run(t, "playlist", "create", "Summer 2026")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if len(h.api.created) != 1 {
		t.Fatalf("created %d playlists", len(h.api.created))
	}
	created := h.api.created[0]
	if created.Name != "Summer 2026" {
		t.Errorf("name = %q", created.Name)
	}
	if !slices.Equal(created.Tracks, []string{"1", "2"}) {
		t.Errorf("tracks = %q", created.Tracks)
	}
}

func TestImportIsAnAliasForCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "list.txt")
	if err := os.WriteFile(path, []byte("Blur - Song 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness()
	h.api.answer("Blur Song 2", song("1", "Blur", "Song 2", "Blur"))

	res := h.run(t, "import", "-p", "Summer 2026", "-f", path, "-d", "sunshine")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if len(h.api.created) != 1 || h.api.created[0].Description != "sunshine" {
		t.Errorf("created = %+v", h.api.created)
	}
}

// TestCreateChunksBeyondThePageLimit pins the 100-track cap: the rest must
// follow in a second request rather than being dropped.
func TestCreateChunksBeyondThePageLimit(t *testing.T) {
	h := newHarness()
	var lines []string
	for i := range 150 {
		artist, title := fmt.Sprintf("Artist%d", i), fmt.Sprintf("Title%d", i)
		lines = append(lines, artist+" - "+title)
		h.api.answer(artist+" "+title, song(fmt.Sprint(i), artist, title, "Album"))
	}
	h.stdin = strings.Join(lines, "\n") + "\n"

	res := h.run(t, "playlist", "create", "Big")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if got := len(h.api.created[0].Tracks); got != applemusic.PageLimit {
		t.Errorf("the create request carried %d tracks, want %d", got, applemusic.PageLimit)
	}
	if got := len(h.api.added["p.new1"]); got != 50 {
		t.Errorf("%d tracks followed in a second request, want 50", got)
	}
}

// ===== exit code 3 =====

func TestUnmatchedTracksExitThree(t *testing.T) {
	h := newHarness()
	h.stdin = "Blur - Song 2\nNobody - Nothing at All\n"
	h.api.answer("Blur Song 2", song("1", "Blur", "Song 2", "Blur"))

	res := h.run(t, "playlist", "create", "Summer 2026")
	if res.code != ExitIncomplete {
		t.Fatalf("exit code = %d, want %d\n%s", res.code, ExitIncomplete, res.err)
	}
	if !slices.Equal(h.api.created[0].Tracks, []string{"1"}) {
		t.Errorf("tracks = %q; the playlist should still have been created", h.api.created[0].Tracks)
	}
	if !strings.Contains(res.out, "missing\tNobody - Nothing at All") {
		t.Errorf("stdout = %q, want a missing record naming the source line", res.out)
	}
	if !strings.Contains(res.err, "1 of 2") {
		t.Errorf("stderr = %q, want a count of the misses", res.err)
	}
}

// ===== dry run =====

func TestDryRunWritesNothing(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026", song("0", "Nobody", "Nothing", "Nowhere"))
	h.api.answer("Blur Song 2", song("1", "Blur", "Song 2", "Blur"))

	res := h.run(t, "--dry-run", "playlist", "add", "Summer 2026", "Blur - Song 2")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if len(h.api.added) != 0 {
		t.Errorf("a dry run wrote %v", h.api.added)
	}
	if !strings.HasPrefix(res.out, "would-add\t") {
		t.Errorf("stdout = %q, want a would-add record", res.out)
	}
	if !strings.Contains(res.err, "Dry run") {
		t.Errorf("stderr = %q", res.err)
	}
}

func TestDryRunRemove(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026")
	h.music.add("Summer 2026", localTrack("T1", "Blur", "Song 2"))

	res := h.run(t, "-n", "playlist", "remove", "Summer 2026", "Blur - Song 2")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if len(h.music.removed) != 0 {
		t.Errorf("a dry run removed %v", h.music.removed)
	}
	if !strings.HasPrefix(res.out, "would-remove\t") {
		t.Errorf("stdout = %q", res.out)
	}
}

func TestDryRunDelete(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026", localTrack("T1", "Blur", "Song 2"))

	res := h.run(t, "-n", "playlist", "delete", "Summer 2026")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if h.music.deleted != "" {
		t.Errorf("a dry run deleted %q", h.music.deleted)
	}
}

// ===== confirmation =====

// TestDestructiveCommandsRefuseToGuess pins the no-terminal rule: a script that
// pipes into musickit must say --yes rather than have a deletion assumed.
func TestDestructiveCommandsRefuseToGuess(t *testing.T) {
	cases := map[string][]string{
		"remove": {"playlist", "remove", "Summer 2026", "--all"},
		"delete": {"playlist", "delete", "Summer 2026"},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			h.music.add("Summer 2026", localTrack("T1", "Blur", "Song 2"))

			res := h.run(t, argv...)
			if res.code != ExitError {
				t.Fatalf("exit code = %d, want %d", res.code, ExitError)
			}
			if !strings.Contains(res.err, "--yes") {
				t.Errorf("stderr %q does not point at --yes", res.err)
			}
			if len(h.music.removed) != 0 || h.music.deleted != "" {
				t.Error("the command acted without confirmation")
			}
		})
	}
}

func TestYesConfirmsInAdvance(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026", localTrack("T1", "Blur", "Song 2"))

	res := h.run(t, "--yes", "playlist", "delete", "Summer 2026")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if h.music.deleted != "Summer 2026" {
		t.Errorf("deleted = %q", h.music.deleted)
	}
	if !strings.HasPrefix(res.out, "deleted\t") {
		t.Errorf("stdout = %q", res.out)
	}
}

// ===== remove =====

func TestRemoveMatchesByArtistAndTitle(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026",
		localTrack("T1", "Blur", "Song 2"),
		localTrack("T2", "Air", "Sexy Boy"),
		localTrack("T3", "Phoenix", "1901"))

	res := h.run(t, "-y", "playlist", "remove", "Summer 2026", "Air - Sexy Boy")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	removed := h.music.removed["Summer 2026"]
	if len(removed) != 1 || removed[0].ID != "T2" {
		t.Errorf("removed = %+v, want only the Air track", removed)
	}
}

// TestRemoveTakesOneCopyPerRequest pins the duplicate rule: asking once removes
// one of two identical tracks, asking twice removes both.
func TestRemoveTakesOneCopyPerRequest(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026",
		localTrack("T1", "Blur", "Song 2"),
		localTrack("T2", "Blur", "Song 2"))

	once := h.run(t, "-y", "playlist", "remove", "Summer 2026", "Blur - Song 2")
	if once.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", once.code, once.err)
	}
	if got := len(h.music.removed["Summer 2026"]); got != 1 {
		t.Errorf("removed %d tracks for one request, want 1", got)
	}

	h.music.removed = map[string][]musicapp.Track{}
	twice := h.run(t, "-y", "playlist", "remove", "Summer 2026", "Blur - Song 2", "Blur - Song 2")
	if twice.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", twice.code, twice.err)
	}
	if got := len(h.music.removed["Summer 2026"]); got != 2 {
		t.Errorf("removed %d tracks for two requests, want 2", got)
	}
}

func TestRemoveReportsTracksNotInThePlaylist(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026", localTrack("T1", "Blur", "Song 2"))

	res := h.run(t, "-y", "playlist", "remove", "Summer 2026", "Air - Sexy Boy")
	if res.code != ExitIncomplete {
		t.Fatalf("exit code = %d, want %d\n%s", res.code, ExitIncomplete, res.err)
	}
	if !strings.Contains(res.out, "not-in-playlist\tAir - Sexy Boy") {
		t.Errorf("stdout = %q", res.out)
	}
	if len(h.music.removed) != 0 {
		t.Error("nothing should have been removed")
	}
}

func TestRemoveAll(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026",
		localTrack("T1", "Blur", "Song 2"),
		localTrack("T2", "Air", "Sexy Boy"))

	res := h.run(t, "-y", "playlist", "remove", "Summer 2026", "--all")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if got := len(h.music.removed["Summer 2026"]); got != 2 {
		t.Errorf("removed %d tracks, want all 2", got)
	}
}

func TestRemoveAllRejectsATrackList(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026", localTrack("T1", "Blur", "Song 2"))

	res := h.run(t, "-y", "playlist", "remove", "Summer 2026", "--all", "Blur - Song 2")
	if res.code != ExitError {
		t.Errorf("exit code = %d, want %d", res.code, ExitError)
	}
	if len(h.music.removed) != 0 {
		t.Error("--all with a track list is ambiguous and must not act")
	}
}

// TestRemoveWarnsAboutSkips pins the concurrent-change path: AppleScript
// refused to delete, and the user is told to re-run rather than being told it
// worked.
func TestRemoveWarnsAboutSkips(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026",
		localTrack("T1", "Blur", "Song 2"),
		localTrack("T2", "Air", "Sexy Boy"))
	h.music.skip["T2"] = true

	res := h.run(t, "-y", "playlist", "remove", "Summer 2026", "--all")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if !strings.Contains(res.err, "Re-run") {
		t.Errorf("stderr %q does not tell the user to re-run", res.err)
	}
	if strings.Count(res.out, "removed\t") != 1 {
		t.Errorf("stdout = %q, want exactly one removed record", res.out)
	}
}

// TestRemoveWarnsAboutSkipsEvenWhenQuiet: --quiet hides progress, never a
// half-finished deletion.
func TestRemoveWarnsAboutSkipsEvenWhenQuiet(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026", localTrack("T1", "Blur", "Song 2"))
	h.music.skip["T1"] = true

	res := h.run(t, "-y", "-q", "playlist", "remove", "Summer 2026", "--all")
	if !strings.Contains(res.err, "skipped") {
		t.Errorf("stderr = %q; --quiet must not hide a warning", res.err)
	}
}

func TestRemoveOnAPlaylistTheMusicAppCannotSee(t *testing.T) {
	h := newHarness()
	h.music.add("Some Other Playlist")

	res := h.run(t, "-y", "playlist", "remove", "Summer 2026", "--all")
	if res.code != ExitError {
		t.Fatalf("exit code = %d, want %d", res.code, ExitError)
	}
	if !strings.Contains(res.err, "playlist list --local") {
		t.Errorf("stderr %q does not suggest the next command", res.err)
	}
}

// TestRemoveTranslatesAnAPIIdentifier: "p.xxx" means nothing to AppleScript, so
// it is resolved to a name through the API first.
func TestRemoveTranslatesAnAPIIdentifier(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026", song("1", "Blur", "Song 2", "Blur"))
	h.music.add("Summer 2026", localTrack("T1", "Blur", "Song 2"))

	res := h.run(t, "-y", "playlist", "remove", "p.1", "--all")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if got := len(h.music.removed["Summer 2026"]); got != 1 {
		t.Errorf("removed %d tracks from the named playlist", got)
	}
}

// ===== rename =====

func TestRename(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026", localTrack("T1", "Blur", "Song 2"))

	res := h.run(t, "playlist", "rename", "Summer 2026", "Summer 2027")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if h.music.renamed != [2]string{"Summer 2026", "Summer 2027"} {
		t.Errorf("renamed = %v", h.music.renamed)
	}
	if !strings.HasPrefix(res.out, "renamed\t") {
		t.Errorf("stdout = %q", res.out)
	}
}

func TestRenameAliasMv(t *testing.T) {
	h := newHarness()
	h.music.add("A")
	if res := h.run(t, "pl", "mv", "A", "B"); res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if h.music.renamed != [2]string{"A", "B"} {
		t.Errorf("renamed = %v", h.music.renamed)
	}
}

// ===== local listing =====

func TestListLocalUsesNoNetwork(t *testing.T) {
	h := newHarness()
	h.music.add("Summer 2026", localTrack("T1", "Blur", "Song 2"))
	h.api.fail = func(*http.Request) (int, string) {
		return http.StatusInternalServerError, "the network should not have been touched"
	}

	res := h.run(t, "playlist", "list", "--local")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if len(h.api.requests) != 0 {
		t.Errorf("--local made requests: %q", h.api.requests)
	}
	if !strings.Contains(res.out, "Summer 2026\t1") {
		t.Errorf("stdout = %q, want the name and track count", res.out)
	}
}

func TestListLimit(t *testing.T) {
	h := newHarness()
	h.music.add("One")
	h.music.add("Two")
	res := h.run(t, "playlist", "list", "--local", "-l", "1")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if got := len(res.lines()); got != 1 {
		t.Errorf("got %d lines, want 1", got)
	}
}

// ===== failure paths =====

func TestUnauthorizedSuggestsReauth(t *testing.T) {
	h := newHarness()
	h.api.fail = func(*http.Request) (int, string) {
		return http.StatusUnauthorized, `{"errors":[{"detail":"expired"}]}`
	}

	res := h.run(t, "playlist", "list")
	if res.code != ExitError {
		t.Fatalf("exit code = %d, want %d", res.code, ExitError)
	}
	if !strings.Contains(res.err, "auth --reauth") {
		t.Errorf("stderr %q does not suggest re-authorising", res.err)
	}
}

func TestMusicAppUnsupportedIsExplained(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"playlist", "delete", "x"},
		IO{In: strings.NewReader(""), Out: &out, Err: &errOut}, "test",
		func(rt *Runtime) {
			rt.NewMusic = func() (MusicApp, error) { return nil, musicapp.ErrUnsupported }
		})

	if code != ExitError {
		t.Fatalf("exit code = %d, want %d", code, ExitError)
	}
	if !strings.Contains(errOut.String(), "macOS") {
		t.Errorf("stderr %q does not explain the platform requirement", errOut.String())
	}
}

func TestInterruptExits130(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026")
	srv := h.api.serve(t)

	var out, errOut bytes.Buffer
	code := Run(ctx, []string{"playlist", "list"},
		IO{In: strings.NewReader(""), Out: &out, Err: &errOut}, "test",
		func(rt *Runtime) {
			rt.NewAPI = func(context.Context, bool) (*applemusic.Client, error) {
				return &applemusic.Client{BaseURL: srv.URL, HTTP: srv.Client(), Storefront: "fr"}, nil
			}
		})

	if code != ExitInterrupt {
		t.Errorf("exit code = %d, want %d", code, ExitInterrupt)
	}
	if !strings.Contains(errOut.String(), "interrupted") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// ===== auth =====

func TestAuthLogoutRemovesTheCachedToken(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "user-token")
	if err := os.WriteFile(tokenPath, []byte("cached\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"--config-dir", dir, "auth", "--logout"},
		IO{In: strings.NewReader(""), Out: &out, Err: &errOut}, "test", nil)

	if code != ExitOK {
		t.Fatalf("exit code = %d\n%s", code, errOut.String())
	}
	if _, err := os.Stat(tokenPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the token file survived --logout (%v)", err)
	}
}

func TestAuthReportsTheStorefront(t *testing.T) {
	h := newHarness()
	res := h.run(t, "auth")
	if res.code != ExitOK {
		t.Fatalf("exit code = %d\n%s", res.code, res.err)
	}
	if strings.TrimRight(res.out, "\n") != "ok\tfr" {
		t.Errorf("stdout = %q", res.out)
	}
}

// TestNoTokenIsEverPrinted guards the one thing that must never reach a
// terminal, a log or a pipe.
func TestNoTokenIsEverPrinted(t *testing.T) {
	h := newHarness()
	h.api.addPlaylist("p.1", "Summer 2026", song("1", "Blur", "Song 2", "Blur"))

	for _, argv := range [][]string{
		{"auth"},
		{"playlist", "list"},
		{"playlist", "show", "Summer 2026"},
	} {
		res := h.run(t, argv...)
		for _, secret := range []string{"dev", "user"} {
			for stream, text := range map[string]string{"stdout": res.out, "stderr": res.err} {
				if strings.Contains(text, "Bearer "+secret) || strings.Contains(text, "Music-User-Token") {
					t.Errorf("%v leaked a credential on %s: %q", argv, stream, text)
				}
			}
		}
	}
}

// ===== runtime plumbing =====

func TestRuntimeMemoisesTheClient(t *testing.T) {
	var dials int
	rt := &Runtime{G: &Globals{}}
	rt.NewAPI = func(context.Context, bool) (*applemusic.Client, error) {
		dials++
		return &applemusic.Client{UserToken: "u"}, nil
	}

	for range 3 {
		if _, err := rt.API(context.Background(), true); err != nil {
			t.Fatal(err)
		}
	}
	if dials != 1 {
		t.Errorf("dialled %d times, want 1", dials)
	}
}

// TestRuntimeUpgradesToAUserToken: a read dialled without one must not satisfy
// a later write.
func TestRuntimeUpgradesToAUserToken(t *testing.T) {
	var wanted []bool
	rt := &Runtime{G: &Globals{}}
	rt.NewAPI = func(_ context.Context, needUser bool) (*applemusic.Client, error) {
		wanted = append(wanted, needUser)
		c := &applemusic.Client{}
		if needUser {
			c.UserToken = "u"
		}
		return c, nil
	}

	if _, err := rt.API(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.API(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(wanted, []bool{false, true}) {
		t.Errorf("dials = %v, want a second dial for the user token", wanted)
	}
}

func TestRuntimeMemoisesTheMusicApp(t *testing.T) {
	var calls int
	rt := &Runtime{G: &Globals{}}
	rt.NewMusic = func() (MusicApp, error) {
		calls++
		return newFakeMusic(), nil
	}
	for range 3 {
		if _, err := rt.Music(); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("built %d handles, want 1", calls)
	}
}

func TestConfirm(t *testing.T) {
	cases := []struct {
		name    string
		yes     bool
		wantErr bool
	}{
		{"--yes answers in advance", true, false},
		{"no terminal, no guess", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var errOut bytes.Buffer
			rt := &Runtime{G: &Globals{Yes: c.yes}, In: strings.NewReader("y\n"), Err: &errOut}
			err := rt.Confirm("Delete %q?", "x")
			if (err != nil) != c.wantErr {
				t.Errorf("Confirm = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestDuration(t *testing.T) {
	cases := map[int]string{0: "-", -1: "-", 195000: "3:15", 59000: "0:59", 3600000: "60:00", 5000: "0:05"}
	for ms, want := range cases {
		if got := duration(ms); got != want {
			t.Errorf("duration(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "fr", "us"); got != "fr" {
		t.Errorf("firstNonEmpty = %q, want fr", got)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Errorf("firstNonEmpty = %q, want empty", got)
	}
}

func TestIncompleteError(t *testing.T) {
	if err := incomplete(0, 10); err != nil {
		t.Errorf("incomplete(0, 10) = %v, want nil", err)
	}
	err := incomplete(2, 10)
	var target *incompleteError
	if !errors.As(err, &target) {
		t.Fatalf("incomplete(2, 10) = %v, want an *incompleteError", err)
	}
	if !strings.Contains(err.Error(), "2 of 10") {
		t.Errorf("error = %q", err)
	}
}

func TestTimeoutFlagIsParsed(t *testing.T) {
	var captured time.Duration
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"--timeout", "5s", "search", "x"},
		IO{In: strings.NewReader(""), Out: &out, Err: &errOut}, "test",
		func(rt *Runtime) {
			captured = rt.G.Timeout
			rt.NewAPI = func(context.Context, bool) (*applemusic.Client, error) {
				return nil, errors.New("stop here")
			}
		})
	if code != ExitError {
		t.Fatalf("exit code = %d", code)
	}
	if captured != 5*time.Second {
		t.Errorf("timeout = %s, want 5s", captured)
	}
}

// TestOutputIsNotBufferedIntoOblivion is a smoke test that every writer the
// commands use goes to the streams the harness handed in.
func TestOutputIsNotBufferedIntoOblivion(t *testing.T) {
	var out bytes.Buffer
	rt := &Runtime{G: &Globals{}, Out: &out, Err: io.Discard}
	rt.Row("a", "b")
	rt.Printf("%s\n", "c")
	if err := rt.JSON(map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "a\tb\nc\n{\"k\":\"v\"}\n" {
		t.Errorf("stdout = %q", got)
	}
}
