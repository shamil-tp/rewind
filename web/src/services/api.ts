// Client for the Rewind HTTP API. All requests use relative URLs, so the same
// code works behind the Vite dev proxy and when served by the Go binary.
// Authentication relies on the HttpOnly session cookie the browser manages;
// no token or password is ever stored by this code.

export interface Health {
  status: string
  name: string
  version: string
}

export interface User {
  id: number
  username: string
}

export interface AuthStatus {
  setup_required: boolean
  authenticated: boolean
}

export class ApiError extends Error {
  /** HTTP status, or undefined when the server could not be reached. */
  readonly status: number | undefined
  /** Machine-readable error code from the server, e.g. "invalid_credentials". */
  readonly code: string | undefined

  constructor(message: string, status?: number, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

function isAbort(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const init: RequestInit = { method, signal, credentials: 'same-origin', headers }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }

  let res: Response
  try {
    res = await fetch(path, init)
  } catch (err) {
    if (isAbort(err)) throw err
    throw new ApiError('Cannot reach the Rewind server', undefined, 'network_error')
  }
  if (res.status === 204) {
    return undefined as T
  }

  let data: unknown
  try {
    data = await res.json()
  } catch {
    throw new ApiError(
      res.ok ? 'Server returned an invalid response' : `Server responded with HTTP ${res.status}`,
      res.status,
    )
  }
  if (!res.ok) {
    const err = (data as { error?: { code?: unknown; message?: unknown } } | null)?.error
    const code = typeof err?.code === 'string' ? err.code : undefined
    // 4xx messages are written for users; 5xx details are not shown.
    const message =
      res.status >= 500
        ? 'The server had a problem. Try again.'
        : typeof err?.message === 'string'
          ? err.message
          : `Request failed (HTTP ${res.status})`
    throw new ApiError(message, res.status, code)
  }
  return data as T
}

export function getHealth(signal?: AbortSignal): Promise<Health> {
  return request<Health>('GET', '/api/health', undefined, signal)
}

export function getAuthStatus(signal?: AbortSignal): Promise<AuthStatus> {
  return request<AuthStatus>('GET', '/api/auth/status', undefined, signal)
}

export async function getMe(signal?: AbortSignal): Promise<User> {
  return (await request<{ user: User }>('GET', '/api/auth/me', undefined, signal)).user
}

export async function setupAccount(setupCode: string, username: string, password: string): Promise<User> {
  const res = await request<{ user: User }>('POST', '/api/auth/setup', {
    setup_code: setupCode,
    username,
    password,
  })
  return res.user
}

export async function login(username: string, password: string): Promise<User> {
  return (await request<{ user: User }>('POST', '/api/auth/login', { username, password })).user
}

export function logout(): Promise<void> {
  return request<void>('POST', '/api/auth/logout')
}

// --- Music library ---

/** A track as returned by GET /api/tracks. Optional fields are omitted when unknown. */
export interface Track {
  id: number
  root_id: number
  title: string
  artist?: string
  album?: string
  album_artist?: string
  genre?: string
  year?: number
  track_number?: number
  disc_number?: number
  duration_ms?: number
  format: string
  codec?: string
  status: 'present' | 'missing'
  available: boolean
  metadata_status: string
  relative_path?: string
  release_date?: string
  composer?: string
  has_lyrics?: boolean
  /** Only on single-track reads (GET /api/tracks/{id}). */
  lyrics?: string
  /** Changes with every metadata change; used to refresh cached artwork. */
  revision?: number
  album_id?: string
  /** Each credited artist, with the ID of its page. */
  artists?: ArtistRef[]
  /** The signed-in user's data. */
  favorite?: boolean
  play_count?: number
  last_played_at?: string
}

export interface ArtistRef {
  id: string
  name: string
}

export interface Pagination {
  page: number
  limit: number
  total: number
  total_pages: number
}

export interface TrackPage {
  items: Track[]
  pagination: Pagination
}

/** Sort keys accepted by GET /api/tracks. */
export type TrackSort = 'title' | 'artist' | 'album' | 'album_artist' | 'year' | 'duration' | 'added'

export interface TrackQuery {
  page?: number
  limit?: number
  q?: string
  sort?: TrackSort
  order?: 'asc' | 'desc'
}

export function listTracks(query: TrackQuery = {}, signal?: AbortSignal): Promise<TrackPage> {
  const params = new URLSearchParams()
  if (query.page) params.set('page', String(query.page))
  if (query.limit) params.set('limit', String(query.limit))
  if (query.q) params.set('q', query.q)
  if (query.sort) params.set('sort', query.sort)
  if (query.order) params.set('order', query.order)
  const qs = params.toString()
  return request<TrackPage>('GET', qs ? `/api/tracks?${qs}` : '/api/tracks', undefined, signal)
}

/** The authenticated streaming URL, built only from the numeric track ID. */
export function streamUrl(id: number): string {
  if (!Number.isSafeInteger(id) || id < 1) throw new Error('invalid track id')
  return `/api/tracks/${id}/stream`
}

/**
 * Checks why a stream failed with a cheap HEAD request (no audio is
 * downloaded). Returns the HTTP status, or 0 if the server is unreachable.
 */
export async function streamStatus(id: number): Promise<number> {
  try {
    const res = await fetch(streamUrl(id), { method: 'HEAD', credentials: 'same-origin' })
    return res.status
  } catch {
    return 0
  }
}

export interface ScanCounts {
  files_found: number
  files_skipped: number
  files_failed: number
  dirs_failed: number
  tracks_added: number
  tracks_updated: number
  tracks_unchanged: number
  tracks_renamed: number
  tracks_missing: number
  tracks_restored: number
}

export interface ActiveScan {
  root_id: number
  scan_id: number
  started_at: string
  counts: ScanCounts
}

/** GET /api/library */
export interface LibrarySummary {
  tracks: number
  available_tracks: number
  missing_tracks: number
  artists: number
  albums: number
  total_duration_ms: number
  roots: { total: number; available: number; unavailable: number; pending: number; disabled: number }
  last_successful_scan_at?: string
  scan: { running: boolean; active: ActiveScan[]; queued: number[] }
}

export interface ScanReport {
  id: number
  status: 'running' | 'completed' | 'partial' | 'failed' | 'interrupted'
  started_at: string
  finished_at?: string
  counts: ScanCounts
  error?: string
}

/** An item of GET /api/library/roots */
export interface LibraryRoot {
  id: number
  name: string
  enabled: boolean
  status: 'pending' | 'available' | 'unavailable'
  status_message?: string
  track_count: number
  missing_count: number
  scanning: boolean
  queued: boolean
  last_scan?: ScanReport
}

export interface ScanStart {
  started: number[]
  already_running: number[]
}

export function getLibrarySummary(signal?: AbortSignal): Promise<LibrarySummary> {
  return request<LibrarySummary>('GET', '/api/library', undefined, signal)
}

export async function getLibraryRoots(signal?: AbortSignal): Promise<LibraryRoot[]> {
  return (await request<{ items: LibraryRoot[] }>('GET', '/api/library/roots', undefined, signal)).items
}

/** Requests a background rescan of every enabled root. Returns at once (202). */
export function startScan(): Promise<ScanStart> {
  return request<ScanStart>('POST', '/api/library/scan')
}

// --- Playlists ---

/** A playlist without its tracks (GET /api/playlists). */
export interface Playlist {
  id: number
  name: string
  description?: string
  track_count: number
  /** Tracks that cannot be played right now (missing files, unplugged drives). */
  unavailable_count: number
  duration_ms: number
  created_at: string
  updated_at: string
  /** The track whose artwork represents the playlist (chosen or first). */
  artwork_track_id?: number
  artwork_chosen?: boolean
}

export interface PlaylistEntry {
  /** 1-based, contiguous. */
  position: number
  added_at: string
  track: Track
}

export interface PlaylistDetail extends Playlist {
  entries: PlaylistEntry[]
}

/** Playlist IDs come from the server; this guards URL building anyway. */
function playlistPath(id: number): string {
  if (!Number.isSafeInteger(id) || id < 1) throw new Error('invalid playlist id')
  return `/api/playlists/${id}`
}

export async function listPlaylists(signal?: AbortSignal): Promise<Playlist[]> {
  return (await request<{ items: Playlist[] }>('GET', '/api/playlists', undefined, signal)).items
}

export async function getPlaylist(id: number, signal?: AbortSignal): Promise<PlaylistDetail> {
  return (await request<{ playlist: PlaylistDetail }>('GET', playlistPath(id), undefined, signal)).playlist
}

export async function createPlaylist(name: string, description = ''): Promise<Playlist> {
  return (await request<{ playlist: Playlist }>('POST', '/api/playlists', { name, description })).playlist
}

/** Renames and/or re-describes a playlist. An empty description removes it. */
export async function updatePlaylist(id: number, changes: { name?: string; description?: string }): Promise<Playlist> {
  return (await request<{ playlist: Playlist }>('PATCH', playlistPath(id), changes)).playlist
}

export function deletePlaylist(id: number): Promise<void> {
  return request<void>('DELETE', playlistPath(id))
}

export async function addToPlaylist(id: number, trackId: number): Promise<PlaylistEntry> {
  return (await request<{ entry: PlaylistEntry }>('POST', `${playlistPath(id)}/tracks`, { track_id: trackId })).entry
}

export function removeFromPlaylist(id: number, trackId: number): Promise<void> {
  if (!Number.isSafeInteger(trackId) || trackId < 1) throw new Error('invalid track id')
  return request<void>('DELETE', `${playlistPath(id)}/tracks/${trackId}`)
}

/** Sets the order; trackIds must list every track of the playlist once. */
export async function reorderPlaylist(id: number, trackIds: number[]): Promise<PlaylistDetail> {
  return (await request<{ playlist: PlaylistDetail }>('PUT', `${playlistPath(id)}/tracks`, { track_ids: trackIds })).playlist
}

// --- Preferences ---

/** Values of the library sort menu; also the library_sort preference values. */
export type LibrarySortChoice = 'artist' | 'album' | 'title' | 'added' | 'year' | 'duration'

export interface Preferences {
  library_sort: LibrarySortChoice
  autoplay_next: boolean
  /**
   * The active theme's ID. Read-only here: themes are applied through the
   * theme API (applyThemeById), which is authoritative.
   */
  theme: string
  /**
   * Custom colors from before per-theme customization ("" for none). They
   * still apply to themes the user has not customized; the server reports
   * them as each theme's "legacy" customization.
   */
  accent_color: string
  primary_color: string
  secondary_color: string
  /** Percent of a track (at most four minutes) that counts as a play. */
  play_threshold_percent: number
  /** False: plays are not recorded (privacy mode). */
  record_history: boolean
}

export const DEFAULT_PREFERENCES: Preferences = {
  library_sort: 'artist',
  autoplay_next: true,
  theme: 'rewind-dark',
  accent_color: '',
  primary_color: '',
  secondary_color: '',
  play_threshold_percent: 50,
  record_history: true,
}

export async function getPreferences(signal?: AbortSignal): Promise<Preferences> {
  return (await request<{ preferences: Preferences }>('GET', '/api/preferences', undefined, signal)).preferences
}

/** Saves the given preferences; returns all preferences as stored. */
export async function savePreferences(changes: Partial<Preferences>): Promise<Preferences> {
  return (await request<{ preferences: Preferences }>('PATCH', '/api/preferences', changes)).preferences
}

// --- Themes ---

export type ThemeSource = 'bundled' | 'installed'
export type ThemeAvailability = 'available' | 'invalid' | 'incompatible'

/**
 * One theme as the signed-in user sees it. Name, description, author and
 * homepage come from the theme's manifest: render them as text only.
 */
export interface ThemeInfo {
  id: string
  name?: string
  version?: string
  description?: string
  author?: string
  homepage?: string
  scheme?: 'dark' | 'light'
  source: ThemeSource
  state: ThemeAvailability
  /** Why the theme cannot be used, written by Rewind. */
  error?: string
  enabled: boolean
  active: boolean
  /** The theme's own value for every token (available themes only). */
  tokens?: Record<string, string | number>
  /** Tokens the user may customize in this theme. */
  customizable?: string[]
  /** The user's values for this theme. */
  customization: Record<string, string | number>
  /** "legacy": the theme was never customized and the earlier custom colors apply. */
  customization_source: 'none' | 'saved' | 'legacy'
}

export interface ThemeState {
  theme_api: number
  /** True when the administrator configured a theme directory. */
  directory_configured: boolean
  /** The theme shown: always an available, enabled theme. */
  active: string
  default_theme: string
  /** Why the active theme is not the saved one, if it is not. */
  notice?: string
  items: ThemeInfo[]
}

const themePath = (id: string) => `/api/themes/${encodeURIComponent(id)}`

export function getThemes(signal?: AbortSignal): Promise<ThemeState> {
  return request<ThemeState>('GET', '/api/themes', undefined, signal)
}

/** Makes a theme the active one; returns the new state. */
export function applyThemeById(id: string): Promise<ThemeState> {
  return request<ThemeState>('PUT', '/api/themes/active', { id })
}

export function setThemeEnabled(id: string, enabled: boolean): Promise<ThemeState> {
  return request<ThemeState>('PUT', `${themePath(id)}/enabled`, { enabled })
}

/** Replaces the user's customization of one theme ({} resets it); returns the new state. */
export function saveThemeCustomization(id: string, tokens: Record<string, string | number>): Promise<ThemeState> {
  return request<ThemeState>('PUT', `${themePath(id)}/customization`, { tokens })
}

// --- Plugins (read-only status) ---

export type PluginState = 'running' | 'disabled' | 'failed' | 'invalid' | 'incompatible' | 'unavailable' | 'missing'

/** One installed or enabled plugin. All text is untrusted; render it as text only. */
export interface PluginStatus {
  id: string
  name?: string
  version?: string
  description?: string
  author?: string
  homepage?: string
  capabilities: string[]
  state: PluginState
  /** A summary written by Rewind; details are in the server log. */
  error?: string
}

export interface PluginStatusList {
  /** False when the server has no plugin directory configured. */
  configured: boolean
  api_version: number
  items: PluginStatus[]
}

export function getPlugins(signal?: AbortSignal): Promise<PluginStatusList> {
  return request<PluginStatusList>('GET', '/api/plugins', undefined, signal)
}

// --- Albums and artists ---

export interface Album {
  id: string
  title: string
  artist: string
  artists: ArtistRef[]
  year?: number
  genre?: string
  track_count: number
  disc_count: number
  duration_ms: number
  unavailable_count: number
  artwork_track_id: number
  added_at: string
}

export interface AlbumDetail extends Album {
  /** Identifies the album's current metadata, for edits. */
  version: string
  /** Folders holding the files, as "<library>/<path>". */
  folders: string[]
  tracks: Track[]
}

export interface Artist {
  id: string
  name: string
  track_count: number
  album_count: number
  duration_ms: number
  artwork_track_id: number
}

export interface ArtistDetail extends Artist {
  version: string
  albums: Album[]
  appears_on: Album[]
  tracks: Track[]
  tracks_truncated: boolean
}

export interface ArtistPage {
  artist: ArtistDetail
  top_tracks: Track[]
  recent_tracks: Track[]
  listening_ms: number
}

export interface Page<T> {
  items: T[]
  pagination: Pagination
}

export interface BrowseQuery {
  page?: number
  limit?: number
  q?: string
  sort?: string
  order?: 'asc' | 'desc'
}

function queryString(q: object): string {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) if (v !== undefined && v !== '') params.set(k, String(v))
  const qs = params.toString()
  return qs ? `?${qs}` : ''
}

