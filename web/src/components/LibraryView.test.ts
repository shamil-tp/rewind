import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import type { Track } from '../services/api'
import { apiError, deferred, json, mockFetch } from '../test/fetch'
import LibraryView from './LibraryView.svelte'

// Shapes match the real GET /api/tracks response (see docs/api.md).
function track(id: number, extra: Partial<Track> = {}): Track {
  return {
    id,
    root_id: 1,
    title: `Song ${id}`,
    artist: 'Band',
    album: 'Record',
    duration_ms: 125_000,
    format: 'flac',
    status: 'present',
    available: true,
    metadata_status: 'ok',
    ...extra,
  }
}

function page(items: Track[], total = items.length, p = 1) {
  return json(200, { items, pagination: { page: p, limit: 50, total, total_pages: Math.ceil(total / 50) } })
}

const url = (extra = '') => `GET /api/tracks?page=1&limit=50${extra}&sort=artist&order=asc`

function props(over: Record<string, unknown> = {}) {
  return {
    currentId: null,
    playing: false,
    reloadKey: 0,
    defaultSort: 'artist' as const,
    onSelect: vi.fn(),
    onUnauthorized: vi.fn(),
    ...over,
  }
}

async function rows() {
  const list = await screen.findByRole('list', { name: 'Tracks' })
  // Each row is a play button plus an "add to playlist" button; return the play buttons.
  return within(list)
    .getAllByRole('button')
    .filter((b) => !b.getAttribute('aria-label')?.endsWith(' to a playlist'))
}

