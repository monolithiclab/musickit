package applemusic

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

// DefaultStorefront is used when the account's own storefront cannot be read.
const DefaultStorefront = "us"

// FetchStorefront returns the storefront of the authenticated account.
func (c *Client) FetchStorefront(ctx context.Context) (string, error) {
	raw, err := c.Get(ctx, "/v1/me/storefront")
	if err != nil {
		return "", err
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Data) == 0 {
		return DefaultStorefront, nil
	}
	return out.Data[0].ID, nil
}

// SearchSongs runs a catalogue search. It needs only the developer token.
func (c *Client) SearchSongs(ctx context.Context, term string, limit int) ([]Track, error) {
	if limit <= 0 {
		limit = 10
	}
	q := url.Values{}
	q.Set("term", term)
	q.Set("types", "songs")
	q.Set("limit", strconv.Itoa(limit))

	storefront := c.Storefront
	if storefront == "" {
		storefront = DefaultStorefront
	}
	raw, err := c.Get(ctx, "/v1/catalog/"+url.PathEscape(storefront)+"/search?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var out struct {
		Results struct {
			Songs struct {
				Data []Track `json:"data"`
			} `json:"songs"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Results.Songs.Data, nil
}

// Ping makes the cheapest possible authenticated call, to check the developer
// token before anything expensive — or a browser — gets involved.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Get(ctx, "/v1/catalog/us/search?term=daft+punk&types=songs&limit=1")
	return err
}
