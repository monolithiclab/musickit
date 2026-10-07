package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monolithiclab/musickit/internal/applemusic"
	"github.com/monolithiclab/musickit/internal/match"
	"github.com/monolithiclab/musickit/internal/musicapp"
	"github.com/monolithiclab/musickit/internal/tracklist"
)

// PlaylistCreateCmd creates a playlist, optionally filling it from a list.
type PlaylistCreateCmd struct {
	Name        string `arg:"" help:"Name for the new playlist."`
	Source      `embed:""`
	Description string `short:"d" help:"Playlist description."`
}

// Run creates the playlist.
func (c *PlaylistCreateCmd) Run(ctx context.Context, rt *Runtime) error {
	entries, err := rt.optionalEntries(c.Source)
	if err != nil {
		return err
	}
	client, err := rt.API(ctx, true)
	if err != nil {
		return err
	}

	refs, missing, err := rt.resolve(ctx, client, entries)
	if err != nil {
		return err
	}

	if rt.G.DryRun {
		rt.Logf("Dry run: would create %q with %d track(s).", c.Name, len(refs))
		return incomplete(missing, len(entries))
	}

	description := c.Description
	if description == "" && len(entries) > 0 {
		description = "Created by musickit on " + time.Now().Format("2006-01-02")
	}

	head := refs
	if len(head) > applemusic.PageLimit {
		head = head[:applemusic.PageLimit]
	}
	playlist, err := client.CreatePlaylist(ctx, c.Name, description, head)
	if err != nil {
		return err
	}
	if len(refs) > applemusic.PageLimit {
		if err := client.AddTracks(ctx, playlist.ID, refs[applemusic.PageLimit:]); err != nil {
			return fmt.Errorf("created %q (%s) but: %w", c.Name, playlist.ID, err)
		}
	}

	rt.Logf("Created %q with %d track(s) (id %s).", c.Name, len(refs), playlist.ID)
	return incomplete(missing, len(entries))
}

// PlaylistAddCmd adds tracks to an existing playlist.
type PlaylistAddCmd struct {
	Playlist string `arg:"" help:"Playlist name or id."`
	Source   `embed:""`
}

// Run adds the tracks.
func (c *PlaylistAddCmd) Run(ctx context.Context, rt *Runtime) error {
	entries, err := rt.Entries(c.Source)
	if err != nil {
		return err
	}
	client, playlist, err := openPlaylist(ctx, rt, c.Playlist)
	if err != nil {
		return err
	}

	refs, missing, err := rt.resolve(ctx, client, entries)
	if err != nil {
		return err
	}
	if rt.G.DryRun {
		rt.Logf("Dry run: would add %d track(s) to %q.", len(refs), playlist.Name())
		return incomplete(missing, len(entries))
	}
	if len(refs) > 0 {
		if err := client.AddTracks(ctx, playlist.ID, refs); err != nil {
			return err
		}
	}
	rt.Logf("Added %d track(s) to %q.", len(refs), playlist.Name())
	return incomplete(missing, len(entries))
}

// PlaylistRemoveCmd removes tracks from a playlist.
//
// This one goes through the Music app: Apple's public API has no endpoint for
// removing a track from a playlist. See the musicapp package.
type PlaylistRemoveCmd struct {
	Playlist string `arg:"" help:"Playlist name or id."`
	Source   `embed:""`
	All      bool `help:"Remove every track, leaving the playlist empty."`
}