/** Album and artist IDs are opaque URL-safe strings from the server. */
function groupID(id: string): string {
  if (!/^[A-Za-z0-9_~-]{1,4096}$/.test(id)) throw new Error('invalid id')
  return id
}

function trackPath(id: number): string {
  if (!Number.isSafeInteger(id) || id < 1) throw new Error('invalid track id')
  return `/api/tracks/${id}`
}

export function listAlbums(q: BrowseQuery = {}, signal?: AbortSignal): Promise<Page<Album>> {
  return request<Page<Album>>('GET', `/api/albums${queryString(q)}`, undefined, signal)
}

export async function getAlbum(id: string, signal?: AbortSignal): Promise<AlbumDetail> {
  return (await request<{ album: AlbumDetail }>('GET', `/api/albums/${groupID(id)}`, undefined, signal)).album
}

/** Edits album, album_artist, year and genre of every track; returns the album's (possibly new) ID. */
export async function editAlbum(id: string, version: string, changes: Record<string, MetadataValue>): Promise<string> {
  return (await request<{ id: string }>('PATCH', `/api/albums/${groupID(id)}/metadata`, { version, changes })).id
}

export function listArtists(q: BrowseQuery = {}, signal?: AbortSignal): Promise<Page<Artist>> {
  return request<Page<Artist>>('GET', `/api/artists${queryString(q)}`, undefined, signal)
}

