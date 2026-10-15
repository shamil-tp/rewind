// SPDX-License-Identifier: Apache-2.0

package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shamil-tp/rewind/internal/testaudio"
)

// listeningEnv scans a library with a two-disc album, a compilation-style
// album with an album artist, multi-artist credits and a loose track.
func listeningEnv(t *testing.T) (*env, string, Root, map[string]int64) {
	t.Helper()
	e := newEnv(t)
	dir := t.TempDir()
	tg := func(title, artist, album, albumArtist, track, disc, year string) map[string][]string {
		m := map[string][]string{"TITLE": {title}, "ARTIST": {artist}}
		for k, v := range map[string]string{"ALBUM": album, "ALBUMARTIST": albumArtist, "TRACKNUMBER": track, "DISCNUMBER": disc, "DATE": year} {
			if v != "" {
				m[k] = []string{v}
			}
		}
		return m
	}
	testaudio.Write(t, dir, "Band/Big/d2-01.mp3", 2, tg("Disc Two Opener", "Band", "Big Album", "", "1", "2", "2001"))
	testaudio.Write(t, dir, "Band/Big/d1-02.mp3", 2, tg("Second", "Band", "Big Album", "", "2", "1", "2001"))
	testaudio.Write(t, dir, "Band/Big/d1-01.mp3", 3, tg("First", "Band", "big album", "", "1", "1", "2001"))
	testaudio.Write(t, dir, "Mix/01.mp3", 2, tg("Duet", "Band; Singer", "Friends", "Various Artists", "1", "", "1999"))
	testaudio.Write(t, dir, "Mix/02.mp3", 2, tg("Solo", "Singer", "Friends", "Various Artists", "2", "", "1999"))
	testaudio.Write(t, dir, "loose.mp3", 1, tg("Loose", "Band", "", "", "", "", ""))
	root := e.addRoot(t, dir)
	e.scan(t, e.scanner(t, Options{}), root)
	ids := map[string]int64{}
	for rel, r := range e.tracks(t, root.ID) {
		ids[rel] = r.ID
	}
	return e, dir, root, ids
}

func trackTitles(ts []Track) string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Title)
	}
	return strings.Join(out, ",")
}

