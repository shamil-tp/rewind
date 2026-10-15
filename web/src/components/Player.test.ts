import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { tick } from 'svelte'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import type { Track } from '../services/api'
import { listening } from '../services/listening.svelte'
import { player } from '../services/player.svelte'
import { ui } from '../services/ui.svelte'
import { json, mockFetch } from '../test/fetch'
import Player from './Player.svelte'

function track(id: number, extra: Partial<Track> = {}): Track {
  return {
    id, root_id: 1, title: `Song ${id}`, artist: 'Band', album: 'Record', duration_ms: 200_000, format: 'flac',
    status: 'present', available: true, metadata_status: 'ok', artists: [{ id: btoa('band'), name: 'Band' }], ...extra,
  }
}

// jsdom has no media engine: stub the methods and emit the events a browser would.
function stubMedia(playResult?: () => Promise<void>) {
  const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(function (this: HTMLMediaElement) {
    if (playResult) return playResult()
    this.dispatchEvent(new Event('play'))
    this.dispatchEvent(new Event('playing'))
    return Promise.resolve()
  })
  const pause = vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(function (this: HTMLMediaElement) {
    this.dispatchEvent(new Event('pause'))
  })
  const load = vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  return { play, pause, load }
}

const audio = () => document.querySelector<HTMLAudioElement>('audio[data-rewind-player]')!

/** Makes currentTime/duration controllable on a jsdom media element. */
function controllable(a: HTMLAudioElement) {
  const state = { currentTime: 0, duration: NaN }
  Object.defineProperty(a, 'currentTime', { configurable: true, get: () => state.currentTime, set: (v: number) => (state.currentTime = v) })
  Object.defineProperty(a, 'duration', { configurable: true, get: () => state.duration })
  Object.defineProperty(a, 'readyState', { configurable: true, get: () => 4 })
  return state
}

function failWith(code: number) {
  Object.defineProperty(audio(), 'error', { configurable: true, value: { code, MEDIA_ERR_ABORTED: 1 } })
  audio().dispatchEvent(new Event('error'))
}

let onUnauthorized: Mock<() => void>
let media: ReturnType<typeof stubMedia>
beforeEach(() => {
  media = stubMedia()
  onUnauthorized = vi.fn<() => void>()
  player.attach(onUnauthorized)
  mockFetch({ 'PUT /api/playback': () => json(200, {}) })
})
afterEach(() => {
  player.detach()
  ui.nowPlayingOpen = ui.queueOpen = false
})

async function start(tracks: Track[] = [track(1), track(2), track(3)], index = 0) {
  render(Player, { props: { onUnauthorized } })
  player.playTracks(tracks, index, { kind: 'album', id: 'x', name: 'Record' })
  await tick()
}

