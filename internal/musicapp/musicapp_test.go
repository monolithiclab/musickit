package musicapp

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// fakeRunner records what would have been sent to osascript and replays a
// canned answer, so the whole package can be tested without a Music app.
type fakeRunner struct {
	out  string
	err  error
	call struct {
		script string
		args   []string
	}
	calls int
}

func (f *fakeRunner) run(_ context.Context, script string, args ...string) (string, error) {
	f.calls++
	f.call.script = script
	f.call.args = args
	return f.out, f.err
}

func appWith(f *fakeRunner) *App { return &App{Run: f.run} }

func TestPlaylists(t *testing.T) {
	f := &fakeRunner{out: "Summer 2026\tA1B2C3\t50\nRoad Trip\tD4E5F6\t12\n"}

	playlists, err := appWith(f).Playlists(context.Background())
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	want := []Playlist{
		{Name: "Summer 2026", ID: "A1B2C3", Count: 50},
		{Name: "Road Trip", ID: "D4E5F6", Count: 12},
	}
	if !slices.Equal(playlists, want) {
		t.Errorf("playlists = %+v, want %+v", playlists, want)
	}
	if len(f.call.args) != 0 {
		t.Errorf("Playlists passed arguments %q; it takes none", f.call.args)
	}
	if !strings.Contains(f.call.script, "smart is false") {
		t.Error("the script does not exclude smart playlists, which cannot be edited by hand")
	}
}

func TestTracks(t *testing.T) {
	f := &fakeRunner{out: "1\tPID1\tBlur\tSong 2\tBlur\n2\tPID2\tAir\tSexy Boy\tMoon Safari\n"}

	tracks, err := appWith(f).Tracks(context.Background(), "Summer 2026")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2: %+v", len(tracks), tracks)
	}
	if tracks[0] != (Track{Index: 1, ID: "PID1", Artist: "Blur", Title: "Song 2", Album: "Blur"}) {
		t.Errorf("track 0 = %+v", tracks[0])
	}
	if got := f.call.args; len(got) != 1 || got[0] != "Summer 2026" {
		t.Errorf("args = %q, want the playlist name passed as argv", got)
	}
}

// TestTracksKeepsTabsInNames pins the reason for SplitN: an album called
// "Songs\tFor" would otherwise shift every field.
func TestTracksKeepsTabsInNames(t *testing.T) {
	f := &fakeRunner{out: "1\tPID1\tBlur\tSong 2\tGreatest\tHits\n"}
	tracks, err := appWith(f).Tracks(context.Background(), "x")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Album != "Greatest\tHits" {
		t.Errorf("tracks = %+v; the trailing tab should stay in the last field", tracks)
	}
}

func TestTracksIgnoresMalformedLines(t *testing.T) {
	f := &fakeRunner{out: "\nnot a record\n1\tPID1\tBlur\tSong 2\tBlur\nx\tPID2\tA\tB\tC\n"}
	tracks, err := appWith(f).Tracks(context.Background(), "x")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 {
		t.Errorf("got %d tracks, want only the well-formed one: %+v", len(tracks), tracks)
	}
}

func TestTracksOnEmptyPlaylist(t *testing.T) {
	tracks, err := appWith(&fakeRunner{out: "\n"}).Tracks(context.Background(), "x")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("tracks = %+v, want none", tracks)
	}
}

// TestRemoveDeletesHighestIndexFirst is the whole safety argument for Remove:
// deleting position 2 before position 5 would shift 5 onto a different song.
func TestRemoveDeletesHighestIndexFirst(t *testing.T) {
	f := &fakeRunner{out: "OK\tPID5\nOK\tPID3\nOK\tPID1\n"}
	tracks := []Track{
		{Index: 1, ID: "PID1", Title: "One"},
		{Index: 5, ID: "PID5", Title: "Five"},
		{Index: 3, ID: "PID3", Title: "Three"},
	}

	removed, err := appWith(f).Remove(context.Background(), "Summer 2026", tracks)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}

	want := []string{"Summer 2026", "5:PID5", "3:PID3", "1:PID1"}
	if !slices.Equal(f.call.args, want) {
		t.Errorf("args = %q, want %q", f.call.args, want)
	}
	if len(removed) != 3 {
		t.Errorf("removed %d tracks, want 3: %+v", len(removed), removed)
	}
}

// TestRemoveReportsOnlyWhatWentAway pins the SKIP path: the playlist changed
// under us, AppleScript refused, and the CLI must not claim a deletion.
func TestRemoveReportsOnlyWhatWentAway(t *testing.T) {
	f := &fakeRunner{out: "OK\tPID5\nSKIP\tPID3\nOK\tPID1\n"}
	tracks := []Track{
		{Index: 1, ID: "PID1", Title: "One"},
		{Index: 3, ID: "PID3", Title: "Three"},
		{Index: 5, ID: "PID5", Title: "Five"},
	}

	removed, err := appWith(f).Remove(context.Background(), "p", tracks)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %+v, want the two OK tracks", removed)
	}
	for _, track := range removed {
		if track.ID == "PID3" {
			t.Error("a skipped track was reported as removed")
		}
	}
}

