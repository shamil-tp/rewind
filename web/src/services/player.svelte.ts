// The playback engine: one <audio> element and one authoritative playback
// state for the whole app. Views (mini player, Now Playing, queue, track
// lists) only read this state and call its actions; none of them owns audio,
// so navigation never interrupts playback.
//
// - One element, created once (attach), with each listener added once.
// - Every load gets a token; asynchronous work (error probes, retries,
//   restore) checks it, so a late result never touches a newer track.
// - The queue and settings are saved on the server (PUT /api/playback),
//   debounced, and restored at sign-in without starting audio. Volume and
//   mute are per device (localStorage).
// - A play is recorded once per playback of a track (a random play key makes
//   retries idempotent) when the listened time reaches the user's threshold:
//   a percentage of the track, at most four minutes. Queueing a track or
//   opening its page never counts.
// - Audio is streamed by the browser from /api/tracks/{id}/stream with Range
//   requests; nothing is downloaded into JavaScript.
//
// Queue rules are in services/queue.ts.

import {
  getPlayback,
  recordPlay,
  savePlayback,
  streamStatus,
  streamUrl,
  type PlaybackContext,
  type PlaybackState,
  type RepeatMode,
  type SourceKind,
  type Track,
} from './api'
import { listening } from './listening.svelte'
import {
  entriesOf,
  insertNext,
  insertNextOriginal,
  move,
  nextIndex,
  previousIndex,
  remove,
  shuffleAll,
  shuffleUpcoming,
  unshuffle,
  withoutKeys,
  type Entry,
} from './queue'

export type { Entry } from './queue'

const DEVICE_KEY = 'rewind.player'
const SAVE_DELAY_MS = 800
const POSITION_SAVE_MS = 15_000
const RESTART_THRESHOLD_S = 3
const MAX_PLAY_SECONDS = 240
const RETRY_LIMIT = 5
export const SEEK_STEP_S = 10
export const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2] as const

function newPlayKey(): string {
  try {
    return crypto.randomUUID()
  } catch {
    return `p${Date.now().toString(36)}${Math.random().toString(36).slice(2, 12)}`
  }
}

/** Options when starting a new queue. */
export interface PlayOptions {
  shuffle?: boolean
}

export class Player {
  // --- Queue (authoritative) ---
  entries = $state<Entry[]>([])
  index = $state(-1)
  /** Order before shuffling; null while shuffle is off. */
  original = $state<Entry[] | null>(null)
  context = $state<PlaybackContext | null>(null)

  // --- Playback ---
  playing = $state(false)
  loading = $state(false)
  error = $state('')
  notice = $state('')
  position = $state(0)
  duration = $state(0)
  buffered = $state(0)

  // --- Settings ---
  volume = $state(1)
  muted = $state(false)
  speed = $state(1)
  shuffle = $state(false)
  repeat = $state<RepeatMode>('off')

  // --- History settings (from preferences) ---
  thresholdPercent = 50
  recordHistory = $state(true)
  /** Continue with the next track when one ends (preference autoplay_next). */
  autoAdvance = true

  current = $derived<Track | null>(this.entries[this.index]?.track ?? null)
  currentKey = $derived<number | null>(this.entries[this.index]?.key ?? null)
  hasNext = $derived(nextIndex(this.entries, this.index, this.repeat) >= 0)
  hasPrevious = $derived(this.index >= 0)
  upcoming = $derived(this.entries.slice(this.index + 1))

  private audio: HTMLAudioElement | null = null
  private token = 0
  private loadedKey: number | null = null
  private pendingSeek: number | null = null
  private autoAdvanced = false
  private retries = 0
  private onUnauthorized: () => void = () => {}
  private revision = 0
  private saveTimer: ReturnType<typeof setTimeout> | undefined
  private lastPositionSave = 0
  private touched = false
  private listen = { key: '', seconds: 0, last: 0, reported: false, trackId: 0 }
  private cleanup: (() => void)[] = []