func TestAlbumsAndAlbumDetail(t *testing.T) {
	e, _, _, _ := listeningEnv(t)
	c := NewCatalog(e.db)
	albums, total, err := c.Albums(bg, AlbumQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(albums) != 2 || albums[0].Title != "Big Album" || albums[1].Title != "Friends" {
		t.Fatalf("albums = %+v", albums)
	}
	big := albums[0]
	if big.TrackCount != 3 || big.DiscCount != 2 || big.Artist != "Band" || big.Year == nil || *big.Year != 2001 {
		t.Errorf("big album = %+v", big)
	}
	if albums[1].Artist != "Various Artists" || albums[1].TrackCount != 2 {
		t.Errorf("compilation = %+v", albums[1])
	}
	d, err := c.Album(bg, big.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Disc, then track number: deterministic order.
	if got := trackTitles(d.Tracks); got != "First,Second,Disc Two Opener" {
		t.Errorf("album order = %s", got)
	}
	if d.Version == "" || len(d.Folders) != 1 || !strings.HasSuffix(d.Folders[0], "/Band/Big") {
		t.Errorf("version %q folders %v", d.Version, d.Folders)
	}
	for _, tr := range d.Tracks {
		if tr.AlbumID == nil || *tr.AlbumID != big.ID {
			t.Errorf("%s album id = %v", tr.Title, tr.AlbumID)
		}
	}
	if _, err := c.Album(bg, "not base64!"); !errors.Is(err, ErrAlbumNotFound) {
		t.Errorf("bad id: %v", err)
	}
	if _, err := c.Album(bg, EncodeID("nope\x1fnope")); !errors.Is(err, ErrAlbumNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	found, _, _ := c.Albums(bg, AlbumQuery{Limit: 50, Search: "friend"})
	if len(found) != 1 {
		t.Errorf("search = %+v", found)
	}
}

func TestAlbumQueryUsesIndex(t *testing.T) {
	e, _, _, _ := listeningEnv(t)
	var plan strings.Builder
	rows, err := e.db.Query("EXPLAIN QUERY PLAN SELECT t.id FROM tracks t WHERE t.album IS NOT NULL AND "+albumKeyExpr+" = ?", "x")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		rows.Scan(&id, &parent, &unused, &detail)
		plan.WriteString(detail + ";")
	}
	if !strings.Contains(plan.String(), "tracks_album_key") {
		t.Errorf("album lookups do not use the index: %s", plan.String())
	}
}

func TestArtistsSplitCredits(t *testing.T) {
	e, _, _, _ := listeningEnv(t)
	c := NewCatalog(e.db)
	artists, total, err := c.Artists(bg, ArtistQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]Artist{}
	for _, a := range artists {
		names[a.Name] = a
	}
	if total != 3 || names["Band"].TrackCount != 5 || names["Singer"].TrackCount != 2 || names["Various Artists"].TrackCount != 2 {
		t.Fatalf("artists = %+v", artists)
	}
	d, err := c.Artist(bg, names["Singer"].ID)
	if err != nil {
		t.Fatal(err)
	}
	// Singer is credited on a compilation, not as album artist.
	if len(d.Albums) != 0 || len(d.AppearsOn) != 1 || d.AppearsOn[0].Title != "Friends" || d.TrackCount != 2 {
		t.Errorf("singer = %+v", d)
	}
	band, err := c.Artist(bg, names["Band"].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(band.Albums) != 1 || band.Albums[0].Title != "Big Album" || len(band.AppearsOn) != 1 || band.TrackCount != 5 {
		t.Errorf("band albums %+v appears %+v count %d", band.Albums, band.AppearsOn, band.TrackCount)
	}
	if _, err := c.Artist(bg, EncodeID("nobody")); !errors.Is(err, ErrArtistNotFound) {
		t.Errorf("unknown: %v", err)
	}
	// Multi-artist tracks link to each artist.
	for _, tr := range band.Tracks {
		if tr.Title == "Duet" && (len(tr.Artists) != 2 || tr.Artists[1].Name != "Singer") {
			t.Errorf("duet artists = %+v", tr.Artists)
		}
	}
}

func TestMetadataEditsSurviveRescanAndRevert(t *testing.T) {
	e, dir, root, ids := listeningEnv(t)
	ed := NewEditor(e.db)
	id := ids["loose.mp3"]
	md, err := ed.Get(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := ParseChanges(map[string]json.RawMessage{
		"title": json.RawMessage(`"Lose — 失われた"`), "album": json.RawMessage(`"Singles"`), "year": json.RawMessage(`2020`),
		"lyrics": json.RawMessage(`"line one\r\nline two"`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	md, err = ed.Update(bg, id, md.Revision, changes)
	if err != nil {
		t.Fatal(err)
	}
	if md.Values["title"] != "Lose — 失われた" || md.Values["lyrics"] != "line one\nline two" || len(md.Edited) != 4 ||
		md.FromFile["title"] != "Loose" || md.FromFile["album"] != nil {
		t.Fatalf("after edit = %+v", md)
	}
	// A stale revision is refused.
	if _, err := ed.Update(bg, id, md.Revision-1, changes); !errors.Is(err, ErrEditConflict) {
		t.Errorf("stale update: %v", err)
	}
	// The file changes and is rescanned: the edit is applied again, and the
	// new tag value becomes the original.
	testaudio.Tag(t, filepath.Join(dir, "loose.mp3"), map[string][]string{"TITLE": {"Loose v2"}, "ARTIST": {"Band"}})
	os.Chtimes(filepath.Join(dir, "loose.mp3"), time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	e.scan(t, e.scanner(t, Options{}), root)
	md, _ = ed.Get(bg, id)
	if md.Values["title"] != "Lose — 失われた" || md.FromFile["title"] != "Loose v2" {
		t.Fatalf("after rescan = %+v", md)
	}
	// Reverting the title restores the file's value; other edits stay.
	md, err = ed.Revert(bg, id, md.Revision, []string{"title"})
	if err != nil {
		t.Fatal(err)
	}
	if md.Values["title"] != "Loose v2" || len(md.Edited) != 3 {
		t.Errorf("after revert = %+v", md)
	}
	// Changing a value back to the file's value drops the edit.
	back, _ := ParseChanges(map[string]json.RawMessage{"album": json.RawMessage(`null`)}, nil)
	md, _ = ed.Update(bg, id, md.Revision, back)
	if md.Values["album"] != nil || len(md.Edited) != 2 {
		t.Errorf("after clearing = %+v", md)
	}
	// The music file was never modified by an edit.
	data, _ := os.ReadFile(filepath.Join(dir, "loose.mp3"))
	if bytes.Contains(data, []byte("Singles")) || bytes.Contains(data, []byte("2020")) {
		t.Error("edit was written into the file")
	}
}

func TestEditValidation(t *testing.T) {
	for name, raw := range map[string]string{
		"title": `null`, "track_number": `0`, "year": `999`, "disc_number": `"2"`, "genre": `5`, "path": `"x"`, "artist": `{}`,
	} {
		if _, err := ParseChanges(map[string]json.RawMessage{name: json.RawMessage(raw)}, nil); err == nil {
			t.Errorf("%s=%s accepted", name, raw)
		}
	}
	if _, err := ParseChanges(map[string]json.RawMessage{"title": json.RawMessage(`"x"`)}, AlbumEditFields); err == nil {
		t.Error("title accepted as an album field")
	}
	if _, err := ParseChanges(map[string]json.RawMessage{"title": json.RawMessage(`"` + strings.Repeat("a", 513) + `"`)}, nil); err == nil {
		t.Error("overlong title accepted")
	}
}

func TestAlbumEditAndArtistRename(t *testing.T) {
	e, _, _, _ := listeningEnv(t)
	c, ed := NewCatalog(e.db), NewEditor(e.db)
	albums, _, _ := c.Albums(bg, AlbumQuery{Limit: 10})
	d, _ := c.Album(bg, albums[0].ID)
	changes, _ := ParseChanges(map[string]json.RawMessage{"genre": json.RawMessage(`"Rock"`), "album": json.RawMessage(`"Big Album (Deluxe)"`)}, AlbumEditFields)
	if _, err := ed.UpdateAlbum(bg, d.ID, "wrong", changes); !errors.Is(err, ErrEditConflict) {
		t.Errorf("stale album edit: %v", err)
	}
	newID, err := ed.UpdateAlbum(bg, d.ID, d.Version, changes)
	if err != nil {
		t.Fatal(err)
	}
	nd, err := c.Album(bg, newID)
	if err != nil || nd.Title != "Big Album (Deluxe)" || nd.TrackCount != 3 || nd.Genre == nil || *nd.Genre != "Rock" {
		t.Fatalf("edited album = %+v, %v", nd.Album, err)
	}

	artists, _, _ := c.Artists(bg, ArtistQuery{Limit: 10, Search: "singer"})
	sd, _ := c.Artist(bg, artists[0].ID)
	newArtist, err := ed.RenameArtist(bg, sd.ID, sd.Version, "The Singer")
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := c.Artist(bg, newArtist)
	if err != nil || renamed.Name != "The Singer" || renamed.TrackCount != 2 {
		t.Fatalf("renamed = %+v, %v", renamed.Artist, err)
	}
	// The other credit on the duet is kept.
	for _, tr := range renamed.Tracks {
		if tr.Title == "Duet" && *tr.Artist != "Band; The Singer" {
			t.Errorf("duet artist = %q", *tr.Artist)
		}
	}
	if _, err := ed.RenameArtist(bg, renamed.ID, renamed.Version, "A; B"); err == nil {
		t.Error("name with ';' accepted")
	}
}

func TestFavoritesAndHistory(t *testing.T) {
	e, _, root, ids := listeningEnv(t)
	p := NewPersonal(e.db)
	u, other := e.user(t, "owner"), e.user(t, "other")
	first, second := ids["Band/Big/d1-01.mp3"], ids["Mix/01.mp3"]
	clock := time.Unix(1_700_000_000, 0)
	p.now = func() time.Time { clock = clock.Add(time.Minute); return clock }

	if err := p.SetFavorite(bg, u, first, true); err != nil {
		t.Fatal(err)
	}
	p.SetFavorite(bg, u, first, true) // repeated: harmless
	if err := p.SetFavorite(bg, u, 99999, true); !errors.Is(err, ErrTrackNotFound) {
		t.Errorf("unknown track: %v", err)
	}
	favs, total, _ := p.Favorites(bg, u, 10, 0)
	if total != 1 || favs[0].ID != first {
		t.Errorf("favorites = %v", favs)
	}

	play := func(track int64, key string) bool {
		t.Helper()
		ok, err := p.RecordPlay(bg, u, PlayEvent{TrackID: track, PlayKey: key, ListenedMS: 90_000, SourceKind: "album", SourceID: "x"})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !play(first, "play-0001") || play(first, "play-0001") {
		t.Error("a retried play must count exactly once")
	}
	play(first, "play-0002")
	play(second, "play-0003")
	for _, bad := range []PlayEvent{
		{TrackID: first, PlayKey: "short"},
		{TrackID: first, PlayKey: "bad key with spaces"},
		{TrackID: first, PlayKey: "play-0009", ListenedMS: -1},
		{TrackID: first, PlayKey: "play-0009", SourceKind: "radio"},
	} {
		var in *HistoryInputError
		if _, err := p.RecordPlay(bg, u, bad); !errors.As(err, &in) {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	tracks := []Track{{ID: first}, {ID: second}, {ID: ids["loose.mp3"]}}
	if err := p.Annotate(bg, u, tracks); err != nil {
		t.Fatal(err)
	}
	if !tracks[0].Favorite || tracks[0].PlayCount != 2 || tracks[0].LastPlayedAt == nil || tracks[1].PlayCount != 1 || tracks[2].PlayCount != 0 {
		t.Errorf("annotated = %+v", tracks)
	}
	// Another user's data is separate.
	o := []Track{{ID: first}}
	p.Annotate(bg, other, o)
	if o[0].Favorite || o[0].PlayCount != 0 {
		t.Errorf("other user sees %+v", o[0])
	}

	hist, total, _ := p.History(bg, u, HistoryQuery{Limit: 10})
	if total != 3 || hist[0].Track.ID != second || *hist[0].SourceKind != "album" {
		t.Fatalf("history = %+v", hist)
	}
	if h, _, _ := p.History(bg, u, HistoryQuery{Limit: 10, Search: "duet"}); len(h) != 1 {
		t.Errorf("search = %d", len(h))
	}
	if h, _, _ := p.History(bg, u, HistoryQuery{Limit: 10, From: hist[0].PlayedAt}); len(h) != 1 {
		t.Errorf("date filter = %d", len(h))
	}
	rec, _ := p.Recent(bg, u, 10)
	if trackTitles(rec.Tracks) != "Duet,First" || len(rec.Albums) != 2 || rec.Albums[0].Title != "Friends" || len(rec.Artists) != 2 {
		t.Errorf("recent = %s / %+v / %+v", trackTitles(rec.Tracks), rec.Albums, rec.Artists)
	}
	top, _ := p.TopTracks(bg, u, "", 10)
	if trackTitles(top) != "First,Duet" {
		t.Errorf("top = %s", trackTitles(top))
	}
	ms, _ := p.ListeningMS(bg, u, EncodeID("singer"))
	if ms != 90_000 {
		t.Errorf("singer listening = %d", ms)
	}

	// History of a track whose file disappears is kept; the track says so.
	os.Remove(filepath.Join(root.Path, "Mix", "01.mp3"))
	e.scan(t, e.scanner(t, Options{}), root)
	hist, _, _ = p.History(bg, u, HistoryQuery{Limit: 10})
	if hist[0].Track.Status != "missing" || hist[0].Track.Available {
		t.Errorf("missing track in history = %+v", hist[0].Track)
	}

	if n, _ := p.DeleteHistory(bg, other, []int64{hist[0].ID}); n != 0 {
		t.Error("another user deleted a play")
	}
	if n, _ := p.DeleteHistory(bg, u, []int64{hist[0].ID}); n != 1 {
		t.Error("delete failed")
	}
	if n, _ := p.ClearHistory(bg, u); n != 2 {
		t.Errorf("clear removed %d", n)
	}
}

func TestPlaybackStateStore(t *testing.T) {
	e, _, _, ids := listeningEnv(t)
	s := NewPlaybackStore(e.db)
	u := e.user(t, "owner")
	saved, err := s.Get(bg, u)
	if err != nil || saved.State.Index != -1 || len(saved.State.Queue) != 0 || saved.Revision != 0 {
		t.Fatalf("default = %+v, %v", saved, err)
	}
	a, b, c := ids["Band/Big/d1-01.mp3"], ids["Band/Big/d1-02.mp3"], ids["Mix/02.mp3"]
	st := PlaybackState{Queue: []int64{a, b, c, a}, Original: []int64{a, a, b, c}, Index: 2, PositionMS: 42_000, Shuffle: true,
		Repeat: "all", Speed: 1.25, Context: &PlaybackContext{Kind: "album", ID: "x", Name: "Big"}}
	if err := s.Put(bg, u, 10, st); err != nil {
		t.Fatal(err)
	}
	// An older write never replaces a newer one.
	if err := s.Put(bg, u, 9, PlaybackState{Queue: []int64{a}, Index: 0}); !errors.Is(err, ErrStaleState) {
		t.Errorf("stale write: %v", err)
	}
	saved, _ = s.Get(bg, u)
	if saved.Revision != 10 || saved.State.Index != 2 || saved.State.Speed != 1.25 || len(saved.Tracks) != 3 || saved.State.PositionMS != 42_000 {
		t.Fatalf("saved = %+v", saved)
	}
	for name, bad := range map[string]PlaybackState{
		"negative id":  {Queue: []int64{-1}, Index: 0},
		"index":        {Queue: []int64{a}, Index: 1},
		"repeat":       {Queue: []int64{a}, Repeat: "twice"},
		"speed":        {Queue: []int64{a}, Speed: 4},
		"original":     {Queue: []int64{a, b}, Original: []int64{a, a}, Shuffle: true},
		"no shuffle":   {Queue: []int64{a}, Original: []int64{a}},
		"context kind": {Queue: []int64{a}, Context: &PlaybackContext{Kind: "radio"}},
		"too long":     {Queue: make([]int64, MaxQueueLength+1)},
	} {
		var in *PlaybackInputError
		if err := s.Put(bg, u, 100, bad); !errors.As(err, &in) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Entries whose tracks no longer exist are dropped; the index follows.
	e.db.Exec("DELETE FROM tracks WHERE id = ?", b)
	saved, _ = s.Get(bg, u)
	if len(saved.State.Queue) != 3 || saved.State.Index != 1 || saved.State.Queue[1] != c {
		t.Errorf("after delete = %+v", saved.State)
	}
}

func TestPlaylistDuplicateBulkAddAndArtwork(t *testing.T) {
	e, s, u, _, _, ids := playlistEnv(t)
	pl, err := s.Create(bg, u, "Mix", "")
	if err != nil {
		t.Fatal(err)
	}
	all := []int64{ids["a.flac"], ids["b.mp3"], ids["a.flac"], ids["c.ogg"]}
	added, skipped, err := s.AddTracks(bg, u, pl.ID, all)
	if err != nil || added != 3 || skipped != 1 {
		t.Fatalf("AddTracks = %d, %d, %v", added, skipped, err)
	}
	if _, _, err := s.AddTracks(bg, u, pl.ID, []int64{ids["b.mp3"], 99999}); !errors.Is(err, ErrTrackNotFound) {
		t.Errorf("unknown track: %v", err)
	}
	set, err := s.SetArtwork(bg, u, pl.ID, &all[1])
	if err != nil || !set.ArtworkChosen || *set.ArtworkTrackID != all[1] {
		t.Fatalf("SetArtwork = %+v, %v", set, err)
	}
	dup, err := s.Duplicate(bg, u, pl.ID)
	if err != nil || dup.Name != "Mix (copy)" || dup.TrackCount != 3 || *dup.ArtworkTrackID != all[1] {
		t.Fatalf("Duplicate = %+v, %v", dup, err)
	}
	dup2, _ := s.Duplicate(bg, u, pl.ID)
	if dup2.Name != "Mix (copy 2)" {
		t.Errorf("second copy = %q", dup2.Name)
	}
	orig, _ := s.Get(bg, u, pl.ID)
	cp, _ := s.Get(bg, u, dup.ID)
	if trackTitles(entryTracks(orig)) != trackTitles(entryTracks(cp)) {
		t.Errorf("copy order differs")
	}
	// Editing the copy leaves the original alone.
	s.RemoveTrack(bg, u, dup.ID, all[0])
	orig, _ = s.Get(bg, u, pl.ID)
	if len(orig.Entries) != 3 {
		t.Error("original changed")
	}
	other := e.user(t, "other")
	if _, err := s.Duplicate(bg, other, pl.ID); !errors.Is(err, ErrPlaylistNotFound) {
		t.Errorf("other user duplicated: %v", err)
	}
	unset, _ := s.SetArtwork(bg, u, pl.ID, nil)
	if unset.ArtworkChosen || *unset.ArtworkTrackID != all[0] {
		t.Errorf("default artwork = %+v", unset)
	}
}

func entryTracks(d PlaylistDetail) []Track {
	out := make([]Track, len(d.Entries))
	for i, e := range d.Entries {
		out[i] = e.Track
	}
	return out
}

func pngImage(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestArtworkEmbeddedSidecarAndCache(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	emb := testaudio.Write(t, dir, "A/one.mp3", 1, tags("One", "X", "A", "1"))
	testaudio.Image(t, emb, pngImage(t, 1200, 600, color.RGBA{200, 10, 10, 255}))
	testaudio.Write(t, dir, "B/two.mp3", 1, tags("Two", "X", "B", "1"))
	os.WriteFile(filepath.Join(dir, "B", "Cover.PNG"), pngImage(t, 64, 64, color.RGBA{0, 0, 200, 255}), 0o644)
	testaudio.Write(t, dir, "C/three.mp3", 1, tags("Three", "X", "C", "1"))
	os.WriteFile(filepath.Join(dir, "C", "cover.jpg"), []byte("not an image"), 0o644)
	root := e.addRoot(t, dir)
	e.scan(t, e.scanner(t, Options{}), root)
	ids := map[string]int64{}
	for rel, r := range e.tracks(t, root.ID) {
		ids[rel] = r.ID
	}
	cat := NewCatalog(e.db)
	cacheDir := filepath.Join(t.TempDir(), "artwork")
	a, err := NewArtworkCache(e.db, cat, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(emb)

	p, hash, err := a.Get(bg, ids["A/one.mp3"], ArtworkLarge)
	if err != nil || len(hash) != 32 {
		t.Fatalf("embedded: %v", err)
	}
	f, _ := os.Open(p)
	cfg, format, err := image.DecodeConfig(f)
	f.Close()
	if err != nil || format != "jpeg" || cfg.Width != 1000 || cfg.Height != 500 {
		t.Errorf("large rendition = %v %s %v (aspect ratio must be kept)", cfg, format, err)
	}
	small, _, _ := a.Get(bg, ids["A/one.mp3"], ArtworkSmall)
	f, _ = os.Open(small)
	cfg, _, _ = image.DecodeConfig(f)
	f.Close()
	if cfg.Width != 320 || cfg.Height != 160 {
		t.Errorf("small rendition = %v", cfg)
	}
	if after, _ := os.ReadFile(emb); !bytes.Equal(before, after) {
		t.Error("the music file changed")
	}

	if _, src, err := a.Get(bg, ids["B/two.mp3"], ArtworkSmall); err != nil || src == "" {
		t.Errorf("sidecar: %v", err)
	}
	if _, _, err := a.Get(bg, ids["C/three.mp3"], ArtworkSmall); !errors.Is(err, ErrNoArtwork) {
		t.Errorf("invalid cover: %v", err)
	}
	var status string
	e.db.QueryRow("SELECT status FROM track_artwork WHERE track_id = ?", ids["C/three.mp3"]).Scan(&status)
	if status != "none" {
		t.Errorf("'none' not remembered: %q", status)
	}
	if _, _, err := a.Get(bg, 99999, ArtworkSmall); !errors.Is(err, ErrTrackNotFound) {
		t.Errorf("unknown track: %v", err)
	}

	// Served from the cache while the drive is unplugged.
	os.Rename(dir, dir+"-gone")
	t.Cleanup(func() { os.Rename(dir+"-gone", dir) })
	if _, _, err := a.Get(bg, ids["A/one.mp3"], ArtworkLarge); err != nil {
		t.Errorf("cached artwork while unplugged: %v", err)
	}

	// Cleanup removes only unreferenced renditions and temporary files.
	os.WriteFile(filepath.Join(cacheDir, strings.Repeat("a", 32)+"-small.jpg"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(cacheDir, ".tmp-123"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(cacheDir, "keep.txt"), []byte("x"), 0o600)
	n, err := a.Cleanup(bg)
	if err != nil || n != 2 {
		t.Errorf("Cleanup = %d, %v", n, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error("a used rendition was removed")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "keep.txt")); err != nil {
		t.Error("an unrelated file was removed")
	}
}

func TestOversizedArtworkRejected(t *testing.T) {
	if validImage(pngImage(t, 13000, 1, color.Black)) {
		t.Error("image wider than the limit accepted")
	}
	if validImage([]byte("GIF89a garbage")) || validImage(nil) {
		t.Error("malformed image accepted")
	}
	if !validImage(pngImage(t, 10, 10, color.White)) {
		t.Error("small png rejected")
	}
}

func TestRenameMergeMovesListeningData(t *testing.T) {
	e, dir, root, ids := listeningEnv(t)
	p, ed := NewPersonal(e.db), NewEditor(e.db)
	u := e.user(t, "owner")
	id := ids["loose.mp3"]
	p.SetFavorite(bg, u, id, true)
	p.RecordPlay(bg, u, PlayEvent{TrackID: id, PlayKey: "rename-01", ListenedMS: 1000})
	md, _ := ed.Get(bg, id)
	ch, _ := ParseChanges(map[string]json.RawMessage{"genre": json.RawMessage(`"Edited"`)}, nil)
	ed.Update(bg, id, md.Revision, ch)

	if err := os.Rename(filepath.Join(dir, "loose.mp3"), filepath.Join(dir, "moved.mp3")); err != nil {
		t.Fatal(err)
	}
	e.scan(t, e.scanner(t, Options{}), root)
	tr := e.tracks(t, root.ID)
	if tr["moved.mp3"].ID != id {
		t.Fatalf("rename not detected: %+v", tr["moved.mp3"])
	}
	ts := []Track{{ID: id}}
	p.Annotate(bg, u, ts)
	md, _ = ed.Get(bg, id)
	if !ts[0].Favorite || ts[0].PlayCount != 1 || md.Values["genre"] != "Edited" {
		t.Errorf("after rename: %+v, genre %v", ts[0], md.Values["genre"])
	}
}

func TestComposerAndLyricsAreRead(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	testaudio.Write(t, dir, "song.flac", 1, map[string][]string{"TITLE": {"Song"}, "COMPOSER": {"Writer"}, "LYRICS": {"la la\r\nla"}})
	root := e.addRoot(t, dir)
	e.scan(t, e.scanner(t, Options{}), root)
	tr, err := NewCatalog(e.db).Track(bg, e.tracks(t, root.ID)["song.flac"].ID)
	if err != nil || tr.Composer == nil || *tr.Composer != "Writer" || !tr.HasLyrics || tr.Lyrics == nil || *tr.Lyrics != "la la\nla" {
		t.Fatalf("track = %+v, %v", tr, err)
	}
	// Files indexed by an older extractor are read again once.
	e.db.Exec("UPDATE tracks SET metadata_version = 0, composer = NULL")
	e.scan(t, e.scanner(t, Options{}), root)
	tr, _ = NewCatalog(e.db).Track(bg, tr.ID)
	if tr.Composer == nil {
		t.Error("old extractor version was not re-read")
	}
}
