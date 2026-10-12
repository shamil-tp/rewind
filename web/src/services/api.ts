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
   * A built-in theme ID (see services/themes.ts). Typed as string because a
   * server of another version may send an ID this client does not know;
   * themeById falls back to the default.
   */
  theme: string
  /** "" for the theme's accent, or a custom "#rrggbb" accent. */
  accent_color: string
}

export const DEFAULT_PREFERENCES: Preferences = {
  library_sort: 'artist',
  autoplay_next: true,
  theme: 'rewind-dark',
  accent_color: '',
}

export async function getPreferences(signal?: AbortSignal): Promise<Preferences> {
  return (await request<{ preferences: Preferences }>('GET', '/api/preferences', undefined, signal)).preferences
}

/** Saves the given preferences; returns all preferences as stored. */
export async function savePreferences(changes: Partial<Preferences>): Promise<Preferences> {
  return (await request<{ preferences: Preferences }>('PATCH', '/api/preferences', changes)).preferences
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
