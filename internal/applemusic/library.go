package applemusic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// PageLimit is Apple's maximum number of items per request, and so the chunk
// size for playlist writes.
const PageLimit = 100

// Playlists lists the playlists in the user's library. A limit of zero means
// all of them.
func (c *Client) Playlists(ctx context.Context, limit int) ([]Playlist, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(pageSize(limit)))
	return fetchAll[Playlist](ctx, c, "/v1/me/library/playlists?"+q.Encode(), limit)
}

// Playlist fetches a single library playlist by identifier.
func (c *Client) Playlist(ctx context.Context, id string) (Playlist, error) {
	raw, err := c.Get(ctx, "/v1/me/library/playlists/"+url.PathEscape(id))
	if err != nil {
		return Playlist{}, err
	}
	var out struct {
		Data []Playlist `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Playlist{}, err
	}
	if len(out.Data) == 0 {
		return Playlist{}, fmt.Errorf("no playlist with id %s", id)
	}
	return out.Data[0], nil
}

// PlaylistTracks lists the tracks of a library playlist, in playlist order.
func (c *Client) PlaylistTracks(ctx context.Context, id string, limit int) ([]Track, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(pageSize(limit)))
	path := "/v1/me/library/playlists/" + url.PathEscape(id) + "/tracks?" + q.Encode()
	tracks, err := fetchAll[Track](ctx, c, path, limit)
	if err != nil {
		// An empty playlist answers 404 rather than an empty collection.
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return tracks, nil
}

// CreatePlaylist creates a library playlist holding the first PageLimit
// tracks; use AddTracks for the rest.
func (c *Client) CreatePlaylist(ctx context.Context, name, description string, tracks []TrackRef) (Playlist, error) {
	type trackData struct {
		Data []TrackRef `json:"data"`
	}
	type attributes struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}
	body := struct {
		Attributes    attributes `json:"attributes"`
		Relationships *struct {
			Tracks trackData `json:"tracks"`
		} `json:"relationships,omitempty"`
	}{Attributes: attributes{Name: name, Description: description}}

	if len(tracks) > 0 {
		body.Relationships = &struct {
			Tracks trackData `json:"tracks"`
		}{Tracks: trackData{Data: tracks}}
	}

	raw, err := c.Do(ctx, http.MethodPost, "/v1/me/library/playlists", body)
	if err != nil {
		return Playlist{}, err
	}
	var out struct {
		Data []Playlist `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Playlist{}, err
	}
	if len(out.Data) == 0 {
		return Playlist{}, errors.New("playlist accepted but Apple returned no id")
	}
	return out.Data[0], nil
}

// AddTracks appends tracks to a playlist, in chunks Apple will accept.
func (c *Client) AddTracks(ctx context.Context, playlistID string, tracks []TrackRef) error {
	path := "/v1/me/library/playlists/" + url.PathEscape(playlistID) + "/tracks"
	for start := 0; start < len(tracks); start += PageLimit {
		end := min(start+PageLimit, len(tracks))
		body := struct {
			Data []TrackRef `json:"data"`
		}{Data: tracks[start:end]}
		if _, err := c.Do(ctx, http.MethodPost, path, body); err != nil {
			return fmt.Errorf("adding tracks %d-%d: %w", start+1, end, err)
		}
	}
	return nil
}

func pageSize(limit int) int {
	if limit > 0 && limit < PageLimit {
		return limit
	}
	return PageLimit
}
