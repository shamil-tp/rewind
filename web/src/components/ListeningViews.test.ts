import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Track } from '../services/api'
import { listening } from '../services/listening.svelte'
import { player } from '../services/player.svelte'
import { isTyping } from '../services/shortcuts'
import { ui } from '../services/ui.svelte'
import { apiError, json, mockFetch } from '../test/fetch'
import AlbumView from './AlbumView.svelte'
import ArtistView from './ArtistView.svelte'
import BrowseGrid from './BrowseGrid.svelte'
import HistoryView from './HistoryView.svelte'
import MetadataEditor from './MetadataEditor.svelte'
import QueuePanel from './QueuePanel.svelte'

function track(id: number, extra: Partial<Track> = {}): Track {
  return {
    id, root_id: 1, title: `Song ${id}`, artist: 'Band', album: 'Record', duration_ms: 60_000, format: 'mp3',
    status: 'present', available: true, metadata_status: 'ok', artists: [{ id: 'YmFuZA', name: 'Band' }], ...extra,
  }
}

const pg = (items: unknown[], total = items.length) => ({ items, pagination: { page: 1, limit: 50, total, total_pages: 1 } })

beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(function (this: HTMLMediaElement) {
    this.dispatchEvent(new Event('playing'))
    return Promise.resolve()
  })
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  player.attach(() => {})
})
afterEach(() => {
  player.detach()
  ui.queueOpen = false
  ui.editTrackId = null
})

describe('HistoryView', () => {
  const entry = (id: number, t: Track, at = '2026-10-10T12:00:00Z') => ({ id, played_at: at, listened_ms: 60000, track: t })
  const recent = { tracks: [track(1)], albums: [{ id: 'YQ', title: 'Record', artist: 'Band', artists: [], artwork_track_id: 1, last_played_at: '' }], artists: [] }

  function routes(items: unknown[], extra = {}) {
    return {
      'GET /api/history/recent?limit=12': () => json(200, recent),
      'GET /api/history/top?limit=10': () => json(200, { items: [{ ...track(1), play_count: 7 }] }),
      'GET /api/history?page=1&limit=50&order=desc': () => json(200, pg(items)),
      ...extra,
    }
  }

  it('shows recent albums, most played and the history with when each play happened', async () => {
    mockFetch(routes([entry(10, track(1)), entry(11, track(2, { status: 'missing', available: false }))]))
    render(HistoryView, { props: { active: true, onUnauthorized: vi.fn() } })
    expect(await screen.findByText('2 plays')).toBeTruthy()
    expect(screen.getByRole('link', { name: /Record/ }).getAttribute('href')).toBe('#/albums/YQ')
    const top = screen.getByRole('list', { name: 'Most played' })
    expect(within(top).getByText(/7 plays/)).toBeTruthy()
    const all = screen.getByRole('list', { name: 'All plays' })
    expect(within(all).getByText('File missing')).toBeTruthy()
  })

  it('filters by search and date range', async () => {
    const fetch = mockFetch(routes([entry(10, track(1))], {
      'GET /api/history?page=1&limit=50&q=song&order=desc': () => json(200, pg([])),
    }))
    render(HistoryView, { props: { active: true, onUnauthorized: vi.fn() } })
    await screen.findByText('1 play')
    await fireEvent.input(screen.getByLabelText('Search'), { target: { value: 'song' } })
    await fireEvent.submit(screen.getByLabelText('Search').closest('form')!)
    expect(await screen.findByText('No plays match these filters.')).toBeTruthy()
    // A date range: the whole local day of "to" is included.
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (u: string) => (calls.push(String(u)), json(200, pg([])))))
    const from = screen.getByLabelText('From')
    await fireEvent.input(from, { target: { value: '2026-10-01' } })
    await fireEvent.change(from)
    await vi.waitFor(() => expect(calls.some((u) => /from=2026-(09-30|10-01)T/.test(decodeURIComponent(u)))).toBe(true))
    expect(fetch).toBeDefined()
  })

  it('removes selected plays and clears all after confirmation', async () => {
    const fetch = mockFetch(routes([entry(10, track(1)), entry(11, track(2))], {
      'DELETE /api/history': () => json(200, { deleted: 1 }),
    }))
    render(HistoryView, { props: { active: true, onUnauthorized: vi.fn() } })
    await screen.findByText('2 plays')
    await fireEvent.click(screen.getByLabelText('Select Song 2'))
    expect(screen.getByText('1 selected')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Remove from history' }))
    await vi.waitFor(() => expect(fetch.mock.calls.some(([, i]) => i?.method === 'DELETE')).toBe(true))
    const del = fetch.mock.calls.find(([, i]) => i?.method === 'DELETE')!
    expect(JSON.parse(String(del[1]?.body))).toEqual({ ids: [11] })

    await fireEvent.click(screen.getByRole('button', { name: 'Clear all history…' }))
    const confirm = screen.getByRole('alertdialog', { name: 'Clear all history?' })
    await fireEvent.click(within(confirm).getByRole('button', { name: 'Clear all' }))
    await vi.waitFor(() => {
      const bodies = fetch.mock.calls.filter(([, i]) => i?.method === 'DELETE').map(([, i]) => JSON.parse(String(i?.body)))
      expect(bodies).toContainEqual({ all: true })
    })
  })

  it('plays a track from history', async () => {
    mockFetch({ ...routes([entry(10, track(1)), entry(11, track(2))]), 'PUT /api/playback': () => json(200, {}) })
    render(HistoryView, { props: { active: true, onUnauthorized: vi.fn() } })
    const all = await screen.findByRole('list', { name: 'All plays' })
    await fireEvent.click(within(all).getByRole('button', { name: 'Play Song 2 by Band' }))
    expect(player.current?.id).toBe(2)
    expect(player.context?.kind).toBe('history')
  })

  it('shows the empty state', async () => {
    mockFetch({ ...routes([]), 'GET /api/history/recent?limit=12': () => json(200, { tracks: [], albums: [], artists: [] }),
      'GET /api/history/top?limit=10': () => json(200, { items: [] }) })
    render(HistoryView, { props: { active: true, onUnauthorized: vi.fn() } })
    expect(await screen.findByText(/Nothing played yet/)).toBeTruthy()
  })
})

