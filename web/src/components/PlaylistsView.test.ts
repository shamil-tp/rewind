import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import type { Playlist } from '../services/api'
import { apiError, deferred, json, mockFetch } from '../test/fetch'
import PlaylistsView from './PlaylistsView.svelte'

// Shapes match the real GET /api/playlists response (see docs/api.md).
function playlist(id: number, extra: Partial<Playlist> = {}): Playlist {
  return {
    id,
    name: `List ${id}`,
    track_count: 2,
    unavailable_count: 0,
    duration_ms: 245_000,
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    ...extra,
  }
}

function props(over: Record<string, unknown> = {}) {
  return { playlistId: null, active: true, currentId: null, playing: false, onPlay: vi.fn(), onUnauthorized: vi.fn(), ...over }
}

describe('PlaylistsView', () => {
  it('shows loading, then the playlists with their summaries', async () => {
    const pending = deferred<Response>()
    mockFetch({ 'GET /api/playlists': () => pending.promise })
    render(PlaylistsView, { props: props() })
    expect(screen.getByText('Loading playlists…')).toBeTruthy()
    pending.resolve(json(200, { items: [playlist(1, { name: 'Road trip' }), playlist(2, { track_count: 1, unavailable_count: 1, duration_ms: 0 })] }))

    const list = await screen.findByRole('list', { name: 'Your playlists' })
    const links = within(list).getAllByRole('link')
    expect(links[0].getAttribute('href')).toBe('#/playlists/1')
    expect(links[0].textContent).toContain('Road trip')
    expect(links[0].textContent).toContain('2 tracks · 4:05')
    expect(links[1].textContent).toContain('1 track · 1 unavailable')
  })

  it('explains the empty state', async () => {
    mockFetch({ 'GET /api/playlists': () => json(200, { items: [] }) })
    render(PlaylistsView, { props: props() })
    expect(await screen.findByText('No playlists yet.')).toBeTruthy()
  })

  it('shows load errors with a retry', async () => {
    let fail = true
    mockFetch({
      'GET /api/playlists': () => (fail ? json(500, { error: { code: 'internal_error', message: 'x' } }) : json(200, { items: [playlist(1)] })),
    })
    render(PlaylistsView, { props: props() })
    expect((await screen.findByRole('alert')).textContent).toBe('The server had a problem. Try again.')
    fail = false
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByText('List 1')).toBeTruthy()
  })

  it('creates a playlist and refreshes the list', async () => {
    let items: Playlist[] = []
    const fetch = mockFetch({
      'GET /api/playlists': () => json(200, { items }),
      'POST /api/playlists': (init) => {
        const body = JSON.parse(String(init?.body))
        items = [playlist(7, { name: body.name, track_count: 0, duration_ms: 0 })]
        return json(201, { playlist: items[0] })
      },
    })
    render(PlaylistsView, { props: props() })
    await screen.findByText('No playlists yet.')

    // Client-side check first: nothing is sent for a blank name.
    await fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(screen.getByText('Enter a name.')).toBeTruthy()
    expect(fetch.mock.calls.filter(([, i]) => i?.method === 'POST')).toHaveLength(0)

    await fireEvent.input(screen.getByLabelText('New playlist'), { target: { value: '  Evening  ' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Created “Evening”.')).toBeTruthy()
    const post = fetch.mock.calls.find(([, i]) => i?.method === 'POST')!
    expect(JSON.parse(String(post[1]?.body))).toEqual({ name: 'Evening', description: '' })
    expect(await screen.findByRole('link', { name: /Evening/ })).toBeTruthy()
    expect((screen.getByLabelText('New playlist') as HTMLInputElement).value).toBe('')
  })

  it('shows server validation errors when creating', async () => {
    mockFetch({
      'GET /api/playlists': () => json(200, { items: [playlist(1, { name: 'Mix' })] }),
      'POST /api/playlists': () => apiError(409, 'name_taken', 'a playlist with this name already exists'),
    })
    render(PlaylistsView, { props: props() })
    await screen.findByText('Mix')
    await fireEvent.input(screen.getByLabelText('New playlist'), { target: { value: 'mix' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect((await screen.findByRole('alert')).textContent).toBe('A playlist with this name already exists.')
  })

  it('reports an expired session', async () => {
    mockFetch({ 'GET /api/playlists': () => apiError(401, 'unauthenticated', 'sign in required') })
    const p = props()
    render(PlaylistsView, { props: p })
    await vi.waitFor(() => expect(p.onUnauthorized).toHaveBeenCalled())
  })

  it('does not load while hidden', async () => {
    const fetch = mockFetch({})
    render(PlaylistsView, { props: props({ active: false }) })
    await new Promise((r) => setTimeout(r, 0))
    expect(fetch).not.toHaveBeenCalled()
  })
})
