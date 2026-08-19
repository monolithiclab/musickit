package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"musickit/internal/applemusic"
	"musickit/internal/config"
	"musickit/internal/match"
	"musickit/internal/tracklist"
)

// Source is the track-list input shared by the commands that take one.
type Source struct {
	From   string   `short:"f" help:"Read the track list from FILE, or - for stdin." placeholder:"FILE"`
	Tracks []string `arg:"" optional:"" name:"track" help:"Tracks as \"Artist - Title\"."`
}

// Entries resolves where the track list comes from. In order: command-line
// arguments, --from, then stdin when it is a pipe. A terminal on stdin is
// never read from — that would look like a hang.
func (rt *Runtime) Entries(src Source) ([]tracklist.Entry, error) {
	switch {
	case len(src.Tracks) > 0 && src.From != "":
		return nil, errors.New("give tracks as arguments or with --from, not both")

	case len(src.Tracks) > 0:
		return tracklist.ParseArgs(src.Tracks), nil

	case src.From == "-":
		return tracklist.Parse(rt.In)

	case src.From != "":
		path := config.ExpandHome(src.From)
		// #nosec G304 -- the path comes from --from, i.e. the operator's own shell.
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return tracklist.Parse(f)

	case !isTerminal(rt.In):
		return tracklist.Parse(rt.In)

	default:
		return nil, errors.New(`no tracks given: pass them as arguments, --from FILE, or pipe a list on stdin`)
	}
}

// resolvePlaylist turns a name or an identifier into a library playlist.
// Names are matched exactly first, then case- and accent-insensitively;
// an ambiguous name is an error listing the candidates rather than a guess.
func resolvePlaylist(ctx context.Context, c *applemusic.Client, ref string) (applemusic.Playlist, error) {
	all, err := c.Playlists(ctx, 0)
	if err != nil {
		return applemusic.Playlist{}, err
	}
	if len(all) == 0 {
		return applemusic.Playlist{}, errors.New("this library has no playlists")
	}

	var exact, loose []applemusic.Playlist
	wanted := match.Normalise(ref)
	for _, p := range all {
		switch {
		case p.ID == ref:
			return p, nil
		case p.Name() == ref:
			exact = append(exact, p)
		case match.Normalise(p.Name()) == wanted:
			loose = append(loose, p)
		}
	}

	for _, candidates := range [][]applemusic.Playlist{exact, loose} {
		switch len(candidates) {
		case 0:
			continue
		case 1:
			return candidates[0], nil
		default:
			return applemusic.Playlist{}, ambiguous(ref, candidates)
		}
	}
	return applemusic.Playlist{}, fmt.Errorf("no playlist called %q — try: musickit playlist list", ref)
}

func ambiguous(ref string, candidates []applemusic.Playlist) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%d playlists match %q; use an id instead:", len(candidates), ref)
	for _, p := range candidates {
		fmt.Fprintf(&b, "\n  %s\t%s", p.ID, p.Name())
	}
	return errors.New(b.String())
}
