# REWIND — Complete Music Player Feature, Functionality & UI/UX Specification

> **Implementation status (maintained by the developers).** This file is the
> product specification: it describes the intended product, not the code. The
> verified implementation architecture, decisions and limitations are in
> [docs/architecture.md](docs/architecture.md), and the API in
> [docs/api.md](docs/api.md).
>
> - **Phase 2 — Complete listening experience** (section 59) is implemented;
>   see docs/architecture.md §5f. It includes the parts of "Phase 1" it
>   depends on that did not exist yet: the play queue, favorites, and album and
>   artist browsing.
> - Not yet implemented from "Phase 1": folder browsing and a separate search
>   page (search is part of the Library, Albums, Artists and History views).
> - Metadata editing is **database-only** (section 22.2): Rewind never writes
>   tags into music files. Embedded-tag writing is not implemented.
> - Phases 3–5 of this specification are not started, except the theme
>   system, PWA installation, browser media controls and backup/restore
>   documentation, which exist from earlier work.

## 1. PRODUCT OVERVIEW

Build a modern, self-hosted music player inspired by Spotify, Apple Music, and Plexamp, designed primarily for playing and organizing a personal music collection.

### Core requirements
- Application name: Rewind.
- Architecture: Go backend, Svelte frontend, TypeScript, Vite, SQLite.
- Audio playback: Native browser audio capabilities with HTTP range-based streaming.
- Metadata extraction: Read ID3 tags and supported audio metadata using go-taglib.
- Library storage: Existing music files on server disks, USB drives, and pendrives.
- Database: SQLite; no separate database server.
- Access: Local network and authorized remote devices through Tailscale.
- Application format: Responsive web application and installable Progressive Web App (PWA).
- Design: Dark neo-brutalist, bold typography, sharp edges, distinctive borders, minimal clutter.
- Performance: Lightweight, fast startup, low CPU and RAM usage.
- Extensibility: Plugin system for themes, visualizations, integrations, and additional functionality.
- Security: Private personal library with authenticated access and secure administration.
- Primary objective: Make a personal music collection feel as polished, discoverable, and enjoyable as a commercial music streaming application.

### Product boundaries
- Prioritize playback of locally owned or legally accessible music.
- Do not require a third-party music streaming subscription.
- Do not require a cloud music service or separate database service.
- Do not duplicate music files unnecessarily.
- Do not require AI, external APIs, or internet access for core playback.
- Keep remote access private and configurable.
- Treat cloud streaming catalogs, paid subscriptions, and commercial licensing as outside the core scope.

---

# 2. GLOBAL APPLICATION LAYOUT

## 2.1 Desktop layout

Implement a responsive three-region application shell.

### Left sidebar
- Rewind logo and application name.
- Home navigation.
- Search navigation.
- Explore/Discover navigation.
- Library navigation.
- Songs.
- Albums.
- Artists.
- Genres.
- Playlists.
- Folders.
- Recently played.
- Most played.
- Favorites/Liked Songs.
- Downloads or offline music, where supported.
- Queue shortcut.
- Listening history.
- Settings.
- Server status.
- Collapsible sidebar.
- Sidebar width adjustment.
- User-defined sidebar shortcuts.
- Pinned playlists.
- Recently accessed playlists.
- Drag-and-drop playlist organization, where supported.
- Sidebar tooltips when collapsed.
- Active-page indicator.
- Keyboard-accessible navigation.
- Scrollable sidebar with independent scrolling.

### Main content area
- Current page heading.
- Breadcrumbs where applicable.
- Search and filtering controls.
- Sort controls.
- Content grids and lists.
- Context-sensitive actions.
- Loading, empty, error, and offline states.
- Optional right-side details panel.
- Content-specific menus.
- Smooth but restrained transitions.

### Bottom playback bar
- Current track artwork.
- Track title.
- Artist name.
- Favorite button.
- Additional track actions.
- Previous track.
- Play/pause.
- Next track.
- Shuffle.
- Repeat modes.
- Playback progress bar.
- Current playback time.
- Total track duration.
- Volume control.
- Mute/unmute.
- Queue access.
- Audio output or playback device selector where supported.
- Full-screen player shortcut.
- Lyrics shortcut when lyrics exist.
- Mini-player mode.
- Expandable player details.
- Playback error indication.
- Loading/buffering indication.
- Responsive layout for narrow screens.

## 2.2 Mobile layout

- Bottom navigation for primary destinations.
- Home.
- Search.
- Library.
- Playlists.
- Settings access through a profile/menu entry.
- Compact persistent mini-player.
- Expandable full-screen player.
- Touch-friendly buttons.
- Swipe gestures for supported player actions.
- Mobile-friendly search.
- Responsive grids.
- Bottom-sheet menus.
- Full-screen dialogs where appropriate.
- Safe-area support for device notches.
- Safe-area support for bottom navigation.
- No horizontal overflow.
- Portrait and landscape support.
- Reduced animation option.
- One-handed playback controls.
- Support for mobile browser background playback where available.
- PWA installation.
- Offline application shell.

## 2.3 Global interface behavior

- Preserve playback while navigating between pages.
- Preserve the current queue during ordinary navigation.
- Avoid remounting the audio engine on every page change.
- Display consistent loading indicators.
- Display consistent success and error notifications.
- Support global keyboard shortcuts.
- Use consistent spacing, typography, borders, and iconography.
- Remember sidebar state.
- Remember volume and playback preferences.
- Restore the last open page where appropriate.
- Maintain useful page state when navigating back.
- Use confirmation dialogs for destructive operations.
- Provide undo for reversible destructive actions.
- Show tooltips for icon-only buttons.
- Make every interactive element keyboard-accessible.
- Support browser back and forward navigation.
- Keep the current playback state visible across the application.

---

# 3. HOME PAGE

## 3.1 Welcome section
- Greeting or personalized welcome message.
- Current date or time, optional.
- Recently played content.
- Continue listening.
- Recently added music.
- Quick access to favorite playlists.
- Quick access to favorite albums.
- Quick access to favorite artists.

## 3.2 Home content sections
- Recently played.
- Recently added songs.
- Recently added albums.
- Recently added artists.
- Recently created playlists.
- Continue listening to the last album.
- Continue listening to the last playlist.
- Frequently played songs.
- Most played albums.
- Most played artists.
- Favorites.
- Random album suggestion.
- Random playlist suggestion.
- Unplayed songs.
- Long-unplayed songs.
- Music organized by genre.
- Music organized by folder.
- New music detected during the latest scan.
- Recently completed albums.
- Personal listening statistics.
- Pinned collections.
- Customizable home sections.

## 3.3 Home interactions
- Play a section.
- Shuffle a section.
- Open an album.
- Open an artist.
- Open a playlist.
- Add an item to the queue.
- Play an item next.
- Add an item to a playlist.
- Favorite or unfavorite a track.
- Hide a home section.
- Reorder sections.
- Refresh a section.
- Show all items in a section.
- Display skeleton loading placeholders.
- Display an empty state when the library has no music.

---

# 4. SEARCH PAGE

## 4.1 Search input
- Global search available from every page.
- Dedicated search page.
- Keyboard shortcut to open search.
- Search input autofocus.
- Search-as-you-type.
- Debounced search requests.
- Search suggestions.
- Recent searches.
- Clear search button.
- Escape key to clear or close search.
- Enter key to submit search.
- Search history management.
- Clear search history.
- Search result highlighting.
- Case-insensitive matching.
- Partial title matching.
- Partial artist matching.
- Partial album matching.
- Unicode-aware searching.
- Search for Malayalam and other non-Latin titles.
- Support punctuation and special characters.
- Search without requiring an internet connection.

## 4.2 Search categories
- All.
- Songs.
- Artists.
- Albums.
- Playlists.
- Genres.
- Folders.
- File names.
- Track titles.
- Album titles.
- Artist names.
- Album artists.
- Composer.
- Genre.
- Year.
- Album description, where available.
- Track number.
- File format.
- User-created playlists.

## 4.3 Search filters
- Genre.
- Artist.
- Album.
- Year range.
- Duration range.
- Audio format.
- Favorite status.
- Recently added.
- Recently played.
- Play count.
- Folder or library source.
- Unplayed tracks.
- Tracks with missing metadata.
- Tracks with missing artwork.
- Sort by relevance.
- Sort alphabetically.
- Sort by date added.
- Sort by duration.

## 4.4 Search result interactions
- Play immediately.
- Play next.
- Add to queue.
- Add to playlist.
- Open album.
- Open artist.
- Open containing folder.
- Copy track title.
- Copy file path.
- Favorite/unfavorite.
- Open metadata editor.
- View track details.
- Show result count.
- Clear all filters.
- Display helpful no-results suggestions.

---

# 5. MUSIC LIBRARY

## 5.1 Library overview
- All songs.
- Albums.
- Artists.
- Genres.
- Playlists.
- Folders.
- Favorites.
- Recently added.
- Recently played.
- Most played.
- Listening history.
- Missing metadata.
- Duplicate candidates.
- Library statistics.