describe('Player: mini player', () => {
  it('stays hidden without a track and never owns audio itself', () => {
    const { container } = render(Player, { props: { onUnauthorized } })
    expect((container.querySelector('section.playbar') as HTMLElement).hidden).toBe(true)
    expect(container.querySelector('audio')).toBeNull()
    expect(document.querySelectorAll('audio[data-rewind-player]')).toHaveLength(1)
  })

  it('shows the track with artwork, and drives play, pause, previous and next', async () => {
    await start()
    const bar = await screen.findByRole('region', { name: 'Player' })
    expect(within(bar).getByText('Song 1')).toBeTruthy()
    expect(within(bar).getByText('Band · Record')).toBeTruthy()
    expect(bar.querySelector('img')?.getAttribute('src')).toBe('/api/tracks/1/artwork?size=small')
    expect(audio().getAttribute('src')).toBe('/api/tracks/1/stream')
    await fireEvent.click(within(bar).getByRole('button', { name: 'Pause' }))
    expect(media.pause).toHaveBeenCalled()
    await fireEvent.click(within(bar).getByRole('button', { name: 'Play' }))
    await fireEvent.click(within(bar).getByRole('button', { name: 'Next track' }))
    expect(within(bar).getByText('Song 2')).toBeTruthy()
    expect(audio().getAttribute('src')).toBe('/api/tracks/2/stream')
    await fireEvent.click(within(bar).getByRole('button', { name: 'Previous track' }))
    expect(within(bar).getByText('Song 1')).toBeTruthy()
  })

  it('shows time and duration, and seeks', async () => {
    await start([track(1)])
    const state = controllable(audio())
    expect(screen.getAllByLabelText('Duration')[0].textContent).toBe('3:20') // indexed duration first
    state.duration = 245
    audio().dispatchEvent(new Event('durationchange'))
    state.currentTime = 61
    audio().dispatchEvent(new Event('timeupdate'))
    await vi.waitFor(() => expect(screen.getAllByLabelText('Elapsed time')[0].textContent).toBe('1:01'))
    expect(screen.getAllByLabelText('Duration')[0].textContent).toBe('4:05')
    await fireEvent.input(screen.getAllByRole('slider', { name: 'Seek' })[0], { target: { value: '120' } })
    expect(state.currentTime).toBe(120)
  })

  it('shows loading while buffering, and a notice when autoplay is blocked', async () => {
    media.play.mockImplementation(() => Promise.reject(new DOMException('blocked', 'NotAllowedError')))
    await start([track(1)])
    expect(await screen.findByText('Press play to start.')).toBeTruthy()
    audio().dispatchEvent(new Event('waiting'))
    expect(await screen.findByText('Loading…')).toBeTruthy()
    expect(screen.getByRole('region', { name: 'Player' }).getAttribute('aria-busy')).toBe('true')
  })

  for (const [status, message] of [
    [404, /file was not found/],
    [409, /no longer configured/],
    [503, /drive is not available/],
    [200, /cannot play FLAC files/],
  ] as const) {
    it(`explains a playback error when the stream answers ${status}`, async () => {
      mockFetch({ 'PUT /api/playback': () => json(200, {}), 'HEAD /api/tracks/1/stream': () => new Response(null, { status }) })
      await start([track(1)])
      failWith(4)
      expect((await screen.findByRole('alert')).textContent).toMatch(message)
    })
  }

  it('reports an expired session', async () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}), 'HEAD /api/tracks/1/stream': () => new Response(null, { status: 401 }) })
    await start([track(1)])
    failWith(2)
    await vi.waitFor(() => expect(onUnauthorized).toHaveBeenCalled())
  })

  it('retries when the server is unreachable', async () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}), 'HEAD /api/tracks/1/stream': () => Promise.reject(new TypeError('offline')) })
    await start([track(1)])
    failWith(2)
    expect(await screen.findByText('Connection lost. Retrying…')).toBeTruthy()
    media.load.mockClear()
    window.dispatchEvent(new Event('online'))
    expect(media.load).toHaveBeenCalled()
    expect(audio().getAttribute('src')).toBe('/api/tracks/1/stream')
  })

  it('ignores aborted loads', async () => {
    await start([track(1)])
    failWith(1)
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('toggles favorite, shuffle, repeat and mute, showing the state in text and pressed buttons', async () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}), 'PUT /api/favorites/tracks/1': () => json(200, {}) })
    await start()
    const bar = screen.getByRole('region', { name: 'Player' })
    const fav = within(bar).getByRole('button', { name: 'Favorite: Song 1' })
    await fireEvent.click(fav)
    await vi.waitFor(() => expect(fav.getAttribute('aria-pressed')).toBe('true'))
    expect(listening.isFavorite(track(1))).toBe(true)

    await fireEvent.click(within(bar).getByRole('button', { name: 'Shuffle' }))
    expect(within(bar).getByRole('button', { name: 'Shuffle' }).getAttribute('aria-pressed')).toBe('true')
    await fireEvent.click(within(bar).getByRole('button', { name: 'Repeat: off' }))
    expect(within(bar).getByRole('button', { name: 'Repeat: all' })).toBeTruthy()
    await fireEvent.click(within(bar).getByRole('button', { name: 'Mute' }))
    expect(audio().muted).toBe(true)
    await fireEvent.input(within(bar).getByRole('slider', { name: 'Volume' }), { target: { value: '0.4' } })
    expect(audio().volume).toBeCloseTo(0.4)
    expect(audio().muted).toBe(false)

    await fireEvent.click(within(bar).getByRole('button', { name: 'Queue' }))
    expect(ui.queueOpen).toBe(true)
  })
})

