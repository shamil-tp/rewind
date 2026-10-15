import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PlaylistDetail as Detail, Track } from '../services/api'
import { listening } from '../services/listening.svelte'
import { player } from '../services/player.svelte'
import { apiError, json, mockFetch } from '../test/fetch'
import PlaylistDetail from './PlaylistDetail.svelte'

beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(function (this: HTMLMediaElement) {
    this.dispatchEvent(new Event('playing'))
    return Promise.resolve()
  })
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  player.attach(() => {})
})
afterEach(() => player.detach())

function track(id: number, extra: Partial<Track> = {}): Track {
  return {
    id, root_id: 1, title: `Song ${id}`, artist: 'Band', album: 'Record', duration_ms: 60_000,
    format: 'mp3', status: 'present', available: true, metadata_status: 'ok', ...extra,
  }
}

// Shapes match the real GET /api/playlists/{id} response (see docs/api.md).
function detail(tracks: Track[], extra: Partial<Detail> = {}): Detail {
  return {
    id: 5, name: 'Mix', track_count: tracks.length, unavailable_count: tracks.filter((t) => !t.available).length,
    duration_ms: tracks.length * 60_000, created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
    entries: tracks.map((t, i) => ({ position: i + 1, added_at: '2026-10-01T00:00:00Z', track: t })),
    ...extra,
  }
}

function props(over: Record<string, unknown> = {}) {
  return { id: 5, active: true, onUnauthorized: vi.fn(), ...over }
}

async function entryButtons() {
  const list = await screen.findByRole('list', { name: 'Tracks' })
  return within(list).getAllByRole('button', { name: /^Play / })
}