## 5.2 Song library view
- Track artwork.
- Track title.
- Artist.
- Album.
- Duration.
- Track number.
- Disc number.
- Genre.
- Release year.
- Date added.
- Play count.
- Last played.
- Favorite indicator.
- Audio format.
- File size, optionally.
- Bitrate, when available.
- Sample rate, when available.
- File location, where useful.
- Selectable rows.
- Multi-select actions.
- Play on row click or an explicit play action.
- Double-click to play, configurable.
- Context menu on each row.
- Drag tracks into playlists.
- Drag tracks to queue.
- Column visibility controls.
- Resizable columns.
- Reorderable columns.
- Compact and comfortable density modes.
- Sticky table header.
- Virtualized rendering for large libraries.
- Pagination or incremental loading where appropriate.

## 5.3 Album library view
- Album artwork.
- Album title.
- Primary artist.
- Album artist.
- Release year.
- Genre.
- Track count.
- Total duration.
- Date added.
- Favorite status.
- Sort by title.
- Sort by artist.
- Sort by year.
- Sort by date added.
- Grid view.
- List view.
- Album grouping.
- Multi-disc album support.
- Compilation album support.
- Albums with unknown artists.
- Albums with missing artwork.

## 5.4 Artist library view
- Artist name.
- Artist image or generated placeholder.
- Number of albums.
- Number of tracks.
- Total listening time.
- Favorite status.
- Recently played tracks.
- Top tracks based on personal play counts.
- Albums grouped by year.
- Singles and standalone tracks.
- Compilation appearances.
- Various Artists support.
- Sort alphabetically.
- Filter by genre.
- Open artist details.
- Play artist tracks.
- Shuffle artist tracks.
- Add artist tracks to a playlist.

## 5.5 Genre library view
- Genre name.
- Number of songs.
- Number of albums.
- Total duration.
- Representative artwork.
- Play all.
- Shuffle genre.
- Filter songs by genre.
- Sort genres alphabetically.
- Handle missing genres.
- Normalize common genre spelling where configurable.

## 5.6 Folder library view
- Browse actual music folders.
- Display directory hierarchy.
- Folder names.
- Folder artwork where available.
- Track count.
- Subfolder count.
- Total size.
- Last scanned time.
- Expand/collapse folder tree.
- Breadcrumb navigation.
- Play folder contents.
- Shuffle folder contents.
- Add folder tracks to a playlist.
- Open the folder's containing path.
- Display inaccessible folders.
- Display offline USB drives.
- Display empty folders.
- Distinguish indexed tracks from unindexed files.
- Never expose unrestricted filesystem browsing outside configured library roots.

## 5.7 Bulk library actions
- Select all visible tracks.
- Select multiple tracks.
- Select a range of tracks.
- Add selected tracks to a playlist.
- Add selected tracks to queue.
- Play selected tracks.
- Shuffle selected tracks.
- Favorite selected tracks.
- Remove selected tracks from a playlist.
- Edit metadata in bulk.
- Change genre in bulk.
- Change album artist in bulk.
- Rescan selected folders.
- Export selected track metadata.
- Remove entries from the index without deleting files.
- Confirmation for destructive operations.
- Progress indicators for long-running tasks.

---

# 6. SONG DETAILS PAGE

## 6.1 Song information
- Large artwork.
- Track title.
- Artist.
- Album artist.
- Album.
- Track number.
- Disc number.
- Release year.
- Genre.
- Duration.
- Composer.
- Lyrics, when available.
- File format.
- File size.
- Bitrate.
- Sample rate.
- Bit depth, where available.
- Channel count.
- File path.
- Date added.
- Last played.
- Play count.
- Favorite status.
- File availability status.

## 6.2 Song actions
- Play.
- Pause.
- Play next.
- Add to queue.
- Add to playlist.
- Favorite/unfavorite.
- Open artist.
- Open album.
- Open containing folder.
- View metadata.
- Edit metadata.
- View lyrics.
- Copy track title.
- Copy file path.
- Share a track link within the private application, if supported.
- Remove from playlist.
- Remove from library index.
- Delete physical file only through a separate, explicit administrative action.

## 6.3 Related content
- Other tracks from the same album.
- More tracks by the same artist.
- Tracks with the same genre.
- Frequently played tracks by the artist.
- Previously played tracks.
- Tracks from the same folder.

---

# 7. ALBUM DETAILS PAGE

## 7.1 Album header
- Album artwork.
- Album title.
- Album artist.
- Release year.
- Genre.
- Total number of tracks.
- Total duration.
- Number of discs.
- Album folder location.
- Date added.
- Favorite album control.
- Album description, if locally available.
- Missing metadata indicator.

## 7.2 Album actions
- Play album.
- Shuffle album.
- Play next.
- Add album to queue.
- Add album to playlist.
- Favorite album.
- Edit album metadata.
- Open album artist.
- View album folder.
- Rescan album folder.
- Export album track list.

## 7.3 Track listing
- Track number.
- Track title.
- Artist.
- Duration.
- Favorite indicator.
- Play state.
- Currently playing indicator.
- Explicit or content advisory tag when available.
- Context menu.
- Reorder tracks for a custom playlist without changing original album ordering.
- Separate discs visually.
- Handle missing tracks and numbering gaps.
- Handle tracks with unknown metadata.

---

# 8. ARTIST DETAILS PAGE

- Artist name.
- Artist artwork.
- Artist biography from local metadata, if available.
- Number of albums.
- Number of tracks.
- Total listening time.
- Favorite artist control.
- Play all tracks.
- Shuffle all tracks.
- Top tracks by personal play count.
- Recently played tracks.
- Album list.
- Albums grouped chronologically.
- Singles and standalone tracks.
- Compilation appearances.
- Tracks featuring the artist, where metadata supports it.
- Genre breakdown.
- Artist listening statistics.
- Add artist tracks to a playlist.
- Open artist folder.
- Edit artist metadata.
- Placeholder artwork for unknown artists.
- Avoid requiring external metadata services.

---

# 9. PLAYLISTS

## 9.1 Playlist management
- Create playlist.
- Edit playlist name.
- Edit playlist description.
- Set playlist artwork.
- Choose artwork from an existing album or track.
- Choose a custom uploaded image.
- Delete playlist.
- Duplicate playlist.
- Rename playlist.
- Pin playlist.
- Favorite playlist.
- Hide playlist from the sidebar.
- Reorder playlists.
- Sort playlists.
- Create folders or groups for playlists.
- Add playlists to groups.
- Move playlists between groups.
- Export playlist.
- Import playlist.
- Playlist creation date.
- Playlist modification date.
- Track count.
- Total duration.
- Empty playlist support.

## 9.2 Playlist playback
- Play playlist.
- Shuffle playlist.
- Play next.
- Add playlist to queue.
- Replace queue with playlist.
- Resume playlist from last position.
- Repeat playlist.
- Continue playback after the last track.
- Display current track position.
- Preserve queue order.
- Respect manually reordered tracks.
- Handle missing or unavailable tracks gracefully.

## 9.3 Playlist editing
- Add one track.
- Add multiple tracks.
- Add album.
- Add artist tracks.
- Add folder contents.
- Remove tracks.
- Reorder tracks with drag and drop.
- Move tracks up or down.
- Sort by title.
- Sort by artist.
- Sort by album.
- Sort by duration.
- Sort by date added.
- Restore custom ordering.
- Remove duplicate entries.
- Select multiple tracks.
- Add a note or description.
- Display track count and total duration.
- Show duplicate tracks without silently removing them.
- Undo recent playlist changes.

## 9.4 Playlist context menus
- Play.
- Shuffle.
- Play next.
- Add to queue.
- Add to another playlist.
- Rename.
- Edit description.
- Change artwork.
- Duplicate.
- Export.
- Pin/unpin.
- Delete.

## 9.5 Playlist types
- Standard manual playlist.
- Favorites playlist.
- Recently played playlist.
- Recently added playlist.
- Most played playlist.
- Unplayed tracks playlist.
- Genre playlist.
- Folder-based playlist.
- Smart playlist.
- Temporary session playlist.

## 9.6 Smart playlists
- Rules based on artist.
- Rules based on album.
- Rules based on genre.
- Rules based on release year.
- Rules based on date added.
- Rules based on play count.
- Rules based on last played.
- Rules based on duration.
- Rules based on favorite status.
- Rules based on audio format.
- Multiple conditions.
- AND/OR rule groups.
- Preview matching tracks.
- Dynamic updates.
- Save smart playlist rules.
- Sort order.
- Maximum track count.
- Shuffle matching tracks.

---

# 10. AUDIO PLAYBACK ENGINE

## 10.1 Essential playback controls
- Play.
- Pause.
- Toggle play/pause.
- Previous track.
- Next track.
- Seek forward.
- Seek backward.
- Seek to a specific timestamp.
- Progress bar.
- Current time display.
- Total duration display.
- Remaining time display, optionally.
- Playback speed.
- Volume.
- Mute/unmute.
- Shuffle on/off.
- Repeat off.
- Repeat all.
- Repeat one.
- Queue management.
- Continuous playback.
- Automatic next-track transition.
- Track-end handling.
- Error recovery.
- Loading state.
- Buffering state.
- Playback state persistence.

## 10.2 Advanced playback behavior
- Gapless playback where browser and format support allow.
- Crossfade between tracks.
- Configurable crossfade duration.
- Fade in at track start.
- Fade out at track end.
- ReplayGain support where practical.
- Per-track volume normalization where supported.
- Playback speed from 0.5× to 2× or a wider configurable range.
- Resume playback after refresh.
- Resume playback after browser restart where feasible.
- Restore last queue.
- Restore shuffle state.
- Restore repeat mode.
- Remember last playback position.
- Resume long tracks from their previous position.
- Avoid counting a track as played immediately on playback start.
- Configurable threshold for registering a play.
- Prevent duplicate play-count increments during retries.
- Skip unavailable tracks.
- Skip corrupted or unsupported files gracefully.
- Show a meaningful error if a format cannot be decoded.
- Keep the application responsive during buffering.
- Prevent accidental queue replacement.
- Avoid unwanted audio autoplay where browser policies prohibit it.
- Handle interrupted playback.