describe('Player: Now Playing screen', () => {
  it('opens from the mini player without touching playback, and closes with Escape', async () => {
    await start()
    const before = { play: media.play.mock.calls.length, pause: media.pause.mock.calls.length, load: media.load.mock.calls.length }
    const open = screen.getByRole('button', { name: 'Open Now Playing: Song 1' })
    await fireEvent.click(open)
    const dialog = screen.getByRole('dialog', { name: 'Song 1' })
    expect(within(dialog).getByRole('link', { name: 'Band' }).getAttribute('href')).toBe(`#/artists/${btoa('band')}`)
    expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Close Now Playing' }))
    expect(dialog.querySelector('img')?.getAttribute('src')).toBe('/api/tracks/1/artwork?size=large')
    await fireEvent.keyDown(dialog, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    await vi.waitFor(() => expect(document.activeElement).toBe(open))
    expect({ play: media.play.mock.calls.length, pause: media.pause.mock.calls.length, load: media.load.mock.calls.length }).toEqual(before)
    expect(player.playing).toBe(true)
  })

  it('shows the same state as the mini player and controls the same player', async () => {
    await start()
    await fireEvent.click(screen.getByRole('button', { name: 'Open Now Playing: Song 1' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByText('Record · 1 of 3')).toBeTruthy()
    expect(within(dialog).getByLabelText('Remaining time').textContent).toBe('-3:20')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Next track' }))
    expect(within(dialog).getByRole('heading', { name: 'Song 2' })).toBeTruthy()
    await fireEvent.change(within(dialog).getByRole('combobox'), { target: { value: '1.5' } })
    expect(player.speed).toBe(1.5)
    expect(audio().playbackRate).toBe(1.5)
    await fireEvent.click(within(dialog).getByRole('button', { name: '+10s' }))
    expect(player.position).toBe(10)
  })

  it('keeps keyboard focus inside while open', async () => {
    await start()
    await fireEvent.click(screen.getByRole('button', { name: 'Open Now Playing: Song 1' }))
    const dialog = screen.getByRole('dialog')
    const close = within(dialog).getByRole('button', { name: 'Close Now Playing' })
    close.focus()
    await fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true })
    expect(document.activeElement).not.toBe(close)
    expect(dialog.contains(document.activeElement)).toBe(true)
  })

  it('loads lyrics on request', async () => {
    mockFetch({
      'PUT /api/playback': () => json(200, {}),
      'GET /api/tracks/1': () => json(200, { track: { ...track(1), has_lyrics: true, lyrics: 'first line\nsecond line' } }),
    })
    await start([track(1, { has_lyrics: true })])
    await fireEvent.click(screen.getByRole('button', { name: 'Open Now Playing: Song 1' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Lyrics' }))
    expect(await screen.findByText(/first line\s+second line/)).toBeTruthy()
  })

  it('closes when the queue is cleared', async () => {
    await start()
    await fireEvent.click(screen.getByRole('button', { name: 'Open Now Playing: Song 1' }))
    player.clear()
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('opens when the track info is pulled up, and closes when the header is pulled down', async () => {
    await start()
    const open = screen.getByRole('button', { name: 'Open Now Playing: Song 1' })
    await fireEvent.pointerDown(open, { clientY: 500 })
    await fireEvent.pointerUp(open, { clientY: 400 })
    const dialog = screen.getByRole('dialog')
    const head = dialog.querySelector('.screen-head') as HTMLElement
    await fireEvent.pointerDown(head, { clientY: 100 })
    await fireEvent.pointerUp(head, { clientY: 200 })
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
