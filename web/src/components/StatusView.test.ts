import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import type { LibraryRoot, LibrarySummary } from '../services/api'
import { apiError, json, mockFetch } from '../test/fetch'
import StatusView from './StatusView.svelte'

const counts = {
  files_found: 0, files_skipped: 0, files_failed: 0, dirs_failed: 0, tracks_added: 0,
  tracks_updated: 0, tracks_unchanged: 0, tracks_renamed: 0, tracks_missing: 0, tracks_restored: 0,
}

function summary(over: Partial<LibrarySummary> = {}): LibrarySummary {
  return {
    tracks: 1200, available_tracks: 1150, missing_tracks: 3, artists: 85, albums: 140, total_duration_ms: 3_725_000,
    roots: { total: 2, available: 1, unavailable: 1, pending: 0, disabled: 0 },
    last_successful_scan_at: '2026-10-11T18:00:00Z',
    scan: { running: false, active: [], queued: [] },
    ...over,
  }
}

const roots: LibraryRoot[] = [
  {
    id: 1, name: 'MUSICSTICK', enabled: true, status: 'available', track_count: 1150, missing_count: 3,
    scanning: false, queued: false,
    last_scan: { id: 7, status: 'completed', started_at: '2026-10-11T17:59:00Z', finished_at: '2026-10-11T18:00:00Z', counts: { ...counts, tracks_added: 12 } },
  },
  {
    id: 2, name: 'nas', enabled: true, status: 'unavailable', status_message: 'cannot access directory: does not exist',
    track_count: 50, missing_count: 0, scanning: false, queued: false,
    last_scan: { id: 8, status: 'failed', started_at: '2026-10-11T18:01:00Z', finished_at: '2026-10-11T18:01:01Z', counts, error: 'library root unavailable; no tracks were marked missing' },
  },
]

function props(over: Record<string, unknown> = {}) {
  return { onUnauthorized: vi.fn(), onScanFinished: vi.fn(), pollInterval: 10, ...over }
}

describe('StatusView', () => {
  it('shows real overview figures and root availability', async () => {
    mockFetch({ 'GET /api/library': () => json(200, summary()), 'GET /api/library/roots': () => json(200, { items: roots }) })
    render(StatusView, { props: props() })
    expect(await screen.findByText('MUSICSTICK')).toBeTruthy()
    expect(screen.getByText('1200')).toBeTruthy()
    expect(screen.getByText('1:02:05')).toBeTruthy()
    expect(screen.getByText('Missing files')).toBeTruthy()
    expect(screen.getByText('On unavailable drives')).toBeTruthy()
    // The unavailable root keeps its tracks and says so.
    expect(screen.getByText('Unavailable')).toBeTruthy()
    expect(screen.getByText(/kept while the drive is disconnected/)).toBeTruthy()
    expect(screen.getByText('cannot access directory: does not exist')).toBeTruthy()
    expect(screen.getByText(/no tracks were marked missing/)).toBeTruthy()
    expect(screen.getByText(/12 added/)).toBeTruthy()
  })

  it('rescans, follows the real scan state, and reports completion only when the server says so', async () => {
    let calls = 0
    const queued = summary({ scan: { running: true, active: [], queued: [1] } })
    const active = summary({ scan: { running: true, active: [{ root_id: 1, scan_id: 9, started_at: '2026-10-12T10:00:00Z', counts: { ...counts, files_found: 42 } }], queued: [] } })
    // Each server state lasts several polls, as a real scan would.
    const states = [summary(), ...Array(3).fill(queued), ...Array(15).fill(active), summary({ tracks: 1210 })]
    const fetch = mockFetch({
      'GET /api/library': () => json(200, states[Math.min(calls++, states.length - 1)]),
      'GET /api/library/roots': () => json(200, { items: roots }),
      'POST /api/library/scan': () => json(202, { started: [1, 2], already_running: [] }),
    })
    const p = props()
    render(StatusView, { props: p })
    const button = (await screen.findByRole('button', { name: 'Rescan library' })) as HTMLButtonElement
    await fireEvent.click(button)
    // While the server reports a running scan, the button is disabled and real counts are shown.
    await vi.waitFor(() => expect(screen.getByText(/MUSICSTICK: 42 files checked/)).toBeTruthy())
    expect((screen.getByRole('button', { name: 'Scanning…' }) as HTMLButtonElement).disabled).toBe(true)
    expect(p.onScanFinished).not.toHaveBeenCalled()
    await vi.waitFor(() => expect(screen.getByText('Scan finished. See the results below.')).toBeTruthy())
    expect(p.onScanFinished).toHaveBeenCalledTimes(1)
    expect(screen.getByText('1210')).toBeTruthy()
    expect(fetch.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === 'POST')).toHaveLength(1)
  })

  it('does not submit twice', async () => {
    let release!: () => void
    const fetch = mockFetch({
      'GET /api/library': () => json(200, summary()),
      'GET /api/library/roots': () => json(200, { items: roots }),
      'POST /api/library/scan': () => new Promise<Response>((r) => (release = () => r(json(202, { started: [1], already_running: [] })))),
    })
    render(StatusView, { props: props() })
    const button = (await screen.findByRole('button', { name: 'Rescan library' })) as HTMLButtonElement
    await fireEvent.click(button)
    await fireEvent.click(button)
    expect((screen.getByRole('button', { name: 'Requesting…' }) as HTMLButtonElement).disabled).toBe(true)
    release()
    await vi.waitFor(() => expect(fetch.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === 'POST')).toHaveLength(1))
  })

  it('explains scan conflicts and missing roots without claiming success', async () => {
    let code = 'scan_in_progress'
    mockFetch({
      'GET /api/library': () => json(200, summary()),
      'GET /api/library/roots': () => json(200, { items: roots }),
      'POST /api/library/scan': () => apiError(409, code, 'x'),
    })
    const p = props()
    render(StatusView, { props: p })
    await fireEvent.click(await screen.findByRole('button', { name: 'Rescan library' }))
    // The server reports no running scan, so it is shown as finished immediately.
    await vi.waitFor(() => expect(screen.getByText(/Scan finished|already running/)).toBeTruthy())

    code = 'no_library_roots'
    p.onScanFinished.mockClear()
    await fireEvent.click(screen.getByRole('button', { name: 'Rescan library' }))
    expect(await screen.findByText(/No music folders are configured/)).toBeTruthy()
    expect(p.onScanFinished).not.toHaveBeenCalled()
  })

  it('handles load errors and expired sessions', async () => {
    let status = 500
    mockFetch({
      'GET /api/library': () => (status === 200 ? json(200, summary()) : apiError(status, 'x', 'boom')),
      'GET /api/library/roots': () => json(200, { items: [] }),
    })
    const p = props()
    render(StatusView, { props: p })
    expect((await screen.findByRole('alert')).textContent).toContain('The server had a problem')
    status = 200
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByText(/No music folders are configured/)).toBeTruthy()

    status = 401
    await fireEvent.click(screen.getByRole('button', { name: 'Rescan library' }))
    await vi.waitFor(() => expect(p.onUnauthorized).toHaveBeenCalled())
  })
})
