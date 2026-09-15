# musickit

A single-binary CLI for managing Apple Music playlists from the terminal: search the catalogue,
list, show, export, create, add, remove, rename and delete — all scriptable, all pipeable.

```sh
$ musickit playlist export "Summer 2026" | musickit playlist add "Summer 2027"
$ musickit search "Todd Terje Inspector Norse" --json | jq -r '.[0].attributes.name'
Inspector Norse
```

## Why it's a little unusual

Apple's Music API can create playlists and add tracks to them, but it has no endpoint to remove a
track, rename a playlist, or delete one — the only `DELETE` in the whole API is for personal
ratings. So musickit quietly drives two different backends depending on what you ask for: the HTTP
API for anything additive, and the local Music app (over AppleScript) for anything that Apple
decided you should do by hand. `playlist list --local` shows you exactly which playlists the
second half can reach.

The other unusual part is the matching engine behind `import`/`add`. Apple's search is a ranked
free-text query, not a lookup — asking for a song by artist and title can just as easily return a
karaoke cover, a live take, or a remix as the actual studio recording. musickit scores every
candidate and would rather report a miss than add the wrong track: karaoke and tribute versions are
vetoed outright, live takes are penalized hard, and artist credits are compared as word sets so
"Lindstrøm & Christabelle" still matches when Apple files it as "Christabelle & Lindstrøm". See
[docs/matching.md](docs/matching.md) for the full scoring table and the reasoning behind it.

## Installing

```sh
go install ./cmd/musickit   # or: make build → build/musickit
```

Requires Go 1.26. The only third-party dependency is [Kong](https://github.com/alecthomas/kong)
for the command grammar — everything else is the standard library.

## Setting up Apple Music access

You'll need a MusicKit key, which is free with any Apple Developer account:

1. At [developer.apple.com](https://developer.apple.com) → Certificates, Identifiers & Profiles →
   Keys, create a new key with **MusicKit** enabled and download the `.p8` file. **Apple lets you
   download it exactly once** — keep it somewhere safe.
2. Save it as `~/.config/musickit/AuthKey.p8`.
3. Write `~/.config/musickit/config.json`:

   ```json
   { "teamId": "ABCDE12345", "keyId": "XYZ1234567" }
   ```

That's enough for `search`, which needs no user login. The first command that touches your library
(`playlist create`, `add`, ...) opens a browser tab for a one-time Apple Music sign-in and caches
the result; you won't see it again until the token expires.

Full details, including environment-variable overrides for CI, are in
[docs/authentication.md](docs/authentication.md).

## A quick tour

The repo ships a real 50-track list, `summer-playlist-2026.txt` (Balearic/French-touch, if you're
curious) as both a demo and the project's own regression fixture:

```sh
$ musickit import --dry-run --file summer-playlist-2026.txt --playlist "Summer 2026"
would-add  Todd Terje - Inspector Norse            123456789  Todd Terje  Inspector Norse  It's Album Time
would-add  Lindstrøm & Christabelle - Baby Can't Stop  ...
...
50 track(s), 50 matched.
```

`--dry-run` never writes anything, so it's a safe way to see what a real import would do — `make
run` runs exactly this command. Drop `--dry-run` and it creates the playlist for real; you'll
authorise in the browser the first time.

Output is tab-separated by default, so it composes with the rest of your shell:

```sh
musickit playlist show "Party" --json | jq -r 'select(.attributes.artistName == "Air")'
musickit playlist add "Party" --from list.txt || [ $? -eq 3 ]   # tolerate a few misses
comm -13 <(musickit playlist export "Summer 2026" | sort) <(sort wanted.txt)
```

`--json` switches any command to newline-delimited JSON instead. More on the stream conventions,
record shapes and exit codes in [docs/interface.md](docs/interface.md).

## Commands

| Command                                 | What                                         |
| --------------------------------------- | -------------------------------------------- |
| `search`                                | search the catalogue                         |
| `playlist list` / `show` / `export`     | read a playlist                              |
| `playlist create` / `add`               | write to a playlist (API)                    |
| `playlist remove` / `rename` / `delete` | edit a playlist (Music app, **macOS only**)  |
| `import`                                | shorthand for `playlist create` from a file  |
| `auth`                                  | authorise, or re-authorise, with Apple Music |

Run `musickit --help` or `musickit <command> --help` for the full flag reference.

## Digging deeper

`docs/` holds the design reasoning that doesn't belong in `--help` — the package layout, the
two-token auth flow, the AppleScript safety protocol, the matching weights, and the testing
strategy. Start at [docs/README.md](docs/README.md).
