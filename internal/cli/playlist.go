package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/monolithiclab/musickit/internal/applemusic"
	"github.com/monolithiclab/musickit/internal/tracklist"
)

// PlaylistCmd groups the playlist verbs.
type PlaylistCmd struct {
	List   PlaylistListCmd   `cmd:"" aliases:"ls" help:"List the playlists in the library."`
	Show   PlaylistShowCmd   `cmd:"" help:"List the tracks of a playlist."`
	Export PlaylistExportCmd `cmd:"" help:"Print a playlist as an \"Artist - Title\" list."`
	Create PlaylistCreateCmd `cmd:"" aliases:"new" help:"Create a playlist, optionally from a track list."`
	Add    PlaylistAddCmd    `cmd:"" help:"Add tracks to a playlist."`
	Remove PlaylistRemoveCmd `cmd:"" aliases:"rm" help:"Remove tracks from a playlist (drives the Music app)."`
	Rename PlaylistRenameCmd `cmd:"" aliases:"mv" help:"Rename a playlist (drives the Music app)."`
	Delete PlaylistDeleteCmd `cmd:"" help:"Delete a playlist (drives the Music app)."`
}

// record is one line of machine-readable output. Every command that acts on
// tracks emits these, so their output can be diffed and piped alike.
type record struct {
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
	ID     string `json:"id,omitempty"`
	Artist string `json:"artist,omitempty"`
	Title  string `json:"title,omitempty"`
	Album  string `json:"album,omitempty"`
	Score  int    `json:"score,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func (rt *Runtime) emit(r record) error {
	if rt.G.JSON {
		return rt.JSON(r)
	}
	rt.Row(r.Status, r.Source, r.ID, r.Artist, r.Title, r.Album)
	return nil
}

// PlaylistListCmd lists playlists.
type PlaylistListCmd struct {
	Limit int  `short:"l" help:"Stop after this many playlists."`
	Local bool `help:"List from the Music app instead of the API: no network, and it shows exactly what remove, rename and delete can reach."`
}

// Run lists the playlists.
func (c *PlaylistListCmd) Run(ctx context.Context, rt *Runtime) error {
	if c.Local {
		return c.runLocal(ctx, rt)
	}
	client, err := rt.API(ctx, true)
	if err != nil {
		return err
	}
	playlists, err := client.Playlists(ctx, c.Limit)
	if err != nil {
		return err
	}
	for _, p := range playlists {
		if rt.G.JSON {
			if err := rt.JSON(p); err != nil {
				return err
			}
			continue
		}
		rt.Row(p.ID, p.Name())
	}
	rt.Logf("%d playlist(s).", len(playlists))
	return nil
}

func (c *PlaylistListCmd) runLocal(ctx context.Context, rt *Runtime) error {
	music, err := rt.Music()
	if err != nil {
		return err
	}
	playlists, err := music.Playlists(ctx)
	if err != nil {
		return err
	}
	if c.Limit > 0 && len(playlists) > c.Limit {
		playlists = playlists[:c.Limit]
	}
	for _, p := range playlists {
		if rt.G.JSON {
			if err := rt.JSON(p); err != nil {
				return err
			}
			continue
		}
		rt.Row(p.ID, p.Name, strconv.Itoa(p.Count))
	}
	rt.Logf("%d editable playlist(s) in the Music app.", len(playlists))
	return nil
}

// PlaylistShowCmd prints a playlist's tracks.
type PlaylistShowCmd struct {
	Playlist string `arg:"" help:"Playlist name or id."`
	Limit    int    `short:"l" help:"Stop after this many tracks."`
}

// Run prints the tracks.
func (c *PlaylistShowCmd) Run(ctx context.Context, rt *Runtime) error {
	playlist, tracks, err := loadPlaylist(ctx, rt, c.Playlist, c.Limit)
	if err != nil {
		return err
	}
	for i, t := range tracks {
		if rt.G.JSON {
			if err := rt.JSON(t); err != nil {
				return err
			}
			continue
		}
		rt.Row(strconv.Itoa(i+1), t.CatalogID(), t.Attributes.ArtistName, t.Attributes.Name, t.Attributes.AlbumName)
	}
	rt.Logf("%s: %d track(s).", playlist.Name(), len(tracks))
	return nil
}

// PlaylistExportCmd prints a playlist in the source-list format, so it can be
// piped straight back into add, or diffed against the file it came from.
type PlaylistExportCmd struct {
	Playlist string `arg:"" help:"Playlist name or id."`
	Limit    int    `short:"l" help:"Stop after this many tracks."`
}

// Run prints the list.
func (c *PlaylistExportCmd) Run(ctx context.Context, rt *Runtime) error {
	playlist, tracks, err := loadPlaylist(ctx, rt, c.Playlist, c.Limit)
	if err != nil {
		return err
	}
	entries := make([]tracklist.Entry, 0, len(tracks))
	for _, t := range tracks {
		entries = append(entries, tracklist.Entry{Artist: t.Attributes.ArtistName, Title: t.Attributes.Name})
	}
	if err := tracklist.Write(rt.Out, entries); err != nil {
		return err
	}
	rt.Logf("%s: %d track(s).", playlist.Name(), len(entries))
	return nil
}

// openPlaylist dials the API and resolves a name or id to a playlist.
func openPlaylist(ctx context.Context, rt *Runtime, ref string) (*applemusic.Client, applemusic.Playlist, error) {
	client, err := rt.API(ctx, true)
	if err != nil {
		return nil, applemusic.Playlist{}, err
	}
	playlist, err := resolvePlaylist(ctx, client, ref)
	if err != nil {
		return nil, applemusic.Playlist{}, err
	}
	return client, playlist, nil
}

func loadPlaylist(ctx context.Context, rt *Runtime, ref string, limit int) (applemusic.Playlist, []applemusic.Track, error) {
	client, playlist, err := openPlaylist(ctx, rt, ref)
	if err != nil {
		return applemusic.Playlist{}, nil, err
	}
	tracks, err := client.PlaylistTracks(ctx, playlist.ID, limit)
	if err != nil {
		return playlist, nil, fmt.Errorf("reading %q: %w", playlist.Name(), err)
	}
	return playlist, tracks, nil
}
