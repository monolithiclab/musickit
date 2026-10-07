// Package cli is musickit's command-line interface.
//
// Two conventions run through every command:
//
//   - stdout carries data, stderr carries narration. Records on stdout are
//     tab-separated (or NDJSON under --json) so they pipe into cut, grep and
//     jq; progress and summaries go to stderr and vanish under --quiet.
//   - track lists are one format everywhere. A file, stdin and command-line
//     arguments are all parsed by tracklist, so `musickit playlist export A |
//     musickit playlist add B` works.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"github.com/monolithiclab/musickit/internal/applemusic"
	"github.com/monolithiclab/musickit/internal/config"
	"github.com/monolithiclab/musickit/internal/musicapp"
)

// Exit codes. 2 for usage and 130 for interruption are the usual shell
// conventions; 3 is musickit's own "it worked, but not for every track".
const (
	ExitOK         = 0
	ExitError      = 1
	ExitUsage      = 2
	ExitIncomplete = 3
	ExitInterrupt  = 130
)

const description = `Manage Apple Music playlists from the command line.

Catalogue search and playlist reads and writes go over the Apple Music API.
Removing tracks, renaming and deleting are done through the Music app on
macOS: Apple's public API documents no endpoint for any of them.

Exit codes: 0 success, 1 error, 2 usage, 3 finished with unmatched tracks.`

// IO is the process's standard streams, so tests can substitute buffers.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// CLI is the command grammar.
type CLI struct {
	Globals `embed:""`

	Auth     AuthCmd          `cmd:"" help:"Authorise with Apple Music and cache the user token."`
	Search   SearchCmd        `cmd:"" help:"Search the catalogue."`
	Playlist PlaylistCmd      `cmd:"" aliases:"pl" help:"Manage library playlists."`
	Import   ImportCmd        `cmd:"" help:"Create a playlist from a track list."`
	Version  kong.VersionFlag `short:"v" help:"Print the version and exit."`
}

// Globals are the flags every command accepts.
type Globals struct {
	ConfigDir  string        `help:"Configuration directory." env:"MUSICKIT_CONFIG_DIR" placeholder:"DIR"`
	Storefront string        `help:"Storefront code, e.g. us or fr (default: the account's own)." env:"MUSICKIT_STOREFRONT" placeholder:"CC"`
	JSON       bool          `name:"json" help:"Emit one JSON object per line instead of tab-separated text."`
	Quiet      bool          `short:"q" help:"Suppress progress and summaries on stderr."`
	Yes        bool          `short:"y" help:"Answer yes to confirmation prompts."`
	DryRun     bool          `short:"n" name:"dry-run" help:"Report what would change; write nothing."`
	Timeout    time.Duration `default:"30s" help:"Per-request network timeout."`
}

// Main parses argv, runs the selected command and returns a process exit code.
func Main(argv []string, stdio IO, version string) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return Run(ctx, argv, stdio, version, nil)
}

// Run is Main with the signal handling hoisted out and the runtime left open
// to adjustment, which is how the tests reach in with a stub API and a stub
// Music app.
func Run(ctx context.Context, argv []string, stdio IO, version string, configure func(*Runtime)) int {
	cli := &CLI{}
	parser, err := kong.New(cli,
		kong.Name("musickit"),
		kong.Description(description),
		kong.UsageOnError(),
		kong.Writers(stdio.Out, stdio.Err),
		kong.Vars{"version": "musickit " + version},
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
		// Kong would otherwise call os.Exit itself, which a test cannot survive.
		kong.Exit(func(code int) { panic(exitPanic(code)) }),
	)
	if err != nil {
		fmt.Fprintf(stdio.Err, "musickit: %v\n", err)
		return ExitError
	}

	code, err := dispatch(ctx, parser, cli, argv, stdio, configure)
	if err == nil {
		return code
	}

	switch {
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(stdio.Err, "musickit: interrupted")
		return ExitInterrupt
	case errors.Is(err, musicapp.ErrUnsupported), errors.Is(err, musicapp.ErrNoPlaylist):
		fmt.Fprintf(stdio.Err, "musickit: %v\n", err)
		return ExitError
	}

	fmt.Fprintf(stdio.Err, "musickit: %v\n", err)
	if hint := hintFor(err); hint != "" {
		fmt.Fprintf(stdio.Err, "  %s\n", hint)
	}
	if _, ok := errors.AsType[*incompleteError](err); ok {
		return ExitIncomplete
	}
	return ExitError
}

