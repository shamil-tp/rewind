import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Track } from '../services/api'
import { json, mockFetch } from '../test/fetch'
import Shell from './Shell.svelte'

function track(id: number): Track {
  return {
    id, root_id: 1, title: `Song ${id}`, artist: 'Band', album: 'Record', duration_ms: 60_000,
    format: 'mp3', status: 'present', available: true, metadata_status: 'ok',
  }
}

const routes = {
  'GET /api/tracks?page=1&limit=50&sort=artist&order=asc': () =>
    json(200, { items: [track(1), track(2)], pagination: { page: 1, limit: 50, total: 2, total_pages: 1 } }),
  'GET /api/library': () =>
    json(200, {
      tracks: 2, available_tracks: 2, missing_tracks: 0, artists: 1, albums: 1, total_duration_ms: 120_000,
      roots: { total: 1, available: 1, unavailable: 0, pending: 0, disabled: 0 }, scan: { running: false, active: [], queued: [] },
    }),
  'GET /api/library/roots': () => json(200, { items: [] }),
  'GET /api/preferences': () => json(200, { preferences: { library_sort: 'artist', autoplay_next: true } }),
  'GET /api/plugins': () => json(200, { configured: false, api_version: 1, items: [] }),
}

function stubMedia() {
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(function (this: HTMLMediaElement) {
    this.dispatchEvent(new Event('play'))
    return Promise.resolve()
  })
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(function (this: HTMLMediaElement) {
    this.dispatchEvent(new Event('pause'))
  })
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
}

function props() {
  return { user: { id: 1, username: 'owner' }, signingOut: false, signOutError: '', onSignOut: vi.fn(), onUnauthorized: vi.fn() }
}

async function go(hash: string) {
  location.hash = hash
  window.dispatchEvent(new HashChangeEvent('hashchange'))
  await new Promise((r) => setTimeout(r, 0))
}