// Run removes the tracks.
func (c *PlaylistRemoveCmd) Run(ctx context.Context, rt *Runtime) error {
	if c.All && (len(c.Tracks) > 0 || c.From != "") {
		return errors.New("--all removes everything; do not also give a track list")
	}
	var entries []tracklist.Entry
	if !c.All {
		var err error
		if entries, err = rt.Entries(c.Source); err != nil {
			return err
		}
	}

	name, err := rt.localPlaylist(ctx, c.Playlist)
	if err != nil {
		return err
	}
	music, err := rt.Music()
	if err != nil {
		return err
	}
	present, err := music.Tracks(ctx, name)
	if err != nil {
		return err
	}

	victims, misses := selectTracks(present, entries, c.All)
	for _, e := range misses {
		if err := rt.emit(record{Status: "not-in-playlist", Source: e.Line, Artist: e.Artist, Title: e.Title}); err != nil {
			return err
		}
	}
	if len(victims) == 0 {
		rt.Logf("Nothing to remove from %q.", name)
		return incomplete(len(misses), len(entries))
	}

	if rt.G.DryRun {
		for _, t := range victims {
			if err := rt.emit(record{Status: "would-remove", ID: t.ID, Artist: t.Artist, Title: t.Title, Album: t.Album}); err != nil {
				return err
			}
		}
		rt.Logf("Dry run: would remove %d track(s) from %q.", len(victims), name)
		return incomplete(len(misses), len(entries))
	}

	if err := rt.Confirm("Remove %d track(s) from %q?", len(victims), name); err != nil {
		return err
	}
	removed, err := music.Remove(ctx, name, victims)
	if err != nil {
		return err
	}
	for _, t := range removed {
		if err := rt.emit(record{Status: "removed", ID: t.ID, Artist: t.Artist, Title: t.Title, Album: t.Album}); err != nil {
			return err
		}
	}
	rt.Logf("Removed %d of %d track(s) from %q.", len(removed), len(victims), name)
	if skipped := len(victims) - len(removed); skipped > 0 {
		rt.Warnf("%d track(s) were skipped: the playlist changed while we worked. Re-run to finish.", skipped)
	}
	return incomplete(len(misses), len(entries))
}

// PlaylistRenameCmd renames a playlist through the Music app.
type PlaylistRenameCmd struct {
	Playlist string `arg:"" help:"Playlist name or id."`
	NewName  string `arg:"" help:"New name."`
}

// Run renames the playlist.
func (c *PlaylistRenameCmd) Run(ctx context.Context, rt *Runtime) error {
	name, err := rt.localPlaylist(ctx, c.Playlist)
	if err != nil {
		return err
	}
	if rt.G.DryRun {
		rt.Logf("Dry run: would rename %q to %q.", name, c.NewName)
		return nil
	}
	music, err := rt.Music()
	if err != nil {
		return err
	}
	if err := music.Rename(ctx, name, c.NewName); err != nil {
		return err
	}
	rt.Logf("Renamed %q to %q.", name, c.NewName)
	return rt.emit(record{Status: "renamed", Source: name, Title: c.NewName})
}

// PlaylistDeleteCmd deletes a playlist through the Music app. The tracks stay
// in the library.
type PlaylistDeleteCmd struct {
	Playlist string `arg:"" help:"Playlist name or id."`
}

// Run deletes the playlist.
func (c *PlaylistDeleteCmd) Run(ctx context.Context, rt *Runtime) error {
	name, err := rt.localPlaylist(ctx, c.Playlist)
	if err != nil {
		return err
	}
	music, err := rt.Music()
	if err != nil {
		return err
	}
	tracks, err := music.Tracks(ctx, name)
	if err != nil {
		return err
	}
	if rt.G.DryRun {
		rt.Logf("Dry run: would delete %q (%d track(s)).", name, len(tracks))
		return nil
	}
	if err := rt.Confirm("Delete the playlist %q (%d track(s))? The tracks stay in your library.", name, len(tracks)); err != nil {
		return err
	}
	if err := music.Delete(ctx, name); err != nil {
		return err
	}
	rt.Logf("Deleted %q.", name)
	return rt.emit(record{Status: "deleted", Source: name})
}

