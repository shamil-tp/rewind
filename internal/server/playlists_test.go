// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shamil-tp/rewind/internal/library"
	"github.com/shamil-tp/rewind/internal/prefs"
)

type playlistBody struct {
	Playlist library.PlaylistDetail `json:"playlist"`
}

// libraryWithSession signs in and indexes musicDir. It returns the session
// cookie, the music directory and the track IDs by title.
func libraryWithSession(t *testing.T, e *testEnv) (*http.Cookie, string, map[string]int64) {
	t.Helper()
	c := e.doSetup(t)
	dir := musicDir(t)
	e.scanNow(t, e.addLibrary(t, dir)[dir])
	ids := map[string]int64{}
	for _, tr := range getTracks(t, e, c, "").Items {
		ids[tr.Title] = tr.ID
	}
	return c, dir, ids
}

func createPlaylist(t *testing.T, e *testEnv, c *http.Cookie, name string) library.Playlist {
	t.Helper()
	rec := e.send(t, http.MethodPost, "/api/playlists", map[string]string{"name": name}, withCookie(c))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create playlist = %d %s", rec.Code, rec.Body)
	}
	var b struct {
		Playlist library.Playlist `json:"playlist"`
	}
	decodeBody(t, rec, &b)
	return b.Playlist
}

func getPlaylist(t *testing.T, e *testEnv, c *http.Cookie, id int64) library.PlaylistDetail {
	t.Helper()
	rec := e.send(t, http.MethodGet, "/api/playlists/"+itoa(id), nil, withCookie(c))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET playlist = %d %s", rec.Code, rec.Body)
	}
	var b playlistBody
	decodeBody(t, rec, &b)
	return b.Playlist
}

func entryTitles(d library.PlaylistDetail) string {
	var out []string
	for _, en := range d.Entries {
		out = append(out, en.Track.Title)
	}
	return strings.Join(out, "|")
}

func TestPlaylistEndpointsRequireAuth(t *testing.T) {
	e := newTestEnv(t)
	e.doSetup(t)
	bogus := &http.Cookie{Name: "rewind_session", Value: strings.Repeat("B", 43)}
	for _, rt := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/playlists", nil},
		{http.MethodPost, "/api/playlists", map[string]string{"name": "x"}},
		{http.MethodGet, "/api/playlists/1", nil},
		{http.MethodPatch, "/api/playlists/1", map[string]string{"name": "x"}},
		{http.MethodDelete, "/api/playlists/1", nil},
		{http.MethodPost, "/api/playlists/1/tracks", map[string]int64{"track_id": 1}},
		{http.MethodPut, "/api/playlists/1/tracks", map[string][]int64{"track_ids": {1}}},
		{http.MethodDelete, "/api/playlists/1/tracks/1", nil},
		{http.MethodGet, "/api/preferences", nil},
		{http.MethodPatch, "/api/preferences", map[string]bool{"autoplay_next": false}},
	} {
		for _, c := range []*http.Cookie{nil, bogus} {
			rec := e.send(t, rt.method, rt.path, rt.body, withCookie(c))
			expectError(t, rec, http.StatusUnauthorized, "unauthenticated")
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("%s %s: Cache-Control = %q", rt.method, rt.path, rec.Header().Get("Cache-Control"))
			}
		}
	}
	var n int
	e.db.QueryRow("SELECT (SELECT count(*) FROM playlists) + (SELECT count(*) FROM user_preferences)").Scan(&n)
	if n != 0 {
		t.Errorf("unauthenticated requests created %d rows", n)
	}
}