  /**
   * Creates the audio element and its listeners. Calling it again only
   * updates the session-expired callback, so listeners are never doubled.
   */
  attach(onUnauthorized: () => void): void {
    this.onUnauthorized = onUnauthorized
    if (this.audio) return
    const audio = document.createElement('audio')
    audio.preload = 'none'
    audio.hidden = true
    audio.dataset.rewindPlayer = ''
    document.body.append(audio)
    this.audio = audio
    this.loadDevice()
    audio.volume = this.volume
    audio.muted = this.muted

    const on = <K extends keyof HTMLMediaElementEventMap>(type: K, fn: () => void) => {
      audio.addEventListener(type, fn)
      this.cleanup.push(() => audio.removeEventListener(type, fn))
    }
    on('loadstart', () => (this.loading = this.loadedKey !== null))
    on('waiting', () => (this.loading = true))
    on('canplay', () => (this.loading = false))
    on('playing', () => {
      this.loading = false
      this.playing = true
      this.retries = 0
      if (this.notice.startsWith('Connection')) this.notice = ''
    })
    on('play', () => (this.playing = true))
    on('pause', () => {
      this.playing = false
      this.savePosition()
    })
    on('ended', () => this.onEnded())
    on('timeupdate', () => this.onTime())
    on('durationchange', () => this.updateDuration())
    on('loadedmetadata', () => {
      this.updateDuration()
      if (this.pendingSeek !== null && audio) {
        const t = this.pendingSeek
        this.pendingSeek = null
        if (Number.isFinite(t) && t > 0 && (!this.duration || t < this.duration)) audio.currentTime = t
      }
      audio.playbackRate = this.speed
    })
    on('progress', () => {
      const b = audio.buffered
      this.buffered = b.length > 0 ? b.end(b.length - 1) : 0
    })
    on('error', () => void this.onError())

    const pagehide = () => this.flush(true)
    window.addEventListener('pagehide', pagehide)
    this.cleanup.push(() => window.removeEventListener('pagehide', pagehide))
    this.setupMediaSession()
  }

  /** Stops playback and forgets the queue (sign-out). */
  detach(): void {
    clearTimeout(this.saveTimer)
    for (const fn of this.cleanup) fn()
    this.cleanup = []
    this.release()
    this.audio?.remove()
    this.audio = null
    this.entries = []
    this.original = null
    this.index = -1
    this.context = null
    this.touched = false
    this.revision = 0
    // Settings belong to the signed-in user; the next one restores their own.
    this.shuffle = false
    this.repeat = 'off'
    this.speed = 1
    this.position = this.duration = this.buffered = 0
    this.error = this.notice = ''
  }

  // --- Starting playback ---

  /** Replaces the queue with tracks and plays from start (Play now on a list). */
  playTracks(tracks: readonly Track[], start = 0, context: PlaybackContext | null = null, options: PlayOptions = {}): void {
    if (tracks.length === 0) return
    const base = entriesOf(tracks)
    // start -1 means "no particular track" (Shuffle buttons).
    const chosen = start >= 0 ? Math.min(start, base.length - 1) : -1
    // With shuffle on (or requested), the chosen track plays first and the
    // rest in random order; the list order is kept for turning shuffle off.
    const shuffle = options.shuffle ?? this.shuffle
    const entries = shuffle ? shuffleAll(base, chosen) : base
    let index = shuffle ? 0 : Math.max(chosen, 0)
    if (!entries[index].track.available) {
      index = nextIndex(entries, index, 'off')
      if (index < 0) {
        this.notice = 'None of these tracks can be played right now.'
        return
      }
    }
    this.entries = entries
    this.original = shuffle ? base : null
    this.shuffle = shuffle
    this.context = context
    this.touched = true
    this.load(index, { autoplay: true })
    this.persist()
  }

