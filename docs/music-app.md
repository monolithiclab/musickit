# The Music app backend

## Why it exists

The Apple Music API can create a playlist and append tracks to it. It cannot remove a track, rename
a playlist, or delete one — Apple documents no such endpoint. The only `DELETE` requests in the
entire API are nine "Delete a Personal … Rating" variants.

The Music app on macOS can do all three, and has an AppleScript dictionary. A playlist created over
the API shows up there once iCloud Music Library has synced, so the two backends address the same
playlists. `remove`, `rename` and `delete` therefore shell out to `osascript`.

Do not go looking for the endpoint. It is not there.

## The cost

Those three verbs need macOS, the Music app, and a synced library. Off macOS `musicapp.New` returns
`ErrUnsupported` before anything runs, and the CLI turns that into a plain error plus the hint that
the command drives the Music app. Nothing hangs and nothing half-succeeds.

`playlist list --local` exists to make the boundary visible: it lists exactly the playlists the
three destructive verbs can reach, with no network involved. Smart playlists and the built-in ones
are filtered out — they cannot be edited by hand, so offering them would be a lie.

## How scripts are run

```
osascript -   <script on stdin>   name  arg  arg …
```

`-` makes `osascript` read the script from stdin; the remaining argv is handed to the script's
`on run argv` handler.

**Names are never interpolated into script text.** Playlist and track names contain quotes,
backslashes, curly apostrophes and `¬`; every escaping scheme for AppleScript string literals is a
bug waiting to be reported. Passing them as run-handler arguments sidesteps the question entirely.
`TestRenameDeleteCreatePassNamesAsArguments` asserts that a deliberately nasty name never appears in
the script source.

**No block comments, anywhere.** In AppleScript, `--` inside a `(* … *)` block opens a nested line
comment that eats the closing `*)`, and the script fails to compile with a message that points
nowhere near the problem. `TestScriptsHaveNoBlockComments` enforces this. Use `--` line comments in
scripts, or explain in the surrounding Go.

Output is tab-separated, one record per line, joined by a `joinLines` handler appended to every
script that returns a table. A script that cannot find its playlist raises the sentinel string
`musickit:no-playlist`, which `Osascript` recognises in stderr and converts to `ErrNoPlaylist` —
distinguishable from a genuine AppleScript failure.

## The removal protocol

Removal is the one operation that can destroy something by accident, so it is the one with a
protocol.

AppleScript deletes by position, and a position is only meaningful for as long as nobody else
touches the playlist. Two safeguards:

1. **Highest index first.** Deleting position 5 does not disturb positions 1–4, so a batch of
   deletions stays valid as it proceeds. Go sorts before sending;
   `TestRemoveDeletesHighestIndexFirst` pins the order.
2. **Verify before deleting.** Each argument is `index:persistentID`. The script fetches the track
   at that index, compares its persistent ID, and only deletes on a match. A mismatch — the library
   changed under us, Music.app resorted, a sync landed mid-run — records `SKIP` and moves on.

The script returns one `OK`/`SKIP` record per requested track, and Go reports as removed only the
tracks it was told were removed. Any shortfall is surfaced with `Warnf`, which **is not silenced by
`--quiet`**: "N track(s) were skipped: the playlist changed while we worked. Re-run to finish."
A quiet run that silently removed fewer tracks than asked would be the worst possible outcome.

## Resolution before destruction

`localPlaylist` (in `cli/playlist_edit.go`) maps the reference you typed to a name the Music app
knows, before the verb runs:

- A `p.`-prefixed API identifier means nothing to AppleScript, so it is translated to a name through
  the API first.
- The name is then looked up in the local library, exactly and then case/accent-folded. Unknown
  fails with `ErrNoPlaylist` and a pointer to `playlist list --local`; ambiguous fails listing the
  candidates.

So a typo fails *before* the script that deletes runs, not inside it.

`delete` and non-`--dry-run` `remove` also go through `Confirm`, which respects `--yes` and refuses
outright when there is no terminal to prompt on.

## `Create` is here but unused by the CLI

`App.Create` makes an empty local playlist. The CLI's `playlist create` goes over the API instead,
because the API can add anything in the catalogue while the local app can only add what is already
in the library. `App.Create` stays for symmetry and for offline use.
