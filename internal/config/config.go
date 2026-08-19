// Package config locates and loads musickit's configuration.
//
// Everything musickit reads or writes outside the working directory lives in
// one directory, discovered per the XDG Base Directory spec:
//
//	$MUSICKIT_CONFIG_DIR, else $XDG_CONFIG_HOME/musickit, else ~/.config/musickit
//
//	config.json   teamId, keyId, optional privateKey and storefront
//	AuthKey.p8    the MusicKit private key — default location, no config entry needed
//	user-token    cached Music-User-Token, written after browser authorisation
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// File names inside the configuration directory. They are deliberately
// unhidden and short: the directory is already namespaced.
const (
	FileName  = "config.json"
	KeyName   = "AuthKey.p8"
	TokenName = "user-token"
)

// DirMode and FileMode keep the private key and the cached user token to the
// owner; both are credentials.
const (
	DirMode  os.FileMode = 0o700
	FileMode os.FileMode = 0o600
)

// Paths holds the configuration directory and the files inside it.
type Paths struct {
	Dir    string
	Config string
	Key    string
	Token  string
}

// Discover resolves the configuration directory. An explicit override (the
// --config-dir flag) wins, then $MUSICKIT_CONFIG_DIR, then $XDG_CONFIG_HOME,
// then the ~/.config default. Per the XDG spec a relative $XDG_CONFIG_HOME is
// invalid and ignored rather than resolved against the working directory.
func Discover(override string) Paths {
	dir := override
	if dir == "" {
		dir = os.Getenv("MUSICKIT_CONFIG_DIR")
	}
	if dir == "" {
		if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
			dir = filepath.Join(xdg, "musickit")
		} else {
			dir = Home(".config", "musickit")
		}
	}
	dir = ExpandHome(dir)
	return Paths{
		Dir:    dir,
		Config: filepath.Join(dir, FileName),
		Key:    filepath.Join(dir, KeyName),
		Token:  filepath.Join(dir, TokenName),
	}
}

// Home joins parts onto the user's home directory, falling back to the working
// directory when the home directory cannot be determined.
func Home(parts ...string) string {
	dir, err := os.UserHomeDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(append([]string{dir}, parts...)...)
}

// ExpandHome expands a leading ~/ and makes the result absolute. "-" is left
// alone so callers can keep using it to mean stdin.
func ExpandHome(p string) string {
	if p == "" || p == "-" {
		return p
	}
	if strings.HasPrefix(p, "~/") {
		return Home(p[2:])
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// Config is the contents of config.json, after environment overrides.
type Config struct {
	TeamID     string `json:"teamId"`
	KeyID      string `json:"keyId"`
	PrivateKey string `json:"privateKey,omitempty"` // optional; defaults to <dir>/AuthKey.p8
	Storefront string `json:"storefront,omitempty"`
}

// Load reads config.json, applies the MUSICKIT_* environment overrides and
// resolves the private key path. A missing config.json is not an error as long
// as the environment supplies the credentials.
func Load(p Paths) (Config, error) {
	var cfg Config
	// #nosec G304 -- the path is musickit's own config directory, chosen by the operator.
	if raw, err := os.ReadFile(p.Config); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("%s is not valid JSON: %w", p.Config, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return cfg, err
	}

	cfg.TeamID = firstNonEmpty(os.Getenv("MUSICKIT_TEAM_ID"), cfg.TeamID)
	cfg.KeyID = firstNonEmpty(os.Getenv("MUSICKIT_KEY_ID"), cfg.KeyID)
	cfg.PrivateKey = firstNonEmpty(os.Getenv("MUSICKIT_PRIVATE_KEY"), cfg.PrivateKey)

	var missing []string
	if cfg.TeamID == "" {
		missing = append(missing, "teamId")
	}
	if cfg.KeyID == "" {
		missing = append(missing, "keyId")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing config: %s\nwrite %s:\n  {\n    \"teamId\": \"ABCDE12345\",\n    \"keyId\": \"XYZ1234567\"\n  }",
			strings.Join(missing, ", "), p.Config)
	}

	// No privateKey entry means the conventional location; a relative one is
	// taken as relative to the config directory rather than the cwd.
	switch {
	case cfg.PrivateKey == "":
		cfg.PrivateKey = p.Key
	case strings.HasPrefix(cfg.PrivateKey, "~/"), filepath.IsAbs(cfg.PrivateKey):
		cfg.PrivateKey = ExpandHome(cfg.PrivateKey)
	default:
		cfg.PrivateKey = filepath.Join(p.Dir, cfg.PrivateKey)
	}

	// #nosec G703 -- the path is the operator's own config entry, not remote input;
	// pointing this tool at your own key file is the entire feature.
	if _, err := os.Stat(cfg.PrivateKey); err != nil {
		return cfg, fmt.Errorf("private key not readable at %s\nsave the .p8 Apple gave you as %s, or set \"privateKey\" in %s",
			cfg.PrivateKey, p.Key, p.Config)
	}
	return cfg, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