  /** Plays one track now, keeping the rest of the queue after it. */
  playNow(track: Track, context: PlaybackContext | null = null): void {
    if (!track.available) return
    if (this.entries.length === 0) {
      this.playTracks([track], 0, context)
      return
    }
    const [entry] = entriesOf([track])
    this.entries = insertNext(this.entries, this.index, [entry])
    if (this.original) this.original = insertNextOriginal(this.original, this.entries[this.index], [entry])
    this.touched = true
    this.load(this.index + 1, { autoplay: true })
    this.persist()
  }

  /** Inserts tracks after the current one. */
  playNext(tracks: readonly Track[]): void {
    if (tracks.length === 0) return
    if (this.entries.length === 0) {
      this.playTracks(tracks, 0, null)
      return
    }
    const added = entriesOf(tracks)
    if (this.original) this.original = insertNextOriginal(this.original, this.entries[this.index], added)
    this.entries = insertNext(this.entries, this.index, added)
    this.touched = true
    listening.announce(`${this.count(tracks)} will play next.`)
    this.persist()
  }

  /** Appends tracks to the end of the queue. */
  addToQueue(tracks: readonly Track[]): void {
    if (tracks.length === 0) return
    const added = entriesOf(tracks)
    const wasEmpty = this.entries.length === 0
    this.entries = [...this.entries, ...added]
    if (this.original) this.original = [...this.original, ...added]
    this.touched = true
    if (wasEmpty) this.load(0, { autoplay: false })
    listening.announce(`Added ${this.count(tracks)} to the queue.`)
    this.persist()
  }

  private count(tracks: readonly Track[]): string {
    return tracks.length === 1 ? `“${tracks[0].title}”` : `${tracks.length} tracks`
  }

  // --- Transport ---

  toggle(): void {
    if (!this.audio || !this.current) return
    if (this.error) {
      // Try again after an error (e.g. the drive was plugged back in).
      this.load(this.index, { autoplay: true, position: this.position })
      return
    }
    if (this.playing) this.audio.pause()
    else void this.start()
  }

  play(): void {
    if (!this.playing) this.toggle()
  }

  pause(): void {
    if (this.playing) this.audio?.pause()
  }

  next(): void {
    const i = nextIndex(this.entries, this.index, this.repeat)
    if (i < 0) return
    this.touched = true
    this.load(i, { autoplay: true })
    this.persist()
  }

  previous(): void {
    if (this.index < 0) return
    if (this.position > RESTART_THRESHOLD_S) {
      this.seek(0)
      return
    }
    const i = previousIndex(this.entries, this.index, this.repeat)
    this.touched = true
    if (i < 0) this.seek(0)
    else this.load(i, { autoplay: true })
    this.persist()
  }

  /** Plays the queue entry at index. */
  jumpTo(index: number): void {
    if (index < 0 || index >= this.entries.length || !this.entries[index].track.available) return
    this.touched = true
    this.load(index, { autoplay: true })
    this.persist()
  }

  seek(seconds: number): void {
    if (!this.audio || !Number.isFinite(seconds)) return
    const max = this.duration || Infinity
    const t = Math.min(Math.max(seconds, 0), max)
    this.position = t
    if (this.audio.readyState >= 1) this.audio.currentTime = t
    else this.pendingSeek = t
    this.listen.last = t
    this.persistSoon()
  }

  seekBy(delta: number): void {
    if (this.current) this.seek(this.position + delta)
  }

  setVolume(v: number): void {
    if (!Number.isFinite(v)) return
    this.volume = Math.min(Math.max(v, 0), 1)
    if (this.audio) this.audio.volume = this.volume
    if (this.volume > 0 && this.muted) this.setMuted(false)
    this.saveDevice()
  }

  setMuted(m: boolean): void {
    this.muted = m
    if (this.audio) this.audio.muted = m
    this.saveDevice()
  }

  toggleMute(): void {
    this.setMuted(!this.muted)
  }

