// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shamil-tp/rewind/internal/library"
)

func mustStatus(t *testing.T, rec interface {
	Result() *http.Response
}, want int) {
	t.Helper()
	if got := rec.Result().StatusCode; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

func TestListeningEndpointsRequireAuth(t *testing.T) {
	e := newTestEnv(t)
	e.doSetup(t)
	for _, rt := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/albums", nil},
		{http.MethodGet, "/api/albums/eA", nil},
		{http.MethodPatch, "/api/albums/eA/metadata", map[string]any{"version": "x", "changes": map[string]any{"genre": "x"}}},
		{http.MethodGet, "/api/artists", nil},
		{http.MethodGet, "/api/artists/eA", nil},
		{http.MethodPatch, "/api/artists/eA/name", map[string]any{"version": "x", "name": "y"}},
		{http.MethodGet, "/api/tracks/1/artwork", nil},
		{http.MethodGet, "/api/tracks/1/metadata", nil},
		{http.MethodPatch, "/api/tracks/1/metadata", map[string]any{"revision": 1, "changes": map[string]any{"title": "x"}}},
		{http.MethodPost, "/api/tracks/1/metadata/revert", map[string]any{"revision": 1}},
		{http.MethodGet, "/api/favorites/tracks", nil},
		{http.MethodPut, "/api/favorites/tracks/1", nil},
		{http.MethodDelete, "/api/favorites/tracks/1", nil},
		{http.MethodGet, "/api/history", nil},
		{http.MethodPost, "/api/history", map[string]any{"track_id": 1, "play_key": "abcdefgh"}},
		{http.MethodDelete, "/api/history", map[string]any{"all": true}},
		{http.MethodGet, "/api/history/recent", nil},
		{http.MethodGet, "/api/history/top", nil},
		{http.MethodGet, "/api/playback", nil},
		{http.MethodPut, "/api/playback", map[string]any{"revision": 1, "state": map[string]any{"queue": []int{}}}},
		{http.MethodPost, "/api/playlists/1/duplicate", nil},
		{http.MethodPut, "/api/playlists/1/artwork", map[string]any{"track_id": nil}},
	} {
		expectError(t, e.send(t, rt.method, rt.path, rt.body), http.StatusUnauthorized, "unauthenticated")
	}
	var n int
	e.db.QueryRow(`SELECT (SELECT count(*) FROM favorites) + (SELECT count(*) FROM play_history) +
		(SELECT count(*) FROM playback_state) + (SELECT count(*) FROM track_edits)`).Scan(&n)
	if n != 0 {
		t.Errorf("unauthenticated requests created %d rows", n)
	}
}

func TestListeningCSRF(t *testing.T) {
	e := newTestEnv(t)
	c, _, ids := libraryWithSession(t, e)
	attack := []reqOpt{withCookie(c), withHeader("Sec-Fetch-Site", "cross-site"), withHeader("Origin", "https://evil.example")}
	for _, rt := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/api/favorites/tracks/" + itoa(ids["Tune"]), nil},
		{http.MethodPost, "/api/history", map[string]any{"track_id": ids["Tune"], "play_key": "abcdefgh", "listened_ms": 1}},
		{http.MethodDelete, "/api/history", map[string]any{"all": true}},
		{http.MethodPut, "/api/playback", map[string]any{"revision": 1, "state": map[string]any{"queue": []int64{ids["Tune"]}, "index": 0}}},
		{http.MethodPatch, "/api/tracks/" + itoa(ids["Tune"]) + "/metadata", map[string]any{"revision": 0, "changes": map[string]any{"title": "x"}}},
	} {
		expectError(t, e.send(t, rt.method, rt.path, rt.body, attack...), http.StatusForbidden, "cross_origin")
	}
}

