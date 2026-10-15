import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { json, mockFetch } from '../test/fetch'
import type { Track } from './api'
import { Player } from './player.svelte'

function track(id: number, over: Partial<Track> = {}): Track {
  return {
    id, root_id: 1, title: `Song ${id}`, artist: 'Band', format: 'mp3', status: 'present', available: true,
    metadata_status: 'ok', duration_ms: 100_000, ...over,
  }
}

let plays: Mock<(src: string | null) => void>
function stubMedia() {
  plays = vi.fn<(src: string | null) => void>()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(function (this: HTMLMediaElement) {
    plays(this.getAttribute('src'))
    this.dispatchEvent(new Event('play'))
    this.dispatchEvent(new Event('playing'))
    return Promise.resolve()
  })
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(function (this: HTMLMediaElement) {
    this.dispatchEvent(new Event('pause'))
  })
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
}

/** Simulates playback time on the element. */
function setTime(audio: HTMLAudioElement, t: number) {
  Object.defineProperty(audio, 'currentTime', { configurable: true, get: () => t, set: () => {} })
  audio.dispatchEvent(new Event('timeupdate'))
}

const audioEl = () => document.querySelector<HTMLAudioElement>('audio[data-rewind-player]')!
const src = () => audioEl().getAttribute('src')
const flush = () => new Promise((r) => setTimeout(r, 0))

