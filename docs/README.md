# musickit

A single-binary CLI for managing Apple Music playlists. Search the catalogue, list, show, export,
create, add, remove, rename, delete.

Run `musickit --help` for commands and flags. These documents cover everything that is *not* in the
inline help: why the tool is shaped the way it is, and what you need to know before changing it.

## The one constraint that explains everything

**The Apple Music API is read-mostly.** Apple documents no endpoint for removing a track from a
playlist, renaming a playlist, or deleting one. The only `DELETE` requests in the entire API are
nine "Delete a Personal … Rating" variants.

So musickit has two backends:

| Capability                        | Backend        | Needs                          |
| --------------------------------- | -------------- | ------------------------------ |
| search                            | HTTP API       | developer token                |
| list, show, export                | HTTP API       | developer + user token         |
| create, add                       | HTTP API       | developer + user token         |
| **remove, rename, delete**        | **Music.app**  | **macOS, synced library**      |
| list `--local`                    | Music.app      | macOS                          |

A playlist created over the API appears in Music.app once iCloud Music Library has synced, so both
halves operate on the same playlists. The cost is that three verbs are macOS-only, and they say so
in their help text and in their errors.

## Contents

| Document                                 | Covers                                                       |
| ---------------------------------------- | ------------------------------------------------------------ |
| [architecture.md](architecture.md)       | Package layout, dependency rules, the path of a command       |
| [authentication.md](authentication.md)   | The two tokens, config discovery, credential handling         |
| [music-app.md](music-app.md)             | The AppleScript layer and its safety protocol                 |
| [matching.md](matching.md)               | The track-list format and the scoring algorithm               |
| [interface.md](interface.md)             | Stream discipline, record formats, exit codes, input sources  |
| [development.md](development.md)         | Testing strategy, seams, adding a command, known traps        |

## Status

Go 1.26. One external module: [Kong](https://github.com/alecthomas/kong), for the command grammar —
it has no runtime dependencies of its own. Everything else is the standard library.