func TestAlbumArtistAndMetadataAPI(t *testing.T) {
	e := newTestEnv(t)
	c, _, ids := libraryWithSession(t, e)

	var albums struct {
		Items      []library.Album `json:"items"`
		Pagination pagination      `json:"pagination"`
	}
	decodeBody(t, e.send(t, http.MethodGet, "/api/albums?sort=year&order=desc", nil, withCookie(c)), &albums)
	if albums.Pagination.Total != 2 || len(albums.Items) != 2 {
		t.Fatalf("albums = %+v", albums)
	}
	var record library.Album
	for _, a := range albums.Items {
		if a.Title == "Record" {
			record = a
		}
	}
	var ad struct {
		Album library.AlbumDetail `json:"album"`
	}
	decodeBody(t, e.send(t, http.MethodGet, "/api/albums/"+record.ID, nil, withCookie(c)), &ad)
	if len(ad.Album.Tracks) != 2 || ad.Album.Tracks[0].Title != "Opening" || ad.Album.Version == "" {
		t.Fatalf("album = %+v", ad.Album)
	}
	if strings.Contains(e.send(t, http.MethodGet, "/api/albums/"+record.ID, nil, withCookie(c)).Body.String(), e.dataDir) {
		t.Error("album detail exposes a path")
	}
	expectError(t, e.send(t, http.MethodGet, "/api/albums/%21%21", nil, withCookie(c)), http.StatusNotFound, "not_found")
	expectError(t, e.send(t, http.MethodGet, "/api/albums?sort=bogus", nil, withCookie(c)), http.StatusBadRequest, "invalid_parameter")

	// Album edit with a stale version is refused; with the right one it works.
	expectError(t, e.send(t, http.MethodPatch, "/api/albums/"+record.ID+"/metadata",
		map[string]any{"version": "old", "changes": map[string]any{"genre": "Rock"}}, withCookie(c)), http.StatusConflict, "edit_conflict")
	expectError(t, e.send(t, http.MethodPatch, "/api/albums/"+record.ID+"/metadata",
		map[string]any{"version": ad.Album.Version, "changes": map[string]any{"title": "x"}}, withCookie(c)), http.StatusUnprocessableEntity, "validation_failed")
	rec := e.send(t, http.MethodPatch, "/api/albums/"+record.ID+"/metadata",
		map[string]any{"version": ad.Album.Version, "changes": map[string]any{"genre": "Rock", "year": 1999}}, withCookie(c))
	mustStatus(t, rec, http.StatusOK)

	// Track metadata: read, edit, conflict, revert.
	path := "/api/tracks/" + itoa(ids["Tune"]) + "/metadata"
	var md metadataResponse
	decodeBody(t, e.send(t, http.MethodGet, path, nil, withCookie(c)), &md)
	if md.Metadata.Values["title"] != "Tune" || len(md.Fields) != len(library.EditFields) {
		t.Fatalf("metadata = %+v", md.Metadata)
	}
	rev := md.Metadata.Revision
	decodeBody(t, e.send(t, http.MethodPatch, path, map[string]any{"revision": rev, "changes": map[string]any{"title": "Mélodie", "composer": "Ö"}}, withCookie(c)), &md)
	if md.Track.Title != "Mélodie" || md.Metadata.Revision == rev {
		t.Fatalf("after edit = %+v", md.Track)
	}
	expectError(t, e.send(t, http.MethodPatch, path, map[string]any{"revision": rev, "changes": map[string]any{"title": "x"}}, withCookie(c)),
		http.StatusConflict, "edit_conflict")
	expectError(t, e.send(t, http.MethodPatch, path, map[string]any{"revision": md.Metadata.Revision, "changes": map[string]any{"rel_path": "../x"}}, withCookie(c)),
		http.StatusUnprocessableEntity, "validation_failed")
	decodeBody(t, e.send(t, http.MethodPost, path+"/revert", map[string]any{"revision": md.Metadata.Revision}, withCookie(c)), &md)
	if md.Track.Title != "Tune" || len(md.Metadata.Edited) != 0 {
		t.Errorf("after revert = %+v", md.Metadata)
	}

	// Artists, with personal data.
	var artists struct {
		Items []library.Artist `json:"items"`
	}
	decodeBody(t, e.send(t, http.MethodGet, "/api/artists?q=band", nil, withCookie(c)), &artists)
	if len(artists.Items) != 1 || artists.Items[0].TrackCount != 2 {
		t.Fatalf("artists = %+v", artists.Items)
	}
	var detail struct {
		Artist      library.ArtistDetail `json:"artist"`
		TopTracks   []library.Track      `json:"top_tracks"`
		ListeningMS int64                `json:"listening_ms"`
	}
	decodeBody(t, e.send(t, http.MethodGet, "/api/artists/"+artists.Items[0].ID, nil, withCookie(c)), &detail)
	if detail.Artist.Name != "Band" || len(detail.Artist.Albums) != 1 || detail.TopTracks == nil {
		t.Errorf("artist = %+v", detail)
	}
	rec = e.send(t, http.MethodPatch, "/api/artists/"+artists.Items[0].ID+"/name", map[string]any{"version": detail.Artist.Version, "name": "The Band"}, withCookie(c))
	mustStatus(t, rec, http.StatusOK)
	tracks := getTracks(t, e, c, "?q=Opening")
	if len(tracks.Items) != 1 || *tracks.Items[0].Artist != "The Band" {
		t.Errorf("rename not visible in the library: %+v", tracks.Items)
	}
}