export function getArtist(id: string, signal?: AbortSignal): Promise<ArtistPage> {
  return request<ArtistPage>('GET', `/api/artists/${groupID(id)}`, undefined, signal)
}

/** Renames an artist in every track that credits it; returns the new ID. */
export async function renameArtist(id: string, version: string, name: string): Promise<string> {
  return (await request<{ id: string }>('PATCH', `/api/artists/${groupID(id)}/name`, { version, name })).id
}

/** Artwork of a track; v changes when the track does, so caches refresh. */
export function artworkUrl(trackId: number, size: 'small' | 'large', revision?: number): string {
  return `${trackPath(trackId)}/artwork?size=${size}${revision !== undefined ? `&v=${revision}` : ''}`
}

// --- Metadata editing (stored in Rewind's database; files are never changed) ---

export interface EditField {
  Name: string
  Kind: 'text' | 'multiline' | 'number'
  Min: number
  Max: number
  Required: boolean
}

export type MetadataValue = string | number | null

export interface TrackMetadata {
  track_id: number
  revision: number
  values: Record<string, MetadataValue>
  /** Fields edited in Rewind, and the values the file has for them. */
  edited: string[]
  from_file: Record<string, MetadataValue>
  updated_at: string
}

export interface MetadataResponse {
  metadata: TrackMetadata
  fields: EditField[]
  track: Track
}

