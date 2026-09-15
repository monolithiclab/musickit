# Command-line interface

`--help` lists the commands and flags. This describes the conventions behind them.

## Streams

**stdout is data. stderr is narration.** Every record you might want to keep goes to stdout; counts,
progress, dry-run summaries and prompts go to stderr. So `musickit playlist show X > tracks.txt`
gives you tracks, and the "50 track(s)." line still reaches the terminal.

`--quiet` silences narration (`Logf`) and nothing else. It never touches stdout, and it never
silences a warning (`Warnf`). A warning means something needs attention — tracks that could not be
matched, deletions that were skipped — and a quiet run that hid those would be worse than useless.
`TestQuietSilencesNarrationOnly` and `TestRemoveWarnsAboutSkipsEvenWhenQuiet` pin this.

## Record formats

Default output is tab-separated, one record per line, no header — built for `cut`, `awk` and `grep`.
`--json` switches to **NDJSON**: one JSON object per line, not a wrapped array, so it streams and
`jq` can filter it line by line.

The commands that act on tracks — `create`, `add`, `remove` — all emit the same record shape, so
their output can be diffed against each other:

```
status  source-line  id  artist  title  album
```

with these statuses:

| Status            | Meaning                                        |
| ----------------- | ---------------------------------------------- |
| `added`           | matched and added                              |
| `would-add`       | as above, under `--dry-run`                    |
| `missing`         | no candidate cleared the score floor           |
| `removed`         | removed from the playlist                      |
| `would-remove`    | as above, under `--dry-run`                    |
| `not-in-playlist` | asked for, but not there to remove             |
| `renamed`, `deleted` | one record, for symmetry with the above     |

The read commands emit their own shapes: `search` adds release date and `m:ss` duration, `playlist
list` is `id`/`name` (plus a track count with `--local`), `playlist show` is numbered
`n`/`id`/`artist`/`title`/`album`, and `playlist export` emits the track-list format so it pipes
straight back into `add`.

## Where a track list comes from

Checked in this order:

1. command-line arguments
2. `--from FILE`
3. `--from -` (explicit stdin)
4. stdin, **if it is not a terminal**

Giving both arguments and `--from` is an error, not a merge. If nothing is available, the command
says how to supply a list rather than waiting.

**A terminal on stdin is never read from.** Reading it would look exactly like a hang, and the
convention that "no input" means "explain yourself" is older than any of this. `playlist create` is
the exception in one direction: with a terminal on stdin and no arguments it creates an *empty*
playlist, because that is a real thing to want.

`~/` is expanded in `--from`; `-` is left alone so it keeps meaning stdin.

## Destructive operations

- `--dry-run` (`-n`) writes nothing, anywhere. It still does all the reading and matching, so its
  output tells you exactly what a real run would do. It is available globally, including on `remove`
  and `delete`, and `make run` uses it against a real library.
- `--yes` (`-y`) pre-answers confirmation prompts.
- With no terminal and no `--yes`, a command that would prompt **refuses** rather than assuming.
  Assuming yes destroys data in a cron job; assuming no silently does nothing. Refusing is the only
  answer that tells you something. `TestDestructiveCommandsRefuseToGuess` covers it.

## Exit codes

| Code | Meaning                                   |
| ---- | ----------------------------------------- |
| 0    | success                                   |
| 1    | error                                     |
| 2    | usage error                               |
| 3    | **finished, but some tracks were unmatched** |
| 130  | interrupted (SIGINT/SIGTERM)              |

Code 3 is the interesting one. Importing a fifty-track list and matching forty-eight is neither a
success nor a failure: the playlist exists and is useful, and two lines need attention. Zero would
hide that from a script; one would suggest nothing happened. `incompleteError` carries the count and
maps to 3.

SIGINT is handled through `signal.NotifyContext`, so in-flight requests are cancelled rather than
abandoned, and the process exits 130 as a shell expects.

## Errors

Errors print as `musickit: <message>`, lowercase, no stack. Where the cause is guessable, a second
indented line says what to try — an unauthorised response suggests checking the credentials and
running `auth --reauth`; an unsupported operation explains that it drives the Music app. Apple's
opaque failures are the reason this exists; a bare `401 Unauthorized` helps nobody.

## Composition

Everything above exists to make these work:

```sh
musickit playlist export "Summer 2026" | musickit playlist add "Party"
musickit playlist show "Party" --json | jq -r 'select(.attributes.artistName == "Air")'
musickit playlist add "Party" --from list.txt || [ $? -eq 3 ]   # tolerate misses
comm -13 <(musickit playlist export A | sort) <(sort list.txt)   # in the file, not the playlist
```
