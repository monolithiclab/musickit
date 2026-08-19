package applemusic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient wires a client to a stub server, with no backoff so the retry
// path costs nothing.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		BaseURL:  srv.URL,
		HTTP:     srv.Client(),
		DevToken: "dev-token",
		Backoff:  0,
	}
}

func TestDoSendsCredentials(t *testing.T) {
	var gotAuth, gotUser, gotType, gotBody string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUser = r.Header.Get("Music-User-Token")
		gotType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = io.WriteString(w, `{}`)
	})
	client.UserToken = "user-token"

	if _, err := client.Do(context.Background(), http.MethodPost, "/v1/thing", map[string]string{"a": "b"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != "Bearer dev-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotUser != "user-token" {
		t.Errorf("Music-User-Token = %q", gotUser)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if gotBody != `{"a":"b"}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestDoOmitsUserTokenWhenAbsent(t *testing.T) {
	present := true
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header["Music-User-Token"]
		_, _ = io.WriteString(w, `{}`)
	})
	if _, err := client.Get(context.Background(), "/v1/thing"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if present {
		t.Error("an empty user token was sent as a header; catalogue reads must work without one")
	}
}

func TestDoRetriesRateLimits(t *testing.T) {
	var calls atomic.Int64
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	})

	raw, err := client.Get(context.Background(), "/v1/thing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Errorf("body = %q", raw)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("made %d requests, want 3 (two rejections then success)", got)
	}
}

func TestDoGivesUpOnPersistentRateLimits(t *testing.T) {
	var calls atomic.Int64
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := client.Get(context.Background(), "/v1/thing")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v, want a rate-limit failure", err)
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("made %d attempts, want 4", got)
	}
}

func TestDoRespectsContextWhileBackingOff(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := client.Get(ctx, "/v1/thing"); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %s; the backoff ignored the context", elapsed)
	}
}

func TestAPIError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errors":[{"detail":"nope"}]}`, http.StatusUnauthorized)
	})

	_, err := client.Get(context.Background(), "/v1/thing")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsUnauthorized(err) {
		t.Errorf("IsUnauthorized(%v) = false, want true — the CLI keys re-auth off this", err)
	}
	for _, want := range []string{"GET", "/v1/thing", "401", "nope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestIsUnauthorizedIsNarrow(t *testing.T) {
	if IsUnauthorized(nil) {
		t.Error("nil is not an authorisation failure")
	}
	if IsUnauthorized(&APIError{Status: http.StatusNotFound}) {
		t.Error("404 is not an authorisation failure")
	}
	if !IsUnauthorized(fmt.Errorf("wrapped: %w", &APIError{Status: http.StatusForbidden})) {
		t.Error("403 wrapped in another error should still count")
	}
}

func TestSearchSongsUsesStorefront(t *testing.T) {
	var gotPath, gotQuery string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = io.WriteString(w, `{"results":{"songs":{"data":[
			{"id":"1","type":"songs","attributes":{"name":"Song 2","artistName":"Blur"}}]}}}`)
	})
	client.Storefront = "fr"

	tracks, err := client.SearchSongs(context.Background(), "Blur Song 2", 5)
	if err != nil {
		t.Fatalf("SearchSongs: %v", err)
	}
	if gotPath != "/v1/catalog/fr/search" {
		t.Errorf("path = %q, want the fr storefront", gotPath)
	}
	for _, want := range []string{"term=Blur+Song+2", "types=songs", "limit=5"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q is missing %q", gotQuery, want)
		}
	}
	if len(tracks) != 1 || tracks[0].Attributes.ArtistName != "Blur" {
		t.Errorf("tracks = %+v", tracks)
	}
}

func TestSearchSongsDefaultsStorefront(t *testing.T) {
	var gotPath string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{}`)
	})
	if _, err := client.SearchSongs(context.Background(), "x", 0); err != nil {
		t.Fatalf("SearchSongs: %v", err)
	}
	if gotPath != "/v1/catalog/"+DefaultStorefront+"/search" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestFetchStorefront(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"gb"}]}`)
	})
	got, err := client.FetchStorefront(context.Background())
	if err != nil {
		t.Fatalf("FetchStorefront: %v", err)
	}
	if got != "gb" {
		t.Errorf("storefront = %q, want gb", got)
	}
}

func TestFetchStorefrontFallsBack(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	})
	got, err := client.FetchStorefront(context.Background())
	if err != nil {
		t.Fatalf("FetchStorefront: %v", err)
	}
	if got != DefaultStorefront {
		t.Errorf("storefront = %q, want the %s default", got, DefaultStorefront)
	}
}