describe('Shell', () => {
  beforeEach(() => {
    location.hash = ''
    stubMedia()
  })
  afterEach(() => {
    location.hash = ''
  })

  it('shows only implemented navigation and marks the current page', async () => {
    mockFetch(routes)
    render(Shell, { props: props() })
    const nav = screen.getByRole('navigation', { name: 'Main' })
    const links = within(nav).getAllByRole('link')
    expect(links.map((l) => l.textContent)).toEqual(['Library', 'Playlists', 'Settings', 'Status'])
    expect(within(nav).getByRole('link', { name: 'Library' }).getAttribute('aria-current')).toBe('page')
    expect(screen.getByText('owner')).toBeTruthy()
  })

  it('keeps playback across view changes with a single audio element', async () => {
    mockFetch(routes)
    const { container } = render(Shell, { props: props() })
    expect((container.querySelector('section.playbar') as HTMLElement).hidden).toBe(true)
    await fireEvent.click(await screen.findByRole('button', { name: 'Play Song 2 by Band' }))
    const audio = container.querySelector('audio')!
    expect(audio.getAttribute('src')).toBe('/api/tracks/2/stream')
    const bar = screen.getByRole('region', { name: 'Player' })
    expect(within(bar).getByText('Song 2')).toBeTruthy()
    // The list labels the current track.
    expect(await screen.findByText('Playing')).toBeTruthy()

    await go('#/status')
    expect(await screen.findByRole('heading', { name: 'Status' })).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Status' }).getAttribute('aria-current')).toBe('page')
    expect(container.querySelectorAll('audio')).toHaveLength(1)
    expect(container.querySelector('audio')).toBe(audio)
    expect(audio.getAttribute('src')).toBe('/api/tracks/2/stream')
    expect(within(screen.getByRole('region', { name: 'Player' })).getByText('Song 2')).toBeTruthy()

    await go('#/library')
    expect(container.querySelector('audio')).toBe(audio)
    // The library view kept its state instead of reloading.
    expect(screen.getByRole('button', { name: 'Play Song 2 by Band' }).getAttribute('aria-current')).toBe('true')
  })

  it('makes the page behind the Now Playing screen inert, and keeps playing when it closes', async () => {
    mockFetch(routes)
    const { container } = render(Shell, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Play Song 2 by Band' }))
    const audio = container.querySelector('audio')!
    const main = container.querySelector('main') as HTMLElement & { inert: boolean }
    const rail = container.querySelector('header.rail') as HTMLElement & { inert: boolean }
    expect(main.inert).toBe(false)

    await fireEvent.click(screen.getByRole('button', { name: 'Open Now Playing: Song 2' }))
    expect(screen.getByRole('dialog', { name: 'Song 2' })).toBeTruthy()
    expect(main.inert).toBe(true)
    expect(rail.inert).toBe(true)

    await fireEvent.click(screen.getByRole('button', { name: 'Close Now Playing' }))
    expect(main.inert).toBe(false)
    expect(container.querySelector('audio')).toBe(audio)
    expect(audio.getAttribute('src')).toBe('/api/tracks/2/stream')
    expect(within(screen.getByRole('region', { name: 'Player' })).getByRole('button', { name: 'Pause' })).toBeTruthy()
  })

  it('moves focus to the page heading on navigation', async () => {
    mockFetch(routes)
    render(Shell, { props: props() })
    await go('#/status')
    await vi.waitFor(() => expect(document.activeElement?.id).toBe('status-title'))
  })

  it('signs out through the parent', async () => {
    mockFetch(routes)
    const p = props()
    render(Shell, { props: p })
    await fireEvent.click(screen.getByRole('button', { name: 'Sign out' }))
    expect(p.onSignOut).toHaveBeenCalled()
  })

  it('reports an expired session from the library', async () => {
    mockFetch({
      'GET /api/preferences': routes['GET /api/preferences'],
      'GET /api/tracks?page=1&limit=50&sort=artist&order=asc': () => json(401, { error: { code: 'unauthenticated', message: 'sign in required' } }),
    })
    const p = props()
    render(Shell, { props: p })
    await vi.waitFor(() => expect(p.onUnauthorized).toHaveBeenCalled())
  })

  function playlistRoutes(autoplay: boolean) {
    const entries = [track(1), { ...track(2), available: false, status: 'missing' as const }, track(3)].map((t, i) => ({
      position: i + 1,
      added_at: '',
      track: t,
    }))
    return {
      ...routes,
      'GET /api/preferences': () => json(200, { preferences: { library_sort: 'artist', autoplay_next: autoplay } }),
      'GET /api/playlists/7': () =>
        json(200, {
          playlist: { id: 7, name: 'Trip', track_count: 3, unavailable_count: 1, duration_ms: 180_000, created_at: '', updated_at: '', entries },
        }),
    }
  }

  it('plays a playlist in order through the one player, skipping unavailable tracks', async () => {
    location.hash = '#/playlists/7'
    mockFetch(playlistRoutes(true))
    const { container } = render(Shell, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Play' }))
    const audio = container.querySelector('audio')!
    expect(audio.getAttribute('src')).toBe('/api/tracks/1/stream')
    const bar = screen.getByRole('region', { name: 'Player' })
    expect(within(bar).getByText('Trip · 1 of 3')).toBeTruthy()

    // The track ends: the next playable one (3, not the missing 2) starts.
    audio.dispatchEvent(new Event('ended'))
    await vi.waitFor(() => expect(audio.getAttribute('src')).toBe('/api/tracks/3/stream'))
    expect(within(bar).getByText('Trip · 3 of 3')).toBeTruthy()
    expect((within(bar).getByRole('button', { name: 'Next track' }) as HTMLButtonElement).disabled).toBe(true)

    await fireEvent.click(within(bar).getByRole('button', { name: 'Previous track' }))
    await vi.waitFor(() => expect(audio.getAttribute('src')).toBe('/api/tracks/1/stream'))
    expect(container.querySelectorAll('audio')).toHaveLength(1)

    // Choosing a library track leaves the playlist.
    await go('#/library')
    await fireEvent.click(await screen.findByRole('button', { name: 'Play Song 2 by Band' }))
    expect(audio.getAttribute('src')).toBe('/api/tracks/2/stream')
    expect(within(bar).queryByRole('button', { name: 'Next track' })).toBeNull()
  })

  it('stops at the end of a track when continuing playlists is off', async () => {
    location.hash = '#/playlists/7'
    mockFetch(playlistRoutes(false))
    const { container } = render(Shell, { props: props() })
    // Wait until the saved preference (off) has loaded.
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Play Song 1 by Band' })).toBeTruthy())
    await fireEvent.click(await screen.findByRole('button', { name: 'Play' }))
    const audio = container.querySelector('audio')!
    audio.dispatchEvent(new Event('ended'))
    await new Promise((r) => setTimeout(r, 0))
    expect(audio.getAttribute('src')).toBe('/api/tracks/1/stream')
    // Next still works by hand.
    await fireEvent.click(screen.getByRole('button', { name: 'Next track' }))
    expect(audio.getAttribute('src')).toBe('/api/tracks/3/stream')
  })

  it('uses defaults when preferences cannot be loaded, and says so in Settings', async () => {
    mockFetch({ ...routes, 'GET /api/preferences': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
    render(Shell, { props: props() })
    expect(await screen.findByRole('button', { name: 'Play Song 1 by Band' })).toBeTruthy()
    await go('#/settings')
    expect((await screen.findByRole('alert')).textContent).toContain('Could not load your settings')
  })

  it('applies a saved library sort at once', async () => {
    const fetch = mockFetch({
      ...routes,
      'PATCH /api/preferences': () => json(200, { preferences: { library_sort: 'title', autoplay_next: true } }),
      'GET /api/tracks?page=1&limit=50&sort=title&order=asc': () =>
        json(200, { items: [track(9)], pagination: { page: 1, limit: 50, total: 1, total_pages: 1 } }),
    })
    render(Shell, { props: props() })
    await screen.findByRole('button', { name: 'Play Song 1 by Band' })
    await go('#/settings')
    await fireEvent.change(await screen.findByLabelText('Library sort order'), { target: { value: 'title' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Settings saved.')).toBeTruthy()
    await go('#/library')
    expect(await screen.findByRole('button', { name: 'Play Song 9 by Band' })).toBeTruthy()
    expect(fetch.mock.calls.filter(([u]) => String(u).startsWith('/api/tracks'))).toHaveLength(2)
  })

  describe('themes', () => {
    const bg = () => document.documentElement.style.getPropertyValue('--rw-color-bg')
    afterEach(() => {
      document.documentElement.removeAttribute('style')
      delete document.documentElement.dataset.theme
      localStorage.clear()
    })

    function withTheme(theme: string, extra: Record<string, unknown> = {}) {
      return {
        ...routes,
        'GET /api/preferences': () => json(200, { preferences: { library_sort: 'artist', autoplay_next: true, theme, accent_color: '' } }),
        ...extra,
      }
    }

    it('applies the saved theme once preferences load, and remembers it for the next start', async () => {
      mockFetch(withTheme('light'))
      render(Shell, { props: props() })
      await vi.waitFor(() => expect(bg()).toBe('#f5f5f0'))
      expect(document.documentElement.dataset.theme).toBe('light')
      expect(JSON.parse(localStorage.getItem('rewind.theme')!)).toEqual({ theme: 'light', primary: '', secondary: '', accent: '' })
    })

    it('applies saved primary, secondary and accent colors, and remembers them', async () => {
      mockFetch({
        ...routes,
        'GET /api/preferences': () =>
          json(200, {
            preferences: {
              library_sort: 'artist', autoplay_next: true, theme: 'rewind-dark',
              primary_color: '#80b3ff', secondary_color: '#004466', accent_color: '#ff8800',
            },
          }),
      })
      render(Shell, { props: props() })
      const token = (name: string) => document.documentElement.style.getPropertyValue(name)
      await vi.waitFor(() => expect(token('--rw-color-primary')).toBe('#80b3ff'))
      expect(token('--rw-color-secondary')).toBe('#004466')
      expect(token('--rw-color-accent')).toBe('#ff8800')
      expect(JSON.parse(localStorage.getItem('rewind.theme')!)).toEqual({
        theme: 'rewind-dark', primary: '#80b3ff', secondary: '#004466', accent: '#ff8800',
      })
    })

    it('keeps an accent saved by an older version: buttons use it as before', async () => {
      // A server response from before primary_color and secondary_color existed.
      mockFetch({
        ...routes,
        'GET /api/preferences': () =>
          json(200, { preferences: { library_sort: 'artist', autoplay_next: true, theme: 'rewind-dark', accent_color: '#ff8800' } }),
      })
      render(Shell, { props: props() })
      const token = (name: string) => document.documentElement.style.getPropertyValue(name)
      await vi.waitFor(() => expect(token('--rw-color-accent')).toBe('#ff8800'))
      expect(token('--rw-color-primary')).toBe('#ff8800')
    })

    it('uses Rewind Dark for an unknown saved theme', async () => {
      mockFetch(withTheme('solarized'))
      render(Shell, { props: props() })
      await vi.waitFor(() => expect(document.documentElement.dataset.theme).toBe('rewind-dark'))
      expect(bg()).toBe('#0b0b0b')
    })

    it('keeps the current colors when preferences cannot be loaded', async () => {
      document.documentElement.style.setProperty('--rw-color-bg', '#000000') // e.g. the remembered OLED theme
      mockFetch({ ...routes, 'GET /api/preferences': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
      render(Shell, { props: props() })
      await screen.findByRole('button', { name: 'Play Song 1 by Band' })
      expect(bg()).toBe('#000000')
    })

    it('switches themes from Settings without interrupting playback', async () => {
      mockFetch(
        withTheme('rewind-dark', {
          'PATCH /api/preferences': () =>
            json(200, { preferences: { library_sort: 'artist', autoplay_next: true, theme: 'light', accent_color: '' } }),
        }),
      )
      const { container } = render(Shell, { props: props() })
      await fireEvent.click(await screen.findByRole('button', { name: 'Play Song 2 by Band' }))
      const audio = container.querySelector('audio')!
      const pause = HTMLMediaElement.prototype.pause as unknown as ReturnType<typeof vi.fn>
      const load = HTMLMediaElement.prototype.load as unknown as ReturnType<typeof vi.fn>
      const [pauses, loads] = [pause.mock.calls.length, load.mock.calls.length]

      await go('#/settings')
      await fireEvent.click(await screen.findByRole('radio', { name: /Light/ }))
      // Applied at once, before the server answers.
      expect(bg()).toBe('#f5f5f0')
      expect(await screen.findByText('Theme saved: Light.')).toBeTruthy()
      expect(bg()).toBe('#f5f5f0')

      // Same element, same source, never paused or reloaded.
      expect(container.querySelectorAll('audio')).toHaveLength(1)
      expect(container.querySelector('audio')).toBe(audio)
      expect(audio.getAttribute('src')).toBe('/api/tracks/2/stream')
      expect(pause.mock.calls.length).toBe(pauses)
      expect(load.mock.calls.length).toBe(loads)
      expect(within(screen.getByRole('region', { name: 'Player' })).getByRole('button', { name: 'Pause' })).toBeTruthy()

      // The themed library and playlist views are the same components, unchanged.
      await go('#/library')
      expect(screen.getByRole('button', { name: 'Play Song 2 by Band' }).getAttribute('aria-current')).toBe('true')
    })

    it('reverts to the saved theme if saving fails', async () => {
      mockFetch(withTheme('rewind-dark', { 'PATCH /api/preferences': () => json(500, { error: { code: 'internal_error', message: 'x' } }) }))
      render(Shell, { props: props() })
      await vi.waitFor(() => expect(bg()).toBe('#0b0b0b'))
      await go('#/settings')
      await fireEvent.click(await screen.findByRole('radio', { name: /OLED Black/ }))
      expect(await screen.findByText(/Theme not saved/)).toBeTruthy()
      expect(bg()).toBe('#0b0b0b')
      expect(JSON.parse(localStorage.getItem('rewind.theme')!).theme).toBe('rewind-dark')
    })
  })
})