export function getTrackMetadata(id: number, signal?: AbortSignal): Promise<MetadataResponse> {
  return request<MetadataResponse>('GET', `${trackPath(id)}/metadata`, undefined, signal)
}

export function saveTrackMetadata(id: number, revision: number, changes: Record<string, MetadataValue>): Promise<MetadataResponse> {
  return request<MetadataResponse>('PATCH', `${trackPath(id)}/metadata`, { revision, changes })
}

/** Restores the file's values for fields (all edited fields when omitted). */
export function revertTrackMetadata(id: number, revision: number, fields?: string[]): Promise<MetadataResponse> {
  return request<MetadataResponse>('POST', `${trackPath(id)}/metadata/revert`, fields ? { revision, fields } : { revision })
}

export async function getTrack(id: number, signal?: AbortSignal): Promise<Track> {
  return (await request<{ track: Track }>('GET', trackPath(id), undefined, signal)).track
}

// --- Favorites ---

export async function setFavorite(trackId: number, favorite: boolean): Promise<void> {
  if (!Number.isSafeInteger(trackId) || trackId < 1) throw new Error('invalid track id')
  await request(favorite ? 'PUT' : 'DELETE', `/api/favorites/tracks/${trackId}`)
}

export function listFavorites(q: { page?: number; limit?: number } = {}, signal?: AbortSignal): Promise<Page<Track>> {
  return request<Page<Track>>('GET', `/api/favorites/tracks${queryString(q)}`, undefined, signal)
}

