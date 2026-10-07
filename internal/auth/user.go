package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/monolithiclab/musickit/internal/config"
)

// DefaultAuthPort is the loopback port the authorisation page is served on.
// It is fixed rather than random so it can be allow-listed if need be.
const DefaultAuthPort = 8888

// Authorizer runs the one-page browser handshake that yields a
// Music-User-Token. Every library write needs one and only MusicKit JS can
// mint one: Apple exposes no server-side grant, in any language.
type Authorizer struct {
	// Addr is the loopback address to listen on. Empty means 127.0.0.1:8888.
	Addr string
	// Timeout bounds the wait for the human. Zero means five minutes.
	Timeout time.Duration
	// Open launches the browser. Nil means the platform opener.
	Open func(url string) error
	// Notify reports the URL to visit. Nil means silence.
	Notify func(url string)
}

// Token serves the authorisation page and returns the token the browser posts
// back.
func (a *Authorizer) Token(ctx context.Context, devToken string) (string, error) {
	addr := a.Addr
	if addr == "" {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultAuthPort))
	}
	timeout := a.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("cannot serve the authorisation page on %s: %w", addr, err)
	}
	defer func() { _ = listener.Close() }()

	tokens := make(chan string, 1)
	failures := make(chan error, 1)
	srv := &http.Server{Handler: a.handler(devToken, tokens, failures), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failures <- err
		}
	}()
	defer func() { _ = srv.Close() }()

	link := "http://" + listener.Addr().String() + "/"
	if a.Notify != nil {
		a.Notify(link)
	}
	open := a.Open
	if open == nil {
		open = OpenBrowser
	}
	// A failure to launch the browser is not fatal: the URL has been printed
	// and the human can open it themselves.
	_ = open(link)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case tok := <-tokens:
		return tok, nil
	case err := <-failures:
		return "", err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("timed out waiting for browser authorisation")
		}
		return "", ctx.Err()
	}
}

func (a *Authorizer) handler(devToken string, tokens chan<- string, failures chan<- error) http.Handler {
	page := strings.ReplaceAll(authPageHTML, "__DEV_TOKEN__", strconv.Quote(devToken))

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)
			failures <- err
			return
		}
		tok := strings.TrimSpace(string(body))
		if tok == "" {
			http.Error(w, "empty token", http.StatusBadRequest)
			failures <- errors.New("empty user token returned by Apple")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		tokens <- tok
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Nothing to do if the browser hangs up mid-page; it will retry or the
		// user will see a blank tab.
		_, _ = io.WriteString(w, page)
	})
	return mux
}

// OpenBrowser asks the desktop to open a URL.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// #nosec G204 -- url is the loopback address this process just bound.
		cmd = exec.Command("open", url)
	case "windows":
		// #nosec G204 -- as above.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		// #nosec G204 -- as above.
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// CachedToken returns the stored Music-User-Token, if there is one.
func CachedToken(path string) (string, bool) {
	// #nosec G304 -- musickit's own cache file inside its config directory.
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	tok := strings.TrimSpace(string(raw))
	return tok, tok != ""
}

// SaveToken caches a Music-User-Token, owner-readable only.
func SaveToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), config.DirMode); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token+"\n"), config.FileMode)
}

// ForgetToken drops the cached token. A missing file is not an error.
func ForgetToken(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// No backticks inside — this lives in a Go raw string literal.
const authPageHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>Authorise Apple Music</title>
<style>
  body { font: 16px -apple-system, system-ui, sans-serif; display: grid; place-items: center;
         height: 100vh; margin: 0; background: #111; color: #eee; }
  .card { text-align: center; max-width: 32rem; padding: 2rem; }
  button { font: inherit; padding: .7rem 1.4rem; border-radius: 999px; border: 0;
           background: #fa2d48; color: #fff; cursor: pointer; }
  button[disabled] { opacity: .5; cursor: default; }
  .muted { color: #999; font-size: .875rem; margin-top: 1.5rem; }
</style></head>
<body><div class="card">
  <h1>Authorise Apple Music</h1>
  <p id="status">Loading MusicKit...</p>
  <button id="go" disabled>Authorise</button>
  <p class="muted">Grants musickit permission to read and change playlists in your library.
     The token stays on this machine.</p>
</div>
<script src="https://js-cdn.music.apple.com/musickit/v3/musickit.js" data-web-components async></script>
<script>
  var DEV_TOKEN = __DEV_TOKEN__;
  document.addEventListener('musickitloaded', async function () {
    var status = document.getElementById('status');
    var go = document.getElementById('go');
    try {
      var music = await MusicKit.configure({
        developerToken: DEV_TOKEN,
        app: { name: 'musickit', build: '1.0.0' }
      });
      status.textContent = 'Ready.';
      go.disabled = false;
      go.onclick = async function () {
        go.disabled = true;
        status.textContent = 'Waiting for Apple...';
        try {
          var userToken = await music.authorize();
          await fetch('/token', { method: 'POST', body: userToken });
          status.textContent = 'Authorised - back to the terminal.';
          go.remove();
        } catch (e) {
          status.textContent = 'Authorisation failed: ' + e.message;
          go.disabled = false;
        }
      };
    } catch (e) {
      status.textContent = 'MusicKit.configure failed: ' + e.message +
        ' (check the Team ID, Key ID and .p8)';
    }
  });
</script></body></html>`