  setSpeed(s: number): void {
    if (!Number.isFinite(s) || s < 0.5 || s > 2) return
    this.speed = Math.round(s * 100) / 100
    if (this.audio) {
      this.audio.defaultPlaybackRate = this.speed
      this.audio.playbackRate = this.speed
    }
    this.persist()
  }

  toggleShuffle(): void {
    if (this.shuffle) {
      const restored = this.original ? unshuffle(this.original, this.entries[this.index]) : { entries: this.entries, index: this.index }
      this.entries = restored.entries
      this.index = restored.index
      this.original = null
      this.shuffle = false
    } else {
      const s = shuffleUpcoming(this.entries, this.index)
      this.entries = s.entries
      this.original = s.original
      this.shuffle = true
    }
    listening.announce(this.shuffle ? 'Shuffle on.' : 'Shuffle off.')
    this.persist()
  }

  cycleRepeat(): void {
    this.repeat = this.repeat === 'off' ? 'all' : this.repeat === 'all' ? 'one' : 'off'
    listening.announce(this.repeat === 'off' ? 'Repeat off.' : this.repeat === 'all' ? 'Repeat all.' : 'Repeat one.')
    this.persist()
  }

  // --- Queue editing ---

  removeAt(at: number): void {
    const removed = this.entries[at]
    if (!removed) return
    const r = remove(this.entries, this.index, at)
    this.entries = r.entries
    if (this.original) this.original = withoutKeys(this.original, new Set([removed.key]))
    this.touched = true
    if (r.currentRemoved) {
      const wasPlaying = this.playing
      if (r.index < 0) {
        this.index = -1
        this.release()
      } else {
        this.load(r.index, { autoplay: wasPlaying })
      }
    } else {
      this.index = r.index
    }
    this.persist()
  }

  moveEntry(from: number, to: number): void {
    const r = move(this.entries, this.index, from, to)
    this.entries = r.entries
    this.index = r.index
    this.touched = true
    this.persist()
  }

  /** Removes the tracks after the current one. */
  clearUpcoming(): void {
    const kept = this.entries.slice(0, this.index + 1)
    if (this.original) {
      const keep = new Set(kept.map((e) => e.key))
      this.original = this.original.filter((e) => keep.has(e.key))
    }
    this.entries = kept
    this.touched = true
    this.persist()
  }

  /** Stops playback and empties the queue. */
  clear(): void {
    this.release()
    this.entries = []
    this.original = null
    this.index = -1
    this.context = null
    this.position = 0
    this.duration = 0
    this.touched = true
    this.persist()
  }

  /** Replaces a track's data in the queue (after metadata edits). */
  updateTrack(track: Track): void {
    if (!this.entries.some((e) => e.track.id === track.id)) return
    const swap = (list: Entry[]) => list.map((e) => (e.track.id === track.id ? { key: e.key, track } : e))
    this.entries = swap(this.entries)
    if (this.original) this.original = swap(this.original)
    this.updateMediaSession()
  }

  // --- Loading and events ---

  private load(index: number, opts: { autoplay: boolean; position?: number; auto?: boolean }): void {
    const entry = this.entries[index]
    if (!this.audio || !entry) return
    const token = ++this.token
    this.index = index
    this.error = ''
    this.notice = ''
    this.position = opts.position ?? 0
    this.duration = entry.track.duration_ms ? entry.track.duration_ms / 1000 : 0
    this.buffered = 0
    this.playing = false
    this.autoAdvanced = !!opts.auto
    this.retries = 0
    this.loadedKey = entry.key
    this.pendingSeek = opts.position && opts.position > 0 ? opts.position : null
    this.listen = { key: newPlayKey(), seconds: 0, last: opts.position ?? 0, reported: false, trackId: entry.track.id }
    this.updateMediaSession()
    if (!entry.track.available) {
      this.audio.pause()
      this.audio.removeAttribute('src')
      this.error = 'This track is unavailable: its file is missing or its drive is not connected.'
      this.loading = false
      if (opts.auto) this.skipAfterError(token)
      return
    }
    this.loading = opts.autoplay
    this.audio.src = streamUrl(entry.track.id)
    this.audio.defaultPlaybackRate = this.speed
    this.audio.playbackRate = this.speed
    this.audio.load()
    if (opts.autoplay) void this.start()
  }

