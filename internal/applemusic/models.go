package applemusic

// Track models both a catalogue song ("songs") and a library song
// ("library-songs"); the fields musickit cares about are the same for each.
type Track struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Attributes TrackAttributes `json:"attributes"`
}

// TrackAttributes is the subset of Apple's song attributes musickit uses.
type TrackAttributes struct {
	Name             string     `json:"name"`
	ArtistName       string     `json:"artistName"`
	AlbumName        string     `json:"albumName"`
	ReleaseDate      string     `json:"releaseDate"`
	URL              string     `json:"url"`
	DurationInMillis int        `json:"durationInMillis"`
	PlayParams       PlayParams `json:"playParams"`
}

// PlayParams carries the cross-reference between a library song and the
// catalogue song it was added from.
type PlayParams struct {
	ID        string `json:"id"`
	CatalogID string `json:"catalogId"`
}

// CatalogID returns the catalogue identifier for a track, which is the one
// accepted when adding to a playlist. Library songs carry it under playParams;
// catalogue songs are their own.
func (t Track) CatalogID() string {
	if t.Attributes.PlayParams.CatalogID != "" {
		return t.Attributes.PlayParams.CatalogID
	}
	if t.Type == "" || t.Type == "songs" {
		return t.ID
	}
	return ""
}

// Playlist is a playlist in the user's library.
type Playlist struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Attributes PlaylistAttributes `json:"attributes"`
}

// PlaylistAttributes is the subset of Apple's playlist attributes musickit uses.
type PlaylistAttributes struct {
	Name        string      `json:"name"`
	Description Description `json:"description"`
	CanEdit     bool        `json:"canEdit"`
	DateAdded   string      `json:"dateAdded"`
}

// Description is Apple's wrapper around playlist description text.
type Description struct {
	Standard string `json:"standard"`
}

// Name is the playlist's display name.
func (p Playlist) Name() string { return p.Attributes.Name }

// TrackRef identifies a track in a playlist write.
type TrackRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// SongRef is a TrackRef for a catalogue song.
func SongRef(id string) TrackRef { return TrackRef{ID: id, Type: "songs"} }