// TestPlaylistsFollowsNext walks three pages, which is the case a single
// request would silently truncate.
func TestPlaylistsFollowsNext(t *testing.T) {
	var paths []string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		switch r.URL.Query().Get("offset") {
		case "":
			_, _ = io.WriteString(w, `{"data":[{"id":"p.1","attributes":{"name":"One"}}],
				"next":"/v1/me/library/playlists?limit=100&offset=1"}`)
		case "1":
			_, _ = io.WriteString(w, `{"data":[{"id":"p.2","attributes":{"name":"Two"}}],
				"next":"/v1/me/library/playlists?limit=100&offset=2"}`)
		default:
			_, _ = io.WriteString(w, `{"data":[{"id":"p.3","attributes":{"name":"Three"}}]}`)
		}
	})

	playlists, err := client.Playlists(context.Background(), 0)
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(playlists) != 3 {
		t.Fatalf("got %d playlists, want 3: %+v", len(playlists), playlists)
	}
	if playlists[2].Name() != "Three" {
		t.Errorf("last playlist = %q", playlists[2].Name())
	}
	if len(paths) != 3 {
		t.Errorf("made %d requests, want 3", len(paths))
	}
}

func TestFetchAllHonoursLimit(t *testing.T) {
	var requests atomic.Int64
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"data":[{"id":"p.1"},{"id":"p.2"},{"id":"p.3"}],
			"next":"/v1/me/library/playlists?offset=3"}`)
	})

	playlists, err := client.Playlists(context.Background(), 2)
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(playlists) != 2 {
		t.Errorf("got %d playlists, want the requested 2", len(playlists))
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("made %d requests; the limit was satisfied by the first", got)
	}
}

// TestFetchAllStopsOnStalledNext guards the loop that would otherwise spin on a
// "next" pointing at itself.
func TestFetchAllStopsOnStalledNext(t *testing.T) {
	var requests atomic.Int64
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprintf(w, `{"data":[{"id":"p.1"}],"next":%q}`, r.URL.RequestURI())
	})

	if _, err := client.Playlists(context.Background(), 0); err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("made %d requests; a self-referential next should stop after one", got)
	}
}

func TestFetchAllStopsOnEmptyPage(t *testing.T) {
	var requests atomic.Int64
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"data":[],"next":"/v1/me/library/playlists?offset=99"}`)
	})
	if _, err := client.Playlists(context.Background(), 0); err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("made %d requests; an empty page ends the walk", got)
	}
}

func TestPlaylistByID(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/me/library/playlists/p.AbC%20dEf" && r.URL.EscapedPath() != "/v1/me/library/playlists/p.AbC%20dEf" {
			t.Errorf("path = %q, want the id escaped", r.URL.EscapedPath())
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"p.AbC dEf","attributes":{"name":"Summer","canEdit":true}}]}`)
	})

	playlist, err := client.Playlist(context.Background(), "p.AbC dEf")
	if err != nil {
		t.Fatalf("Playlist: %v", err)
	}
	if playlist.Name() != "Summer" || !playlist.Attributes.CanEdit {
		t.Errorf("playlist = %+v", playlist)
	}
}

func TestPlaylistReportsMissing(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	})
	if _, err := client.Playlist(context.Background(), "p.nope"); err == nil {
		t.Error("expected an error for an empty data array")
	}
}

// TestPlaylistTracksTreatsNotFoundAsEmpty pins Apple's quirk: an empty playlist
// answers 404 rather than an empty collection.
func TestPlaylistTracksTreatsNotFoundAsEmpty(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errors":[{"status":"404"}]}`, http.StatusNotFound)
	})

	tracks, err := client.PlaylistTracks(context.Background(), "p.1", 0)
	if err != nil {
		t.Fatalf("an empty playlist must not be an error, got %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("tracks = %+v, want none", tracks)
	}
}

func TestPlaylistTracksPropagatesOtherErrors(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if _, err := client.PlaylistTracks(context.Background(), "p.1", 0); err == nil {
		t.Error("a 500 should not be swallowed as an empty playlist")
	}
}