  private release(): void {
    this.token++
    this.loadedKey = null
    this.playing = false
    this.loading = false
    this.error = ''
    if (this.audio) {
      this.audio.pause()
      this.audio.removeAttribute('src')
      this.audio.load()
    }
  }

  private async start(): Promise<void> {
    if (!this.audio) return
    const token = this.token
    this.notice = ''
    try {
      await this.audio.play()
    } catch (err) {
      if (token !== this.token) return
      const name = err instanceof DOMException ? err.name : ''
      if (name === 'NotAllowedError') {
        this.loading = false
        this.notice = 'Press play to start.'
      }
      // AbortError: a newer load replaced this one. Decoding and network
      // problems arrive through the element's error event.
    }
  }

  private updateDuration(): void {
    const d = this.audio?.duration
    if (d && Number.isFinite(d) && d > 0) this.duration = d
  }

  private onTime(): void {
    if (!this.audio) return
    const t = this.audio.currentTime
    this.position = t
    // Count only time actually played: small forward steps while playing.
    const delta = t - this.listen.last
    this.listen.last = t
    if (this.playing && delta > 0 && delta < 2 * Math.max(this.speed, 1)) {
      this.listen.seconds += delta
      this.maybeRecord()
    }
    if (this.playing && Date.now() - this.lastPositionSave > POSITION_SAVE_MS) this.savePosition()
  }

  /** The listened seconds after which a play counts. */
  threshold(): number {
    const d = this.duration || (this.current?.duration_ms ?? 0) / 1000
    const pct = Math.min(Math.max(this.thresholdPercent, 10), 100) / 100
    return d > 0 ? Math.min(d * pct, MAX_PLAY_SECONDS) : MAX_PLAY_SECONDS
  }

  private maybeRecord(): void {
    const l = this.listen
    if (l.reported || !this.recordHistory || l.seconds < this.threshold()) return
    l.reported = true
    const event = {
      track_id: l.trackId,
      play_key: l.key,
      listened_ms: Math.round(l.seconds * 1000),
      source_kind: this.context?.kind,
      source_id: this.context?.id,
    }
    void this.sendPlay(event, 0)
  }

  private async sendPlay(event: Parameters<typeof recordPlay>[0], attempt: number): Promise<void> {
    try {
      if (await recordPlay(event)) listening.historyChanged()
    } catch (err) {
      const status = (err as { status?: number }).status
      // Retries are safe: the server ignores a repeated play key.
      if ((status === undefined || status >= 500) && attempt < 3) {
        setTimeout(() => void this.sendPlay(event, attempt + 1), 2000 * 2 ** attempt)
      }
    }
  }

  private onEnded(): void {
    this.playing = false
    this.position = this.duration
    if (this.repeat === 'one' && this.current) {
      this.load(this.index, { autoplay: true, auto: true })
      return
    }
    const i = this.autoAdvance ? nextIndex(this.entries, this.index, this.repeat) : -1
    if (i >= 0) {
      this.load(i, { autoplay: true, auto: true })
      this.persist()
    } else {
      this.notice = this.autoAdvance ? 'End of the queue.' : ''
      this.persist()
    }
  }

  private skipAfterError(token: number): void {
    const i = nextIndex(this.entries, this.index, this.repeat)
    if (i < 0 || i === this.index) return
    setTimeout(() => {
      if (token !== this.token) return
      const title = this.current?.title ?? 'a track'
      this.load(i, { autoplay: true, auto: true })
      this.notice = `Skipped “${title}”: it could not be played.`
      this.persist()
    }, 1500)
  }

