package cli

import (
	"context"
	"strconv"
	"strings"
)

// SearchCmd probes the catalogue. It needs only the developer token, so it
// works before any browser authorisation.
type SearchCmd struct {
	Limit int      `short:"l" default:"10" help:"Maximum number of results."`
	Query []string `arg:"" help:"Search terms."`
}

// Run performs the search.
func (c *SearchCmd) Run(ctx context.Context, rt *Runtime) error {
	client, err := rt.API(ctx, false)
	if err != nil {
		return err
	}
	tracks, err := client.SearchSongs(ctx, strings.Join(c.Query, " "), c.Limit)
	if err != nil {
		return err
	}
	for _, t := range tracks {
		if rt.G.JSON {
			if err := rt.JSON(t); err != nil {
				return err
			}
			continue
		}
		rt.Row(t.ID, t.Attributes.ArtistName, t.Attributes.Name, t.Attributes.AlbumName,
			firstNonEmpty(t.Attributes.ReleaseDate, "-"), duration(t.Attributes.DurationInMillis))
	}
	rt.Logf("%d result(s) from the %s storefront.", len(tracks), client.Storefront)
	return nil
}

// duration renders milliseconds as m:ss.
func duration(ms int) string {
	if ms <= 0 {
		return "-"
	}
	seconds := ms / 1000
	return strconv.Itoa(seconds/60) + ":" + leftPad(strconv.Itoa(seconds%60))
}

func leftPad(s string) string {
	if len(s) < 2 {
		return "0" + s
	}
	return s
}
