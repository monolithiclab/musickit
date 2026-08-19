// Package musicapp drives the Music app on macOS through AppleScript.
//
// It exists because the public Apple Music API is read-mostly: Apple documents
// no endpoint for removing a track from a playlist, renaming a playlist or
// deleting one. The local app can do all three, and a playlist created through
// the API shows up there once iCloud Music Library has synced, so the two
// halves of musickit operate on the same playlists.
//
// The trade-off: these commands only work on a Mac, with the Music app
// installed and the library synced.
package musicapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// ErrUnsupported is returned when the Music app cannot be driven here.
var ErrUnsupported = errors.New("controlling the Music app needs macOS")

// ErrNoPlaylist is returned when the named playlist is not in the local library.
var ErrNoPlaylist = errors.New("no such playlist in the Music app")

// Playlist is a playlist as the Music app sees it.
type Playlist struct {
	Name  string `json:"name"`
	ID    string `json:"id"` // AppleScript persistent ID, not an Apple Music API id
	Count int    `json:"count"`
}

// Track is a track inside a local playlist.
type Track struct {
	Index  int    `json:"index"` // 1-based position in the playlist
	ID     string `json:"id"`    // AppleScript persistent ID
	Artist string `json:"artist"`
	Title  string `json:"title"`
	Album  string `json:"album"`
}

// Runner executes an AppleScript with arguments and returns its output.
type Runner func(ctx context.Context, script string, args ...string) (string, error)

// App is a handle on the local Music app. Tests replace Run.
type App struct {
	Run Runner
}

// New returns an App driving the real Music app, or ErrUnsupported off macOS.
func New() (*App, error) {
	if runtime.GOOS != "darwin" {
		return nil, ErrUnsupported
	}
	if _, err := exec.LookPath("osascript"); err != nil {
		return nil, fmt.Errorf("%w: osascript not found", ErrUnsupported)
	}
	return &App{Run: Osascript}, nil
}

// Osascript runs a script read from stdin, passing args to its `on run argv`
// handler. Passing arguments rather than interpolating them into the source
// keeps playlist and track names — quotes, backslashes and all — out of the
// script text.
func Osascript(ctx context.Context, script string, args ...string) (string, error) {
	// #nosec G204 -- the executable is fixed; args are AppleScript run-handler
	// arguments, never shell words, and are not interpreted by a shell.
	cmd := exec.CommandContext(ctx, "osascript", append([]string{"-"}, args...)...)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, noPlaylistMarker) {
			return "", ErrNoPlaylist
		}
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("osascript: %s", msg)
	}
	return stdout.String(), nil
}

const noPlaylistMarker = "musickit:no-playlist"

// Playlists lists the editable playlists in the local library. Smart playlists
// and the built-in ones are left out: they cannot be edited by hand.
func (a *App) Playlists(ctx context.Context) ([]Playlist, error) {
	out, err := a.Run(ctx, scriptPlaylists)
	if err != nil {
		return nil, err
	}
	var playlists []Playlist
	for _, fields := range records(out, 3) {
		count, _ := strconv.Atoi(fields[2])
		playlists = append(playlists, Playlist{Name: fields[0], ID: fields[1], Count: count})
	}
	return playlists, nil
}

// Tracks lists a playlist's tracks in playlist order.
func (a *App) Tracks(ctx context.Context, playlist string) ([]Track, error) {
	out, err := a.Run(ctx, scriptTracks, playlist)
	if err != nil {
		return nil, err
	}
	var tracks []Track
	for _, fields := range records(out, 5) {
		index, convErr := strconv.Atoi(fields[0])
		if convErr != nil {
			continue
		}
		tracks = append(tracks, Track{
			Index:  index,
			ID:     fields[1],
			Artist: fields[2],
			Title:  fields[3],
			Album:  fields[4],
		})
	}
	return tracks, nil
}

// Remove deletes the given tracks from a playlist, identifying each by both
// position and persistent ID. Positions are deleted highest-first so the
// earlier ones stay valid, and a track whose ID no longer matches its position
// is left alone — if the playlist changed under us, we skip rather than delete
// the wrong song. Returns the tracks actually removed.
func (a *App) Remove(ctx context.Context, playlist string, tracks []Track) ([]Track, error) {
	if len(tracks) == 0 {
		return nil, nil
	}
	ordered := make([]Track, len(tracks))
	copy(ordered, tracks)
	// Highest index first.
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j].Index > ordered[j-1].Index; j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}

	byID := make(map[string]Track, len(ordered))
	args := make([]string, 0, len(ordered)+1)
	args = append(args, playlist)
	for _, t := range ordered {
		args = append(args, strconv.Itoa(t.Index)+":"+t.ID)
		byID[t.ID] = t
	}

	out, err := a.Run(ctx, scriptRemove, args...)
	if err != nil {
		return nil, err
	}

	var removed []Track
	for _, fields := range records(out, 2) {
		if fields[0] != "OK" {
			continue
		}
		if t, ok := byID[fields[1]]; ok {
			removed = append(removed, t)
		}
	}
	return removed, nil
}

// Rename renames a playlist.
func (a *App) Rename(ctx context.Context, playlist, newName string) error {
	_, err := a.Run(ctx, scriptRename, playlist, newName)
	return err
}

// Delete deletes a playlist. Its tracks stay in the library.
func (a *App) Delete(ctx context.Context, playlist string) error {
	_, err := a.Run(ctx, scriptDelete, playlist)
	return err
}

// Create makes an empty playlist. Tracks are added over the API, which can
// reach the whole catalogue; the local app can only add what is already in the
// library.
func (a *App) Create(ctx context.Context, name string) error {
	_, err := a.Run(ctx, scriptCreate, name)
	return err
}

// records splits AppleScript output into tab-separated fields, dropping blank
// and short lines.
func records(out string, fields int) [][]string {
	var recs [][]string
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", fields)
		if len(parts) < fields {
			continue
		}
		recs = append(recs, parts)
	}
	return recs
}