func TestPlaylistAPIFlow(t *testing.T) {
	e := newTestEnv(t)
	c, dir, ids := libraryWithSession(t, e)

	// Empty list.
	var list struct {
		Items []library.Playlist `json:"items"`
	}
	decodeBody(t, e.send(t, http.MethodGet, "/api/playlists", nil, withCookie(c)), &list)
	if list.Items == nil || len(list.Items) != 0 {
		t.Fatalf("initial list = %#v", list.Items)
	}

	pl := createPlaylist(t, e, c, "  Mix ")
	if pl.Name != "Mix" || pl.TrackCount != 0 || pl.ID < 1 {
		t.Fatalf("created = %+v", pl)
	}
	base := "/api/playlists/" + itoa(pl.ID)
	if d := getPlaylist(t, e, c, pl.ID); d.Entries == nil || len(d.Entries) != 0 {
		t.Fatalf("empty playlist entries = %#v", d.Entries)
	}

	for _, title := range []string{"Tune", "Opening", "Second Song"} {
		rec := e.send(t, http.MethodPost, base+"/tracks", map[string]int64{"track_id": ids[title]}, withCookie(c))
		if rec.Code != http.StatusCreated {
			t.Fatalf("add %s = %d %s", title, rec.Code, rec.Body)
		}
	}
	var added struct {
		Entry library.PlaylistEntry `json:"entry"`
	}
	rec := e.send(t, http.MethodPost, base+"/tracks", map[string]int64{"track_id": ids["no tags"]}, withCookie(c))
	decodeBody(t, rec, &added)
	if rec.Code != http.StatusCreated || added.Entry.Position != 4 || added.Entry.Track.ID != ids["no tags"] {
		t.Fatalf("add response = %d %+v", rec.Code, added.Entry)
	}

	expectError(t, e.send(t, http.MethodPost, base+"/tracks", map[string]int64{"track_id": ids["Tune"]}, withCookie(c)),
		http.StatusConflict, "already_in_playlist")
	expectError(t, e.send(t, http.MethodPost, base+"/tracks", map[string]int64{"track_id": 999999}, withCookie(c)),
		http.StatusNotFound, "track_not_found")

	d := getPlaylist(t, e, c, pl.ID)
	if got := entryTitles(d); got != "Tune|Opening|Second Song|no tags" || d.TrackCount != 4 {
		t.Fatalf("detail = %s, %+v", got, d.Playlist)
	}
	// Responses carry relative paths only.
	raw := e.send(t, http.MethodGet, base, nil, withCookie(c)).Body.String()
	if strings.Contains(raw, dir) || strings.Contains(raw, strings.ReplaceAll(dir, `\`, `\\`)) {
		t.Errorf("playlist response leaks the library path: %s", raw)
	}

	// Remove, then reorder what is left.
	if rec := e.send(t, http.MethodDelete, base+"/tracks/"+itoa(ids["Opening"]), nil, withCookie(c)); rec.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body)
	}
	expectError(t, e.send(t, http.MethodDelete, base+"/tracks/"+itoa(ids["Opening"]), nil, withCookie(c)),
		http.StatusNotFound, "not_in_playlist")
	order := []int64{ids["no tags"], ids["Tune"], ids["Second Song"]}
	rec = e.send(t, http.MethodPut, base+"/tracks", map[string][]int64{"track_ids": order}, withCookie(c))
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder = %d %s", rec.Code, rec.Body)
	}
	var reordered playlistBody
	decodeBody(t, rec, &reordered)
	if got := entryTitles(reordered.Playlist); got != "no tags|Tune|Second Song" {
		t.Errorf("reorder response = %s", got)
	}
	expectError(t, e.send(t, http.MethodPut, base+"/tracks", map[string][]int64{"track_ids": order[:2]}, withCookie(c)),
		http.StatusConflict, "order_mismatch")
	if got := entryTitles(getPlaylist(t, e, c, pl.ID)); got != "no tags|Tune|Second Song" {
		t.Errorf("order after rejected reorder = %s", got)
	}

	// Playlist entries play through the existing streaming endpoint.
	if rec := e.send(t, http.MethodGet, "/api/tracks/"+itoa(reordered.Playlist.Entries[0].Track.ID)+"/stream", nil,
		withCookie(c), withHeader("Range", "bytes=0-9")); rec.Code != http.StatusPartialContent {
		t.Errorf("stream playlist entry = %d", rec.Code)
	}

	// Rename and describe.
	rec = e.send(t, http.MethodPatch, base, map[string]string{"name": "Evening", "description": "quiet"}, withCookie(c))
	var patched struct {
		Playlist library.Playlist `json:"playlist"`
	}
	decodeBody(t, rec, &patched)
	if rec.Code != http.StatusOK || patched.Playlist.Name != "Evening" || *patched.Playlist.Description != "quiet" || patched.Playlist.TrackCount != 3 {
		t.Fatalf("patch = %d %+v", rec.Code, patched.Playlist)
	}
	createPlaylist(t, e, c, "Other")
	expectError(t, e.send(t, http.MethodPatch, base, map[string]string{"name": "OTHER"}, withCookie(c)), http.StatusConflict, "name_taken")
	expectError(t, e.send(t, http.MethodPost, "/api/playlists", map[string]string{"name": "evening"}, withCookie(c)), http.StatusConflict, "name_taken")

	decodeBody(t, e.send(t, http.MethodGet, "/api/playlists", nil, withCookie(c)), &list)
	if len(list.Items) != 2 || list.Items[0].Name != "Evening" || list.Items[0].TrackCount != 3 || list.Items[1].Name != "Other" {
		t.Errorf("list = %+v", list.Items)
	}

	if rec := e.send(t, http.MethodDelete, base, nil, withCookie(c)); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("delete = %d %q", rec.Code, rec.Body)
	}
	expectError(t, e.send(t, http.MethodGet, base, nil, withCookie(c)), http.StatusNotFound, "not_found")
	if n := len(getTracks(t, e, c, "").Items); n != 4 {
		t.Errorf("library has %d tracks after deleting a playlist, want 4", n)
	}
}

func TestPlaylistAPIValidation(t *testing.T) {
	e := newTestEnv(t)
	c, _, ids := libraryWithSession(t, e)
	pl := createPlaylist(t, e, c, "P")
	base := "/api/playlists/" + itoa(pl.ID)

	for _, tc := range []struct {
		method, path string
		opts         []reqOpt
		body         any
		status       int
		code         string
	}{
		{http.MethodPost, "/api/playlists", nil, map[string]string{}, 422, "validation_failed"},
		{http.MethodPost, "/api/playlists", nil, map[string]string{"name": "  "}, 422, "validation_failed"},
		{http.MethodPost, "/api/playlists", nil, map[string]string{"name": strings.Repeat("n", 101)}, 422, "validation_failed"},
		{http.MethodPost, "/api/playlists", nil, map[string]string{"name": "a\u0007b"}, 422, "validation_failed"},
		{http.MethodPost, "/api/playlists", nil, map[string]any{"name": "x", "owner": 2}, 400, "invalid_request"},
		{http.MethodPost, "/api/playlists", nil, map[string]any{"name": 5}, 400, "invalid_request"},
		{http.MethodPost, "/api/playlists", []reqOpt{withRawBody(`{"name":"x"}`), withContentType("text/plain")}, nil, 415, "unsupported_media_type"},
		{http.MethodPost, "/api/playlists", []reqOpt{withRawBody(`{"name":"x"} {}`), withContentType("application/json")}, nil, 400, "invalid_request"},
		{http.MethodGet, "/api/playlists?sort=name", nil, nil, 400, "invalid_parameter"},
		{http.MethodGet, "/api/playlists/abc", nil, nil, 400, "invalid_parameter"},
		{http.MethodGet, "/api/playlists/0", nil, nil, 400, "invalid_parameter"},
		{http.MethodGet, "/api/playlists/-1", nil, nil, 400, "invalid_parameter"},
		{http.MethodGet, "/api/playlists/99999", nil, nil, 404, "not_found"},
		{http.MethodGet, "/api/playlists/1%20OR%201=1", nil, nil, 400, "invalid_parameter"},
		{http.MethodPatch, base, nil, map[string]string{}, 422, "validation_failed"},
		{http.MethodPatch, "/api/playlists/99999", nil, map[string]string{"name": "x"}, 404, "not_found"},
		{http.MethodPost, base + "/tracks", nil, map[string]int64{"track_id": 0}, 422, "validation_failed"},
		{http.MethodPost, base + "/tracks", nil, map[string]string{"track_id": "1"}, 400, "invalid_request"},
		{http.MethodPost, base + "/tracks", nil, map[string]any{"track_id": 1, "position": 0}, 400, "invalid_request"},
		{http.MethodPost, base + "/tracks", nil, map[string]string{"path": "/etc/passwd"}, 400, "invalid_request"},
		{http.MethodPost, "/api/playlists/99999/tracks", nil, map[string]int64{"track_id": ids["Tune"]}, 404, "not_found"},
		{http.MethodPut, base + "/tracks", nil, map[string]any{}, 422, "validation_failed"},
		{http.MethodPut, base + "/tracks", nil, map[string][]int64{"track_ids": {-3}}, 422, "validation_failed"},
		{http.MethodPut, base + "/tracks", nil, map[string][]int64{"track_ids": make([]int64, library.MaxPlaylistTracks+1)}, 422, "validation_failed"},
		{http.MethodDelete, base + "/tracks/x", nil, nil, 400, "invalid_parameter"},
		{http.MethodDelete, "/api/playlists/99999/tracks/1", nil, nil, 404, "not_found"},
		{http.MethodPut, base, nil, map[string]string{"name": "x"}, 405, "method_not_allowed"},
		{http.MethodGet, base + "/tracks", nil, nil, 405, "method_not_allowed"},
		{http.MethodPost, "/api/preferences", nil, map[string]bool{"autoplay_next": true}, 405, "method_not_allowed"},
	} {
		rec := e.send(t, tc.method, tc.path, tc.body, append([]reqOpt{withCookie(c)}, tc.opts...)...)
		if rec.Code != tc.status {
			t.Errorf("%s %s: status = %d, want %d; body %s", tc.method, tc.path, rec.Code, tc.status, rec.Body)
			continue
		}
		if got := decodeError(t, rec).Code; got != tc.code {
			t.Errorf("%s %s: code = %q, want %q", tc.method, tc.path, got, tc.code)
		}
	}
	if d := getPlaylist(t, e, c, pl.ID); d.Name != "P" || len(d.Entries) != 0 {
		t.Errorf("playlist changed by invalid requests: %+v", d)
	}
}

func TestPlaylistSQLInjectionHTTP(t *testing.T) {
	e := newTestEnv(t)
	c, _, _ := libraryWithSession(t, e)
	name := `Robert'); DROP TABLE playlist_tracks;--`
	pl := createPlaylist(t, e, c, name)
	if d := getPlaylist(t, e, c, pl.ID); d.Name != name {
		t.Errorf("name = %q", d.Name)
	}
	var n int
	if err := e.db.QueryRow("SELECT count(*) FROM playlist_tracks").Scan(&n); err != nil {
		t.Errorf("playlist_tracks damaged: %v", err)
	}
}

func TestPlaylistOfAnotherUserIsHidden(t *testing.T) {
	e := newTestEnv(t)
	c, _, ids := libraryWithSession(t, e)
	// Rewind has a single owner account today; a second user is inserted
	// directly to prove that every query is scoped to the session's user.
	res, err := e.db.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at, password_changed_at) VALUES ('other', 'h', 1, 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()
	theirs, err := library.NewPlaylists(e.db).Create(t.Context(), other, "Private", "")
	if err != nil {
		t.Fatal(err)
	}
	library.NewPlaylists(e.db).AddTrack(t.Context(), other, theirs.ID, ids["Tune"])
	base := "/api/playlists/" + itoa(theirs.ID)

	for _, rt := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, base, nil},
		{http.MethodPatch, base, map[string]string{"name": "Mine now"}},
		{http.MethodDelete, base, nil},
		{http.MethodPost, base + "/tracks", map[string]int64{"track_id": ids["Opening"]}},
		{http.MethodPut, base + "/tracks", map[string][]int64{"track_ids": {ids["Tune"]}}},
		{http.MethodDelete, base + "/tracks/" + itoa(ids["Tune"]), nil},
	} {
		expectError(t, e.send(t, rt.method, rt.path, rt.body, withCookie(c)), http.StatusNotFound, "not_found")
	}
	var list struct {
		Items []library.Playlist `json:"items"`
	}
	decodeBody(t, e.send(t, http.MethodGet, "/api/playlists", nil, withCookie(c)), &list)
	if len(list.Items) != 0 {
		t.Errorf("owner sees another user's playlists: %+v", list.Items)
	}
	d, err := library.NewPlaylists(e.db).Get(t.Context(), other, theirs.ID)
	if err != nil || d.Name != "Private" || len(d.Entries) != 1 {
		t.Errorf("other user's playlist changed: %+v, %v", d, err)
	}
}