## 10.3 Seek bar details
- Click to seek.
- Drag to seek.
- Keyboard-accessible seeking.
- Current position indicator.
- Buffered region indicator.
- Hover timestamp preview.
- Touch-friendly seek target.
- Accurate duration display.
- Disable seeking when duration is unavailable.
- Handle live or unknown-duration streams if supported.
- Avoid seeking to invalid positions.
- Preserve position during temporary buffering.
- Display loading indicator during seek operations.

## 10.4 Track transition behavior
- Smooth transition between tracks.
- No unnecessary silence between tracks where gapless playback is possible.
- Reset track-specific state when switching songs.
- Update artwork and title together.
- Update queue highlight.
- Update browser media metadata.
- Update play count according to the configured threshold.
- Preserve global volume.
- Respect repeat-one mode.
- Respect repeat-all mode.
- Handle the final queue item.
- Handle removed files in the queue.
- Handle a drive being disconnected during playback.

---

# 11. QUEUE MANAGEMENT

## 11.1 Queue view
- Current track.
- Up next.
- Previously played tracks.
- Queue position.
- Track artwork.
- Track title.
- Artist.
- Duration.
- Currently playing indicator.
- Loading or unavailable status.
- Scrollable track list.
- Drag-and-drop ordering.

## 11.2 Queue actions
- Play track immediately.
- Play next.
- Add to end of queue.
- Remove from queue.
- Clear queue.
- Clear upcoming tracks.
- Move track up or down.
- Move track to top.
- Move track to bottom.
- Reorder multiple tracks.
- Save queue as playlist.
- Export queue.
- Restore previous queue.
- Undo queue removal.
- Skip unavailable tracks.
- Display why a track cannot be played.

## 11.3 Queue behavior
- Distinguish queue from saved playlists.
- Keep the queue persistent during navigation.
- Preserve the current track when editing upcoming tracks.
- Do not silently replace the queue when starting a new playlist unless intended.
- Provide a clear Play Now versus Add to Queue distinction.
- Support temporary tracks added from search.
- Support manual ordering while shuffle is active.
- Show a clear empty-queue state.
- Confirm before clearing a large queue.
- Restore queue after a page refresh where feasible.

---

# 12. SHUFFLE AND REPEAT

- Shuffle on/off.
- Randomized queue order.
- Avoid immediately repeating the same track where possible.
- Preserve the original playlist order.
- Display shuffle state clearly.
- Repeat off.
- Repeat all.
- Repeat one.
- Cycle through repeat modes.
- Display the active repeat mode.
- Keep repeat behavior consistent when switching playlists.
- Define shuffle behavior for a single-track queue.
- Define shuffle behavior when tracks are added during playback.
- Define behavior when a track is removed from the queue.
- Maintain a history of played tracks for Previous Track behavior.

---

# 13. FULL-SCREEN NOW PLAYING VIEW

## 13.1 Main content
- Large square album artwork.
- Track title.
- Artist name.
- Album name.
- Release year, optionally.
- Favorite button.
- Playback progress.
- Current time.
- Total duration.
- Previous.
- Play/pause.
- Next.
- Shuffle.
- Repeat.
- Volume.
- Queue shortcut.
- Lyrics shortcut.
- Track options menu.

## 13.2 Additional panels
- Queue panel.
- Lyrics panel.
- Track information panel.
- Album information panel.
- Audio metadata panel.
- Listening history panel, optionally.
- Visualization panel.
- Related tracks panel.

## 13.3 Visual behavior
- Artwork-driven background treatment, optional.
- Respect the global theme.
- Configurable artwork background blur.
- Reduced-motion mode.
- Smooth panel transitions.
- Responsive layout.
- Mobile portrait support.
- Mobile landscape support.
- Keep playback controls visible.
- Escape to close the expanded player.
- Do not accidentally stop playback when closing the view.

---

# 14. MINI-PLAYER

- Persistent compact player.
- Artwork thumbnail.
- Track title.
- Artist name.
- Play/pause.
- Next track.
- Favorite action.
- Progress indicator.
- Expand player.
- Open queue.
- Close or hide player without stopping playback, if configured.
- Compact mobile layout.
- Horizontal scrolling or truncation for long titles.
- Tooltip for truncated titles.
- Clear buffering indication.
- Clear unavailable-track indication.
- Maintain a large enough touch target on mobile.

---

# 15. AUDIO OUTPUT AND DEVICE SUPPORT

- Playback through the current browser output device.
- Volume control.
- Mute/unmute.
- Respect system volume.
- Display output device information where supported.
- Optional output device selection using browser-supported APIs.
- Detect unsupported device-selection capabilities.
- Bluetooth headphone compatibility through the operating system/browser.
- Wired headphone compatibility.
- Respect audio focus and interruption behavior where supported.
- Recover when an audio output device disconnects.
- Display helpful messages when playback cannot begin.
- Do not promise arbitrary server-side audio routing from a normal browser.
- Keep any future multi-device playback functionality modular.

---

# 16. AUDIO QUALITY AND FORMAT SUPPORT

## 16.1 File format handling
- MP3.
- AAC/M4A.
- Ogg Vorbis.
- Opus.
- FLAC.
- WAV.
- AIFF, where supported.
- WebM audio, where supported.
- Other browser-supported formats.
- Format detection from file metadata and MIME type.
- Graceful handling of unsupported formats.
- Avoid relying solely on file extensions.
- Display format information.
- Validate range requests for streaming.

Actual playback support must be determined by the browser's media decoder. Do not assume every browser supports every format.

## 16.2 Audio metadata
- Codec.
- Container format.
- Duration.
- Bitrate.
- Sample rate.
- Bit depth, when available.
- Channel count.
- File size.
- MIME type.
- Embedded artwork.
- ReplayGain tags, where available.
- VBR/CBR indication, where detectable.
- Missing or malformed metadata indication.

## 16.3 Streaming implementation requirements
- HTTP range requests.
- Correct partial-content responses.
- Correct content length.
- Correct content type.
- Seekable playback.
- Efficient file streaming.
- No unnecessary loading of entire audio files into RAM.
- Efficient handling of large files.
- File handle cleanup.
- Streaming error recovery.
- Authorization on every protected media request.
- Safe path resolution.
- No unrestricted filesystem access.
- Support interrupted network connections.
- Respect browser caching rules.
- Prevent unauthorized access to media URLs.
- Avoid leaking filesystem paths to unauthorized clients.

---

# 17. FAVORITES AND LIKED MUSIC

- Favorite/unfavorite a track.
- Favorite/unfavorite an album.
- Favorite/unfavorite an artist.
- Favorite/unfavorite a playlist.
- Dedicated Favorites page.
- Dedicated Liked Songs playlist or equivalent view.
- Sort favorites by title.
- Sort favorites by artist.
- Sort favorites by date added.
- Sort favorites by last played.
- Search within favorites.
- Filter favorites by genre.
- Play all favorites.
- Shuffle favorites.
- Add favorites to another playlist.
- Remove selected favorites in bulk.
- Favorite action available from search, queue, player, album, artist, and library views.
- Synchronize favorite state across authorized clients.
- Confirm before bulk removal.
- Persist favorites in SQLite.

---

# 18. RECENTLY PLAYED AND LISTENING HISTORY

- Recently played tracks.
- Recently played albums.
- Recently played artists.
- Recently played playlists.
- Listening history timeline.
- Date and time of playback.
- Play count.
- Last played timestamp.
- Session history.
- Filter by date range.
- Search history.
- Clear selected history entries.
- Clear all history.
- Export listening history.
- Play a track from history.
- Add a history track to a playlist.
- Resume from a previous track.
- Prevent duplicate entries caused by short playback retries.
- Configurable history retention.
- Optional privacy mode that does not record history.
- Statistics derived from history.
- Clear distinction between listening history and the current queue.

---

# 19. MUSIC DISCOVERY

Discovery should work primarily from the user's own library.

- Random track.
- Random album.
- Random artist.
- Random playlist.
- Shuffle entire library.
- Discover unplayed music.
- Discover forgotten music.
- Discover tracks from a selected genre.
- Discover albums from a selected year.
- Discover tracks from a selected folder.
- Discover underplayed artists.
- Discover tracks similar by locally available metadata.
- Explore by genre.
- Explore by decade.
- Explore by album artist.
- Explore by file location.
- Exclude favorites from random selections, optionally.
- Exclude recently played tracks, optionally.
- Filter out unavailable files.
- Control discovery randomness.
- Save discovered tracks to a playlist.
- Refresh discovery suggestions.

Do not make external recommendations, online catalogs, or AI services necessary for this page to work.

---

# 20. LYRICS