func TestFavoritesHistoryAndPlaybackAPI(t *testing.T) {
	e := newTestEnv(t)
	c, _, ids := libraryWithSession(t, e)
	tune := ids["Tune"]

	mustStatus(t, e.send(t, http.MethodPut, "/api/favorites/tracks/"+itoa(tune), nil, withCookie(c)), http.StatusOK)
	expectError(t, e.send(t, http.MethodPut, "/api/favorites/tracks/99999", nil, withCookie(c)), http.StatusNotFound, "track_not_found")
	if tr := getTracks(t, e, c, "?q=Tune"); !tr.Items[0].Favorite {
		t.Error("track list does not show the favorite")
	}

	play := map[string]any{"track_id": tune, "play_key": "play-key-01", "listened_ms": 60000, "source_kind": "album", "source_id": "abc"}
	mustStatus(t, e.send(t, http.MethodPost, "/api/history", play, withCookie(c)), http.StatusCreated)
	var res struct {
		Recorded bool `json:"recorded"`
	}
	decodeBody(t, e.send(t, http.MethodPost, "/api/history", play, withCookie(c)), &res)
	if res.Recorded {
		t.Error("retry recorded twice")
	}
	expectError(t, e.send(t, http.MethodPost, "/api/history", map[string]any{"track_id": tune, "play_key": "x"}, withCookie(c)),
		http.StatusUnprocessableEntity, "validation_failed")
	if tr := getTracks(t, e, c, "?q=Tune"); tr.Items[0].PlayCount != 1 || tr.Items[0].LastPlayedAt == nil {
		t.Errorf("play count = %+v", tr.Items[0])
	}

	// Privacy mode: nothing is recorded.
	mustStatus(t, e.send(t, http.MethodPatch, "/api/preferences", map[string]any{"record_history": false}, withCookie(c)), http.StatusOK)
	play["play_key"] = "play-key-02"
	decodeBody(t, e.send(t, http.MethodPost, "/api/history", play, withCookie(c)), &res)
	if res.Recorded {
		t.Error("recorded while history is off")
	}
	e.send(t, http.MethodPatch, "/api/preferences", map[string]any{"record_history": true}, withCookie(c))

	var hist struct {
		Items []library.HistoryEntry `json:"items"`
	}
	decodeBody(t, e.send(t, http.MethodGet, "/api/history?from=2000-01-01T00:00:00Z", nil, withCookie(c)), &hist)
	if len(hist.Items) != 1 || hist.Items[0].Track.ID != tune {
		t.Fatalf("history = %+v", hist.Items)
	}
	expectError(t, e.send(t, http.MethodGet, "/api/history?from=yesterday", nil, withCookie(c)), http.StatusBadRequest, "invalid_parameter")
	var recent library.Recent
	decodeBody(t, e.send(t, http.MethodGet, "/api/history/recent", nil, withCookie(c)), &recent)
	if len(recent.Tracks) != 1 || len(recent.Albums) != 1 || len(recent.Artists) != 1 {
		t.Errorf("recent = %+v", recent)
	}
	expectError(t, e.send(t, http.MethodDelete, "/api/history", map[string]any{"ids": []int64{1}, "all": true}, withCookie(c)),
		http.StatusUnprocessableEntity, "validation_failed")
	var del struct {
		Deleted int64 `json:"deleted"`
	}
	decodeBody(t, e.send(t, http.MethodDelete, "/api/history", map[string]any{"all": true}, withCookie(c)), &del)
	if del.Deleted != 1 {
		t.Errorf("deleted = %d", del.Deleted)
	}

	// Playback state: saved, restored with tracks, stale writes refused.
	state := map[string]any{"queue": []int64{tune, ids["Opening"]}, "index": 1, "position_ms": 1500, "repeat": "one", "speed": 1.5,
		"context": map[string]any{"kind": "album", "id": "x", "name": "Record"}}
	mustStatus(t, e.send(t, http.MethodPut, "/api/playback", map[string]any{"revision": 5, "state": state}, withCookie(c)), http.StatusOK)
	expectError(t, e.send(t, http.MethodPut, "/api/playback", map[string]any{"revision": 4, "state": state}, withCookie(c)),
		http.StatusConflict, "stale_state")
	expectError(t, e.send(t, http.MethodPut, "/api/playback", map[string]any{"revision": 6, "state": map[string]any{"queue": []int64{tune}, "speed": 9}}, withCookie(c)),
		http.StatusUnprocessableEntity, "validation_failed")
	var saved library.SavedPlayback
	decodeBody(t, e.send(t, http.MethodGet, "/api/playback", nil, withCookie(c)), &saved)
	if saved.Revision != 5 || saved.State.Index != 1 || saved.State.Repeat != "one" || len(saved.Tracks) != 2 {
		t.Errorf("saved = %+v", saved)
	}

	// Playlists: bulk add, duplicate, artwork.
	pl := createPlaylist(t, e, c, "Mine")
	var added struct {
		Added, Skipped int
		Playlist       library.PlaylistDetail `json:"playlist"`
	}
	decodeBody(t, e.send(t, http.MethodPost, "/api/playlists/"+itoa(pl.ID)+"/tracks",
		map[string]any{"track_ids": []int64{tune, ids["Opening"], tune}}, withCookie(c)), &added)
	if added.Added != 2 || added.Skipped != 1 || len(added.Playlist.Entries) != 2 {
		t.Fatalf("bulk add = %+v", added)
	}
	var dup struct {
		Playlist library.Playlist `json:"playlist"`
	}
	rec := e.send(t, http.MethodPost, "/api/playlists/"+itoa(pl.ID)+"/duplicate", nil, withCookie(c))
	mustStatus(t, rec, http.StatusCreated)
	json.Unmarshal(rec.Body.Bytes(), &dup)
	if dup.Playlist.Name != "Mine (copy)" || dup.Playlist.TrackCount != 2 {
		t.Errorf("duplicate = %+v", dup.Playlist)
	}
	var art struct {
		Playlist library.Playlist `json:"playlist"`
	}
	decodeBody(t, e.send(t, http.MethodPut, "/api/playlists/"+itoa(pl.ID)+"/artwork", map[string]any{"track_id": ids["Opening"]}, withCookie(c)), &art)
	if !art.Playlist.ArtworkChosen || *art.Playlist.ArtworkTrackID != ids["Opening"] {
		t.Errorf("artwork = %+v", art.Playlist)
	}

	// Test files have no artwork: a clean 404, never a path or a crash.
	expectError(t, e.send(t, http.MethodGet, "/api/tracks/"+itoa(tune)+"/artwork?size=large", nil, withCookie(c)), http.StatusNotFound, "no_artwork")
	expectError(t, e.send(t, http.MethodGet, "/api/tracks/"+itoa(tune)+"/artwork?size=huge", nil, withCookie(c)), http.StatusBadRequest, "invalid_parameter")
}