// --- Listening history ---

export type SourceKind = 'library' | 'album' | 'artist' | 'playlist' | 'history' | 'queue' | 'favorites'

export interface PlayEvent {
  track_id: number
  play_key: string
  listened_ms: number
  source_kind?: SourceKind
  source_id?: string
}

export async function recordPlay(e: PlayEvent): Promise<boolean> {
  return (await request<{ recorded: boolean }>('POST', '/api/history', e)).recorded
}

export interface HistoryEntry {
  id: number
  played_at: string
  listened_ms: number
  source_kind?: SourceKind
  source_id?: string
  track: Track
}

export interface HistoryQuery {
  page?: number
  limit?: number
  q?: string
  /** RFC 3339 times; to is exclusive. */
  from?: string
  to?: string
  /** desc (newest first, default) or asc. */
  order?: 'asc' | 'desc'
}

export function listHistory(q: HistoryQuery = {}, signal?: AbortSignal): Promise<Page<HistoryEntry>> {
  return request<Page<HistoryEntry>>('GET', `/api/history${queryString(q)}`, undefined, signal)
}

export async function deleteHistory(ids: number[]): Promise<number> {
  return (await request<{ deleted: number }>('DELETE', '/api/history', { ids })).deleted
}

export async function clearHistory(): Promise<number> {
  return (await request<{ deleted: number }>('DELETE', '/api/history', { all: true })).deleted
}