- Display embedded lyrics when available.
- Display external local `.lrc` lyrics where supported.
- Plain-text lyrics support.
- Timestamped lyrics.
- Synchronized lyric highlighting.
- Scroll lyrics with playback.
- Manual lyric scrolling.
- Automatically follow the current line.
- Toggle synchronized scrolling.
- Seek to a lyric timestamp.
- Display lyrics in the full-screen player.
- Display lyrics on mobile.
- Show an empty state when lyrics are missing.
- Edit lyrics.
- Save edited lyrics only through an explicit operation.
- Support Unicode and multilingual lyrics.
- Handle line breaks correctly.
- Do not require an online lyrics provider.
- Treat online lyrics integrations as optional plugins.

---

# 21. ALBUM ARTWORK AND IMAGES

- Read embedded cover artwork.
- Read supported sidecar artwork files.
- Support common image formats.
- Album artwork thumbnails.
- Artist artwork.
- Playlist artwork.
- Generated placeholders when artwork is missing.
- Consistent placeholder designs.
- Image resizing for thumbnails.
- Lazy-load images.
- Cache generated thumbnails.
- Prevent duplicate thumbnail generation.
- Handle corrupt image files.
- Handle very large embedded artwork.
- Preserve aspect ratio.
- Avoid stretched images.
- Use appropriate image cropping.
- Avoid layout shifts while images load.
- Display fallback artwork after loading errors.
- Allow custom artwork.
- Allow users to reset custom artwork.
- Validate image uploads.
- Limit upload size.
- Avoid executing or serving uploaded files as application code.
- Clean up unused generated thumbnails safely.
- Keep original music files unchanged when updating artwork in the database or application cache.

---

# 22. METADATA MANAGEMENT

## 22.1 Metadata extraction
- Track title.
- Artist.
- Album artist.
- Album.
- Track number.
- Total tracks.
- Disc number.
- Total discs.
- Release year.
- Full release date, when available.
- Genre.
- Composer.
- Conductor, where available.
- Lyrics.
- Embedded artwork.
- Copyright information.
- Publisher or label.
- MusicBrainz identifiers, if present.
- ReplayGain information.
- File duration.
- File size.
- File format.
- Bitrate.
- Sample rate.

## 22.2 Metadata editing
- Edit track title.
- Edit artist.
- Edit album artist.
- Edit album.
- Edit track number.
- Edit disc number.
- Edit release year.
- Edit genre.
- Edit composer.
- Edit lyrics.
- Edit comments.
- Edit selected metadata in bulk.
- Edit album metadata.
- Edit artist display name.
- Preview changes before saving.
- Validate input.
- Show unsaved-change indicators.
- Cancel edits.
- Undo application-level metadata changes when possible.
- Rescan metadata.
- Detect conflicting metadata.
- Handle read-only files.
- Handle disconnected drives.
- Clearly distinguish database-only edits from embedded file-tag edits.
- Make embedded file-tag writing an explicit optional capability.
- Never silently overwrite original tags.

## 22.3 Metadata cleanup
- Find tracks with missing titles.
- Find unknown artists.
- Find missing albums.
- Find missing artwork.
- Find missing genres.
- Find inconsistent artist spelling.
- Find duplicate candidates.
- Find malformed track numbers.
- Find inconsistent album artist fields.
- Bulk-edit selected records.
- Preview normalization before applying it.
- Never automatically merge distinct artists or albums without appropriate confirmation.

---

# 23. LIBRARY SCANNING AND FILE INDEXING

## 23.1 Library source management
- Add library root.
- Remove library root.
- Edit library root settings.
- Support multiple library roots.
- Support external USB drives and pendrives.
- Configure `REWIND_LIBRARY_ROOTS`.
- Display configured roots.
- Display mount availability.
- Display permissions status.
- Detect inaccessible roots.
- Prevent directory traversal outside configured roots.
- Handle symbolic links according to a configurable safe policy.
- Detect duplicate physical paths across library roots.
- Provide a dry-run scan option.

## 23.2 Scanning behavior
- Scan folders recursively.
- Detect supported audio files.
- Extract metadata.
- Extract embedded artwork.
- Generate or update thumbnails.
- Record file size.
- Record modification time.
- Record stable file identity where available.
- Detect added files.
- Detect changed files.
- Detect renamed or moved files where safely identifiable.
- Detect deleted or missing files.
- Detect inaccessible files.
- Detect unsupported files.
- Avoid unnecessary rescanning of unchanged files.
- Provide manual full scan.
- Provide incremental scan.
- Provide scan on startup, configurable.
- Provide scheduled scans, configurable.
- Provide filesystem watching where reliable.
- Fall back to manual or periodic scanning when filesystem events are unavailable.
- Handle USB disconnection during a scan.
- Handle interrupted scans.
- Resume or restart safely.
- Avoid blocking playback during scans.

## 23.3 Scan progress UI
- Overall progress.
- Current directory.
- Current file.
- Files discovered.
- Files processed.
- Files skipped.
- Files failed.
- Newly added tracks.
- Updated tracks.
- Missing tracks.
- Elapsed time.
- Estimated remaining time when reliable.
- Pause scanning, if supported.
- Cancel scanning.
- Retry failed files.
- View scan logs.
- Show last successful scan time.
- Show current scan status.
- Prevent accidental duplicate scans.
- Display meaningful completion summaries.

## 23.4 File deletion and removal rules
- Removing a library root must not delete its files.
- Removing an index entry must not delete the physical file.
- Missing files must be marked unavailable instead of immediately erasing user data.
- Provide a configurable grace period before purging missing entries.
- Explicitly distinguish Remove from Library and Delete File.
- Require confirmation before deleting physical files.
- Check whether the file is referenced by playlists or history.
- Explain the effect of deletion on playlists.
- Avoid deleting files outside configured roots.
- Record administrative actions where appropriate.

---

# 24. USB DRIVE AND PENDRIVE MANAGEMENT

- Detect available library paths.
- Display drive availability.
- Display mount path.
- Display indexed track count per drive.
- Display scan status.
- Show offline drive indicator.
- Handle drive removal gracefully.
- Handle drive reconnection.
- Detect files that have become unavailable.
- Preserve playlist references while the drive is offline.
- Preserve track metadata while offline.
- Resume availability after the drive reconnects.
- Rescan the drive when configured.
- Avoid rescanning unchanged files unnecessarily.
- Warn before removing an active library root.
- Warn if the current track is playing from a drive that disconnects.
- Recover gracefully if a drive is remounted at a different path.
- Provide instructions for mounting external drives on the server.
- Avoid automatically mounting drives with elevated privileges unless explicitly configured outside the application.
- Show storage and permission errors clearly.

---

# 25. PERSONAL LISTENING STATISTICS

## 25.1 Overview statistics
- Total tracks.
- Total albums.
- Total artists.
- Total playlists.
- Total genres.
- Total library duration.
- Total indexed music size.
- Total favorites.
- Total listening time.
- Total plays.
- Most played track.
- Most played artist.
- Most played album.
- Most played genre.
- Recently added tracks.
- Unplayed track count.

## 25.2 Time-based statistics
- Listening time today.
- Listening time this week.
- Listening time this month.
- Listening time this year.
- Plays per day.
- Plays per week.
- Plays per month.
- Most active listening day.
- Most active listening hour.
- Longest listening session.
- Average session duration.

## 25.3 Detailed statistics
- Top 10 tracks.
- Top 50 tracks.
- Top artists.
- Top albums.
- Top genres.
- Listening history chart.
- Monthly listening trends.
- Genre distribution.
- Library format distribution.
- Listening streak, optionally.
- Personal year-in-review summary.
- Filter by date range.
- Export statistics.
- Exclude private sessions from statistics.
- Clearly distinguish estimated and measured statistics.

---

# 26. THEMES AND APPEARANCE

## 26.1 Built-in themes
- Default Rewind dark neo-brutalist theme.
- Dark minimal theme.
- Light theme.
- High-contrast theme.
- OLED-friendly dark theme.
- Additional themes supplied through plugins.

## 26.2 Theme customization
- Theme selector.
- Background color.
- Surface color.
- Primary accent color.
- Secondary accent color.
- Text color.
- Muted text color.
- Border color.
- Button color.
- Hover color.
- Focus-ring color.
- Error color.
- Success color.
- Warning color.
- Font family.
- Font size.
- Font weight.
- Border thickness.
- Border radius.
- Shadow intensity.
- Spacing density.
- Artwork corner style.
- Player layout.
- Sidebar width.
- Grid card size.
- Table row height.
- Animation intensity.
- Reduced-motion setting.
- Custom CSS only if explicitly enabled and safely isolated.
- Theme import.
- Theme export.
- Theme preview.
- Reset theme to defaults.
- Per-device theme preferences, optionally.
- System theme preference.
- Persistent user-selected theme.
- Theme compatibility validation.

## 26.3 Neo-brutalist design details
- Strong, readable typography.
- High-contrast surfaces.
- Clear borders.
- Distinctive but restrained shadows.
- Bold active states.
- Clear visual hierarchy.
- Minimal unnecessary gradients.
- Consistent spacing scale.
- Strong focus indicators.
- Limited decorative animation.
- Clear button hierarchy.
- Predictable placement of controls.
- Avoid excessive card nesting.
- Avoid cluttering the player with secondary controls.
- Preserve usability on small screens.

---

# 27. PLUGIN SYSTEM

