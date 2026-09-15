# Track lists and matching

## The format

One track per line:

```
Artist - Title
Artist - Title | version hint
# a comment
```

- The separator is `" - "` — spaces included, so hyphenated names survive: `Jean-Michel Jarre -
  Oxygène IV` splits correctly.
- Everything after the first `|` is a **hint**: album, remixer, year, anything that discriminates
  between pressings. It joins the search term and is scored separately.
- Blank lines and lines beginning with `#` are skipped.
- A line with no separator is not dropped. It comes back as an `Entry` with `Unparsed` set, is
  reported as a miss with the reason `expected "Artist - Title"`, and costs no search. One bad line
  does not sink a batch of fifty.
- Lines are capped at 1 MB.

The same parser handles files, stdin and command-line arguments, and the same renderer produces
`playlist export` output. That is what makes this work:

```sh
musickit playlist export "Summer 2026" | musickit playlist add "Summer 2027"
musickit playlist export "Summer 2026" | diff - summer-playlist-2026.txt
```

## Why matching is needed at all

Apple's search returns ranked results for a free-text query, not a lookup by artist and title. For
"Danger - 4:30" it will happily return a karaoke version, a live take, a sped-up edit, and the
studio recording, in an order of its own choosing. Something has to choose.

**The governing principle: report a miss rather than add the wrong track.** A miss is printed, has a
non-zero exit code, and can be fixed by hand in ten seconds. A wrong track lands silently in
someone's playlist and is found months later.

## The algorithm

For each line: search `"artist title hint"`, take the top 15 results, score each, keep the best if
it clears `MinScore` (45). Lines are searched by four concurrent workers; results keep input order.

Everything is compared after `Normalise`: lowercased, `&` → `and`, diacritics folded, punctuation
collapsed to single spaces. So `L'Impératrice` and `L Imperatrice` compare equal. The fold table is
hand-rolled to keep `golang.org/x/text` out of the module — add runes to the table, not the
dependency.

### Weights

| Signal                                        | Score |
| --------------------------------------------- | ----- |
| Title exact / contains / contained-in          | +50 / +30 / +15 |
| Artist exact                                   | +40 |
| Artist substring either way                    | +22 |
| All the wanted artist's words present          | +34 |
| Some of them present                           |  0  |
| **None of them present**                       | **−15** |
| Hint found in title+album / not found          | +45 / −5 |
| Live take when none was asked for              | −35 |
| Remix/edit/version/mix/rework/dub, unasked     | −10 |
| Instrumental, acoustic, demo, commentary, …    | −25 |
| **Karaoke / tribute / "in the style of"**      | **−200** |

Three of these carry the design:

**Karaoke, −200.** Not a penalty, a veto: nothing recovers from it. This is the check that keeps
"If I Ever Feel Better" by The Karaoke Channel out of a Phoenix playlist. It is also the only check
that searches the *artist* field, because that is where knockoffs give themselves away. The live and
variant checks deliberately do not, or the band Live would be unmatchable.

**Wrong artist, −15.** Without it, an exact title alone scores 50 and clears the floor, so every
"Song 2" in the catalogue matches. With it, 50 − 15 = 35, below the floor: a miss.

**Word-set overlap, +34.** Artist credits are not stable strings. Apple files "Lindstrøm &
Christabelle" under "Christabelle & Lindstrøm", and "Daft Punk" under "Daft Punk, Pharrell Williams
& Nile Rodgers". Neither equality nor substring catches those, and the −15 penalty would fire on
both. So the credits are compared as sets of words, with joining words (`the`, `and`, `feat`, `ft`,
`with`, `vs`, …) removed first — otherwise "The Beatles" and "The Rolling Stones" would look
related. Full overlap scores just under an exact match; partial overlap is neutral, neither rewarded
nor punished.

The live penalty is harsh (−35) and the variant penalty is mild (−10) because sometimes the only
pressing on the service *is* an "edit", while a live take is almost never what a bare title means.

### Tuning

These numbers are calibrated against a real 50-track list, `summer-playlist-2026.txt`, which
`make run` matches as a dry run. Changing a weight requires:

1. a row or case in `TestScoreRanking` (or a sibling test) covering what changed, and
2. `make run` still matching 50 of 50.

Both. A weight change that passes the unit tests and drops the real list to 49 is a regression — it
happened once already, which is why `TestScoreAcceptsReorderedCredits` exists.

## Removal matching

Removal does not search the catalogue; it matches the requested lines against the tracks actually in
the playlist, using the same `Normalise`. Two rules:

- Each requested line consumes **at most one** track. Asking for the same song twice removes two
  copies and no more — playlists legitimately contain duplicates.
- A line with no artist matches on title alone, which is convenient and slightly risky, so it is
  opt-in by omission rather than the default.

Lines matching nothing are emitted as `not-in-playlist` records and counted toward exit code 3.