  private async onError(): Promise<void> {
    const audio = this.audio
    const mediaError = audio?.error
    const entry = this.entries[this.index]
    if (!audio || !entry || this.loadedKey !== entry.key || !mediaError || mediaError.code === mediaError.MEDIA_ERR_ABORTED) return
    const token = this.token
    const wasPlaying = this.playing || this.loading
    const at = this.position
    this.loading = false
    this.playing = false
    // The media element does not expose HTTP status codes; ask the server.
    const status = await streamStatus(entry.track.id)
    if (token !== this.token) return // another track was loaded meanwhile
    switch (status) {
      case 401:
        this.error = 'Your session has expired. Sign in again.'
        this.onUnauthorized()
        return
      case 0:
        this.recoverNetwork(token, at, wasPlaying)
        return
      case 404:
        this.error = "This track's file was not found. It may have been moved or deleted; rescan the library."
        break
      case 409:
        this.error = "This track's library folder is no longer configured."
        break
      case 503:
        this.error = 'The library drive is not available. Check that it is plugged in and mounted.'
        break
      case 200:
      case 206: {
        const t = entry.track
        this.error = `This browser cannot play ${t.format.toUpperCase()}${t.codec ? ` (${t.codec})` : ''} files.`
        break
      }
      default:
        this.error = 'Playback failed.'
    }
    if (this.autoAdvanced) this.skipAfterError(token)
  }

  /** Waits for the network, then reloads at the same position. */
  private recoverNetwork(token: number, at: number, resume: boolean): void {
    if (this.retries >= RETRY_LIMIT) {
      this.error = 'Cannot reach the Rewind server. Press play to try again.'
      return
    }
    this.retries++
    this.notice = 'Connection lost. Retrying…'
    const retry = () => {
      window.removeEventListener('online', retry)
      clearTimeout(timer)
      if (token !== this.token) return
      const retries = this.retries
      this.load(this.index, { autoplay: resume, position: at })
      this.retries = retries
      if (!resume) this.notice = ''
    }
    window.addEventListener('online', retry)
    const timer = setTimeout(retry, 2000 * this.retries)
  }

  // --- Persistence ---

  /** Saves the queue soon (debounced). */
  persist(): void {
    if (!this.touched) return
    clearTimeout(this.saveTimer)
    this.saveTimer = setTimeout(() => this.flush(false), SAVE_DELAY_MS)
  }

  private persistSoon(): void {
    if (this.entries.length > 0) this.persist()
  }

  private savePosition(): void {
    this.lastPositionSave = Date.now()
    if (this.entries.length > 0) this.persist()
  }

  /** The state as saved on the server. */
  snapshot(): PlaybackState {
    return {
      queue: this.entries.map((e) => e.track.id),
      original: this.shuffle && this.original ? this.original.map((e) => e.track.id) : undefined,
      index: this.entries.length > 0 ? Math.max(this.index, 0) : -1,
      position_ms: Math.max(0, Math.round(this.position * 1000)),
      shuffle: this.shuffle,
      repeat: this.repeat,
      speed: this.speed,
      context: this.context ?? undefined,
    }
  }

  /** Saves now; keepalive is used while the page closes. */
  flush(keepalive: boolean): void {
    clearTimeout(this.saveTimer)
    if (!this.touched) return
    // Revisions only grow, so a late, older save cannot win on the server.
    this.revision = Math.max(Date.now(), this.revision + 1)
    void savePlayback(this.revision, this.snapshot(), keepalive).catch((err: { status?: number }) => {
      // After sign-out (detached) a late 401 is expected, not an expired session.
      if (err?.status === 401 && this.audio) this.onUnauthorized()
      // 409: a newer state (another tab) was saved; nothing to do.
    })
  }