describe('ArtistView', () => {
  const page = {
    artist: {
      id: 'YmFuZA', name: 'Band', track_count: 3, album_count: 1, duration_ms: 180000, artwork_track_id: 1, version: 'v1',
      albums: [{ id: 'YQ', title: 'Record', artist: 'Band', artists: [], year: 2001, track_count: 2, disc_count: 1, duration_ms: 1, unavailable_count: 0, artwork_track_id: 1, added_at: '' }],
      appears_on: [{ id: 'Yg', title: 'Mix', artist: 'Various', artists: [], track_count: 1, disc_count: 1, duration_ms: 1, unavailable_count: 0, artwork_track_id: 3, added_at: '' }],
      tracks: [track(1), track(2), track(3)], tracks_truncated: false,
    },
    top_tracks: [{ ...track(2), play_count: 4 }],
    recent_tracks: [track(1)],
    listening_ms: 600000,
  }

  it('shows albums by year, appearances, top tracks and plays all tracks', async () => {
    mockFetch({ 'GET /api/artists/YmFuZA': () => json(200, page), 'PUT /api/playback': () => json(200, {}) })
    render(ArtistView, { props: { id: 'YmFuZA', active: true, onUnauthorized: vi.fn() } })
    expect(await screen.findByRole('heading', { name: 'Band', level: 1 })).toBeTruthy()
    expect(screen.getByText(/you listened 10:00/)).toBeTruthy()
    expect(screen.getByRole('heading', { name: '2001' })).toBeTruthy()
    expect(screen.getByRole('list', { name: 'Appears on' })).toBeTruthy()
    expect(within(screen.getByRole('list', { name: 'Your top tracks' })).getByText(/4 plays/)).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Play all' }))
    expect(player.entries.map((e) => e.track.id)).toEqual([1, 2, 3])
    expect(player.context).toEqual({ kind: 'artist', id: 'YmFuZA', name: 'Band' })
  })

  it('renames the artist and opens the new page', async () => {
    const fetch = mockFetch({ 'GET /api/artists/YmFuZA': () => json(200, page), 'PATCH /api/artists/YmFuZA/name': () => json(200, { id: 'TmV3' }) })
    render(ArtistView, { props: { id: 'YmFuZA', active: true, onUnauthorized: vi.fn() } })
    await fireEvent.click(await screen.findByRole('button', { name: 'Rename…' }))
    await fireEvent.input(screen.getByLabelText('Artist name'), { target: { value: 'New' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await vi.waitFor(() => expect(location.hash).toBe('#/artists/TmV3'))
    const patch = fetch.mock.calls.find(([, i]) => i?.method === 'PATCH')!
    expect(JSON.parse(String(patch[1]?.body))).toEqual({ version: 'v1', name: 'New' })
    location.hash = ''
  })

  it('says when the artist no longer exists', async () => {
    mockFetch({ 'GET /api/artists/eA': () => apiError(404, 'not_found', 'artist not found') })
    render(ArtistView, { props: { id: 'eA', active: true, onUnauthorized: vi.fn() } })
    expect(await screen.findByRole('heading', { name: 'Artist not found' })).toBeTruthy()
  })
})

describe('AlbumView', () => {
  const album = {
    id: 'YQ', title: 'Record', artist: 'Band', artists: [{ id: 'YmFuZA', name: 'Band' }], year: undefined, track_count: 3, disc_count: 2,
    duration_ms: 180000, unavailable_count: 1, artwork_track_id: 1, added_at: '', version: 'v1', folders: ['MUSIC/Band/Record'],
    tracks: [track(1, { disc_number: 1, track_number: 1 }), track(2, { disc_number: 2, track_number: 1 }), track(3, { disc_number: 2, available: false })],
  }

  it('groups discs, notes missing details and plays next / queues the album', async () => {
    mockFetch({ 'GET /api/albums/YQ': () => json(200, { album }), 'PUT /api/playback': () => json(200, {}) })
    render(AlbumView, { props: { id: 'YQ', active: true, onUnauthorized: vi.fn() } })
    expect(await screen.findByText('Disc 2')).toBeTruthy()
    expect(screen.getByText('Missing details: no year, no genre, 1 without a track number.')).toBeTruthy()
    expect(screen.getByText('1 track is unavailable.')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Add to queue' }))
    expect(player.entries.map((e) => e.track.id)).toEqual([1, 2])
  })

  it('edits the album for all its tracks, and reloads on a conflict', async () => {
    let calls = 0
    const fetch = mockFetch({
      'GET /api/albums/YQ': () => json(200, { album }),
      'PATCH /api/albums/YQ/metadata': () => (++calls === 1 ? apiError(409, 'edit_conflict', 'changed') : json(200, { id: 'YQ' })),
    })
    render(AlbumView, { props: { id: 'YQ', active: true, onUnauthorized: vi.fn() } })
    await fireEvent.click(await screen.findByRole('button', { name: 'Edit album…' }))
    await fireEvent.input(screen.getByLabelText('Genre'), { target: { value: 'Rock' } })
    await fireEvent.input(screen.getByLabelText('Year'), { target: { value: '01' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(screen.getByText('The year must have four digits.')).toBeTruthy()
    await fireEvent.input(screen.getByLabelText('Year'), { target: { value: '2001' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/changed meanwhile/)).toBeTruthy()
    const version = listening.libraryVersion
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await vi.waitFor(() => expect(listening.libraryVersion).toBe(version + 1))
    const patch = fetch.mock.calls.filter(([, i]) => i?.method === 'PATCH').pop()!
    expect(JSON.parse(String(patch[1]?.body))).toEqual({ version: 'v1', changes: { year: 2001, genre: 'Rock' } })
  })
})

describe('BrowseGrid', () => {
  it('lists albums with artwork and searches', async () => {
    const a = { id: 'YQ', title: 'Record', artist: 'Band', artists: [], year: 2001, track_count: 2, disc_count: 1, duration_ms: 120000, unavailable_count: 0, artwork_track_id: 4, added_at: '' }
    mockFetch({
      'GET /api/albums?page=1&limit=60&sort=title&order=asc': () => json(200, pg([a])),
      'GET /api/albums?page=1&limit=60&q=zz&sort=title&order=asc': () => json(200, pg([])),
    })
    const { container } = render(BrowseGrid, { props: { kind: 'albums', active: true, onUnauthorized: vi.fn() } })
    const link = await screen.findByRole('link', { name: /Record/ })
    expect(link.getAttribute('href')).toBe('#/albums/YQ')
    expect(container.querySelector('img')?.getAttribute('src')).toBe('/api/tracks/4/artwork?size=small')
    await fireEvent.input(screen.getByLabelText('Search'), { target: { value: 'zz' } })
    expect(await screen.findByText('No albums match “zz”.', {}, { timeout: 2000 })).toBeTruthy()
  })
})

describe('QueuePanel', () => {
  it('saves the queue as a playlist, once per track', async () => {
    const fetch = mockFetch({
      'PUT /api/playback': () => json(200, {}),
      'POST /api/playlists': () => json(201, { playlist: { id: 9, name: 'Evening', track_count: 0, unavailable_count: 0, duration_ms: 0, created_at: '', updated_at: '' } }),
      'POST /api/playlists/9/tracks': () => json(200, { added: 2, skipped: 0, playlist: {} }),
    })
    player.playTracks([track(1), track(2), track(1)], 0)
    ui.queueOpen = true
    render(QueuePanel, { props: { onUnauthorized: vi.fn() } })
    await fireEvent.input(await screen.findByLabelText('Save as playlist'), { target: { value: 'Evening' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await vi.waitFor(() => expect(listening.announcement).toBe('Saved the queue as “Evening” (2 tracks).'))
    const add = fetch.mock.calls.find(([u]) => String(u) === '/api/playlists/9/tracks')!
    expect(JSON.parse(String(add[1]?.body))).toEqual({ track_ids: [1, 2] })
  })

  it('asks before clearing the queue, and removes single tracks', async () => {
    mockFetch({ 'PUT /api/playback': () => json(200, {}) })
    player.playTracks([track(1), track(2), track(3)], 0)
    ui.queueOpen = true
    render(QueuePanel, { props: { onUnauthorized: vi.fn() } })
    await fireEvent.click(await screen.findByRole('button', { name: 'Remove Song 3 by Band from the queue' }))
    expect(player.entries.map((e) => e.track.id)).toEqual([1, 2])
    await fireEvent.click(screen.getByRole('button', { name: 'Clear queue…' }))
    expect(player.entries).toHaveLength(2)
    await fireEvent.click(screen.getByRole('button', { name: 'Clear queue' }))
    expect(player.entries).toHaveLength(0)
    expect(await screen.findByText(/The queue is empty/)).toBeTruthy()
  })
})

describe('MetadataEditor', () => {
  const fields = [
    { Name: 'title', Kind: 'text', Min: 0, Max: 512, Required: true },
    { Name: 'genre', Kind: 'text', Min: 0, Max: 512, Required: false },
    { Name: 'lyrics', Kind: 'multiline', Min: 0, Max: 20000, Required: false },
  ]
  const md = (over = {}) => ({
    metadata: { track_id: 1, revision: 2, values: { title: 'Edited', genre: null, lyrics: null }, edited: ['title'], from_file: { title: 'Original' }, updated_at: '', ...over },
    fields, track: track(1),
  })

  it('shows the file value of an edited field and restores it', async () => {
    const fetch = mockFetch({
      'GET /api/tracks/1/metadata': () => json(200, md()),
      'POST /api/tracks/1/metadata/revert': () => json(200, md({ values: { title: 'Original', genre: null, lyrics: null }, edited: [], revision: 3 })),
    })
    ui.editTrackId = 1
    render(MetadataEditor, { props: { trackId: 1, onUnauthorized: vi.fn() } })
    expect(await screen.findByText(/In the file: Original/)).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Use file value' }))
    await vi.waitFor(() => expect((screen.getByLabelText('Title') as HTMLInputElement).value).toBe('Original'))
    const post = fetch.mock.calls.find(([, i]) => i?.method === 'POST')!
    expect(JSON.parse(String(post[1]?.body))).toEqual({ revision: 2, fields: ['title'] })
  })

  it('reports a concurrent change and offers a reload; asks before discarding', async () => {
    mockFetch({
      'GET /api/tracks/1/metadata': () => json(200, md()),
      'PATCH /api/tracks/1/metadata': () => apiError(409, 'edit_conflict', 'changed'),
    })
    ui.editTrackId = 1
    render(MetadataEditor, { props: { trackId: 1, onUnauthorized: vi.fn() } })
    await fireEvent.input(await screen.findByLabelText('Genre'), { target: { value: 'Ambient' } })
    await fireEvent.input(screen.getByLabelText('Lyrics'), { target: { value: 'ला ला\r\nla' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/changed meanwhile/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Reload' })).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getByRole('alertdialog', { name: 'Discard changes?' })).toBeTruthy()
    expect(ui.editTrackId).toBe(1)
    await fireEvent.click(screen.getByRole('button', { name: 'Discard' }))
    expect(ui.editTrackId).toBeNull()
  })
})

describe('shortcuts', () => {
  it('knows when the user is typing', () => {
    const text = document.createElement('input')
    const range = Object.assign(document.createElement('input'), { type: 'range' })
    const box = Object.assign(document.createElement('input'), { type: 'checkbox' })
    expect(isTyping(text)).toBe(true)
    expect(isTyping(range)).toBe(true) // ranges use the arrow keys
    expect(isTyping(document.createElement('textarea'))).toBe(true)
    expect(isTyping(box)).toBe(false)
    expect(isTyping(document.createElement('button'))).toBe(false)
    expect(isTyping(document.body)).toBe(false)
  })
})
