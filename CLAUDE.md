# Agents Context

This file provides guidance to AI agents when working with code in this repository.

## Project Overview

`musickit` is a single-binary CLI for managing Apple Music playlists: search the catalogue, list,
show, export, create, add, remove, rename and delete. Go 1.26, one external module (Kong, for the
command grammar).

## Layout

```
cmd/musickit/       main; SETUP doc comment and the version variable
internal/cli/       Kong grammar, Runtime, one file per command group
internal/applemusic/HTTP client: search, library playlists, pagination, retry
internal/musicapp/  the local Music app, driven by AppleScript
internal/auth/      ES256 developer token, browser handshake, token cache
internal/config/    XDG discovery, config.json, credential resolution
internal/tracklist/ the "Artist - Title | hint" format: parse, write
internal/match/     scoring a catalogue result against a wanted line
```

## Commands

```bash
make ci                     # codefix + format + lint + test (USE THIS)
make test                   # tests with race detection and coverage
make lint -j8               # parallelize linting
make build                  # → build/musickit
make run                    # dry-run import of summer-playlist-2026.txt
make vulncheck              # govulncheck (needs network; not in `make ci`)
FORCE_UPDATE=1 make lint    # reinstall linters
```

Always use Makefile targets. `make ci` is the single command to validate changes.

## Constraints That Look Like Bugs

- **The API cannot remove a track, rename a playlist or delete one.** Apple documents no such
  endpoint — the only `DELETE`s in the whole API are personal ratings. `remove`, `rename` and
  `delete` therefore drive the local Music app through `osascript`, which is why they are
  macOS-only. Do not go looking for the endpoint; it is not there.
- **The browser auth step cannot be removed.** A Music-User-Token is required for every library
  write and only MusicKit JS can mint one. Hence the one-page localhost server in
  `auth.Authorizer`. Catalogue _reads_ need only the developer token, which is why `search` runs
  before authorisation.
- **The ES256 signature must be raw `r||s`, never ASN.1.** `ecdsa.SignASN1` produces a token Apple
  rejects with an opaque 401. `TestDeveloperTokenIsValidES256` pins the 64-byte length and verifies
  against the public key; do not relax it.
- **AppleScript is passed on stdin with names in `argv`.** Never interpolate a playlist or track
  name into a script, and never write `--` inside a `(* *)` block comment: it opens a nested line
  comment that swallows the closing delimiter.
- **Removal re-checks before it deletes.** Go sends `index:persistentID` pairs highest-index-first;
  the script verifies the ID at that index and reports `SKIP` rather than deleting the wrong song.
  A skip is warned about even under `--quiet`.
- **Accent folding is hand-rolled** (`foldTable`) because `golang.org/x/text` would be a dependency
  this repo does not want. Add runes to the table rather than the dependency.
- **`make run` is a dry run on purpose** — the wet import writes to a real Apple Music library.
- **Matching reports a miss rather than adding the wrong track.** The `match.Score` penalties
  (karaoke −200, unrequested live −35, unknown artist −15) and the `MinScore` floor exist so a bad
  match never lands silently in someone's playlist. A change here needs a test row, and a re-run of
  `make run` against the 50-track list, which must still match 50.

## Unix Conventions

- stdout is data, stderr is narration. Records are tab-separated, or one JSON object per line
  under `--json`. `--quiet` silences stderr progress but never a warning and never stdout.
- Track lists come from arguments, `--from FILE`, `--from -`, or a piped stdin — in that order. A
  terminal on stdin is never read from; the command says how to give it a list instead.
- One track-list format everywhere, so `playlist export A | playlist add B` works.
- `--dry-run` writes nothing; `--yes` pre-answers prompts; with no terminal a destructive command
  refuses rather than guessing.
- Exit codes: 0 success, 1 error, 2 usage, 3 finished with unmatched tracks, 130 interrupted.

## Secrets

`*.p8` is gitignored and the key lives in `~/.config/musickit/`, never in the tree. Apple allows a
single download per key, so a lost `.p8` means a new key. Never print a token or key to any stream.

## Config

`--config-dir`, else `$MUSICKIT_CONFIG_DIR`, else `$XDG_CONFIG_HOME/musickit`, else
`~/.config/musickit` — holding `config.json`, `AuthKey.p8`, `user-token`. Files are unhidden and
short-named; a relative `privateKey` resolves against the config dir, not the cwd.

## Conventions

- Go 1.26+, `any` over `interface{}`, errors wrapped with `fmt.Errorf("context: %w", err)` where
  the wrap adds something the callee did not already say
- Error strings lowercase (ST1005) — reword rather than capitalise, even for "Apple"
- Never name a local after a builtin or an imported package (`real`, `min`, `path`) — `make lint`
  runs gocritic with `builtinShadow,importShadow` and will fail the build
- `#nosec Gxxx -- reason` inline, with the reason, for the operator-supplied paths gosec flags
- Playlist writes chunk at `applemusic.PageLimit` (100), Apple's per-request maximum
- Tests never touch the network and never open a browser: the API is an `httptest` server, the
  Music app a fake `Runner`, and `cli.Run` takes a `configure func(*Runtime)` seam for both.
  Anything needing Apple is exercised by hand with `make run`

## Workflow

1. Minimal, focused change
2. Add or update tests
3. `make ci` must pass
4. Draft and print the commit message; the user commits manually
