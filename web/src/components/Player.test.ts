import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import type { Track } from '../services/api'
import { mockFetch } from '../test/fetch'
import Player from './Player.svelte'

function track(id: number, extra: Partial<Track> = {}): Track {
  return {
    id,
    root_id: 1,
    title: `Song ${id}`,
    artist: 'Band',
    album: 'Record',
    duration_ms: 200_000,
    format: 'flac',
    status: 'present',
    available: true,
    metadata_status: 'ok',
    ...extra,
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

/** Makes currentTime/duration controllable on a jsdom media element. */
function controllable(audio: HTMLAudioElement) {
  const state = { currentTime: 0, duration: NaN }
  Object.defineProperty(audio, 'currentTime', {
    configurable: true,
    get: () => state.currentTime,
    set: (v: number) => {
      state.currentTime = v
    },
  })
  Object.defineProperty(audio, 'duration', { configurable: true, get: () => state.duration })
  return state
}

function audioOf(container: HTMLElement): HTMLAudioElement {
  const els = container.querySelectorAll('audio')
  expect(els.length).toBe(1)
  return els[0]
}

function failWith(audio: HTMLAudioElement, code: number) {
  Object.defineProperty(audio, 'error', { configurable: true, value: { code, MEDIA_ERR_ABORTED: 1 } })
  audio.dispatchEvent(new Event('error'))
}

describe('Player', () => {
  it('stays mounted but hidden without a track', () => {
    stubMedia()
    const { container } = render(Player, { props: { track: null, onUnauthorized: vi.fn() } })
    const bar = container.querySelector('section.playbar') as HTMLElement
    expect(bar.hidden).toBe(true)
    audioOf(container) // the single audio element exists already
    expect(screen.queryByRole('button', { name: 'Play' })).toBeNull()
  })

  it('reports play state changes to the parent', async () => {
    stubMedia()
    const onPlayingChange = vi.fn()
    render(Player, { props: { track: track(1), onUnauthorized: vi.fn(), onPlayingChange } })
    await vi.waitFor(() => expect(onPlayingChange).toHaveBeenLastCalledWith(true))
    await fireEvent.click(screen.getByRole('button', { name: 'Pause' }))
    expect(onPlayingChange).toHaveBeenLastCalledWith(false)
  })

  it('selecting a track sets the stream URL and starts playback', async () => {
    const media = stubMedia()
    const { container } = render(Player, { props: { track: track(7), onUnauthorized: vi.fn() } })
    const audio = audioOf(container)
    expect(audio.getAttribute('src')).toBe('/api/tracks/7/stream')
    expect(media.load).toHaveBeenCalled()
    expect(media.play).toHaveBeenCalled()
    expect(screen.getByText('Song 7')).toBeTruthy()
    expect(screen.getByText('Band · Record')).toBeTruthy()
    expect(await screen.findByRole('button', { name: 'Pause' })).toBeTruthy()
  })

  it('play and pause controls drive the audio element', async () => {
    const media = stubMedia()
    render(Player, { props: { track: track(1), onUnauthorized: vi.fn() } })
    await fireEvent.click(await screen.findByRole('button', { name: 'Pause' }))
    expect(media.pause).toHaveBeenCalled()
    await fireEvent.click(await screen.findByRole('button', { name: 'Play' }))
    expect(media.play).toHaveBeenCalledTimes(2)
    expect(await screen.findByRole('button', { name: 'Pause' })).toBeTruthy()
  })

  it('shows current time and duration from the element', async () => {
    stubMedia()
    const { container } = render(Player, { props: { track: track(1, { duration_ms: undefined }), onUnauthorized: vi.fn() } })
    const audio = audioOf(container)
    const state = controllable(audio)
    expect(screen.getByLabelText('Duration').textContent).toBe('--:--')
    state.duration = 245
    await fireEvent(audio, new Event('durationchange'))
    state.currentTime = 62.4
    await fireEvent(audio, new Event('timeupdate'))
    expect(screen.getByLabelText('Duration').textContent).toBe('4:05')
    expect(screen.getByLabelText('Elapsed time').textContent).toBe('1:02')
    expect(screen.getByLabelText('Seek').getAttribute('aria-valuetext')).toBe('1:02 of 4:05')
  })

  it('uses the indexed duration until the element reports one', () => {
    stubMedia()
    render(Player, { props: { track: track(1, { duration_ms: 3_725_000 }), onUnauthorized: vi.fn() } })
    expect(screen.getByLabelText('Duration').textContent).toBe('1:02:05')
  })

  it('seeking sets the playback position', async () => {
    stubMedia()
    const { container } = render(Player, { props: { track: track(1), onUnauthorized: vi.fn() } })
    const state = controllable(audioOf(container))
    await fireEvent.input(screen.getByLabelText('Seek'), { target: { value: '150' } })
    expect(state.currentTime).toBe(150)
    expect(screen.getByLabelText('Elapsed time').textContent).toBe('2:30')
  })

  it('shows loading while buffering', async () => {
    stubMedia(() => new Promise(() => {})) // play() never settles
    const { container } = render(Player, { props: { track: track(1), onUnauthorized: vi.fn() } })
    const audio = audioOf(container)
    await fireEvent(audio, new Event('loadstart'))
    expect(screen.getByRole('status').textContent).toContain('Loading')
    await fireEvent(audio, new Event('canplay'))
    expect(screen.getByRole('status').textContent).not.toContain('Loading')
    await fireEvent(audio, new Event('waiting'))
    expect(screen.getByRole('status').textContent).toContain('Loading')
  })

  it('asks the user to press play when autoplay is blocked', async () => {
    stubMedia(() => Promise.reject(new DOMException('blocked', 'NotAllowedError')))
    render(Player, { props: { track: track(1), onUnauthorized: vi.fn() } })
    expect(await screen.findByText('Press play to start.')).toBeTruthy()
  })

  it.each([
    [404, "This track's file was not found"],
    [503, 'The library drive is not available'],
    [409, 'no longer configured'],
    [200, 'This browser cannot play FLAC (flac) files.'],
  ])('explains a playback error when the stream answers %i', async (status, message) => {
    stubMedia()
    const fetch = mockFetch({ 'HEAD /api/tracks/3/stream': () => new Response(null, { status }) })
    const { container } = render(Player, { props: { track: track(3, { codec: 'flac' }), onUnauthorized: vi.fn() } })
    failWith(audioOf(container), 4)
    expect((await screen.findByRole('alert')).textContent).toContain(message)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect((screen.getByRole('button', { name: 'Play' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('reports an expired session', async () => {
    stubMedia()
    mockFetch({ 'HEAD /api/tracks/3/stream': () => new Response(null, { status: 401 }) })
    const onUnauthorized = vi.fn()
    const { container } = render(Player, { props: { track: track(3), onUnauthorized } })
    failWith(audioOf(container), 2)
    expect((await screen.findByRole('alert')).textContent).toContain('session has expired')
    expect(onUnauthorized).toHaveBeenCalled()
  })

  it('reports an unreachable server', async () => {
    stubMedia()
    mockFetch({
      'HEAD /api/tracks/3/stream': () => {
        throw new TypeError('Failed to fetch')
      },
    })
    const { container } = render(Player, { props: { track: track(3), onUnauthorized: vi.fn() } })
    failWith(audioOf(container), 2)
    expect((await screen.findByRole('alert')).textContent).toContain('Cannot reach the Rewind server')
  })

  it('ignores aborted loads', async () => {
    stubMedia()
    const fetch = mockFetch({})
    const { container } = render(Player, { props: { track: track(3), onUnauthorized: vi.fn() } })
    failWith(audioOf(container), 1) // MEDIA_ERR_ABORTED
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('selecting another track replaces the source on the same element', async () => {
    const media = stubMedia()
    const { container, rerender } = render(Player, { props: { track: track(1), onUnauthorized: vi.fn() } })
    const audio = audioOf(container)
    await rerender({ track: track(2), onUnauthorized: vi.fn() })
    expect(audioOf(container)).toBe(audio)
    expect(audio.getAttribute('src')).toBe('/api/tracks/2/stream')
    expect(screen.getByText('Song 2')).toBeTruthy()
    expect(media.load).toHaveBeenCalledTimes(2)
  })

  it('clears an error when another track is selected', async () => {
    stubMedia()
    mockFetch({ 'HEAD /api/tracks/3/stream': () => new Response(null, { status: 404 }) })
    const { container, rerender } = render(Player, { props: { track: track(3), onUnauthorized: vi.fn() } })
    failWith(audioOf(container), 4)
    await screen.findByRole('alert')
    await rerender({ track: track(4), onUnauthorized: vi.fn() })
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('releases the media when the track is cleared', async () => {
    const media = stubMedia()
    const { container, rerender } = render(Player, { props: { track: track(1), onUnauthorized: vi.fn() } })
    await rerender({ track: null, onUnauthorized: vi.fn() })
    expect(audioOf(container).hasAttribute('src')).toBe(false)
    expect(media.pause).toHaveBeenCalled()
    expect((container.querySelector('section.playbar') as HTMLElement).hidden).toBe(true)
  })

  it('shows no previous/next controls outside a playlist', () => {
    stubMedia()
    render(Player, { props: { track: track(1), onUnauthorized: vi.fn() } })
    expect(screen.queryByRole('button', { name: 'Next track' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Previous track' })).toBeNull()
  })

  it('offers previous/next while a playlist plays and reports the end of a track', async () => {
    stubMedia()
    const onPrevious = vi.fn()
    const onNext = vi.fn()
    const onEnded = vi.fn()
    const { container } = render(Player, {
      props: {
        track: track(1),
        onUnauthorized: vi.fn(),
        onEnded,
        sequence: { label: 'Mix · 1 of 3', hasPrevious: false, hasNext: true, onPrevious, onNext },
      },
    })
    expect(screen.getByText('Mix · 1 of 3')).toBeTruthy()
    const prev = screen.getByRole('button', { name: 'Previous track' }) as HTMLButtonElement
    expect(prev.disabled).toBe(true)
    await fireEvent.click(screen.getByRole('button', { name: 'Next track' }))
    expect(onNext).toHaveBeenCalledTimes(1)
    expect(onPrevious).not.toHaveBeenCalled()

    audioOf(container).dispatchEvent(new Event('ended'))
    expect(onEnded).toHaveBeenCalledTimes(1)
  })
})
