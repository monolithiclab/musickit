# Authentication

Apple Music needs two different tokens, obtained two different ways.

## The developer token

A JWT that says "this program is registered with Apple". It is enough on its own for catalogue
reads, which is why `search` works before you have authorised anything.

Minting one needs a Team ID, a Key ID, and a MusicKit private key (`.p8`, ECDSA P-256, PKCS#8).
`auth.DeveloperToken` signs `ES256` over `iss`/`iat`/`exp` with a 180-day lifetime — Apple's maximum
is six months. The token is signed fresh on every run and never cached: signing costs microseconds
and a cached JWT is one more credential on disk.

**The signature must be raw `r||s`, 64 bytes, big-endian, zero-padded.** Go's `ecdsa.SignASN1`
produces a DER signature, which is what most JWT code reaches for and which Apple rejects with a
bare 401 and no explanation. `auth.DeveloperToken` calls `ecdsa.Sign` and lays out `r` and `s` with
`FillBytes`. `TestDeveloperTokenIsValidES256` pins the 64-byte length; do not relax it.

### Getting the key

developer.apple.com → Certificates, Identifiers & Profiles → Keys → new key with **MusicKit**
enabled → download `AuthKey_XXXXXXXXXX.p8`. The ten characters in the filename are the Key ID.

**Apple lets you download a key exactly once.** Lose the file and the only remedy is revoking the
key and issuing a new one. It belongs in the config directory, mode 0600, not in a repository —
`*.p8` is gitignored here for that reason.

## The Music-User-Token

A token that says "this user granted this program access to their library". Every library read and
every library write needs one.

**Only MusicKit JS can mint one.** Apple exposes no server-side grant, no device flow, and no
refresh endpoint — in any language. So `auth.Authorizer` binds `127.0.0.1:8888`, serves a single
HTML page that loads MusicKit JS from Apple's CDN, configures it with the developer token, and waits
for the page to `POST` the resulting token back to `/token`. Then the server shuts down.

Details that matter:

- The port is **fixed**, not random, so it can be allow-listed in a restrictive network.
- Before opening a browser, the developer token is checked with a cheap catalogue request. A bad
  `.p8` produces a clear error here instead of a confusing MusicKit failure three clicks later.
- Failing to *launch* the browser is not fatal. The URL is printed to stderr first; opening it by
  hand works just as well.
- The wait is bounded (five minutes) and cancellable.
- The token is written to `user-token` at mode 0600 and reused until it stops working. Expiry shows
  up as a 401/403, which the CLI answers with a hint to run `auth --reauth`.

`auth --logout` deletes the cached token and nothing else; the `.p8` stays.

## The config directory

Everything musickit persists lives in one directory, resolved in this order:

1. `--config-dir`
2. `$MUSICKIT_CONFIG_DIR`
3. `$XDG_CONFIG_HOME/musickit` — **only if `$XDG_CONFIG_HOME` is absolute**; the XDG spec says a
   relative value is invalid, so it is ignored rather than resolved against the cwd
4. `~/.config/musickit`

It holds three files:

| File          | What                                                      | Mode |
| ------------- | --------------------------------------------------------- | ---- |
| `config.json` | `teamId`, `keyId`, optional `privateKey` and `storefront`   | —    |
| `AuthKey.p8`  | the MusicKit private key, at its conventional name          | 0600 |
| `user-token`  | the cached Music-User-Token                                 | 0600 |

Names are unhidden and short: the directory is already namespaced, so a leading dot buys nothing.

A minimal `config.json`:

```json
{ "teamId": "ABCDE12345", "keyId": "XYZ1234567" }
```

`privateKey` is optional and only needed if the key is not at `AuthKey.p8`. A **relative**
`privateKey` resolves against the config directory, not the working directory — the config file
should mean the same thing regardless of where you run from. `~/` is expanded.

`MUSICKIT_TEAM_ID`, `MUSICKIT_KEY_ID` and `MUSICKIT_PRIVATE_KEY` override the file, so CI can supply
credentials without writing one. With all three set, `config.json` need not exist at all.

## Storefront

The storefront decides which catalogue you search — the same song has different identifiers in
different regions. Resolution order: `--storefront`, `$MUSICKIT_STOREFRONT`, `config.json`, then the
account's own storefront (one request to `/v1/me/storefront`). Without a user token there is no
account to ask, so catalogue-only commands fall back to `us`.

## Handling

No token or key is ever printed. The developer token is passed to the auth page as a JSON-quoted
JavaScript literal, and that page is only ever served on loopback. `Warnf` mentions paths and never
contents. When adding output, keep it that way.
