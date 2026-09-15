# Architecture

## Layout

```
cmd/musickit/          main: setup instructions, the version variable, three lines of code
internal/cli/          Kong grammar, Runtime, one file per command group
internal/applemusic/   HTTP client: search, library playlists, pagination, retry
internal/musicapp/     the local Music app, driven by AppleScript
internal/auth/         ES256 developer token, browser handshake, token cache
internal/config/       XDG discovery, config.json, credential resolution
internal/tracklist/    the "Artist - Title | hint" format: parse, write
internal/match/        scoring a catalogue result against a wanted line
```

## Dependencies

```
                    cli
     ┌───────┬───────┼───────┬────────┐
     ▼       ▼       ▼       ▼        ▼
 applemusic musicapp auth  match  tracklist
                      │      │        ▲
                      ▼      └────────┘
                    config
```

Two rules:

- **Nothing below `cli` imports `cli`.** The command layer knows about every backend; no backend
  knows about the command layer.
- **`applemusic`, `musicapp`, `config` and `tracklist` are leaves.** They import nothing else in the
  module. `match` bridges `applemusic` and `tracklist` because scoring is precisely the act of
  comparing one against the other; `auth` needs `config` only for the two file modes.

`musicapp` and `applemusic` never reference each other. They model the same domain twice — a local
`musicapp.Track` carries an AppleScript persistent ID and a playlist position; an
`applemusic.Track` carries a catalogue identifier. They are not interchangeable and merging them
would hide that.

## The path of a command

`main` → `cli.Main` → `cli.Run` → `dispatch` → `kctx.Run()` → a command's `Run` method.

1. **`cli.Main`** installs a `signal.NotifyContext` for SIGINT/SIGTERM and hands off to `Run`.
2. **`cli.Run`** builds the Kong parser and maps whatever comes back to a process exit code. Kong is
   told to `panic` instead of calling `os.Exit`, so `--help` and parse errors are survivable in a
   test; `dispatch` recovers that panic.
3. **`dispatch`** parses argv, constructs a `Runtime`, applies the test `configure` hook if there is
   one, binds the `context.Context` and the `Runtime` into Kong's injector, and runs the command.
4. **The command's `Run(ctx, rt)`** does the work, writing through `rt`.

### Runtime

`Runtime` is what a command needs that is not one of its own flags: the global flags, the three
standard streams, the resolved config paths, and lazy accessors for the two backends.

```go
rt.API(ctx, needUser bool) (*applemusic.Client, error)   // memoised
rt.Music() (MusicApp, error)                             // memoised
```

`needUser` is the important parameter. Catalogue reads need only a developer token; anything
touching the library needs a Music-User-Token, which costs a browser round trip the first time. A
client dialled without a user token does **not** satisfy a later call that wants one — `API`
re-dials. This is why `search` works before you have ever authorised.

`MusicApp` is an interface declared in `cli`, not in `musicapp`: the consumer defines the six
methods it uses, so tests can substitute a fake without `musicapp` knowing tests exist.

## Command files

| File                      | Commands                                        |
| ------------------------- | ----------------------------------------------- |
| `cli/auth.go`             | `auth`, plus `dialAPI` — the real client builder |
| `cli/search.go`           | `search`                                        |
| `cli/playlist.go`         | `list`, `show`, `export` (read paths)            |
| `cli/playlist_edit.go`    | `create`, `add`, `remove`, `rename`, `delete`    |
| `cli/import.go`           | `import` — a thin alias for `playlist create`    |
| `cli/input.go`            | where a track list comes from; playlist resolution |
| `cli/output.go`           | the stdout/stderr split, `Confirm`               |

`import` exists because it reads better for the thing musickit was originally written for. It
constructs a `PlaylistCreateCmd` and calls its `Run`; there is no second implementation.

## Playlist resolution

A playlist reference is a name or an identifier, and the two backends identify playlists
differently, so there are two resolvers.

- **`resolvePlaylist`** (`cli/input.go`) resolves against the API. Exact identifier match, then
  exact name, then normalised name (case- and accent-folded). Ambiguity is an error that lists the
  candidates with their identifiers — never a guess.
- **`localPlaylist`** (`cli/playlist_edit.go`) resolves to a name Music.app knows. A `p.`-prefixed
  API identifier means nothing to AppleScript, so it is translated through the API first; the result
  is then checked against the local library, which means a typo fails *before* anything is deleted.

## The client

`applemusic.Client` is a thin wrapper over `net/http`:

- `Do` retries up to four attempts while Apple answers 429, honouring `Retry-After` with a linear
  backoff, and aborts the wait if the context is cancelled.
- `APIError` carries the status, method, path and body. `IsUnauthorized` covers 401 and 403 and is
  what the CLI keys its "run `auth --reauth`" hint off.
- `fetchAll[T]` walks Apple's `next` links. It stops on an empty page or a `next` that does not
  advance — either would otherwise loop forever — and at a hard `maxPages` of 200.
- Writes chunk at `PageLimit` (100), Apple's per-request maximum. `CreatePlaylist` takes the first
  chunk and `AddTracks` follows with the rest; `AddTracks` names the failed range in its error
  ("adding tracks 101-150") so a partial write is diagnosable.

An empty library playlist answers 404 rather than an empty collection. `PlaylistTracks` translates
that one status into an empty result and lets every other error through.
