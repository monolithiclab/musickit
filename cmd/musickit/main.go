// Command musickit manages Apple Music playlists from the command line.
//
// SETUP (once)
//
// At developer.apple.com > Certificates, Identifiers & Profiles > Keys, hit
// "+", tick MusicKit, register, and download AuthKey_XXXXXXXXXX.p8 — Apple
// allows a single download, so keep it safe. Note the Key ID shown on that
// page and the Team ID from the top right of the portal. Save the key as
// ~/.config/musickit/AuthKey.p8, then write ~/.config/musickit/config.json:
//
//	{
//	  "teamId": "ABCDE12345",
//	  "keyId":  "XYZ1234567"
//	}
//
// Run `musickit --help` for the commands.
package main

import (
	"os"

	"musickit/internal/cli"
)

// version is stamped by the build: -ldflags "-X main.version=$(VERSION)".
var version = "dev"

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, version))
}