export interface RecentAlbum {
  id: string
  title: string
  artist: string
  artists: ArtistRef[]
  artwork_track_id: number
  last_played_at: string
}

export interface RecentArtist extends ArtistRef {
  artwork_track_id: number
  last_played_at: string
}

export interface Recent {
  tracks: Track[]
  albums: RecentAlbum[]
  artists: RecentArtist[]
}

export function getRecent(limit = 12, signal?: AbortSignal): Promise<Recent> {
  return request<Recent>('GET', `/api/history/recent?limit=${limit}`, undefined, signal)
}

export async function getTopTracks(limit = 25, signal?: AbortSignal): Promise<Track[]> {
  return (await request<{ items: Track[] }>('GET', `/api/history/top?limit=${limit}`, undefined, signal)).items
}

// --- Saved play queue ---

export type RepeatMode = 'off' | 'all' | 'one'

export interface PlaybackContext {
  kind: SourceKind
  id?: string
  name?: string
}

export interface PlaybackState {
  queue: number[]
  original?: number[]
  index: number
  position_ms: number
  shuffle: boolean
  repeat: RepeatMode
  speed: number
  context?: PlaybackContext
}

export interface SavedPlayback {
  state: PlaybackState
  revision: number
  tracks: Track[]
}

export function getPlayback(signal?: AbortSignal): Promise<SavedPlayback> {
  return request<SavedPlayback>('GET', '/api/playback', undefined, signal)
}

/**
 * Saves the queue. keepalive lets the request finish while the page is
 * being closed (used on pagehide).
 */
export async function savePlayback(revision: number, state: PlaybackState, keepalive = false): Promise<void> {
  if (!keepalive) {
    await request('PUT', '/api/playback', { revision, state })
    return
  }
  try {
    await fetch('/api/playback', {
      method: 'PUT',
      credentials: 'same-origin',
      keepalive: true,
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ revision, state }),
    })
  } catch {
    // Best effort while the page closes.
  }
}

// --- Playlist extras ---

export interface AddTracksResult {
  added: number
  skipped: number
  playlist: PlaylistDetail
}

/** Adds several tracks at once; tracks already in the playlist are skipped. */
export function addTracksToPlaylist(id: number, trackIds: number[]): Promise<AddTracksResult> {
  return request<AddTracksResult>('POST', `${playlistPath(id)}/tracks`, { track_ids: trackIds })
}

export async function duplicatePlaylist(id: number): Promise<Playlist> {
  return (await request<{ playlist: Playlist }>('POST', `${playlistPath(id)}/duplicate`)).playlist
}

/** Chooses the track whose artwork represents the playlist; null uses the first track's. */
export async function setPlaylistArtwork(id: number, trackId: number | null): Promise<Playlist> {
  return (await request<{ playlist: Playlist }>('PUT', `${playlistPath(id)}/artwork`, { track_id: trackId })).playlist
}
