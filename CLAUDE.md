# CLAUDE.md

musickit is a single-binary Go CLI that manages Apple Music playlists, driving the HTTP API for additive
work and the local Music app over AppleScript for the rest. See [README.md](README.md) for what it does and
how to run it, `docs/` (table below) for the reasoning, and `make help` for every command.

## Hard rules

- **Never interpolate a playlist or track name into an AppleScript**, and never write `--` inside a
  `(* *)` block comment: it opens a nested line comment that swallows the closing delimiter.
- **Never print a token or key to any stream.** `*.p8` is gitignored and lives in `~/.config/musickit/`,
  never in the tree; Apple allows a single download per key.
- **Tests never touch the network or a browser.** Use the `cli.Run` `configure func(*Runtime)` hook and the
  fakes in [docs/development.md](docs/development.md).

## Constraints that look like bugs

Facts about Apple's platform, not artifacts of this code — do not go looking for a way around them.

- **The API cannot remove a track, rename a playlist or delete one.** No such endpoint exists;
  `remove`, `rename` and `delete` drive the local Music app over AppleScript instead, which is why
  they are macOS-only.
- **The browser auth step cannot be removed.** Only MusicKit JS can mint a Music-User-Token; hence
  the one-page localhost server in `auth.Authorizer`.
- **The ES256 signature must be raw `r||s`, never ASN.1.** `ecdsa.SignASN1` produces a token Apple
  rejects with an opaque 401. `TestDeveloperTokenIsValidES256` pins this; do not relax it.
- **`make run` is a dry run on purpose** — the wet import writes to a real Apple Music library.
- **Matching reports a miss rather than adding the wrong track.** A `match.Score` weight change
  needs a test row and a `make run` that still matches 50 of 50 against the checked-in sample list.

Full reasoning: [docs/music-app.md](docs/music-app.md), [docs/authentication.md](docs/authentication.md),
[docs/matching.md](docs/matching.md).

## Traps `make ci` won't explain

- gocritic runs with `builtinShadow,importShadow` — never name a local after a builtin or an
  imported package (`real`, `min`, `path`). This bites more often than expected.
- `#nosec Gxxx -- reason` inline, with the reason, for operator-supplied paths gosec flags.

## Where things go

- A new command: a file in `internal/cli/`, wired into the Kong grammar in `cli.go`, with a `cli_test.go`
  row driven through `Run`; the Apple endpoint goes in `internal/applemusic/`, an AppleScript in
  `internal/musicapp/scripts.go`.
- Scoring weights live in `internal/match/match.go`; the track-list parser in `internal/tracklist/`.

## Documentation

`docs/` is part of the code. A change that alters behaviour, structure or reasoning updates the
matching document **in the same change** — not later, not in a follow-up.

| Document                 | Owns                                                                      |
| ------------------------ | ------------------------------------------------------------------------- |
| `docs/README.md`         | what musickit is; which capability uses which backend                     |
| `docs/architecture.md`   | packages, dependency rules, `Runtime`, command dispatch, client behaviour |
| `docs/authentication.md` | the two tokens, config discovery, credential handling                     |
| `docs/music-app.md`      | the AppleScript layer and the removal protocol                            |
| `docs/matching.md`       | the track-list format and the scoring weights                             |
| `docs/interface.md`      | streams, record shapes, input sources, exit codes                         |
| `docs/development.md`    | testing seams, adding a command, conventions, traps                       |

Two rules: never restate `--help` — document the _why_, the binary documents the _what_ — and prune
as well as add, since a document describing behaviour that no longer exists is worse than none.
Verify a claim against the code before writing it.

## Workflow

1. Minimal change, with tests, plus the owning `docs/` page in the same change
2. `make ci` green
3. Commit (`type(scope): summary`) with the session's trailers; push only when asked
