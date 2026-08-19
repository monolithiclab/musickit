package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearEnv removes every variable that can steer discovery, so a developer's
// own shell cannot change the result.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"MUSICKIT_CONFIG_DIR", "XDG_CONFIG_HOME",
		"MUSICKIT_TEAM_ID", "MUSICKIT_KEY_ID", "MUSICKIT_PRIVATE_KEY",
	} {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
}

func TestDiscover(t *testing.T) {
	t.Run("explicit override wins", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("MUSICKIT_CONFIG_DIR", "/from/env")
		if got := Discover("/from/flag").Dir; got != "/from/flag" {
			t.Errorf("Dir = %q, want /from/flag", got)
		}
	})

	t.Run("environment beats XDG", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		t.Setenv("MUSICKIT_CONFIG_DIR", "/from/env")
		if got := Discover("").Dir; got != "/from/env" {
			t.Errorf("Dir = %q, want /from/env", got)
		}
	})

	t.Run("XDG_CONFIG_HOME is namespaced", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		if got := Discover("").Dir; got != "/xdg/musickit" {
			t.Errorf("Dir = %q, want /xdg/musickit", got)
		}
	})

	t.Run("a relative XDG_CONFIG_HOME is ignored", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("XDG_CONFIG_HOME", "relative/path")
		got := Discover("").Dir
		if want := Home(".config", "musickit"); got != want {
			t.Errorf("Dir = %q, want %q — the spec says a relative value is invalid", got, want)
		}
	})

	t.Run("default is ~/.config/musickit", func(t *testing.T) {
		clearEnv(t)
		if got, want := Discover("").Dir, Home(".config", "musickit"); got != want {
			t.Errorf("Dir = %q, want %q", got, want)
		}
	})

	t.Run("no file is hidden", func(t *testing.T) {
		clearEnv(t)
		p := Discover("/tmp/cfg")
		for _, path := range []string{p.Config, p.Key, p.Token} {
			if base := filepath.Base(path); strings.HasPrefix(base, ".") {
				t.Errorf("%s starts with a dot; the directory is already namespaced", base)
			}
		}
	})

	t.Run("file names sit inside the directory", func(t *testing.T) {
		clearEnv(t)
		p := Discover("/tmp/cfg")
		if p.Config != "/tmp/cfg/config.json" || p.Key != "/tmp/cfg/AuthKey.p8" || p.Token != "/tmp/cfg/user-token" {
			t.Errorf("unexpected paths: %+v", p)
		}
	})
}

// writeKey drops a placeholder key file so Load's readability check passes.
func writeKey(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), DirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a real key"), FileMode); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	t.Run("reads config.json", func(t *testing.T) {
		clearEnv(t)
		dir := t.TempDir()
		p := Discover(dir)
		writeKey(t, p.Key)
		if err := os.WriteFile(p.Config, []byte(`{"teamId":"TEAM123456","keyId":"KEY1234567"}`), FileMode); err != nil {
			t.Fatal(err)
		}

		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.TeamID != "TEAM123456" || cfg.KeyID != "KEY1234567" {
			t.Errorf("got %+v", cfg)
		}
		if cfg.PrivateKey != p.Key {
			t.Errorf("PrivateKey = %q, want the conventional %q", cfg.PrivateKey, p.Key)
		}
	})

	t.Run("environment overrides the file", func(t *testing.T) {
		clearEnv(t)
		dir := t.TempDir()
		p := Discover(dir)
		writeKey(t, p.Key)
		if err := os.WriteFile(p.Config, []byte(`{"teamId":"FROMFILE12","keyId":"FROMFILE34"}`), FileMode); err != nil {
			t.Fatal(err)
		}
		t.Setenv("MUSICKIT_TEAM_ID", "FROMENV123")

		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.TeamID != "FROMENV123" {
			t.Errorf("TeamID = %q, want the environment value", cfg.TeamID)
		}
		if cfg.KeyID != "FROMFILE34" {
			t.Errorf("KeyID = %q; an unset variable must not blank the file value", cfg.KeyID)
		}
	})

	t.Run("works with no config file at all", func(t *testing.T) {
		clearEnv(t)
		dir := t.TempDir()
		p := Discover(dir)
		writeKey(t, p.Key)
		t.Setenv("MUSICKIT_TEAM_ID", "TEAM123456")
		t.Setenv("MUSICKIT_KEY_ID", "KEY1234567")

		if _, err := Load(p); err != nil {
			t.Fatalf("Load: %v", err)
		}
	})

	t.Run("a relative privateKey resolves against the config directory", func(t *testing.T) {
		clearEnv(t)
		dir := t.TempDir()
		p := Discover(dir)
		writeKey(t, filepath.Join(dir, "other.p8"))
		if err := os.WriteFile(p.Config, []byte(`{"teamId":"T","keyId":"K","privateKey":"other.p8"}`), FileMode); err != nil {
			t.Fatal(err)
		}

		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if want := filepath.Join(dir, "other.p8"); cfg.PrivateKey != want {
			t.Errorf("PrivateKey = %q, want %q — not the working directory", cfg.PrivateKey, want)
		}
	})

	t.Run("an absolute privateKey is left alone", func(t *testing.T) {
		clearEnv(t)
		dir := t.TempDir()
		elsewhere := filepath.Join(t.TempDir(), "key.p8")
		writeKey(t, elsewhere)
		p := Discover(dir)
		if err := os.WriteFile(p.Config, []byte(`{"teamId":"T","keyId":"K","privateKey":"`+elsewhere+`"}`), FileMode); err != nil {
			t.Fatal(err)
		}

		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.PrivateKey != elsewhere {
			t.Errorf("PrivateKey = %q, want %q", cfg.PrivateKey, elsewhere)
		}
	})

	t.Run("missing credentials name themselves", func(t *testing.T) {
		clearEnv(t)
		p := Discover(t.TempDir())
		_, err := Load(p)
		if err == nil {
			t.Fatal("expected an error")
		}
		for _, want := range []string{"teamId", "keyId"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})

	t.Run("an unreadable key points at the fix", func(t *testing.T) {
		clearEnv(t)
		p := Discover(t.TempDir())
		t.Setenv("MUSICKIT_TEAM_ID", "T")
		t.Setenv("MUSICKIT_KEY_ID", "K")
		_, err := Load(p)
		if err == nil {
			t.Fatal("expected an error about the missing key")
		}
		if !strings.Contains(err.Error(), KeyName) {
			t.Errorf("error %q does not name %s", err, KeyName)
		}
	})

	t.Run("invalid JSON says so", func(t *testing.T) {
		clearEnv(t)
		p := Discover(t.TempDir())
		if err := os.MkdirAll(p.Dir, DirMode); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.Config, []byte("{nope}"), FileMode); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
			t.Errorf("error = %v, want a JSON complaint", err)
		}
	})
}

func TestExpandHome(t *testing.T) {
	if got := ExpandHome("-"); got != "-" {
		t.Errorf("ExpandHome(-) = %q; the stdin marker must survive", got)
	}
	if got := ExpandHome(""); got != "" {
		t.Errorf("ExpandHome(\"\") = %q", got)
	}
	if got := ExpandHome("~/x"); got != Home("x") {
		t.Errorf("ExpandHome(~/x) = %q, want %q", got, Home("x"))
	}
	if got := ExpandHome("/already/absolute"); got != "/already/absolute" {
		t.Errorf("ExpandHome = %q", got)
	}
	if got := ExpandHome("relative"); !filepath.IsAbs(got) {
		t.Errorf("ExpandHome(relative) = %q, want an absolute path", got)
	}
}