func TestPlaylistCSRF(t *testing.T) {
	e := newTestEnv(t)
	c, _, ids := libraryWithSession(t, e)
	pl := createPlaylist(t, e, c, "Keep")
	e.send(t, http.MethodPost, "/api/playlists/"+itoa(pl.ID)+"/tracks", map[string]int64{"track_id": ids["Tune"]}, withCookie(c))
	base := "/api/playlists/" + itoa(pl.ID)
	attack := []reqOpt{withCookie(c), withHeader("Sec-Fetch-Site", "cross-site"), withHeader("Origin", "https://evil.example")}
	for _, rt := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/playlists", map[string]string{"name": "Injected"}},
		{http.MethodPatch, base, map[string]string{"name": "Hijacked"}},
		{http.MethodDelete, base, nil},
		{http.MethodPost, base + "/tracks", map[string]int64{"track_id": ids["Opening"]}},
		{http.MethodPut, base + "/tracks", map[string][]int64{"track_ids": {ids["Tune"]}}},
		{http.MethodDelete, base + "/tracks/" + itoa(ids["Tune"]), nil},
		{http.MethodPatch, "/api/preferences", map[string]bool{"autoplay_next": false}},
	} {
		expectError(t, e.send(t, rt.method, rt.path, rt.body, attack...), http.StatusForbidden, "cross_origin")
	}
	d := getPlaylist(t, e, c, pl.ID)
	if d.Name != "Keep" || entryTitles(d) != "Tune" {
		t.Errorf("playlist changed by cross-site requests: %+v", d)
	}
	var n int
	e.db.QueryRow("SELECT (SELECT count(*) FROM playlists) + (SELECT count(*) FROM user_preferences)").Scan(&n)
	if n != 1 {
		t.Errorf("rows after cross-site requests = %d, want 1 playlist", n)
	}
}