## 27.1 Plugin management UI
- Plugin marketplace or local plugin directory browser.
- Installed plugins page.
- Available plugins list.
- Plugin name.
- Description.
- Author.
- Version.
- Compatibility information.
- Enabled/disabled state.
- Plugin permissions.
- Installation source.
- Installation date.
- Enable plugin.
- Disable plugin.
- Uninstall plugin.
- Update plugin.
- View plugin settings.
- View plugin logs.
- Restart-required indicator.
- Plugin dependency validation.
- Compatibility error messages.
- Failed initialization indicator.
- Plugin version history where available.
- Confirm actions that affect application behavior.
- Preserve plugin settings when appropriate during updates.

## 27.2 Plugin capabilities
- Theme plugins.
- Player visualization plugins.
- Additional metadata readers.
- Additional audio integrations.
- Optional lyrics providers.
- Optional metadata providers.
- Additional file format handlers where technically possible.
- Statistics extensions.
- Custom home sections.
- Import/export formats.
- Notification integrations.
- External device integrations.
- Additional keyboard shortcuts.
- Custom player layouts.
- Additional library filters.
- Custom widgets.

## 27.3 Plugin security
- Define a stable plugin API.
- Version the plugin API.
- Validate plugin manifests.
- Validate plugin permissions.
- Restrict filesystem access.
- Restrict network access.
- Avoid executing untrusted plugins with unrestricted server privileges.
- Require explicit administrator approval for privileged capabilities.
- Isolate plugin settings.
- Log plugin errors.
- Handle plugin crashes without crashing core playback.
- Provide a safe disable mechanism.
- Verify plugin package integrity where supported.
- Warn about unsigned or untrusted plugins.
- Ensure a plugin cannot bypass user authentication.
- Ensure plugins cannot access arbitrary secrets by default.

---

# 28. PWA AND MOBILE EXPERIENCE

## 28.1 Installation
- Web app manifest.
- Application name.
- Short name.
- Application icons.
- Theme color.
- Background color.
- Standalone display mode.
- Install prompt where supported.
- Manual installation instructions.
- Splash screen support where available.
- Proper mobile home-screen icon.
- Responsive layout.
- Install status detection where supported.

## 28.2 Offline capabilities
- Cache application shell.
- Cache essential static assets.
- Cache theme assets.
- Show offline status.
- Explain that the server cannot be reached.
- Retry server connection.
- Preserve non-sensitive interface preferences.
- Avoid caching protected audio indiscriminately.
- Avoid assuming the full music library is available offline.
- Optional offline track downloads only when deliberately implemented with storage limits and authorization.
- Display offline availability per track where supported.
- Handle storage quota errors.
- Provide a cache management interface.
- Clear application cache.
- Never confuse cached UI availability with actual server connectivity.

## 28.3 Mobile playback
- Persistent mini-player.
- Full-screen now-playing view.
- Touch-friendly controls.
- Mobile queue drawer.
- Mobile search.
- Gesture-based seeking, where implemented.
- Prevent accidental page navigation during seeking.
- Orientation-aware layout.
- Keep controls usable on narrow displays.
- Handle screen locking and background playback according to browser limitations.
- Display the current track in system media controls where supported.
- Recover after returning from the background.
- Handle interruptions and network changes.

---

# 29. BROWSER MEDIA INTEGRATION

- Media Session API integration where supported.
- Browser media notification artwork.
- Track title in system media controls.
- Artist name in system media controls.
- Album name in system media controls.
- Play/pause action handling.
- Next-track action handling.
- Previous-track action handling.
- Seek action handling where supported.
- Update metadata on track changes.
- Update playback position where supported.
- Respond to supported hardware media keys.
- Handle browser tab suspension.
- Display a favicon or title indicator for playback where useful.
- Avoid claiming universal background playback support.
- Detect unsupported browser APIs.
- Provide fallback controls inside the application.

---

# 30. KEYBOARD SHORTCUTS

## Playback shortcuts
- Space: Play/pause when focus is not inside a text-entry control.
- Left arrow: Seek backward when appropriate.
- Right arrow: Seek forward when appropriate.
- Previous-track shortcut.
- Next-track shortcut.
- Volume up.
- Volume down.
- M: Mute/unmute.
- S: Toggle shuffle.
- R: Cycle repeat mode.
- F: Toggle favorite, where unambiguous.

## Navigation shortcuts
- Search shortcut.
- Home shortcut.
- Library shortcut.
- Queue shortcut.
- Toggle sidebar.
- Open now-playing view.
- Escape to close menus and dialogs.
- Browser-standard navigation should remain intact.

## Shortcut requirements
- Dedicated keyboard shortcuts settings page.
- View all shortcuts.
- Customize shortcuts where practical.
- Detect shortcut conflicts.
- Display shortcut hints in tooltips.
- Disable conflicting shortcuts while typing.
- Support keyboard-only navigation.
- Avoid overriding essential browser shortcuts.
- Provide alternatives for users without physical keyboards.

---

# 31. CONTEXT MENUS AND QUICK ACTIONS

Implement consistent context menus for tracks, albums, artists, playlists, folders, and queue items.

## Common actions
- Play now.
- Play next.
- Add to queue.
- Add to playlist.
- Favorite/unfavorite.
- View details.
- Open containing album.
- Open artist.
- Open containing folder.
- Copy title.
- Copy path.
- Edit metadata.
- Export metadata.
- Remove from playlist.
- Remove from library index.

## Context menu UX
- Right-click support on desktop.
- Long-press or overflow menu on touch devices.
- Keyboard access through the context-menu key or Shift+F10 where appropriate.
- Position menus within the viewport.
- Close on Escape.
- Close on outside click.
- Prevent menus from extending beyond screen edges.
- Display disabled actions with an explanation where useful.
- Group related actions.
- Separate destructive actions visually.
- Require confirmation for irreversible actions.
- Use consistent icons and labels.

---

# 32. NOTIFICATIONS AND FEEDBACK

- Track changed notification only when useful.
- Scan started.
- Scan completed.
- Scan failed.
- New tracks discovered.
- Playlist created.
- Playlist updated.
- Playlist deleted.
- Track added to playlist.
- Track removed from playlist.
- Metadata saved.
- Metadata save failed.
- Favorite added or removed.
- Plugin installed.
- Plugin failed.
- Server disconnected.
- Server reconnected.
- Authentication expired.
- Session ended.
- File unavailable.
- Unsupported format.
- Playback failed.
- Storage almost full, when detectable.
- Background task completed.

## Notification behavior
- Success notifications.
- Error notifications.
- Warning notifications.
- Informational notifications.
- Auto-dismiss noncritical messages.
- Persistent messages for important failures.
- Dismiss button.
- Avoid duplicate notifications.
- Avoid notifications covering playback controls.
- Screen-reader announcements.
- Action buttons when useful.
- Optional notification history.
- Do not expose sensitive file paths unnecessarily.

---

# 33. SETTINGS — GENERAL

- Application name display.
- Language selection, if translations are implemented.
- Default landing page.
- Theme selection.
- Appearance settings.
- Default library view.
- Default sort order.
- Sidebar behavior.
- Default playback behavior.
- Default shuffle behavior.
- Default repeat behavior.
- Default volume.
- Playback speed.
- Crossfade settings.
- Audio normalization settings.
- Keyboard shortcuts.
- Date and time display.
- Duration format.
- Confirmation dialog preferences.
- Notification preferences.
- Reduced-motion setting.
- Accessibility settings.
- PWA installation guidance.
- Reset preferences.
- Export settings.
- Import settings.
- Version information.
- Application update information.

---

# 34. SETTINGS — LIBRARY

- View configured library roots.
- Add library root.
- Remove library root.
- Edit root settings.
- Scan on startup.
- Periodic scanning interval.
- Filesystem watching configuration.
- Supported file extensions.
- Metadata extraction settings.
- Artwork extraction settings.
- Thumbnail cache size.
- Missing-file retention policy.
- Symbolic-link policy.
- Ignore hidden files.
- Ignore selected folders.
- Ignore selected patterns.
- Follow or exclude nested folders.
- Rescan unchanged files, if needed.
- Scan concurrency.
- Maximum file size for artwork processing.
- Scan logs.
- Last successful scan.
- Current scan progress.
- Retry failed files.
- Clear derived caches.
- Rebuild library index.
- Library integrity check.
- Duplicate candidate detection.
- Import/export library metadata.

---

# 35. SETTINGS — PLAYER

- Default volume.
- Remember volume.
- Default playback speed.
- Remember playback speed.
- Default shuffle state.
- Default repeat mode.
- Crossfade enable/disable.
- Crossfade duration.
- Gapless playback preference.
- Audio normalization preference.
- ReplayGain preference, if supported.
- Resume playback after restart.
- Resume position for long tracks.
- Play-count threshold.
- Skip unavailable tracks.
- Auto-advance.
- Queue persistence.
- Previous-track behavior.
- Keyboard shortcuts.
- System media controls.
- Player animation intensity.
- Mini-player behavior.
- Full-screen player behavior.
- Playback error diagnostics.

---

# 36. SETTINGS — SERVER AND STORAGE

- Server hostname.
- Server port.
- Application base URL.
- Listening interface.
- Local network access status.
- Tailscale access instructions.
- Server health.
- CPU usage, where available.
- Memory usage, where available.
- Disk usage, where available.
- Library storage locations.
- Database location.
- Thumbnail cache location.
- Log location.
- Available disk space.
- Active client sessions.
- Background scan tasks.
- Server uptime.
- Application version.
- Restart instructions.
- Graceful shutdown status.
- Configuration validation.
- Read-only configuration indicators.
- Storage permissions.
- Diagnostic report generation.
- Avoid exposing privileged system controls to ordinary users.