describe('PlaylistDetail', () => {
  afterEach(() => {
    location.hash = ''
  })

  it('shows the tracks in order and marks unavailable ones', async () => {
    mockFetch({
      'GET /api/playlists/5': () =>
        json(200, { playlist: detail([track(1, { available: false, status: 'missing' }), track(2)], { description: 'For the car' }) }),
    })
    render(PlaylistDetail, { props: props() })
    expect(await screen.findByRole('heading', { name: 'Mix' })).toBeTruthy()
    expect(screen.getByText('For the car')).toBeTruthy()
    expect(screen.getByText('2 tracks · 2:00 · 1 unavailable')).toBeTruthy()
    const rows = await entryButtons()
    expect(rows.map((r) => r.getAttribute('aria-label'))).toEqual(['Play Song 1 by Band (unavailable)', 'Play Song 2 by Band'])
    expect((rows[0] as HTMLButtonElement).disabled).toBe(true)
    // A missing file is told apart from an unplugged drive.
    expect(screen.getByText('File missing')).toBeTruthy()
  })

  it('plays from the first available track, or from a chosen one', async () => {
    const d = detail([track(1, { available: false }), track(2), track(3)])
    mockFetch({ 'GET /api/playlists/5': () => json(200, { playlist: d }) })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Play' }))
    expect(player.current?.id).toBe(2)
    expect(player.context).toEqual({ kind: 'playlist', id: '5', name: 'Mix' })
    expect(player.entries.map((e) => e.track.id)).toEqual([1, 2, 3])
    await fireEvent.click(screen.getByRole('button', { name: 'Play Song 3 by Band' }))
    expect(player.current?.id).toBe(3)
  })

  it('shuffles, plays next and adds the playlist to the queue', async () => {
    mockFetch({ 'GET /api/playlists/5': () => json(200, { playlist: detail([track(1), track(2), track(3, { available: false })]) }) })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Shuffle' }))
    expect(player.shuffle).toBe(true)
    expect(player.entries).toHaveLength(3)
    player.clear()
    await fireEvent.click(screen.getByRole('button', { name: 'Add to queue' }))
    // Only playable tracks are queued.
    expect(player.entries.map((e) => e.track.id)).toEqual([1, 2])
  })

  it('duplicates the playlist and opens the copy', async () => {
    mockFetch({
      'GET /api/playlists/5': () => json(200, { playlist: detail([track(1)]) }),
      'POST /api/playlists/5/duplicate': () => json(201, { playlist: { ...detail([track(1)]), id: 8, name: 'Mix (copy)', entries: undefined } }),
    })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Duplicate' }))
    await vi.waitFor(() => expect(location.hash).toBe('#/playlists/8'))
  })

  it('chooses the artwork from one of its tracks', async () => {
    const fetch = mockFetch({
      'GET /api/playlists/5': () => json(200, { playlist: detail([track(1), track(2)], { artwork_track_id: 1 }) }),
      'PUT /api/playlists/5/artwork': () => json(200, { playlist: { ...detail([track(1), track(2)]), artwork_track_id: 2, artwork_chosen: true, entries: undefined } }),
    })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Change the playlist artwork' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Use the artwork of Song 2' }))
    expect(await screen.findByText('Artwork changed.')).toBeTruthy()
    const put = fetch.mock.calls.find(([u]) => String(u).endsWith('/artwork'))!
    expect(JSON.parse(String(put[1]?.body))).toEqual({ track_id: 2 })
  })

  it('reorders by drag and drop', async () => {
    const fetch = mockFetch({
      'GET /api/playlists/5': () => json(200, { playlist: detail([track(1), track(2), track(3)]) }),
      'PUT /api/playlists/5/tracks': () => json(200, { playlist: detail([track(2), track(3), track(1)]) }),
    })
    const { container } = render(PlaylistDetail, { props: props() })
    await entryButtons()
    const items = container.querySelectorAll('li.entry')
    await fireEvent.dragStart(items[0])
    await fireEvent.dragOver(items[2])
    await fireEvent.drop(items[2])
    expect(await screen.findByText('Moved “Song 1” to position 3.')).toBeTruthy()
    const put = fetch.mock.calls.find(([, i]) => i?.method === 'PUT')!
    expect(JSON.parse(String(put[1]?.body))).toEqual({ track_ids: [2, 3, 1] })
  })

  it('adds its tracks to another playlist, never offering itself', async () => {
    const fetch = mockFetch({
      'GET /api/playlists/5': () => json(200, { playlist: detail([track(1), track(2)]) }),
      'GET /api/playlists': () =>
        json(200, { items: [{ ...detail([]), entries: undefined }, { ...detail([]), id: 7, name: 'Other', entries: undefined }] }),
      'POST /api/playlists/7/tracks': () => json(200, { added: 1, skipped: 1, playlist: detail([track(1), track(2)]) }),
    })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Add to another playlist…' }))
    const select = (await screen.findByLabelText('Add to playlist')) as HTMLSelectElement
    expect([...select.options].map((o) => o.textContent)).toEqual(['Other (0)'])
    await fireEvent.click(screen.getByRole('button', { name: 'Add' }))
    await vi.waitFor(() => expect(listening.announcement).toBe('Added 1 track to “Other” (1 already there).'))
    const post = fetch.mock.calls.find(([, i]) => i?.method === 'POST')!
    expect(JSON.parse(String(post[1]?.body))).toEqual({ track_ids: [1, 2] })
  })

  it('disables Play when nothing can be played', async () => {
    mockFetch({ 'GET /api/playlists/5': () => json(200, { playlist: detail([track(1, { available: false })]) }) })
    render(PlaylistDetail, { props: props() })
    expect(((await screen.findByRole('button', { name: 'Play' })) as HTMLButtonElement).disabled).toBe(true)
  })

  it('explains an empty playlist', async () => {
    mockFetch({ 'GET /api/playlists/5': () => json(200, { playlist: detail([]) }) })
    render(PlaylistDetail, { props: props() })
    expect(await screen.findByText('This playlist is empty.')).toBeTruthy()
  })

  it('moves a track by sending the complete new order', async () => {
    const fetch = mockFetch({
      'GET /api/playlists/5': () => json(200, { playlist: detail([track(1), track(2), track(3)]) }),
      'PUT /api/playlists/5/tracks': () => json(200, { playlist: detail([track(2), track(1), track(3)]) }),
    })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Move Song 1 down' }))
    expect(await screen.findByText('Moved “Song 1” to position 2.')).toBeTruthy()
    const put = fetch.mock.calls.find(([, i]) => i?.method === 'PUT')!
    expect(JSON.parse(String(put[1]?.body))).toEqual({ track_ids: [2, 1, 3] })
    expect((await entryButtons()).map((b) => b.textContent?.trim())).toEqual(['Song 2 Band', 'Song 1 Band', 'Song 3 Band'])
    expect((screen.getByRole('button', { name: 'Move Song 2 up' }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Move Song 3 down' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('reloads when the playlist changed elsewhere', async () => {
    let current = detail([track(1), track(2)])
    mockFetch({
      'GET /api/playlists/5': () => json(200, { playlist: current }),
      'PUT /api/playlists/5/tracks': () => {
        current = detail([track(1), track(2), track(9)])
        return apiError(409, 'order_mismatch', 'the new order must list every track')
      },
    })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Move Song 1 down' }))
    expect((await screen.findByRole('alert')).textContent).toBe('The playlist changed elsewhere and was reloaded. Try again.')
    expect(await screen.findByText('Song 9')).toBeTruthy()
  })

  it('removes a track and reloads the playlist', async () => {
    let current = detail([track(1), track(2)])
    const fetch = mockFetch({
      'GET /api/playlists/5': () => json(200, { playlist: current }),
      'DELETE /api/playlists/5/tracks/1': () => {
        current = detail([track(2)])
        return new Response(null, { status: 204 })
      },
    })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'More actions for Song 1' }))
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Remove from playlist' }))
    expect(await screen.findByText('Removed “Song 1”. It is still in your library.')).toBeTruthy()
    expect(screen.queryByText('Song 1')).toBeNull()
    expect(fetch.mock.calls.map(([u, i]) => `${i?.method ?? 'GET'} ${u}`)).toEqual([
      'GET /api/playlists/5',
      'DELETE /api/playlists/5/tracks/1',
      'GET /api/playlists/5',
    ])
  })

  it('renames the playlist', async () => {
    const fetch = mockFetch({
      'GET /api/playlists/5': () => json(200, { playlist: detail([track(1)], { description: 'old' }) }),
      'PATCH /api/playlists/5': () => json(200, { playlist: { ...detail([track(1)]), entries: undefined, name: 'Evening' } }),
    })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: ' Evening ' } })
    await fireEvent.input(screen.getByLabelText(/Description/), { target: { value: '' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('heading', { name: 'Evening' })).toBeTruthy()
    expect(screen.queryByText('old')).toBeNull()
    expect(screen.getByText('Song 1')).toBeTruthy() // entries kept
    const patch = fetch.mock.calls.find(([, i]) => i?.method === 'PATCH')!
    expect(JSON.parse(String(patch[1]?.body))).toEqual({ name: 'Evening', description: '' })
  })

  it('keeps the form and shows the error when a rename fails', async () => {
    mockFetch({
      'GET /api/playlists/5': () => json(200, { playlist: detail([]) }),
      'PATCH /api/playlists/5': () => apiError(409, 'name_taken', 'a playlist with this name already exists'),
    })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Taken' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect((await screen.findByRole('alert')).textContent).toBe('A playlist with this name already exists.')
    expect((screen.getByLabelText('Name') as HTMLInputElement).value).toBe('Taken')
    expect(screen.getByRole('heading', { name: 'Mix' })).toBeTruthy()
  })

  it('asks for confirmation before deleting', async () => {
    const fetch = mockFetch({
      'GET /api/playlists/5': () => json(200, { playlist: detail([track(1)]) }),
      'DELETE /api/playlists/5': () => new Response(null, { status: 204 }),
    })
    render(PlaylistDetail, { props: props() })
    await fireEvent.click(await screen.findByRole('button', { name: 'Delete' }))
    expect(screen.getByText(/The tracks stay in your library/)).toBeTruthy()
    expect(fetch.mock.calls.some(([, i]) => i?.method === 'DELETE')).toBe(false)

    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByText(/The tracks stay in your library/)).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Delete playlist' }))
    await vi.waitFor(() => expect(location.hash).toBe('#/playlists'))
  })

  it('says when the playlist does not exist', async () => {
    mockFetch({ 'GET /api/playlists/5': () => apiError(404, 'not_found', 'playlist not found') })
    render(PlaylistDetail, { props: props() })
    expect(await screen.findByRole('heading', { name: 'Not found' })).toBeTruthy()
  })

  it('reports an expired session', async () => {
    mockFetch({ 'GET /api/playlists/5': () => apiError(401, 'unauthenticated', 'sign in required') })
    const p = props()
    render(PlaylistDetail, { props: p })
    await vi.waitFor(() => expect(p.onUnauthorized).toHaveBeenCalled())
  })

  it('shows load errors with a retry', async () => {
    mockFetch({ 'GET /api/playlists/5': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
    render(PlaylistDetail, { props: props() })
    expect((await screen.findByRole('alert')).textContent).toBe('The server had a problem. Try again.')
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy()
  })
})