  /**
   * Restores the saved queue without starting audio (browsers block
   * autoplay, and the user decides when to resume). Ignored if the user
   * already started something.
   */
  async restore(): Promise<void> {
    let saved
    try {
      saved = await getPlayback()
    } catch {
      return
    }
    if (this.touched) return
    const st = saved.state
    this.revision = Math.max(this.revision, saved.revision)
    this.repeat = st.repeat
    this.speed = st.speed || 1
    if (this.audio) this.audio.defaultPlaybackRate = this.speed
    const byId = new Map(saved.tracks.map((t) => [t.id, t]))
    const tracks = st.queue.map((id) => byId.get(id)).filter((t): t is Track => !!t)
    if (tracks.length === 0) return
    const entries = entriesOf(tracks)
    this.entries = entries
    this.context = st.context ?? null
    this.shuffle = st.shuffle
    if (st.shuffle && st.original && st.original.length === entries.length) {
      // Rebuild the original order from the same entries.
      const pool = new Map<number, Entry[]>()
      for (const e of entries) pool.set(e.track.id, [...(pool.get(e.track.id) ?? []), e])
      this.original = st.original.map((id) => pool.get(id)?.shift()).filter((e): e is Entry => !!e)
    } else {
      this.original = st.shuffle ? [...entries] : null
    }
    const index = Math.min(Math.max(st.index, 0), entries.length - 1)
    this.load(index, { autoplay: false, position: st.position_ms / 1000 })
    if (this.current?.available) this.notice = 'Press play to resume.'
  }

  private loadDevice(): void {
    try {
      const raw = JSON.parse(localStorage.getItem(DEVICE_KEY) ?? 'null')
      if (raw && typeof raw === 'object') {
        if (typeof raw.volume === 'number' && raw.volume >= 0 && raw.volume <= 1) this.volume = raw.volume
        if (typeof raw.muted === 'boolean') this.muted = raw.muted
      }
    } catch {
      // Storage may be unavailable; defaults apply.
    }
  }

  private saveDevice(): void {
    try {
      localStorage.setItem(DEVICE_KEY, JSON.stringify({ volume: this.volume, muted: this.muted }))
    } catch {
      // Optional.
    }
  }

  // --- Browser media controls (lock screen, headset keys) ---

  private setupMediaSession(): void {
    const ms = typeof navigator !== 'undefined' ? navigator.mediaSession : undefined
    if (!ms) return
    const handlers: [MediaSessionAction, MediaSessionActionHandler][] = [
      ['play', () => this.play()],
      ['pause', () => this.pause()],
      ['previoustrack', () => this.previous()],
      ['nexttrack', () => this.next()],
      ['seekbackward', () => this.seekBy(-SEEK_STEP_S)],
      ['seekforward', () => this.seekBy(SEEK_STEP_S)],
      ['seekto', (d) => d.seekTime !== undefined && this.seek(d.seekTime)],
    ]
    for (const [action, fn] of handlers) {
      try {
        ms.setActionHandler(action, fn)
      } catch {
        // Unsupported action.
      }
    }
  }

  private updateMediaSession(): void {
    const ms = typeof navigator !== 'undefined' ? navigator.mediaSession : undefined
    const t = this.current
    if (!ms || typeof MediaMetadata === 'undefined') return
    ms.metadata = t
      ? new MediaMetadata({
          title: t.title,
          artist: t.artist ?? '',
          album: t.album ?? '',
          artwork: [{ src: `/api/tracks/${t.id}/artwork?size=large`, sizes: '1000x1000', type: 'image/jpeg' }],
        })
      : null
  }

  /** For "Playing"/"Paused" labels in lists. */
  isCurrentTrack(id: number): boolean {
    return this.current?.id === id
  }

  /** Where playback started, for history. */
  static context(kind: SourceKind, id?: string, name?: string): PlaybackContext {
    return { kind, id, name }
  }
}

/** The app's one player. */
export const player = new Player()