// resolve searches the catalogue for every entry and emits one record per
// line. It returns the references to add and the number of misses.
func (rt *Runtime) resolve(ctx context.Context, client *applemusic.Client, entries []tracklist.Entry) (refs []applemusic.TrackRef, missing int, err error) {
	if len(entries) == 0 {
		return nil, 0, nil
	}
	rt.Logf("Matching %d line(s) against the %s storefront...", len(entries), client.Storefront)

	status := "added"
	if rt.G.DryRun {
		status = "would-add"
	}
	for _, result := range match.Resolve(ctx, client, entries, match.DefaultWorkers) {
		if ctx.Err() != nil {
			return nil, missing, ctx.Err()
		}
		if !result.Found {
			missing++
			reason := ""
			if result.Err != nil {
				reason = result.Err.Error()
			}
			if err := rt.emit(record{Status: "missing", Source: result.Entry.Line, Reason: reason}); err != nil {
				return nil, missing, err
			}
			continue
		}
		refs = append(refs, applemusic.SongRef(result.Track.ID))
		emitErr := rt.emit(record{
			Status: status,
			Source: result.Entry.Line,
			ID:     result.Track.ID,
			Artist: result.Track.Attributes.ArtistName,
			Title:  result.Track.Attributes.Name,
			Album:  result.Track.Attributes.AlbumName,
			Score:  result.Score,
		})
		if emitErr != nil {
			return nil, missing, emitErr
		}
	}
	if missing > 0 {
		rt.Warnf("%d of %d line(s) had no confident match and were skipped.", missing, len(entries))
	}
	return refs, missing, nil
}

// optionalEntries is Entries for commands where an empty list is legitimate:
// creating an empty playlist is a real thing to want.
func (rt *Runtime) optionalEntries(src Source) ([]tracklist.Entry, error) {
	if len(src.Tracks) == 0 && src.From == "" && isTerminal(rt.In) {
		return nil, nil
	}
	return rt.Entries(src)
}

// localPlaylist maps a playlist reference to a name the Music app knows. An
// API identifier is translated through the API first; a name is checked
// against the local library so a typo fails before anything is deleted.
func (rt *Runtime) localPlaylist(ctx context.Context, ref string) (string, error) {
	name := ref
	if strings.HasPrefix(ref, "p.") {
		_, playlist, err := openPlaylist(ctx, rt, ref)
		if err != nil {
			return "", err
		}
		name = playlist.Name()
	}

	music, err := rt.Music()
	if err != nil {
		return "", err
	}
	playlists, err := music.Playlists(ctx)
	if err != nil {
		return "", err
	}

	var loose []string
	wanted := match.Normalise(name)
	for _, p := range playlists {
		if p.Name == name {
			return p.Name, nil
		}
		if match.Normalise(p.Name) == wanted {
			loose = append(loose, p.Name)
		}
	}
	switch len(loose) {
	case 0:
		return "", fmt.Errorf("%w: %q — try: musickit playlist list --local", musicapp.ErrNoPlaylist, name)
	case 1:
		return loose[0], nil
	default:
		return "", fmt.Errorf("%d playlists in the Music app match %q: %s", len(loose), name, strings.Join(loose, ", "))
	}
}

// selectTracks pairs wanted entries with the tracks actually in the playlist.
// Each entry consumes at most one track, so asking twice for the same song
// removes two copies of it and no more.
func selectTracks(present []musicapp.Track, entries []tracklist.Entry, all bool) (victims []musicapp.Track, misses []tracklist.Entry) {
	if all {
		return present, nil
	}
	taken := make([]bool, len(present))
	for _, e := range entries {
		found := false
		for i, t := range present {
			if taken[i] || !entryMatches(e, t) {
				continue
			}
			taken[i], found = true, true
			victims = append(victims, t)
			break
		}
		if !found {
			misses = append(misses, e)
		}
	}
	return victims, misses
}

func entryMatches(e tracklist.Entry, t musicapp.Track) bool {
	if e.Unparsed || e.Artist == "" {
		return match.Normalise(t.Title) == match.Normalise(e.Title)
	}
	return match.Equal(t.Artist, t.Title, e.Artist, e.Title)
}

// incomplete reports the "worked, but not for everything" outcome, which the
// shell sees as exit code 3.
func incomplete(missing, total int) error {
	if missing == 0 {
		return nil
	}
	return &incompleteError{missing: missing, total: total}
}
