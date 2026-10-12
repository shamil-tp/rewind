import { render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { apiError, json, mockFetch } from '../test/fetch'
import PluginStatus from './PluginStatus.svelte'

describe('PluginStatus', () => {
  it('says when plugins are off', async () => {
    mockFetch({ 'GET /api/plugins': () => json(200, { configured: false, api_version: 1, items: [] }) })
    render(PluginStatus, { props: { onUnauthorized: vi.fn() } })
    expect(await screen.findByText(/Plugins are off/)).toBeTruthy()
  })

  it('says when none are installed', async () => {
    mockFetch({ 'GET /api/plugins': () => json(200, { configured: true, api_version: 1, items: [] }) })
    render(PluginStatus, { props: { onUnauthorized: vi.fn() } })
    expect(await screen.findByText('No plugins are installed.')).toBeTruthy()
  })

  it('lists plugins with their state in words, and renders manifest text as text', async () => {
    const hostile = '<img src=x onerror="window.__pwned = 1">'
    mockFetch({
      'GET /api/plugins': () =>
        json(200, {
          configured: true,
          api_version: 1,
          items: [
            { id: 'missing-tracks', name: 'Missing tracks warning', version: '1.0.0', capabilities: ['events.library_scan'], state: 'running' },
            { id: 'evil', name: hostile, description: '<script>window.__pwned = 2</script>', version: '1.0.0', capabilities: [], state: 'disabled', author: '<b>x</b>' },
            { id: 'broken', capabilities: [], state: 'failed', error: 'initialization failed; see the server log' },
          ],
        }),
    })
    const { container } = render(PluginStatus, { props: { onUnauthorized: vi.fn() } })
    const list = await screen.findByRole('list', { name: 'Installed plugins' })
    const items = within(list).getAllByRole('listitem')
    expect(items).toHaveLength(3)
    expect(items[0].textContent).toContain('Missing tracks warning')
    expect(items[0].textContent).toContain('Running')
    expect(items[1].textContent).toContain('Installed, not enabled')
    expect(items[2].textContent).toContain('Failed to start')
    expect(items[2].textContent).toContain('initialization failed; see the server log')
    expect(items[2].textContent).toContain('ID broken') // no name: the ID is shown

    // The hostile strings appear literally; no element was created from them.
    expect(screen.getByText(hostile)).toBeTruthy()
    expect(container.querySelector('img, script, b')).toBeNull()
    expect((window as unknown as { __pwned?: number }).__pwned).toBeUndefined()
  })

  it('reports an expired session', async () => {
    mockFetch({ 'GET /api/plugins': () => apiError(401, 'unauthenticated', 'sign in required') })
    const onUnauthorized = vi.fn()
    render(PluginStatus, { props: { onUnauthorized } })
    await vi.waitFor(() => expect(onUnauthorized).toHaveBeenCalled())
  })

  it('shows load errors with a retry', async () => {
    mockFetch({ 'GET /api/plugins': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
    render(PluginStatus, { props: { onUnauthorized: vi.fn() } })
    expect((await screen.findByRole('alert')).textContent).toBe('The server had a problem. Try again.')
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy()
  })
})
