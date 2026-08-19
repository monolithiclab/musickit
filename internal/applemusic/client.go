// Package applemusic is a small client for the parts of the Apple Music API
// musickit uses: catalogue search and library playlists.
//
// The public API is read-mostly. Apple documents no endpoint for deleting a
// playlist, renaming one, or removing a track from one — the only DELETE
// requests in the whole API are for personal ratings. Those operations live in
// the musicapp package instead, which drives the local Music app.
package applemusic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is Apple's API host.
const DefaultBaseURL = "https://api.music.apple.com"

// maxPages bounds pagination so a misbehaving "next" link cannot spin forever.
const maxPages = 200

// Client talks to the Apple Music API. The zero value is not usable; use New.
type Client struct {
	BaseURL    string
	HTTP       *http.Client
	DevToken   string
	UserToken  string
	Storefront string

	// Backoff is the pause before retrying a rate-limited request. Tests set
	// it to zero.
	Backoff time.Duration
}

// New returns a client with sensible defaults.
func New(devToken string, timeout time.Duration) *Client {
	return &Client{
		BaseURL:  DefaultBaseURL,
		HTTP:     &http.Client{Timeout: timeout},
		DevToken: devToken,
		Backoff:  2 * time.Second,
	}
}

// APIError is a non-2xx response from Apple.
type APIError struct {
	Status int
	Method string
	Path   string
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, http.StatusText(e.Status))
	}
	return fmt.Sprintf("%s %s: %d %s: %s", e.Method, e.Path, e.Status, http.StatusText(e.Status), e.Body)
}

// Unauthorized reports whether Apple rejected the credentials.
func (e *APIError) Unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// IsUnauthorized reports whether err is a rejected-credentials APIError.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Unauthorized()
}

// Do issues a request, retrying while Apple rate-limits it.
func (c *Client) Do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}

	const attempts = 4
	for attempt := range attempts {
		raw, retryAfter, err := c.attempt(ctx, method, path, payload)
		if err != nil {
			return nil, err
		}
		if retryAfter < 0 {
			return raw, nil
		}
		if attempt == attempts-1 {
			break
		}
		wait := retryAfter * time.Duration(attempt+1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, fmt.Errorf("rate limited repeatedly on %s", path)
}

// attempt performs one request. A non-negative retryAfter means Apple asked us
// to back off and try again.
func (c *Client) attempt(ctx context.Context, method, path string, payload []byte) (raw []byte, retryAfter time.Duration, err error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, reader)
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("Authorization", "Bearer "+c.DevToken)
	if c.UserToken != "" {
		req.Header.Set("Music-User-Token", c.UserToken)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.http().Do(req)
	if err != nil {
		return nil, -1, err
	}
	defer func() { _ = res.Body.Close() }()

	raw, err = io.ReadAll(res.Body)
	if err != nil {
		return nil, -1, err
	}

	if res.StatusCode == http.StatusTooManyRequests {
		wait := c.Backoff
		if v, convErr := strconv.Atoi(res.Header.Get("Retry-After")); convErr == nil && v > 0 {
			wait = time.Duration(v) * time.Second
		}
		return nil, wait, nil
	}
	if res.StatusCode >= 300 {
		return nil, -1, &APIError{
			Status: res.StatusCode,
			Method: method,
			Path:   path,
			Body:   strings.TrimSpace(string(raw)),
		}
	}
	return raw, -1, nil
}

// Get issues a GET request.
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	return c.Do(ctx, http.MethodGet, path, nil)
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// page is Apple's envelope for a collection response.
type page[T any] struct {
	Data []T    `json:"data"`
	Next string `json:"next"`
}

// fetchAll walks Apple's "next" links until the collection is exhausted or
// limit items have been collected. A limit of zero means everything.
func fetchAll[T any](ctx context.Context, c *Client, path string, limit int) ([]T, error) {
	var out []T
	next := path
	for pages := 0; next != "" && pages < maxPages; pages++ {
		raw, err := c.Get(ctx, next)
		if err != nil {
			return nil, err
		}
		var p page[T]
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, p.Data...)
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil
		}
		if p.Next == next || len(p.Data) == 0 {
			break // a "next" that does not advance would loop forever
		}
		next = p.Next
	}
	return out, nil
}