describe('Player', () => {
  let p: Player
  beforeEach(() => {
    stubMedia()
    p = new Player()
    p.attach(() => {})
  })
  afterEach(() => {
    p.detach()
    vi.useRealTimers()
    localStorage.clear()
  })

  it('creates one audio element, however often it is attached', () => {
    p.attach(() => {})
    p.attach(() => {})
    expect(document.querySelectorAll('audio[data-rewind-player]')).toHaveLength(1)
  })

  it('plays a list from the chosen track and steps through it', () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}) })
    p.playTracks([track(1), track(2), track(3)], 1, { kind: 'album', id: 'a' })
    expect(p.current?.id).toBe(2)
    expect(src()).toBe('/api/tracks/2/stream')
    expect(p.playing).toBe(true)
    p.next()
    expect(p.current?.id).toBe(3)
    expect(p.hasNext).toBe(false)
    p.next() // nothing after the last track
    expect(p.current?.id).toBe(3)
    p.previous()
    expect(p.current?.id).toBe(2)
  })

  it('previous restarts the track after 3 seconds', () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}) })
    p.playTracks([track(1), track(2)], 1)
    setTime(audioEl(), 30)
    p.previous()
    expect(p.current?.id).toBe(2)
    expect(p.position).toBe(0)
  })

  it('advances when a track ends, repeats one, and wraps with repeat all', () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}) })
    p.playTracks([track(1), track(2)], 0)
    audioEl().dispatchEvent(new Event('ended'))
    expect(p.current?.id).toBe(2)
    p.cycleRepeat() // all
    audioEl().dispatchEvent(new Event('ended'))
    expect(p.current?.id).toBe(1)
    p.cycleRepeat() // one
    expect(p.repeat).toBe('one')
    plays.mockClear()
    audioEl().dispatchEvent(new Event('ended'))
    expect(p.current?.id).toBe(1)
    expect(plays).toHaveBeenCalledWith('/api/tracks/1/stream')
    p.cycleRepeat() // off
    p.next()
    audioEl().dispatchEvent(new Event('ended'))
    expect(p.current?.id).toBe(2)
    expect(p.notice).toBe('End of the queue.')
  })

  it('skips unavailable tracks', () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}) })
    p.playTracks([track(1, { available: false }), track(2), track(3, { available: false }), track(4)], 0)
    expect(p.current?.id).toBe(2)
    p.next()
    expect(p.current?.id).toBe(4)
  })

  it('play next, add to queue, play now and remove keep the current track defined', () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}) })
    p.playTracks([track(1), track(2)], 0)
    p.addToQueue([track(9)])
    p.playNext([track(5)])
    expect(p.entries.map((e) => e.track.id)).toEqual([1, 5, 2, 9])
    p.moveEntry(3, 1)
    expect(p.entries.map((e) => e.track.id)).toEqual([1, 9, 5, 2])
    expect(p.current?.id).toBe(1)
    // Removing the current entry loads the following one.
    p.removeAt(0)
    expect(p.current?.id).toBe(9)
    expect(src()).toBe('/api/tracks/9/stream')
    p.playNow(track(7))
    expect(p.current?.id).toBe(7)
    expect(p.entries.map((e) => e.track.id)).toEqual([9, 7, 5, 2])
    p.clearUpcoming()
    expect(p.entries.map((e) => e.track.id)).toEqual([9, 7])
    p.clear()
    expect(p.current).toBeNull()
    expect(src()).toBeNull()
  })

  it('shuffle keeps the current track and restores the order when turned off', () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}) })
    const tracks = Array.from({ length: 20 }, (_, i) => track(i + 1))
    p.playTracks(tracks, 3)
    p.toggleShuffle()
    expect(p.current?.id).toBe(4)
    expect(p.entries.map((e) => e.track.id).slice(0, 4)).toEqual([1, 2, 3, 4])
    p.addToQueue([track(99)])
    p.toggleShuffle()
    expect(p.entries.map((e) => e.track.id)).toEqual([...tracks.map((t) => t.id), 99])
    expect(p.current?.id).toBe(4)
    // Shuffle button on a list: random order, everything once.
    p.playTracks(tracks, -1, null, { shuffle: true })
    expect(p.shuffle).toBe(true)
    expect([...p.entries.map((e) => e.track.id)].sort((a, b) => a - b)).toEqual(tracks.map((t) => t.id))
  })

  it('records a qualified play exactly once, counting only played time', async () => {
    const posts: unknown[] = []
    mockFetch({
      'PUT /api/playback': () => json(200, {}),
      'POST /api/history': (init) => {
        posts.push(JSON.parse(String(init?.body)))
        return json(201, { recorded: true })
      },
    })
    p.thresholdPercent = 50
    p.playTracks([track(1, { duration_ms: 20_000 })], 0, { kind: 'playlist', id: '7' })
    const a = audioEl()
    // A seek to the middle is not listening.
    setTime(a, 15)
    expect(posts).toHaveLength(0)
    for (let t = 15.5; t <= 26; t += 0.5) setTime(a, t)
    await flush()
    expect(posts).toHaveLength(1)
    expect(posts[0]).toMatchObject({ track_id: 1, source_kind: 'playlist', source_id: '7' })
    expect((posts[0] as { listened_ms: number }).listened_ms).toBeGreaterThanOrEqual(10_000)
    for (let t = 26; t <= 30; t += 0.5) setTime(a, t)
    await flush()
    expect(posts).toHaveLength(1)
  })

  it('does not record history in privacy mode', async () => {
    const fetch = mockFetch({ 'PUT /api/playback': () => json(200, {}) })
    p.recordHistory = false
    p.playTracks([track(1, { duration_ms: 4000 })], 0)
    for (let t = 0.5; t <= 4; t += 0.5) setTime(audioEl(), t)
    await flush()
    expect(fetch.mock.calls.some(([u]) => String(u) === '/api/history')).toBe(false)
  })

  it('saves the queue (debounced) and restores it without starting audio', async () => {
    vi.useFakeTimers()
    const saved: { revision: number; state: unknown }[] = []
    mockFetch({ 'PUT /api/playback': (init) => (saved.push(JSON.parse(String(init?.body))), json(200, {})) })
    p.playTracks([track(1), track(2)], 1, { kind: 'album', id: 'x', name: 'Rec' })
    p.setSpeed(1.5)
    p.cycleRepeat()
    expect(saved).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(1000)
    expect(saved).toHaveLength(1)
    expect(saved[0].state).toMatchObject({ queue: [1, 2], index: 1, repeat: 'all', speed: 1.5, shuffle: false, context: { kind: 'album', id: 'x' } })
    vi.useRealTimers()

    const q = new Player()
    p.detach()
    stubMedia()
    q.attach(() => {})
    mockFetch({
      'GET /api/playback': () =>
        json(200, { revision: 5, tracks: [track(1), track(2)], state: { queue: [1, 2], index: 1, position_ms: 42000, shuffle: false, repeat: 'one', speed: 1.25 } }),
    })
    await q.restore()
    expect(q.current?.id).toBe(2)
    expect(q.position).toBe(42)
    expect(q.repeat).toBe('one')
    expect(q.speed).toBe(1.25)
    expect(q.playing).toBe(false)
    expect(plays).not.toHaveBeenCalled()
    expect(q.notice).toBe('Press play to resume.')
    p = q
  })

  it('ignores a restore when the user already started playing', async () => {
    let answer!: (r: Response) => void
    mockFetch({ 'GET /api/playback': () => new Promise<Response>((r) => (answer = r)), 'PUT /api/playback': () => json(200, {}) })
    const restoring = p.restore()
    p.playTracks([track(5)], 0)
    answer(json(200, { revision: 1, tracks: [track(1)], state: { queue: [1], index: 0, position_ms: 0, shuffle: false, repeat: 'off', speed: 1 } }))
    await restoring
    expect(p.current?.id).toBe(5)
  })

  it('reports a missing file, and ignores errors of a track that was replaced', async () => {
    let status = 404
    mockFetch({ 'PUT /api/playback': () => json(200, {}), 'HEAD /api/tracks/1/stream': () => new Response(null, { status }) })
    p.playTracks([track(1), track(2)], 0)
    const a = audioEl()
    Object.defineProperty(a, 'error', { configurable: true, get: () => ({ code: 4, MEDIA_ERR_ABORTED: 1 }) })
    a.dispatchEvent(new Event('error'))
    await vi.waitFor(() => expect(p.error).toMatch(/file was not found/))

    // A late error for track 1 after switching to track 2 changes nothing.
    status = 503
    p.next()
    const late = new Promise((r) => setTimeout(r, 0))
    await late
    expect(p.error).toBe('')
    expect(p.current?.id).toBe(2)
  })

  it('remembers volume and mute on this device', () => {
    p.setVolume(0.3)
    p.toggleMute()
    expect(audioEl().volume).toBeCloseTo(0.3)
    expect(audioEl().muted).toBe(true)
    p.detach()
    const q = new Player()
    q.attach(() => {})
    expect(q.volume).toBeCloseTo(0.3)
    expect(q.muted).toBe(true)
    p = q
  })

  it('seeks within the track, and by steps', () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}) })
    p.playTracks([track(1)], 0)
    p.seek(50)
    expect(p.position).toBe(50)
    p.seekBy(-60)
    expect(p.position).toBe(0)
    p.seek(Number.NaN)
    expect(p.position).toBe(0)
  })
})