// dispatch isolates the panic-based exit kong uses for --help and parse errors.
func dispatch(ctx context.Context, parser *kong.Kong, cli *CLI, argv []string, stdio IO, configure func(*Runtime)) (code int, err error) {
	defer func() {
		if r := recover(); r != nil {
			if ec, ok := r.(exitPanic); ok {
				code, err = int(ec), nil
				return
			}
			panic(r)
		}
	}()

	kctx, parseErr := parser.Parse(argv)
	if parseErr != nil {
		parser.Errorf("%s", parseErr)
		return ExitUsage, nil
	}

	rt := NewRuntime(&cli.Globals, stdio)
	if configure != nil {
		configure(rt)
	}
	kctx.BindTo(ctx, (*context.Context)(nil))
	kctx.Bind(rt)
	return ExitOK, kctx.Run()
}

type exitPanic int

// incompleteError marks a command that did its job but could not match every
// track. It maps to exit code 3.
type incompleteError struct {
	missing int
	total   int
}

func (e *incompleteError) Error() string {
	return fmt.Sprintf("%d of %d track(s) could not be matched", e.missing, e.total)
}

// hintFor turns Apple's opaque failures into the next thing to try.
func hintFor(err error) string {
	if applemusic.IsUnauthorized(err) {
		return "check teamId, keyId and AuthKey.p8; if they are right, the cached user token has expired — run: musickit auth --reauth"
	}
	if errors.Is(err, musicapp.ErrUnsupported) {
		return "this command drives the Music app, which needs macOS"
	}
	return ""
}

// Runtime carries everything a command needs that is not one of its own flags.
type Runtime struct {
	G     *Globals
	In    io.Reader
	Out   io.Writer
	Err   io.Writer
	Paths config.Paths

	// NewAPI and NewMusic are replaced in tests.
	NewAPI   func(ctx context.Context, needUser bool) (*applemusic.Client, error)
	NewMusic func() (MusicApp, error)

	client   *applemusic.Client
	haveUser bool
	music    MusicApp

	// Set by the auth command before the client is dialled.
	authPort int
	reauth   bool
}

// MusicApp is the slice of the local Music app the commands use.
type MusicApp interface {
	Playlists(ctx context.Context) ([]musicapp.Playlist, error)
	Tracks(ctx context.Context, playlist string) ([]musicapp.Track, error)
	Remove(ctx context.Context, playlist string, tracks []musicapp.Track) ([]musicapp.Track, error)
	Rename(ctx context.Context, playlist, newName string) error
	Delete(ctx context.Context, playlist string) error
	Create(ctx context.Context, name string) error
}

// NewRuntime builds the runtime the real commands use.
func NewRuntime(g *Globals, stdio IO) *Runtime {
	rt := &Runtime{
		G:     g,
		In:    stdio.In,
		Out:   stdio.Out,
		Err:   stdio.Err,
		Paths: config.Discover(g.ConfigDir),
	}
	rt.NewAPI = rt.dialAPI
	rt.NewMusic = func() (MusicApp, error) { return musicapp.New() }
	return rt
}

// API returns a configured client, authorising in the browser if a user token
// is needed and none is cached.
func (rt *Runtime) API(ctx context.Context, needUser bool) (*applemusic.Client, error) {
	if rt.client != nil && (rt.haveUser || !needUser) {
		return rt.client, nil
	}
	c, err := rt.NewAPI(ctx, needUser)
	if err != nil {
		return nil, err
	}
	rt.client, rt.haveUser = c, needUser || c.UserToken != ""
	return c, nil
}

// Music returns a handle on the local Music app.
func (rt *Runtime) Music() (MusicApp, error) {
	if rt.music != nil {
		return rt.music, nil
	}
	m, err := rt.NewMusic()
	if err != nil {
		return nil, err
	}
	rt.music = m
	return m, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