---

# 37. SETTINGS — PRIVACY AND SECURITY

- Authentication status.
- Change password.
- Session list.
- Revoke other sessions.
- Sign out.
- Sign out all sessions.
- Session expiration settings.
- Listening history controls.
- Clear listening history.
- Clear search history.
- Privacy mode.
- Authorized client information.
- Login history, where appropriate.
- Failed login monitoring.
- Security event logs.
- Secure cookie configuration.
- CSRF protection where applicable.
- Rate limiting.
- Brute-force protection.
- Secure password hashing using Argon2id.
- Secure password reset or recovery procedures.
- No default production password.
- No plaintext password storage.
- No secret exposure in frontend bundles.
- No unrestricted filesystem browsing.
- No unauthenticated protected media access.
- HTTPS for access beyond a trusted local network where appropriate.
- Tailscale access controls.
- Clear session revocation behavior.
- Explicit administrator role.
- Least-privilege access for background services.

---

# 38. AUTHENTICATION AND USER MANAGEMENT

For a primarily personal installation, keep user management simple while supporting future multi-user use.

- First-run administrator creation.
- Secure setup wizard.
- Login page.
- Logout.
- Change password.
- Session expiration.
- Remember session option, securely implemented.
- Password visibility toggle.
- Password validation.
- Login rate limiting.
- Invalid credentials message.
- Generic authentication error messages that do not leak account details.
- Session revocation.
- Administrator role.
- Optional additional user accounts.
- Optional read-only user role.
- Optional library-management role.
- Optional per-user favorites.
- Optional per-user playlists.
- Optional per-user listening history.
- Per-user settings.
- Server-wide settings restricted to administrators.
- Safe recovery if the administrator loses access.
- Do not expose account creation publicly unless explicitly enabled.

---

# 39. FIRST-RUN SETUP WIZARD

- Welcome screen.
- Application overview.
- Server environment check.
- Go/runtime version or build information.
- Writable-directory check.
- Database initialization.
- Administrator account creation.
- Password validation.
- Library root configuration.
- Multiple root support.
- Music directory permission check.
- Optional USB library configuration.
- Network binding configuration.
- Tailscale deployment guidance.
- Theme selection.
- Initial library scan.
- Scan progress.
- Initial scan summary.
- Playback verification.
- Setup completion screen.
- Link to Home.
- Link to Settings.
- Setup status persistence.
- Safe interruption and resume.
- No repeated setup wizard after successful configuration unless an administrator explicitly resets it.
- Clear diagnostics when setup fails.
- Never print passwords or secrets to logs.

---

# 40. SERVER HEALTH AND ADMINISTRATION

- Server status indicator.
- Database connectivity check.
- Library root availability.
- Background task status.
- Scan status.
- Active sessions.
- Application uptime.
- Version display.
- Build information.
- Health endpoint.
- Readiness endpoint.
- Liveness endpoint.
- Structured application logs.
- Log level configuration.
- Log filtering.
- Log rotation.
- Error diagnostics.
- SQLite integrity checks.
- SQLite backup status.
- Database migration status.
- Cache cleanup.
- Graceful shutdown.
- Restart guidance.
- Update guidance.
- Resource usage monitoring where practical.
- Storage warnings.
- Backup and restore guidance.
- Diagnostic export with secrets and sensitive paths redacted.

---

# 41. TAILSCALE AND REMOTE ACCESS

- Local network access.
- Remote access through the configured Tailscale network.
- Clear connection status.
- Display the configured application address.
- Setup instructions for connecting devices.
- Explain that the client device must have authorized Tailscale connectivity.
- Authentication required for protected content.
- Do not expose the service to the public internet by default.
- Configurable listening interface.
- Configurable port.
- Reverse-proxy compatibility.
- Secure forwarded-header handling.
- Respect trusted-proxy configuration.
- Validate allowed hostnames.
- Protect against DNS rebinding where applicable.
- Correct handling of local and remote URLs.
- Session security over remote connections.
- Handle changing client network addresses.
- Reconnect after temporary connectivity loss.
- Show helpful connection errors.
- Do not assume a successful ping means the web application is reachable.
- Do not expose unrestricted server administration over remote access.
- Keep Tailscale setup optional for local-only deployments.

---

# 42. BACKUP AND RESTORE

- Database backup.
- Playlist backup.
- Favorites backup.
- Listening history backup.
- User settings backup.
- Theme configuration backup.
- Plugin settings backup.
- Library configuration backup.
- Scheduled backup configuration.
- Manual backup.
- Backup destination selection.
- Backup status.
- Backup timestamp.
- Backup size.
- Restore from backup.
- Restore validation.
- Database consistency checks.
- Backup version identification.
- Migration compatibility checks.
- Restore confirmation.
- Backup failure notifications.
- Recovery instructions.
- Avoid overwriting current data without confirmation.
- Document which data is backed up and which files remain in the original music directories.
- Do not include secrets in exported backups unless explicitly required and securely protected.

---

# 43. SQLITE AND DATA MANAGEMENT

- SQLite initialization.
- Versioned schema migrations.
- Foreign-key enforcement.
- Database indexes for common queries.
- Transactions for multi-step writes.
- Busy timeout.
- Safe concurrency settings.
- Connection pooling appropriate for SQLite.
- WAL mode only if explicitly selected after considering storage and deployment constraints.
- Database integrity checks.
- Consistent timestamps.
- UTC storage for timestamps.
- Localized timestamp display.
- Database backup.
- Database restore.
- Migration failure handling.
- Rollback or recovery procedures.
- Prepared statements.
- Parameterized queries.
- Avoid SQL injection.
- Avoid excessive full-table scans.
- Database growth monitoring.
- Safe handling of interrupted writes.
- Avoid storing duplicate full-size artwork when thumbnails or references are sufficient.
- Preserve playlist ordering.
- Preserve queue state where configured.
- Preserve references to temporarily unavailable tracks.

---

# 44. ACCESSIBILITY

- Keyboard navigation.
- Visible focus indicators.
- Screen-reader labels.
- Semantic page headings.
- Semantic buttons.
- Accessible dialogs.
- Accessible dropdown menus.
- Accessible context menus.
- Accessible table navigation.
- Accessible progress bars.
- Accessible volume slider.
- Accessible seek slider.
- Accessible play/pause state.
- Accessible loading announcements.
- Accessible error announcements.
- Sufficient color contrast.
- Do not communicate state using color alone.
- Reduced-motion support.
- Scalable text.
- Browser zoom support.
- Large touch targets.
- Accessible icon-only buttons.
- Descriptive tooltips.
- Logical focus order.
- Focus trapping in modal dialogs.
- Focus restoration after dialogs close.
- Captions or text alternatives for relevant visualizations.
- Avoid flashing animations.
- Do not steal focus during playback updates.

---

# 45. EMPTY, LOADING, ERROR, AND OFFLINE STATES

Design these states for every page and major component.

## Empty states
- No music indexed.
- No search results.
- No albums.
- No artists.
- No playlists.
- Empty queue.
- No favorites.
- No listening history.
- No lyrics.
- No artwork.
- No available library roots.
- No matching smart playlist tracks.
- No plugins installed.
- No scan history.

Each empty state should explain the situation and offer an appropriate action.

## Loading states
- Page skeleton.
- Artwork skeleton.
- Track-list skeleton.
- Search loading.
- Library loading.
- Playlist loading.
- Queue loading.
- Initial scan progress.
- Metadata extraction progress.
- Plugin installation progress.
- Backup progress.
- Restore progress.
- Avoid indefinite spinners without an explanation or recovery path.

## Error states
- Server unavailable.
- Database unavailable.
- Authentication required.
- Permission denied.
- Missing file.
- Corrupt audio file.
- Unsupported audio format.
- Streaming interrupted.
- Seek failed.
- Metadata extraction failed.
- Metadata write failed.
- Artwork extraction failed.
- USB disconnected.
- Disk full.
- Library root inaccessible.
- Scan interrupted.
- Plugin failed.
- Backup failed.
- Restore failed.
- Network timeout.
- Invalid configuration.

Each error should include a human-readable explanation, a safe retry option where possible, and technical details in diagnostics where useful.

## Offline states
- Server unreachable.
- Network disconnected.
- Reconnecting.
- Last successful connection time.
- Retry connection.
- Explain whether playback can continue for the current buffered track.
- Explain whether new tracks require a server connection.
- Avoid presenting stale data as current.

---

# 46. PERFORMANCE REQUIREMENTS

- Fast initial application load.
- Lightweight production bundle.
- Code splitting by page.
- Lazy loading of nonessential views.
- Lazy loading of artwork.
- Image thumbnail caching.
- Virtualized large song lists.
- Debounced search.
- Indexed search queries.
- Efficient pagination or incremental fetching.
- Avoid unnecessary database queries.
- Avoid unnecessary API requests.
- Avoid unnecessary component re-renders.
- Efficient audio streaming.
- Never load entire large audio files into application memory unnecessarily.
- Avoid blocking the main thread.
- Process metadata extraction asynchronously.
- Limit scan concurrency.
- Avoid regenerating thumbnails unnecessarily.
- Cancel stale search requests where possible.
- Keep playback independent of page rendering.
- Keep playback working while a library scan runs.
- Efficient server startup.
- Graceful behavior on low-memory servers.
- Efficient cache invalidation.
- Monitor large-library performance.
- Handle thousands of tracks.
- Design for tens of thousands of tracks where practical.
- Use measurable performance targets.
- Test on low-end mobile devices.
- Test on slow local networks.
- Test large FLAC files and long audio files.
- Avoid unnecessary background network traffic.