describe('LibraryView', () => {
  it('shows loading, then real tracks with missing metadata handled', async () => {
    const pending = deferred<Response>()
    mockFetch({ [url()]: () => pending.promise })
    render(LibraryView, { props: props() })
    expect(screen.getByText('Loading tracks…')).toBeTruthy()
    pending.resolve(page([track(1), track(2, { title: 'No Tags', artist: undefined, album: undefined, duration_ms: undefined })]))
    const r = await rows()
    expect(r).toHaveLength(2)
    expect(screen.getByText('2 tracks')).toBeTruthy()
    expect(within(r[0]).getByText('Band')).toBeTruthy()
    expect(within(r[0]).getByText('2:05')).toBeTruthy()
    // Unknown fields show a dash, not invented values.
    expect(within(r[1]).getAllByText('—')).toHaveLength(2)
    expect(within(r[1]).getByText('--:--')).toBeTruthy()
    expect(r[1].getAttribute('aria-label')).toBe('Play No Tags')
  })

  it('shows the empty-library state', async () => {
    mockFetch({ [url()]: () => page([]) })
    render(LibraryView, { props: props() })
    expect(await screen.findByText('Your library is empty.')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Status' }).getAttribute('href')).toBe('#/status')
  })

  it('shows errors with retry', async () => {
    let fail = true
    mockFetch({ [url()]: () => (fail ? apiError(500, 'internal_error', 'db exploded') : page([track(1)])) })
    render(LibraryView, { props: props() })
    expect((await screen.findByRole('alert')).textContent).toBe('The server had a problem. Try again.')
    fail = false
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await rows()).toHaveLength(1)
  })

  it('reports an expired session', async () => {
    mockFetch({ [url()]: () => apiError(401, 'unauthenticated', 'sign in required') })
    const p = props()
    render(LibraryView, { props: p })
    await vi.waitFor(() => expect(p.onUnauthorized).toHaveBeenCalled())
  })

  it('loads further pages from the API', async () => {
    const fetch = mockFetch({
      [url()]: () => page([track(1)], 2),
      'GET /api/tracks?page=2&limit=50&sort=artist&order=asc': () => page([track(2)], 2, 2),
    })
    render(LibraryView, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Load more (1 left)' }))
    await vi.waitFor(async () => expect(await rows()).toHaveLength(2))
    expect(screen.queryByRole('button', { name: /Load more/ })).toBeNull()
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('debounces search, resets paging, and shows no results', async () => {
    const fetch = mockFetch({
      [url()]: () => page([track(1)], 120),
      [url('&q=moon')]: () => page([track(9, { title: 'Moon' })]),
      [url('&q=zzz')]: () => page([]),
    })
    render(LibraryView, { props: props() })
    await rows()
    const box = screen.getByLabelText('Search')
    for (const v of ['m', 'mo', 'moo', 'moon']) await fireEvent.input(box, { target: { value: v } })
    await vi.waitFor(() => expect(screen.getByText('1 track matching “moon”')).toBeTruthy())
    expect(fetch).toHaveBeenCalledTimes(2) // initial + one debounced search, not one per keystroke
    expect((await rows())[0].getAttribute('aria-label')).toBe('Play Moon by Band')

    await fireEvent.input(box, { target: { value: 'zzz' } })
    await fireEvent.submit(box.closest('form')!) // Enter searches at once
    expect(await screen.findByText('No tracks match “zzz”.')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Clear search' }))
    await vi.waitFor(() => expect(screen.getByText('120 tracks')).toBeTruthy())
  })

  it('ignores stale responses', async () => {
    const slow = deferred<Response>()
    mockFetch({
      [url()]: () => page([track(1)]),
      [url('&q=old')]: () => slow.promise,
      [url('&q=new')]: () => page([track(2, { title: 'Newest' })]),
    })
    render(LibraryView, { props: props() })
    await rows()
    const box = screen.getByLabelText('Search')
    await fireEvent.input(box, { target: { value: 'old' } })
    await fireEvent.submit(box.closest('form')!)
    await fireEvent.input(box, { target: { value: 'new' } })
    await fireEvent.submit(box.closest('form')!)
    await vi.waitFor(async () => expect((await rows())[0].getAttribute('aria-label')).toBe('Play Newest by Band'))
    slow.resolve(page([track(3, { title: 'Stale' })])) // arrives late
    await new Promise((r) => setTimeout(r, 0))
    expect((await rows())[0].getAttribute('aria-label')).toBe('Play Newest by Band')
  })

  it('sorts with the API sort keys', async () => {
    const fetch = mockFetch({
      [url()]: () => page([track(1)]),
      'GET /api/tracks?page=1&limit=50&sort=added&order=desc': () => page([track(5, { title: 'Fresh' })]),
    })
    render(LibraryView, { props: props() })
    await rows()
    await fireEvent.change(screen.getByLabelText('Sort by'), { target: { value: 'added' } })
    await vi.waitFor(async () => expect((await rows())[0].getAttribute('aria-label')).toBe('Play Fresh by Band'))
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('selects tracks and marks the current one with text, not colour only', async () => {
    mockFetch({ [url()]: () => page([track(1), track(2), track(3, { available: false })]) })
    const p = props({ currentId: 2, playing: true })
    const { rerender } = render(LibraryView, { props: p })
    const r = await rows()
    expect(r[1].getAttribute('aria-current')).toBe('true')
    expect(within(r[1]).getByText('Playing')).toBeTruthy()
    await rerender({ ...p, playing: false })
    expect(within(r[1]).getByText('Paused')).toBeTruthy()

    await fireEvent.click(r[0])
    expect(p.onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }))
    expect((r[2] as HTMLButtonElement).disabled).toBe(true)
    expect(r[2].getAttribute('aria-label')).toBe('Play Song 3 by Band (unavailable)')
    await fireEvent.click(r[2])
    expect(p.onSelect).toHaveBeenCalledTimes(1)
  })

  it('reloads when the library changes', async () => {
    let n = 1
    mockFetch({ [url()]: () => page(Array.from({ length: n }, (_, i) => track(i + 1))) })
    const p = props()
    const { rerender } = render(LibraryView, { props: p })
    expect(await rows()).toHaveLength(1)
    n = 3
    await rerender({ ...p, reloadKey: 1 })
    await vi.waitFor(async () => expect(await rows()).toHaveLength(3))
  })

  it('starts with the saved sort and follows a newly saved one', async () => {
    const fetch = mockFetch({
      'GET /api/tracks?page=1&limit=50&sort=added&order=desc': () => page([track(1)]),
      'GET /api/tracks?page=1&limit=50&sort=title&order=asc': () => page([track(2)]),
    })
    const p = props({ defaultSort: 'added' })
    const { rerender } = render(LibraryView, { props: p })
    await rows()
    expect((screen.getByLabelText('Sort by') as HTMLSelectElement).value).toBe('added')
    await rerender({ ...p, defaultSort: 'title' })
    await vi.waitFor(async () => expect((await rows())[0].getAttribute('aria-label')).toBe('Play Song 2 by Band'))
    expect((screen.getByLabelText('Sort by') as HTMLSelectElement).value).toBe('title')
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('adds a track to a playlist', async () => {
    const fetch = mockFetch({
      [url()]: () => page([track(1), track(2)]),
      'GET /api/playlists': () =>
        json(200, {
          items: [
            { id: 4, name: 'Mix', track_count: 3, unavailable_count: 0, duration_ms: 0, created_at: '', updated_at: '' },
            { id: 9, name: 'Calm', track_count: 0, unavailable_count: 0, duration_ms: 0, created_at: '', updated_at: '' },
          ],
        }),
      'POST /api/playlists/9/tracks': () => json(201, { entry: { position: 1, added_at: '', track: track(2) } }),
    })
    const p = props()
    render(LibraryView, { props: p })
    await rows()
    const add = screen.getByRole('button', { name: 'Add Song 2 to a playlist' })
    await fireEvent.click(add)
    expect(add.getAttribute('aria-expanded')).toBe('true')
    const select = (await screen.findByLabelText('Add to playlist')) as HTMLSelectElement
    await vi.waitFor(() => expect(document.activeElement).toBe(select))
    await fireEvent.change(select, { target: { value: '9' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }))
    expect(await screen.findByText('Added “Song 2” to “Calm”.')).toBeTruthy()
    expect(screen.queryByLabelText('Add to playlist')).toBeNull()
    const post = fetch.mock.calls.find(([, i]) => i?.method === 'POST')!
    expect(JSON.parse(String(post[1]?.body))).toEqual({ track_id: 2 })
    // Adding never starts playback.
    expect(p.onSelect).not.toHaveBeenCalled()
  })

  it('reports a duplicate and keeps the panel open', async () => {
    mockFetch({
      [url()]: () => page([track(1)]),
      'GET /api/playlists': () =>
        json(200, { items: [{ id: 4, name: 'Mix', track_count: 1, unavailable_count: 0, duration_ms: 0, created_at: '', updated_at: '' }] }),
      'POST /api/playlists/4/tracks': () => apiError(409, 'already_in_playlist', 'the track is already in this playlist'),
    })
    render(LibraryView, { props: props() })
    await rows()
    await fireEvent.click(screen.getByRole('button', { name: 'Add Song 1 to a playlist' }))
    await screen.findByLabelText('Add to playlist')
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }))
    expect((await screen.findByRole('alert')).textContent).toBe('The track is already in this playlist.')
    expect(screen.getByLabelText('Add to playlist')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByLabelText('Add to playlist')).toBeNull()
  })

  it('points to the playlists page when there are no playlists', async () => {
    mockFetch({ [url()]: () => page([track(1)]), 'GET /api/playlists': () => json(200, { items: [] }) })
    render(LibraryView, { props: props() })
    await rows()
    await fireEvent.click(screen.getByRole('button', { name: 'Add Song 1 to a playlist' }))
    const link = await screen.findByRole('link', { name: 'Create one in Playlists' })
    expect(link.getAttribute('href')).toBe('#/playlists')
  })
})
