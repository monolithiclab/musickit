# Development

## Build and check

```sh
make ci          # lint + test, never mutating — the single command that validates a change
make test        # race detector and total coverage
make lint        # gofmt, vet, staticcheck, golangci-lint, gosec, gocritic, govulncheck (needs network), go mod tidy -diff
make lint-fix    # go fix, then gofmt -s
make build       # → build/musickit
make run         # dry-run import of the 50-track sample list
```

Always go through the Makefile; the flags matter. The linters are pinned in `tools/go.mod` and the Go
toolchain in `go.mod`; `make` runs exactly those versions.

`make run` is a **dry run on purpose**. The wet version writes a playlist to a real Apple Music
library, which is not something a bare `make run` should do by surprise. It doubles as the
end-to-end matching check: it must match 50 of 50.

## Testing strategy

Tests never touch the network and never open a browser. Everything reaches the real world through a
seam:

| Seam                             | Replaced with                          |
| -------------------------------- | -------------------------------------- |
| `applemusic.Client.BaseURL/HTTP` | an `httptest.Server` speaking Apple's JSON |
| `musicapp.App.Run` (`Runner`)    | a fake recording script text and argv  |
| `cli.MusicApp`                   | an in-memory fake playlist library     |
| `cli.Run`'s `configure` hook     | injects both of the above into `Runtime` |
| `auth.Authorizer.Open`/`Addr`    | a stub that drives the page over loopback on port 0 |

The `configure func(*Runtime)` parameter on `cli.Run` is the important one: it exists only for
tests, and it is why `cli_test.go` can exercise whole commands — argv in, exit code and both streams
out — without a network or a Mac.

```go
code := Run(context.Background(), argv, stdio, "test", func(rt *Runtime) {
    rt.NewAPI   = func(context.Context, bool) (*applemusic.Client, error) { … }
    rt.NewMusic = func() (MusicApp, error) { return h.music, nil }
})
```

Kong is configured with `kong.Exit(func(code int) { panic(exitPanic(code)) })` rather than letting it
call `os.Exit`, so `--help` and usage errors are observable in a test.

A handful of `musicapp` tests skip themselves off macOS and run real `osascript` — but only on
scripts that touch nothing in the library. Anything that needs Apple is exercised by hand with `make run`.

Coverage sits around 83% overall. The gaps are the browser handshake's live path and the real
`osascript` runner, both of which are structurally untestable in CI.

### Tests worth not breaking

- `TestDeveloperTokenIsValidES256` — pins the 64-byte `r||s` signature. The failure it prevents is a
  silent 401 from Apple.
- `TestScoreRanking` and friends — pin that karaoke loses and live loses to studio.
- `TestRemoveDeletesHighestIndexFirst` — pins the delete order.
- `TestRenameDeleteCreatePassNamesAsArguments` — pins that names never enter script text.
- `TestScriptsHaveNoBlockComments` — pins the AppleScript comment trap.
- `TestQuietSilencesNarrationOnly` — pins that `--quiet` cannot hide a warning.
- `TestDestructiveCommandsRefuseToGuess` — pins the no-terminal refusal.

Each of these encodes a bug that actually happened or an API constraint that is invisible in the
code. If one starts failing, the test is probably right.

## Adding a command

1. Declare the struct in the right `cli/*.go` file, with Kong tags for its own flags. Embed `Source`
   if it takes a track list; the global flags are already on `Runtime.G`.
2. Add it to `PlaylistCmd` (or `CLI`) with a `cmd:""` tag and one line of help.
3. Write `Run(ctx context.Context, rt *Runtime) error`. Kong injects both.
4. Get data through `rt.API(ctx, needUser)` or `rt.Music()`. Pass `needUser: false` if the command
   only reads the catalogue — that keeps it working before authorisation.
5. Emit through `rt.emit`/`rt.Row`/`rt.JSON` (stdout) and `rt.Logf`/`rt.Warnf` (stderr). Honour
   `rt.G.DryRun` before any write and call `rt.Confirm` before anything irreversible.
6. Return `incomplete(missing, total)` if partial success is possible.
7. Add a case to `cli_test.go` using the harness.

## Conventions

- Go 1.26+. `any`, not `interface{}`. Errors wrapped with `fmt.Errorf("context: %w", err)` only when
  the wrap adds something the callee did not already say.
- Error strings lowercase (ST1005) — reword rather than capitalise, even for "Apple".
- **Never name a local after a builtin or an imported package** (`real`, `min`, `path`,
  `comparable`). `make lint` runs gocritic with `builtinShadow,importShadow` and will fail the
  build. This bites more often than you would expect.
- `#nosec Gxxx -- reason` inline, with the reason, for the operator-supplied paths gosec flags.
- Playlist writes chunk at `applemusic.PageLimit` (100), Apple's per-request maximum.

## Traps

Collected here because each one costs an hour if you meet it cold:

- **There is no endpoint to remove a track, rename a playlist or delete one.** See
  [music-app.md](music-app.md). Do not search for it.
- **`ecdsa.SignASN1` produces a token Apple rejects with a bare 401.** See
  [authentication.md](authentication.md).
- **`--` inside an AppleScript `(* *)` block comment swallows the closing delimiter.** There are no
  block comments in `scripts.go` for that reason.
- **An empty library playlist returns 404, not an empty collection.** `PlaylistTracks` translates
  that single status and nothing else.
- **Apple's `next` link can repeat.** `fetchAll` stops when it fails to advance; removing that check
  gives you an infinite loop against the real API and a passing test suite.
- **Accent folding is hand-rolled** to keep `golang.org/x/text` out of the module. Add runes to
  `foldTable`, not the dependency.
- **A `p.`-prefixed identifier means nothing to AppleScript.** Go through `localPlaylist`.

## Dependencies

Kong, and nothing else. It earns its place: the command grammar, `--help`, env-var binding and
dependency injection would otherwise be several hundred lines of hand-written argument parsing. It
has no runtime dependencies of its own, so the module graph stays one deep. Adding a second
dependency should need an argument at least that good.

## Workflow

Minimal focused change → tests → `make ci` green → commit. Pushing waits to be asked.