---

# 47. SECURITY REQUIREMENTS

- Authentication for protected resources.
- Authorization on every protected API endpoint.
- Authorization on every protected media request.
- Password hashing using Argon2id.
- Rate limiting for authentication.
- CSRF protection where relevant.
- Strict request validation.
- Safe file path resolution.
- Prevent directory traversal.
- Prevent arbitrary filesystem access.
- Prevent arbitrary file deletion.
- Restrict media serving to configured roots.
- Prevent SQL injection.
- Escape untrusted metadata before rendering.
- Sanitize uploaded filenames.
- Validate uploaded artwork.
- Set safe response headers.
- Configure Content Security Policy appropriately.
- Use secure cookies.
- Configure allowed origins.
- Avoid wildcard credentialed CORS.
- Validate host headers.
- Avoid exposing stack traces to clients.
- Avoid logging passwords, session tokens, or secrets.
- Keep secrets out of frontend bundles.
- Restrict administrative operations.
- Secure plugin execution.
- Validate configuration files.
- Provide safe production defaults.
- Avoid unauthenticated public registration.
- Use secure transport for remote access.
- Audit security-sensitive operations.
- Ensure error messages do not expose sensitive filesystem details to unauthorized users.

---

# 48. API AND BACKEND FUNCTIONALITY

Implement a versioned API with clear request and response contracts.

## Authentication API
- First-run setup status.
- Initial administrator creation.
- Login.
- Logout.
- Current session.
- Change password.
- Session listing.
- Session revocation.

## Library API
- List tracks.
- Get track details.
- List albums.
- Get album details.
- List artists.
- Get artist details.
- List genres.
- List folders.
- Browse permitted library directories.
- Search library.
- Filter and sort results.
- Paginate results.
- Retrieve library statistics.
- Retrieve missing-file status.

## Playback API
- Stream a track.
- Support HTTP range requests.
- Return correct media headers.
- Validate track identifiers.
- Check access permissions.
- Return meaningful unavailable-file errors.

## Playlist API
- Create playlist.
- List playlists.
- Get playlist.
- Update playlist.
- Delete playlist.
- Add tracks.
- Remove tracks.
- Reorder tracks.
- Duplicate playlist.
- Export playlist.
- Import playlist.
- Manage smart playlist rules.

## Favorites API
- Add favorite.
- Remove favorite.
- List favorites.
- Bulk favorite updates.

## History API
- Record qualifying play events.
- Retrieve history.
- Retrieve play counts.
- Retrieve listening statistics.
- Clear history.
- Export history.

## Metadata API
- Read metadata.
- Update metadata.
- Bulk update metadata.
- Trigger rescan.
- Retrieve metadata extraction status.
- Validate metadata changes.

## Scanner API
- Start scan.
- Retrieve scan status.
- Cancel scan.
- Retrieve scan history.
- Retry failed files.
- Retrieve scan errors.
- Retrieve configured roots.

## Settings API
- Get settings.
- Update settings.
- Validate settings.
- Import settings.
- Export settings.

## Plugin API
- List plugins.
- Retrieve plugin information.
- Enable plugin.
- Disable plugin.
- Configure plugin.
- Install and uninstall plugins only through authorized administrative operations.
- Retrieve plugin status and logs.

## Administration API
- Health.
- Readiness.
- Version.
- Resource diagnostics.
- Database integrity status.
- Backup creation.
- Backup listing.
- Restore validation.
- Restore execution.
- Server configuration status.

## API requirements
- Consistent error format.
- Correct HTTP status codes.
- Request validation.
- Response validation where appropriate.
- Pagination.
- Sorting.
- Filtering.
- Request size limits.
- Authentication middleware.
- Authorization middleware.
- Rate limiting.
- Structured logging.
- Versioning.
- Backward-compatible changes where practical.
- API documentation.
- Automated API tests.

---

# 49. DATA MODEL REQUIREMENTS

Design a relational schema covering the following entities.

- Users.
- Sessions.
- Library roots.
- Tracks.
- Artists.
- Albums.
- Genres.
- Track-artist relationships.
- Album-artist relationships.
- Track metadata.
- Artwork references.
- Playlists.
- Playlist tracks.
- Smart playlist rules.
- Favorites.
- Listening history.
- Play counts.
- Playback sessions.
- Persisted queue state.
- User settings.
- Application settings.
- Theme configurations.
- Plugin registry.
- Plugin settings.
- Scan jobs.
- Scan results.
- File availability status.
- Background task status.
- Audit events.
- Schema migration version.

Data model requirements:
- Stable internal IDs.
- Unique constraints where appropriate.
- Foreign-key relationships.
- Efficient lookup indexes.
- Safe deletion rules.
- Track ordering within playlists.
- Support for multiple artists per track.
- Support for multiple discs per album.
- Support for compilation albums.
- Support for missing files.
- Support for duplicate metadata.
- Avoid identifying tracks exclusively by their file paths.
- Preserve history when file paths change where possible.
- Distinguish a missing file from a deleted database record.
- Support multiple users without requiring multi-user complexity in the initial UI.

---

# 50. RESPONSIVE DESIGN BREAKPOINTS

Define consistent responsive behavior for:

- Small mobile phones.
- Large mobile phones.
- Tablets.
- Small laptops.
- Desktop monitors.
- Large desktop monitors.

Requirements:
- Sidebar collapses on narrow screens.
- Bottom navigation appears on mobile.
- Bottom player becomes compact.
- Full player fills the available mobile viewport.
- Tables adapt to narrow widths.
- Essential track actions remain accessible.
- Artwork scales proportionally.
- Text truncation is intentional.
- Tooltips are not required to understand essential controls.
- Dialogs fit within available viewport height.
- Menus remain within viewport boundaries.
- Safe-area insets are respected.
- Touch targets remain usable.
- No critical action depends exclusively on hover.
- No horizontal overflow on any core page.

---

# 51. VISUAL COMPONENT LIBRARY

Create reusable components for:

- Application shell.
- Sidebar.
- Mobile navigation.
- Top navigation.
- Search bar.
- Global command/search overlay.
- Mini-player.
- Full-screen player.
- Playback controls.
- Seek bar.
- Volume slider.
- Track row.
- Track table.
- Track card.
- Album card.
- Artist card.
- Playlist card.
- Folder row.
- Artwork image.
- Artwork placeholder.
- Queue panel.
- Lyrics panel.
- Context menu.
- Dropdown menu.
- Modal dialog.
- Confirmation dialog.
- Bottom sheet.
- Tooltip.
- Toast notification.
- Loading skeleton.
- Empty state.
- Error state.
- Offline banner.
- Progress indicator.
- Filter chips.
- Sort menu.
- Pagination controls.
- Tabs.
- Breadcrumbs.
- Metadata editor.
- Multi-select toolbar.
- Settings section.
- Theme editor.
- Plugin card.
- Scan progress panel.
- Server health panel.
- Statistics chart.
- Accessible form fields.
- Toggle switches.
- Checkboxes.
- Radio groups.
- Segmented controls.

All components should support consistent states:
- Default.
- Hover.
- Focus.
- Active.
- Selected.
- Disabled.
- Loading.
- Error.
- Success, where relevant.

---

# 52. MICRO-INTERACTIONS

- Hover state on buttons.
- Pressed state on buttons.
- Focus ring on keyboard navigation.
- Tooltip delay.
- Button loading indicators.
- Favorite icon animation.
- Play/pause icon transition.
- Queue insertion feedback.
- Playlist creation feedback.
- Drag-and-drop insertion indicator.
- Track reorder animation.
- Sidebar collapse transition.
- Modal open/close transition.
- Bottom-sheet transition.
- Artwork loading fade.
- Progress bar updates.
- Volume slider feedback.
- Search suggestion appearance.
- Search result highlight.
- Copy-to-clipboard confirmation.
- Scan progress updates.
- Background task completion feedback.
- Connection status transitions.
- Disabled control explanation.
- Unsaved-change indicator.
- Undo notification.
- Clear feedback after saving settings.
- Respect reduced-motion preferences.
- Avoid excessive animation during continuous playback updates.

---

# 53. DATA IMPORT AND EXPORT

## Import
- M3U playlist import.
- M3U8 playlist import.
- Supported playlist path resolution.
- Playlist import preview.
- Unresolved track report.
- Duplicate handling options.
- Metadata import from supported formats.
- Settings import.
- Theme import.
- Backup restore.
- Validate imported content.
- Avoid executing imported content.
- Reject invalid paths outside permitted roots where applicable.

## Export
- M3U playlist export.
- M3U8 playlist export.
- Track metadata export as CSV or JSON.
- Listening history export.
- Statistics export.
- Favorites export.
- Playlist export.
- Settings export.
- Theme export.
- Library inventory export.
- Database backup.
- Explain whether exported playlists use absolute paths, relative paths, or application identifiers.
- Avoid exporting passwords, session tokens, or secrets.

---

# 54. OPTIONAL ADVANCED FEATURES

Implement only after the core player is stable.