func TestCreatePlaylist(t *testing.T) {
	var body map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"p.new","attributes":{"name":"Summer 2026"}}]}`)
	})

	playlist, err := client.CreatePlaylist(context.Background(), "Summer 2026", "sun",
		[]TrackRef{SongRef("1"), SongRef("2")})
	if err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if playlist.ID != "p.new" {
		t.Errorf("id = %q", playlist.ID)
	}

	attrs, _ := body["attributes"].(map[string]any)
	if attrs["name"] != "Summer 2026" || attrs["description"] != "sun" {
		t.Errorf("attributes = %+v", attrs)
	}
	rel, ok := body["relationships"].(map[string]any)
	if !ok {
		t.Fatalf("relationships missing from %+v", body)
	}
	tracks, _ := rel["tracks"].(map[string]any)
	data, _ := tracks["data"].([]any)
	if len(data) != 2 {
		t.Errorf("sent %d tracks, want 2", len(data))
	}
}

// TestCreatePlaylistOmitsEmptyRelationships pins the shape Apple rejects: an
// empty relationships object is a 400, so it must be absent entirely.
func TestCreatePlaylistOmitsEmptyRelationships(t *testing.T) {
	var body map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"p.new"}]}`)
	})

	if _, err := client.CreatePlaylist(context.Background(), "Empty", "", nil); err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if _, present := body["relationships"]; present {
		t.Errorf("relationships was sent for an empty playlist: %+v", body)
	}
	attrs, _ := body["attributes"].(map[string]any)
	if _, present := attrs["description"]; present {
		t.Errorf("an empty description was sent: %+v", attrs)
	}
}

func TestCreatePlaylistReportsMissingID(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	})
	if _, err := client.CreatePlaylist(context.Background(), "x", "", nil); err == nil {
		t.Error("expected an error when Apple returns no id")
	}
}

// TestAddTracksChunks pins the 100-item cap: 250 tracks must go out as 100,
// 100, 50 and not as one request Apple would refuse.
func TestAddTracksChunks(t *testing.T) {
	var sizes []int
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Data []TrackRef `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(body.Data))
		w.WriteHeader(http.StatusNoContent)
	})

	refs := make([]TrackRef, 250)
	for i := range refs {
		refs[i] = SongRef(fmt.Sprint(i))
	}
	if err := client.AddTracks(context.Background(), "p.1", refs); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}
	want := []int{PageLimit, PageLimit, 50}
	if len(sizes) != len(want) {
		t.Fatalf("made %d requests (%v), want %v", len(sizes), sizes, want)
	}
	for i, n := range want {
		if sizes[i] != n {
			t.Errorf("chunk %d had %d tracks, want %d", i, sizes[i], n)
		}
	}
}

func TestAddTracksNamesTheFailedChunk(t *testing.T) {
	var calls atomic.Int64
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 2 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	refs := make([]TrackRef, 150)
	for i := range refs {
		refs[i] = SongRef(fmt.Sprint(i))
	}
	err := client.AddTracks(context.Background(), "p.1", refs)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "101-150") {
		t.Errorf("error %q does not say which tracks failed", err)
	}
}

func TestAddTracksOnEmptySliceIsANoOp(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("no request should be made for an empty track list")
	})
	if err := client.AddTracks(context.Background(), "p.1", nil); err != nil {
		t.Errorf("AddTracks: %v", err)
	}
}

func TestCatalogID(t *testing.T) {
	cases := []struct {
		name  string
		track Track
		want  string
	}{
		{"catalogue song is its own id", Track{ID: "123", Type: "songs"}, "123"},
		{"library song uses playParams", Track{
			ID:         "i.abc",
			Type:       "library-songs",
			Attributes: TrackAttributes{PlayParams: PlayParams{CatalogID: "456"}},
		}, "456"},
		{"library song with no catalogue counterpart", Track{ID: "i.abc", Type: "library-songs"}, ""},
		{"untyped track is treated as catalogue", Track{ID: "789"}, "789"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.track.CatalogID(); got != c.want {
				t.Errorf("CatalogID = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPing(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":{}}`)
	})
	if err := client.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}

	rejecting := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad token", http.StatusUnauthorized)
	})
	if err := rejecting.Ping(context.Background()); !IsUnauthorized(err) {
		t.Errorf("Ping err = %v, want an authorisation failure", err)
	}
}

func TestNewHasWorkingDefaults(t *testing.T) {
	client := New("dev", 5*time.Second)
	if client.base() != DefaultBaseURL {
		t.Errorf("base = %q", client.base())
	}
	if client.HTTP == nil || client.HTTP.Timeout != 5*time.Second {
		t.Errorf("HTTP = %+v", client.HTTP)
	}
	if client.Backoff == 0 {
		t.Error("a zero backoff would hammer Apple on a 429")
	}
	if (&Client{}).http() != http.DefaultClient {
		t.Error("a nil HTTP client should fall back to the default")
	}
}