func TestRemoveIgnoresUnknownIDs(t *testing.T) {
	f := &fakeRunner{out: "OK\tPID-NOT-OURS\n"}
	removed, err := appWith(f).Remove(context.Background(), "p", []Track{{Index: 1, ID: "PID1"}})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %+v; an ID we never sent must not be reported", removed)
	}
}

func TestRemoveOnEmptySliceRunsNothing(t *testing.T) {
	f := &fakeRunner{}
	removed, err := appWith(f).Remove(context.Background(), "p", nil)
	if err != nil || removed != nil {
		t.Errorf("Remove = %+v, %v", removed, err)
	}
	if f.calls != 0 {
		t.Error("an empty removal should not start osascript")
	}
}

func TestRemoveDoesNotReorderTheCallersSlice(t *testing.T) {
	f := &fakeRunner{out: "OK\tPID1\n"}
	tracks := []Track{{Index: 1, ID: "PID1"}, {Index: 9, ID: "PID9"}}
	if _, err := appWith(f).Remove(context.Background(), "p", tracks); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if tracks[0].ID != "PID1" || tracks[1].ID != "PID9" {
		t.Errorf("the caller's slice was reordered: %+v", tracks)
	}
}

func TestRemovePropagatesErrors(t *testing.T) {
	f := &fakeRunner{err: ErrNoPlaylist}
	_, err := appWith(f).Remove(context.Background(), "gone", []Track{{Index: 1, ID: "PID1"}})
	if !errors.Is(err, ErrNoPlaylist) {
		t.Errorf("err = %v, want ErrNoPlaylist", err)
	}
}

func TestRenameDeleteCreatePassNamesAsArguments(t *testing.T) {
	// A name full of quotes and backslashes is exactly what interpolation would
	// break; argv must carry it through untouched.
	nasty := `He said "hi" \ 'bye' — 100%`

	cases := []struct {
		name string
		call func(*App) error
		want []string
	}{
		{"rename", func(a *App) error { return a.Rename(context.Background(), nasty, "new "+nasty) }, []string{nasty, "new " + nasty}},
		{"delete", func(a *App) error { return a.Delete(context.Background(), nasty) }, []string{nasty}},
		{"create", func(a *App) error { return a.Create(context.Background(), nasty) }, []string{nasty}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeRunner{}
			if err := c.call(appWith(f)); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if !slices.Equal(f.call.args, c.want) {
				t.Errorf("args = %q, want %q", f.call.args, c.want)
			}
			if strings.Contains(f.call.script, nasty) {
				t.Error("the name was interpolated into the script text")
			}
		})
	}
}

// TestScriptsHaveNoBlockComments guards the AppleScript trap: "--" inside a
// (* *) comment opens a nested line comment that eats the closing delimiter.
func TestScriptsHaveNoBlockComments(t *testing.T) {
	for name, script := range map[string]string{
		"playlists": scriptPlaylists,
		"tracks":    scriptTracks,
		"remove":    scriptRemove,
		"rename":    scriptRename,
		"delete":    scriptDelete,
		"create":    scriptCreate,
	} {
		if strings.Contains(script, "(*") {
			t.Errorf("%s uses a block comment", name)
		}
		if !strings.Contains(script, "on run argv") {
			t.Errorf("%s has no run handler, so argv would be unreachable", name)
		}
	}
}

func TestNewOffMacOS(t *testing.T) {
	app, err := New()
	if runtime.GOOS == "darwin" {
		if err != nil {
			t.Skipf("no osascript here: %v", err)
		}
		if app.Run == nil {
			t.Error("New returned an App with no runner")
		}
		return
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

// TestOsascriptPassesArgv exercises the real osascript, with a script that
// touches nothing: it proves the stdin-script plus argv mechanism the whole
// package rests on. It never opens or reads the Music library.
func TestOsascriptPassesArgv(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("osascript is macOS only")
	}
	if testing.Short() {
		t.Skip("spawns a process")
	}

	script := "on run argv\n\treturn (item 1 of argv) & \"|\" & (item 2 of argv)\nend run\n"
	out, err := Osascript(context.Background(), script, `quotes "and" \backslashes\`, "second")
	if err != nil {
		t.Fatalf("Osascript: %v", err)
	}
	if got := strings.TrimSpace(out); got != `quotes "and" \backslashes\|second` {
		t.Errorf("output = %q; argv did not survive intact", got)
	}
}

func TestOsascriptTranslatesTheNoPlaylistMarker(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("osascript is macOS only")
	}
	if testing.Short() {
		t.Skip("spawns a process")
	}

	_, err := Osascript(context.Background(), "on run argv\n\terror \""+noPlaylistMarker+"\"\nend run\n")
	if !errors.Is(err, ErrNoPlaylist) {
		t.Errorf("err = %v, want ErrNoPlaylist", err)
	}
}

func TestOsascriptReportsScriptErrors(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("osascript is macOS only")
	}
	if testing.Short() {
		t.Skip("spawns a process")
	}

	_, err := Osascript(context.Background(), "this is not applescript at all {")
	if err == nil {
		t.Fatal("expected a compilation error")
	}
	if errors.Is(err, ErrNoPlaylist) {
		t.Errorf("err = %v, want the compiler message, not ErrNoPlaylist", err)
	}
}
