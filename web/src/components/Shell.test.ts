import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Track } from '../services/api'
import { player } from '../services/player.svelte'
import { ui } from '../services/ui.svelte'
import { json, mockFetch } from '../test/fetch'
import { themeState } from '../test/themes'
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
  'GET /api/themes': () => json(200, themeState()),
  'GET /api/playback': () => json(200, { revision: 0, tracks: [], state: { queue: [], index: -1, position_ms: 0, shuffle: false, repeat: 'off', speed: 1 } }),
  'PUT /api/playback': () => json(200, {}),
}

const audioEl = () => document.querySelector<HTMLAudioElement>('audio[data-rewind-player]')!

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
    expect(links.map((l) => l.textContent)).toEqual(['Library', 'Albums', 'Artists', 'Playlists', 'Favorites', 'History', 'Settings', 'Status'])
    // Phones get the four main destinations plus More.
    const compact = screen.getByRole('navigation', { name: 'Main (compact)' })
    expect(within(compact).getAllByRole('link').map((l) => l.textContent)).toEqual(['Library', 'Albums', 'Artists', 'Playlists'])
    expect(within(nav).getByRole('link', { name: 'Library' }).getAttribute('aria-current')).toBe('page')
    expect(screen.getByText('owner')).toBeTruthy()
  })

  it('keeps playback across view changes with a single audio element', async () => {
    mockFetch(routes)
    const { container } = render(Shell, { props: props() })
    expect((container.querySelector('section.playbar') as HTMLElement).hidden).toBe(true)
    await fireEvent.click(await screen.findByRole('button', { name: 'Play Song 2 by Band' }))
    const audio = audioEl()
    expect(audio.getAttribute('src')).toBe('/api/tracks/2/stream')
    const bar = screen.getByRole('region', { name: 'Player' })
    expect(within(bar).getByText('Song 2')).toBeTruthy()
    // The list labels the current track.
    expect(await screen.findByText('Playing')).toBeTruthy()

    await go('#/status')
    expect(await screen.findByRole('heading', { name: 'Status' })).toBeTruthy()
    expect(within(screen.getByRole('navigation', { name: 'Main' })).getByRole('link', { name: 'Status' }).getAttribute('aria-current')).toBe('page')
    expect(document.querySelectorAll('audio')).toHaveLength(1)
    expect(audioEl()).toBe(audio)
    expect(audio.getAttribute('src')).toBe('/api/tracks/2/stream')
    expect(within(screen.getByRole('region', { name: 'Player' })).getByText('Song 2')).toBeTruthy()

    await go('#/library')
    expect(audioEl()).toBe(audio)
    // The library view kept its state instead of reloading.
    expect(screen.getByRole('button', { name: 'Play Song 2 by Band' }).getAttribute('aria-current')).toBe('true')
  })

  it('makes the page behind the Now Playing screen inert, and keeps playing when it closes', async () => {
    mockFetch(routes)
    const { container } = render(Shell, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Play Song 2 by Band' }))
    const audio = audioEl()
    const main = container.querySelector('main') as HTMLElement & { inert: boolean }
    const rail = container.querySelector('header.rail') as HTMLElement & { inert: boolean }
    expect(main.inert).toBe(false)

    await fireEvent.click(screen.getByRole('button', { name: 'Open Now Playing: Song 2' }))
    expect(screen.getByRole('dialog', { name: 'Song 2' })).toBeTruthy()
    expect(main.inert).toBe(true)
    expect(rail.inert).toBe(true)

    await fireEvent.click(screen.getByRole('button', { name: 'Close Now Playing' }))
    expect(main.inert).toBe(false)
    expect(audioEl()).toBe(audio)
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
    render(Shell, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Play' }))
    const audio = audioEl()
    expect(audio.getAttribute('src')).toBe('/api/tracks/1/stream')
    const bar = screen.getByRole('region', { name: 'Player' })
    expect(player.context).toEqual({ kind: 'playlist', id: '7', name: 'Trip' })

    // The track ends: the next playable one (3, not the missing 2) starts.
    audio.dispatchEvent(new Event('ended'))
    await vi.waitFor(() => expect(audio.getAttribute('src')).toBe('/api/tracks/3/stream'))
    expect(player.index).toBe(2)
    await vi.waitFor(() => expect((within(bar).getByRole('button', { name: 'Next track' }) as HTMLButtonElement).disabled).toBe(true))

    await fireEvent.click(within(bar).getByRole('button', { name: 'Previous track' }))
    await vi.waitFor(() => expect(audio.getAttribute('src')).toBe('/api/tracks/1/stream'))
    expect(document.querySelectorAll('audio')).toHaveLength(1)

    // Choosing a library track plays the library list instead.
    await go('#/library')
    await fireEvent.click(await screen.findByRole('button', { name: 'Play Song 2 by Band' }))
    expect(audio.getAttribute('src')).toBe('/api/tracks/2/stream')
    expect(player.context?.kind).toBe('library')
  })

  it('stops at the end of a track when continuing playlists is off', async () => {
    location.hash = '#/playlists/7'
    mockFetch(playlistRoutes(false))
    render(Shell, { props: props() })
    // Wait until the saved preference (off) has loaded.
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Play Song 1 by Band' })).toBeTruthy())
    await fireEvent.click(await screen.findByRole('button', { name: 'Play' }))
    const audio = audioEl()
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

  describe('listening experience', () => {
    const album = {
      id: 'YmFuZB9yZWNvcmQ', title: 'Record', artist: 'Band', artists: [{ id: 'YmFuZA', name: 'Band' }], year: 2001,
      track_count: 2, disc_count: 1, duration_ms: 120_000, unavailable_count: 0, artwork_track_id: 1, added_at: '',
    }
    const albumRoutes = {
      ...routes,
      'GET /api/albums?page=1&limit=60&sort=title&order=asc': () =>
        json(200, { items: [album], pagination: { page: 1, limit: 60, total: 1, total_pages: 1 } }),
      'GET /api/albums/YmFuZB9yZWNvcmQ': () =>
        json(200, { album: { ...album, version: 'v1', folders: ['MUSIC/Band/Record'], tracks: [track(1), track(2)] } }),
    }

    it('plays an album from its page, and keeps playing while browsing', async () => {
      location.hash = '#/albums'
      mockFetch(albumRoutes)
      render(Shell, { props: props() })
      await fireEvent.click(await screen.findByRole('link', { name: /Record/ }))
      await go(`#/albums/${album.id}`)
      expect(await screen.findByRole('heading', { name: 'Record', level: 1 })).toBeTruthy()
      expect(screen.getByText('Folder: MUSIC/Band/Record')).toBeTruthy()
      await fireEvent.click(screen.getByRole('button', { name: 'Play' }))
      expect(audioEl().getAttribute('src')).toBe('/api/tracks/1/stream')
      expect(player.context).toEqual({ kind: 'album', id: album.id, name: 'Record' })
      const audio = audioEl()
      await go('#/library')
      await go('#/history')
      expect(audioEl()).toBe(audio)
      expect(audio.getAttribute('src')).toBe('/api/tracks/1/stream')
      expect(player.playing).toBe(true)
    })

    it('restores the saved queue after a reload without starting audio', async () => {
      const play = HTMLMediaElement.prototype.play as unknown as ReturnType<typeof vi.fn>
      mockFetch({
        ...routes,
        'GET /api/playback': () =>
          json(200, { revision: 3, tracks: [track(1), track(2)], state: { queue: [1, 2], index: 1, position_ms: 30_000, shuffle: false, repeat: 'all', speed: 1 } }),
      })
      render(Shell, { props: props() })
      const bar = await screen.findByRole('region', { name: 'Player' })
      await vi.waitFor(() => expect(within(bar).getByText('Song 2')).toBeTruthy())
      expect(within(bar).getByText('Press play to resume.')).toBeTruthy()
      expect(within(bar).getByLabelText('Elapsed time').textContent).toBe('0:30')
      expect(play).not.toHaveBeenCalled()
      expect(player.repeat).toBe('all')
    })

    it('handles keyboard shortcuts, but not while typing', async () => {
      mockFetch(routes)
      render(Shell, { props: props() })
      await fireEvent.click(await screen.findByRole('button', { name: 'Play Song 1 by Band' }))
      await fireEvent.keyDown(document.body, { key: ' ' })
      expect(player.playing).toBe(false)
      await fireEvent.keyDown(document.body, { key: ' ' })
      expect(player.playing).toBe(true)
      await fireEvent.keyDown(document.body, { key: 's' })
      expect(player.shuffle).toBe(true)
      await fireEvent.keyDown(document.body, { key: 'r' })
      expect(player.repeat).toBe('all')
      await fireEvent.keyDown(document.body, { key: 'm' })
      expect(player.muted).toBe(true)
      // Typing in the search box does not trigger shortcuts.
      const search = screen.getByLabelText('Search')
      await fireEvent.keyDown(search, { key: 'm' })
      expect(player.muted).toBe(true)
      await fireEvent.keyDown(document.body, { key: 'q' })
      expect(screen.getByRole('dialog', { name: 'Queue' })).toBeTruthy()
      await fireEvent.keyDown(document.body, { key: 'Escape' })
      expect(screen.queryByRole('dialog', { name: 'Queue' })).toBeNull()
      await fireEvent.keyDown(document.body, { key: '?' })
      expect(screen.getByRole('dialog', { name: 'Keyboard shortcuts' })).toBeTruthy()
      // While a dialog is open, only Escape works.
      await fireEvent.keyDown(document.body, { key: 'm' })
      expect(player.muted).toBe(true)
      await fireEvent.keyDown(document.body, { key: 'Escape' })
      expect(ui.shortcutsOpen).toBe(false)
      await fireEvent.keyDown(document.body, { key: '/' })
      await vi.waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText('Search')))
    })

    it('adds to the queue, reorders upcoming tracks and keeps the current one', async () => {
      mockFetch({
        ...routes,
        'GET /api/tracks?page=1&limit=50&sort=artist&order=asc': () =>
          json(200, { items: [track(1), track(2), track(3)], pagination: { page: 1, limit: 50, total: 3, total_pages: 1 } }),
      })
      render(Shell, { props: props() })
      await fireEvent.click(await screen.findByRole('button', { name: 'Play Song 1 by Band' }))
      await fireEvent.click(screen.getByRole('button', { name: 'More actions for Song 1' }))
      await fireEvent.click(screen.getByRole('menuitem', { name: 'Add to queue' }))
      await fireEvent.click(within(screen.getByRole('region', { name: 'Player' })).getByRole('button', { name: 'Queue' }))
      const queue = screen.getByRole('dialog', { name: 'Queue' })
      const upNext = within(queue).getByRole('list', { name: 'Up next' })
      expect(within(upNext).getAllByRole('button', { name: /now$/ }).map((b) => b.getAttribute('aria-label'))).toEqual([
        'Play Song 2 by Band now', 'Play Song 3 by Band now', 'Play Song 1 by Band now',
      ])
      await fireEvent.click(within(queue).getByRole('button', { name: 'Move Song 1 by Band up' }))
      expect(player.entries.map((e) => e.track.id)).toEqual([1, 2, 1, 3])
      expect(player.index).toBe(0)
      expect(audioEl().getAttribute('src')).toBe('/api/tracks/1/stream')
    })

    it('edits track details and refreshes the library', async () => {
      let title = 'Song 1'
      const fetch = mockFetch({
        ...routes,
        'GET /api/tracks?page=1&limit=50&sort=artist&order=asc': () =>
          json(200, { items: [{ ...track(1), title }, track(2)], pagination: { page: 1, limit: 50, total: 2, total_pages: 1 } }),
        'GET /api/tracks/1/metadata': () =>
          json(200, {
            metadata: { track_id: 1, revision: 4, values: { title: 'Song 1', artist: 'Band', year: null }, edited: [], from_file: {}, updated_at: '' },
            fields: [
              { Name: 'title', Kind: 'text', Min: 0, Max: 512, Required: true },
              { Name: 'artist', Kind: 'text', Min: 0, Max: 512, Required: false },
              { Name: 'year', Kind: 'number', Min: 1000, Max: 9999, Required: false },
            ],
            track: track(1),
          }),
        'PATCH /api/tracks/1/metadata': () => {
          title = 'Première'
          return json(200, {
            metadata: { track_id: 1, revision: 5, values: { title, artist: 'Band', year: 1999 }, edited: ['title', 'year'], from_file: { title: 'Song 1', year: null }, updated_at: '' },
            fields: [{ Name: 'title', Kind: 'text', Min: 0, Max: 512, Required: true }],
            track: { ...track(1), title },
          })
        },
      })
      render(Shell, { props: props() })
      await fireEvent.click(await screen.findByRole('button', { name: 'More actions for Song 1' }))
      await fireEvent.click(screen.getByRole('menuitem', { name: 'Edit details…' }))
      const dialog = await screen.findByRole('dialog', { name: 'Edit details' })
      expect(within(dialog).getByText(/Your music files are not modified/)).toBeTruthy()
      await fireEvent.input(await within(dialog).findByLabelText('Year'), { target: { value: '99' } })
      expect(within(dialog).getByText(/whole number from 1000 to 9999/)).toBeTruthy()
      expect((within(dialog).getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true)
      await fireEvent.input(within(dialog).getByLabelText('Year'), { target: { value: '1999' } })
      await fireEvent.input(within(dialog).getByLabelText('Title'), { target: { value: 'Première' } })
      expect(within(dialog).getByText('Unsaved changes.')).toBeTruthy()
      await fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
      const patch = fetch.mock.calls.find(([, i]) => i?.method === 'PATCH')!
      expect(JSON.parse(String(patch[1]?.body))).toEqual({ revision: 4, changes: { title: 'Première', year: 1999 } })
      // The library reloads and shows the new title.
      expect(await screen.findByRole('button', { name: 'Play Première by Band' })).toBeTruthy()
    })
  })

  describe('themes', () => {
    const token = (name: string) => document.documentElement.style.getPropertyValue(name)
    const bg = () => token('--rw-color-bg')
    const hint = () => JSON.parse(localStorage.getItem('rewind.theme') ?? 'null')
    afterEach(() => {
      document.documentElement.removeAttribute('style')
      delete document.documentElement.dataset.theme
      localStorage.clear()
    })

    it('applies the active theme once themes load, and remembers it for the next start', async () => {
      mockFetch({ ...routes, 'GET /api/themes': () => json(200, themeState('light')) })
      render(Shell, { props: props() })
      await vi.waitFor(() => expect(bg()).toBe('#f5f5f0'))
      expect(document.documentElement.dataset.theme).toBe('light')
      expect(hint()).toMatchObject({ v: 2, theme: 'light', values: { 'color.background': '#f5f5f0' } })
    })

    it('applies the saved customization of the active theme', async () => {
      mockFetch({
        ...routes,
        'GET /api/themes': () =>
          json(200, themeState('rewind-dark', { 'rewind-dark': { customization: { 'color.primary': '#80b3ff', 'radius.button': 4 } } })),
      })
      render(Shell, { props: props() })
      await vi.waitFor(() => expect(token('--rw-color-primary')).toBe('#80b3ff'))
      expect(token('--rw-radius-button')).toBe('4px')
      expect(hint().values['radius.button']).toBe(4)
    })

    it('keeps colors chosen before per-theme customization: buttons follow a custom accent', async () => {
      // The server reports the earlier preferences as a "legacy" customization.
      const legacy = { 'color.accent': '#ff8800', 'color.primary': '#ff8800' }
      mockFetch({
        ...routes,
        'GET /api/themes': () => json(200, themeState('rewind-dark', { 'rewind-dark': { customization: legacy, customization_source: 'legacy' } })),
      })
      render(Shell, { props: props() })
      await vi.waitFor(() => expect(token('--rw-color-accent')).toBe('#ff8800'))
      expect(token('--rw-color-primary')).toBe('#ff8800')
    })

    it('uses Rewind Dark when the active theme is not available', async () => {
      mockFetch({ ...routes, 'GET /api/themes': () => json(200, { ...themeState(), active: 'solarized' }) })
      render(Shell, { props: props() })
      await vi.waitFor(() => expect(document.documentElement.dataset.theme).toBe('rewind-dark'))
      expect(bg()).toBe('#0b0b0b')
    })

    it('keeps the current colors when themes cannot be loaded', async () => {
      document.documentElement.style.setProperty('--rw-color-bg', '#000000') // e.g. the remembered OLED theme
      mockFetch({ ...routes, 'GET /api/themes': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
      render(Shell, { props: props() })
      await screen.findByRole('button', { name: 'Play Song 1 by Band' })
      await new Promise((r) => setTimeout(r, 0))
      expect(bg()).toBe('#000000')
      expect(localStorage.getItem('rewind.theme')).toBeNull()
    })

    it('switches themes from Settings without interrupting playback', async () => {
      mockFetch({ ...routes, 'PUT /api/themes/active': () => json(200, themeState('light')) })
      render(Shell, { props: props() })
      await fireEvent.click(await screen.findByRole('button', { name: 'Play Song 2 by Band' }))
      const audio = audioEl()
      const pause = HTMLMediaElement.prototype.pause as unknown as ReturnType<typeof vi.fn>
      const load = HTMLMediaElement.prototype.load as unknown as ReturnType<typeof vi.fn>
      const [pauses, loads] = [pause.mock.calls.length, load.mock.calls.length]

      await go('#/settings')
      await fireEvent.click(within(screen.getByRole('listitem', { name: 'Rewind Light' })).getByRole('button', { name: 'Apply' }))
      expect(await screen.findByText('Applied: Rewind Light.')).toBeTruthy()
      expect(bg()).toBe('#f5f5f0')
      expect(hint().theme).toBe('light')

      // Same element, same source, never paused or reloaded.
      expect(document.querySelectorAll('audio')).toHaveLength(1)
      expect(audioEl()).toBe(audio)
      expect(audio.getAttribute('src')).toBe('/api/tracks/2/stream')
      expect(pause.mock.calls.length).toBe(pauses)
      expect(load.mock.calls.length).toBe(loads)
      expect(within(screen.getByRole('region', { name: 'Player' })).getByRole('button', { name: 'Pause' })).toBeTruthy()

      // The themed library is the same component, unchanged.
      await go('#/library')
      expect(screen.getByRole('button', { name: 'Play Song 2 by Band' }).getAttribute('aria-current')).toBe('true')
    })

    it('keeps the saved theme if applying fails', async () => {
      mockFetch({ ...routes, 'PUT /api/themes/active': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
      render(Shell, { props: props() })
      await vi.waitFor(() => expect(bg()).toBe('#0b0b0b'))
      await go('#/settings')
      await fireEvent.click(within(screen.getByRole('listitem', { name: 'Rewind OLED' })).getByRole('button', { name: 'Apply' }))
      expect(await screen.findByText(/Theme not applied/)).toBeTruthy()
      expect(bg()).toBe('#0b0b0b')
      expect(hint().theme).toBe('rewind-dark')
    })

    it('previews unsaved edits across the app only while Settings is open, never remembering them', async () => {
      mockFetch(routes)
      render(Shell, { props: props() })
      await vi.waitFor(() => expect(bg()).toBe('#0b0b0b'))
      await go('#/settings')
      await fireEvent.click(within(await screen.findByRole('listitem', { name: 'Rewind Light' })).getByRole('button', { name: 'Customize' }))
      // Opening the editor previews its theme across the app.
      expect(bg()).toBe('#f5f5f0')
      await fireEvent.input(screen.getByLabelText('Button radius'), { target: { value: '2' } })
      expect(token('--rw-radius-button')).toBe('2px')
      expect(hint().theme).toBe('rewind-dark')

      // Other pages show the saved theme; the edits wait in Settings.
      await go('#/library')
      expect(bg()).toBe('#0b0b0b')
      expect(token('--rw-radius-button')).toBe('10px')
      await go('#/settings')
      expect(token('--rw-radius-button')).toBe('2px')

      // Discarding returns to the saved theme.
      await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
      await fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }))
      expect(bg()).toBe('#0b0b0b')
      expect(token('--rw-radius-button')).toBe('10px')
    })
  })
})
