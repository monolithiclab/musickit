package cli

import (
	"context"
)

// ImportCmd is the original entry point, kept because it reads better for the
// thing musickit was written for: turning a file into a playlist. It is a thin
// alias for `playlist create NAME --from FILE`.
type ImportCmd struct {
	File        string `short:"f" help:"Track list to import, or - for stdin." placeholder:"FILE"`
	Playlist    string `short:"p" required:"" help:"Name of the playlist to create." placeholder:"NAME"`
	Description string `short:"d" help:"Playlist description."`
}

// Run creates the playlist.
func (c *ImportCmd) Run(ctx context.Context, rt *Runtime) error {
	create := &PlaylistCreateCmd{
		Name:        c.Playlist,
		Source:      Source{From: c.File},
		Description: c.Description,
	}
	return create.Run(ctx, rt)
}