- Audio spectrum visualization.
- Waveform display.
- Configurable equalizer, where technically supported.
- ReplayGain integration.
- Audio normalization.
- Crossfade.
- Gapless playback.
- Smart playlists.
- Album mood tags.
- Custom tags.
- Personal ratings.
- Five-star track ratings.
- Track notes.
- Album notes.
- Artist notes.
- Listening sessions.
- Music discovery filters.
- Advanced duplicate detection.
- Track similarity based on local metadata.
- Folder watch notifications.
- Per-track playback speed.
- Audio bookmarks.
- Sleep timer.
- Scheduled playback.
- Configurable shutdown behavior for dedicated playback environments.
- Local network remote-control interface.
- Optional companion devices.
- Optional plugin-provided integrations.
- Optional local recommendation engine.

## Sleep timer
- Preset durations.
- Custom duration.
- Stop playback at timer expiration.
- Fade out before stopping, if supported.
- Cancel timer.
- Display remaining time.
- Restore timer state only if explicitly desired.

## Personal ratings
- Five-star rating.
- Optional thumbs up/down.
- Filter by rating.
- Sort by rating.
- Smart playlists based on rating.
- Persistent ratings across sessions.

---

# 55. TESTING REQUIREMENTS

## Playback tests
- Play and pause.
- Next and previous.
- Seek.
- Volume.
- Mute.
- Shuffle.
- Repeat modes.
- Queue ordering.
- Queue persistence.
- Playlist playback.
- Album playback.
- Track-end behavior.
- Missing file handling.
- Unsupported format handling.
- Network interruption recovery.
- USB disconnection recovery.
- Long audio files.
- Large audio files.
- Rapid track switching.
- Browser refresh during playback.

## Library tests
- Initial scan.
- Incremental scan.
- Duplicate files.
- Renamed files.
- Moved files.
- Deleted files.
- Missing metadata.
- Missing artwork.
- Corrupt audio.
- Corrupt artwork.
- Read-only folders.
- Inaccessible folders.
- Unicode file names.
- Special characters.
- Large libraries.
- Multiple library roots.

## UI tests
- Desktop layout.
- Mobile layout.
- Tablet layout.
- Dark theme.
- Light theme.
- High-contrast theme.
- Empty states.
- Loading states.
- Error states.
- Keyboard navigation.
- Screen-reader labels.
- Dialog focus management.
- Context menus.
- Drag-and-drop.
- Responsive overflow.

## Backend tests
- Authentication.
- Authorization.
- Session revocation.
- HTTP range requests.
- Invalid track IDs.
- Path traversal prevention.
- Playlist CRUD.
- Queue persistence.
- Metadata editing.
- Database migrations.
- Database integrity.
- Scan cancellation.
- Concurrent scan protection.
- Backup and restore.
- Rate limiting.
- Malformed requests.
- Unauthorized media access.

## Deployment tests
- Fresh Ubuntu installation.
- Reboot after installation.
- Automatic service startup.
- Graceful shutdown.
- Service restart.
- USB drive removal.
- USB drive reconnection.
- Local network access.
- Tailscale remote access.
- Database backup recovery.
- Upgrade from a previous version.
- Low-memory operation.
- Permission errors.
- Logs and diagnostics.

---

# 56. DEVELOPMENT AND DEPLOYMENT REQUIREMENTS

- Go backend.
- Svelte frontend.
- TypeScript.
- Vite.
- SQLite.
- Native browser audio playback.
- HTTP range streaming.
- go-taglib metadata extraction.
- Versioned database migrations.
- Environment-based configuration.
- Configuration validation.
- Example environment file.
- Structured logging.
- Graceful shutdown.
- Health endpoints.
- Automated tests.
- Linting.
- Type checking.
- Production build.
- Secure production defaults.
- Ubuntu server support.
- systemd service.
- Service restart policy.
- Automatic startup after reboot.
- Configurable service user.
- Configurable installation directory.
- Configurable data directory.
- Configurable library roots.
- Log rotation.
- Upgrade instructions.
- Backup and restore instructions.
- Recovery documentation.
- No unnecessary Docker dependency for normal installation.
- No external database dependency.
- No requirement for a paid third-party API.
- No requirement for internet access during normal local playback.
- Optional reverse-proxy deployment.
- Optional Tailscale access.
- Reproducible builds.
- Documented configuration variables.
- A CLI setup wizard.
- Safe, idempotent installation and upgrade scripts.

---

# 57. DOCUMENTATION

Create the following documentation:

- README.
- Feature overview.
- Architecture overview.
- Installation guide.
- First-run setup guide.
- Ubuntu deployment guide.
- systemd service guide.
- Tailscale setup guide.
- Library configuration guide.
- USB drive setup guide.
- Supported audio formats guide.
- Metadata editing guide.
- Playlist guide.
- Keyboard shortcuts guide.
- Theme customization guide.
- Plugin development guide.
- Plugin security guide.
- API documentation.
- Database schema documentation.
- Backup and restore guide.
- Troubleshooting guide.
- Security configuration guide.
- Upgrade guide.
- Uninstallation guide.
- Development setup guide.
- Testing guide.
- Known browser limitations.
- Privacy and data storage explanation.

---

# 58. ACCEPTANCE CRITERIA

The application is ready for its first stable release only when all the following conditions are met.

- The administrator can install and configure the application on Ubuntu.
- The application starts automatically after a server reboot.
- The user can configure one or more music library roots.
- The scanner indexes music files and extracts supported metadata.
- The user can browse songs, albums, artists, genres, and folders.
- Search returns relevant local music results.
- The user can play, pause, seek, skip, shuffle, and repeat.
- Audio streaming supports HTTP range requests.
- Playback continues during ordinary page navigation.
- The queue supports insertion, removal, and reordering.
- Playlists can be created, edited, reordered, and deleted.
- Favorites persist across sessions.
- Listening history and play counts work correctly.
- The player handles missing files and unsupported formats gracefully.
- Artwork and metadata display correctly when available.
- The application works on desktop and mobile.
- The PWA can be installed where supported.
- Local network access works.
- Tailscale access works when configured.
- Protected media cannot be accessed without authorization.
- Music files are not deleted when library entries are removed.
- Library scanning does not unnecessarily interrupt playback.
- The application can back up and restore its database.
- Core functionality does not depend on an external cloud service.
- The interface provides useful loading, empty, error, and offline states.
- Keyboard navigation and accessibility basics are implemented.
- The application remains responsive with a large music library.
- Automated tests cover the critical playback, library, authentication, and streaming workflows.
- Logs do not expose passwords, tokens, or secrets.
- The administrator can diagnose common deployment and storage failures.

---

# 59. IMPLEMENTATION PRIORITY

## Phase 1 — Core foundation
- Application shell.
- Authentication.
- First-run setup.
- SQLite schema and migrations.
- Library root configuration.
- Metadata scanner.
- Song, album, artist, and folder browsing.
- Search.
- Core audio playback.
- HTTP range streaming.
- Queue.
- Basic playlists.
- Basic favorites.
- Dark neo-brutalist UI.

## Phase 2 — Complete listening experience
- Full-screen player.
- Mini-player.
- Album and artist details.
- Recently played.
- Listening history.
- Repeat and shuffle.
- Playlist editing.
- Metadata editor.
- Artwork caching.
- Keyboard shortcuts.
- Mobile responsive design.

## Phase 3 — Personalization
- Theme editor.
- Theme plugins.
- Smart playlists.
- Statistics.
- Lyrics.
- Advanced sorting and filtering.
- PWA installation.
- Browser media controls.
- Backup and restore.

## Phase 4 — Server reliability
- systemd integration.
- Startup recovery.
- Tailscale access.
- USB drive handling.
- Scan scheduling.
- Health diagnostics.
- Logging and rotation.
- Resource monitoring.
- Security hardening.
- Upgrade and recovery workflows.

## Phase 5 — Advanced features
- Plugin management.
- Audio visualizations.
- Advanced metadata tools.
- Duplicate detection.
- Advanced discovery.
- Optional equalizer.
- Crossfade and gapless playback improvements.
- Additional import/export formats.
- Advanced statistics.

---

# 60. FINAL UI/UX QUALITY RULES

- Every page must have a clear purpose.
- Every interactive control must work.
- Every icon-only control must have an accessible label.
- Every destructive operation must be deliberate.
- Every asynchronous action must provide feedback.
- Every loading state must eventually resolve or offer recovery.
- Every page must support empty, error, and populated states.
- Every essential workflow must work with a keyboard.
- Every core workflow must work on mobile.
- Every list must have appropriate sorting and filtering.
- Every artwork image must have a fallback.
- Every player state must remain consistent across views.
- Every setting must have a clear label and understandable effect.
- Every saved change must persist correctly.
- Every remote request must follow the authentication and authorization rules.
- Every file operation must respect configured library boundaries.
- Every feature must work without unnecessary external dependencies.
- Do not add buttons that are decorative but nonfunctional.
- Do not create placeholder pages that appear finished.
- Do not introduce unnecessary animations.
- Do not make the application visually resemble a crowded administration dashboard.
- Keep the primary listening experience simple and intuitive.
- Keep advanced functionality available without overwhelming everyday playback.
- Use consistent terminology throughout the application.
- Prioritize reliability, speed, and audio playback over decorative features.
- Keep the application modular so features can be added without destabilizing the playback engine.

**Primary design principle:** Rewind should feel like a polished, private music streaming application while remaining lightweight, self-hosted, extensible, and entirely centered on the user's own music collection.