func TestPreferencesAPI(t *testing.T) {
	dataDir := t.TempDir()
	e := newTestEnvWith(t, envConfig{dataDir: dataDir})
	c := e.doSetup(t)

	type body struct {
		Preferences prefs.Preferences `json:"preferences"`
		Defaults    prefs.Preferences `json:"defaults"`
	}
	var b body
	decodeBody(t, e.send(t, http.MethodGet, "/api/preferences", nil, withCookie(c)), &b)
	if b.Preferences != prefs.Defaults() || b.Defaults != prefs.Defaults() {
		t.Fatalf("initial = %+v", b)
	}

	rec := e.send(t, http.MethodPatch, "/api/preferences", map[string]any{
		"library_sort": "added", "autoplay_next": false, "theme": "oled-black", "accent_color": "#ff8800",
	}, withCookie(c))
	decodeBody(t, rec, &b)
	if rec.Code != http.StatusOK || b.Preferences.LibrarySort != "added" || b.Preferences.AutoplayNext ||
		b.Preferences.Theme != "oled-black" || b.Preferences.AccentColor != "#ff8800" {
		t.Fatalf("patch = %d %+v", rec.Code, b)
	}

	for _, bad := range []struct {
		body   string
		status int
		code   string
	}{
		{`{"library_sort":"rel_path"}`, 422, "validation_failed"},
		{`{"theme":"dark"}`, 422, "validation_failed"},
		{`{"autoplay_next":"false"}`, 422, "validation_failed"},
		{`{"autoplay_next":null}`, 422, "validation_failed"},
		{`{"theme":"../../etc/passwd"}`, 422, "validation_failed"},
		{`{"accent_color":"red;}body{background:url(https://evil.example)"}`, 422, "validation_failed"},
		{`{"accent_color":"#FFF"}`, 422, "validation_failed"},
		{`{}`, 422, "validation_failed"},
		{`null`, 422, "validation_failed"},
		{`[]`, 400, "invalid_request"},
		{`{"library_sort":"title"`, 400, "invalid_request"},
	} {
		rec := e.send(t, http.MethodPatch, "/api/preferences", nil, withCookie(c), withRawBody(bad.body), withContentType("application/json"))
		if rec.Code != bad.status || decodeError(t, rec).Code != bad.code {
			t.Errorf("PATCH %s = %d %s", bad.body, rec.Code, rec.Body)
		}
	}

	// A restart (new handler over the same data directory) keeps them.
	e2 := newTestEnvWith(t, envConfig{dataDir: dataDir})
	decodeBody(t, e2.send(t, http.MethodGet, "/api/preferences", nil, withCookie(c)), &b)
	if b.Preferences.LibrarySort != "added" || b.Preferences.AutoplayNext || b.Preferences.Theme != "oled-black" || b.Preferences.AccentColor != "#ff8800" {
		t.Errorf("after restart = %+v", b.Preferences)
	}
}
