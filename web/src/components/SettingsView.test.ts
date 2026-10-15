import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { DEFAULT_PREFERENCES, type Preferences } from '../services/api'
import { apiError, json, mockFetch as baseMockFetch } from '../test/fetch'
import { themeState } from '../test/themes'
import SettingsView from './SettingsView.svelte'

// Settings also shows plugin status; these tests are about preferences.
// Themes have their own tests (ThemeSettings.test.ts, ThemeEditor.test.ts).
const mockFetch: typeof baseMockFetch = (routes) =>
  baseMockFetch({ 'GET /api/plugins': () => json(200, { configured: false, api_version: 1, items: [] }), ...routes })

function prefs(over: Partial<Preferences> = {}): Preferences {
  return { ...DEFAULT_PREFERENCES, ...over }
}

function props(over: Record<string, unknown> = {}) {
  return {
    preferences: prefs(),
    status: 'ready' as const,
    loadError: '',
    onRetry: vi.fn(),
    onSaved: vi.fn(),
    themes: themeState(),
    themesStatus: 'ready' as const,
    themesError: '',
    onRetryThemes: vi.fn(),
    onThemesChanged: vi.fn(),
    onPreviewTheme: vi.fn(),
    onUnauthorized: vi.fn(),
    ...over,
  }
}

const save = () => screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement
const patches = (fetch: ReturnType<typeof mockFetch>) =>
  fetch.mock.calls.filter(([, i]) => i?.method === 'PATCH').map(([, i]) => JSON.parse(String(i?.body)))

describe('SettingsView: library and playback', () => {
  it('shows the saved values; Save is enabled only after a change', async () => {
    render(SettingsView, { props: props({ preferences: prefs({ library_sort: 'added', autoplay_next: false }) }) })
    expect((screen.getByLabelText('Library sort order') as HTMLSelectElement).value).toBe('added')
    expect((screen.getByLabelText('Continue playlists') as HTMLInputElement).checked).toBe(false)
    expect(save().disabled).toBe(true)
    await fireEvent.click(screen.getByLabelText('Continue playlists'))
    expect(save().disabled).toBe(false)
  })

  it('saves only the changed preferences and reports success', async () => {
    const stored = prefs({ library_sort: 'year' })
    const fetch = mockFetch({ 'PATCH /api/preferences': () => json(200, { preferences: stored }) })
    const p = props()
    const { rerender } = render(SettingsView, { props: p })
    await fireEvent.change(screen.getByLabelText('Library sort order'), { target: { value: 'year' } })
    await fireEvent.click(save())
    await vi.waitFor(() => expect(p.onSaved).toHaveBeenCalledWith(stored))
    expect(patches(fetch)).toEqual([{ library_sort: 'year' }])
    // The parent passes the saved values back down.
    await rerender({ ...p, preferences: stored })
    expect(await screen.findByText('Settings saved.')).toBeTruthy()
    expect(save().disabled).toBe(true)
  })

  it('does not pretend a failed save worked', async () => {
    mockFetch({ 'PATCH /api/preferences': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
    const p = props()
    render(SettingsView, { props: p })
    await fireEvent.click(screen.getByLabelText('Continue playlists'))
    await fireEvent.click(save())
    expect((await screen.findByRole('alert')).textContent).toBe('Not saved: The server had a problem. Try again.')
    expect(p.onSaved).not.toHaveBeenCalled()
    expect(screen.queryByText('Settings saved.')).toBeNull()
    // The unsaved change is kept for another try.
    expect((screen.getByLabelText('Continue playlists') as HTMLInputElement).checked).toBe(false)
    expect(save().disabled).toBe(false)
  })

  it('reports an expired session', async () => {
    mockFetch({ 'PATCH /api/preferences': () => apiError(401, 'unauthenticated', 'sign in required') })
    const p = props()
    render(SettingsView, { props: p })
    await fireEvent.click(screen.getByLabelText('Continue playlists'))
    await fireEvent.click(save())
    await vi.waitFor(() => expect(p.onUnauthorized).toHaveBeenCalled())
  })

  it('explains a load failure and blocks saving until settings load', async () => {
    const p = props({ status: 'error', loadError: 'Cannot reach the Rewind server.' })
    render(SettingsView, { props: p })
    expect(screen.getByRole('alert').textContent).toContain('Could not load your settings: Cannot reach the Rewind server.')
    expect((screen.getByLabelText('Library sort order') as HTMLSelectElement).disabled).toBe(true)
    expect(save().disabled).toBe(true)
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(p.onRetry).toHaveBeenCalled()
  })

  it('shows a loading state', () => {
    render(SettingsView, { props: props({ status: 'loading' }) })
    expect(screen.getByText('Loading settings…')).toBeTruthy()
  })
})

describe('SettingsView: themes', () => {
  it('shows the theme section with the installed themes', () => {
    render(SettingsView, { props: props() })
    expect(screen.getByRole('heading', { name: 'Theme and style' })).toBeTruthy()
    const names = screen.getAllByRole('heading', { level: 4 }).map((h) => h.textContent)
    expect(names).toEqual(['Rewind Dark', 'Rewind Light', 'Rewind OLED'])
  })

  it('keeps unsaved library edits when the theme state changes', async () => {
    const p = props()
    const { rerender } = render(SettingsView, { props: p })
    await fireEvent.change(screen.getByLabelText('Library sort order'), { target: { value: 'title' } })
    await rerender({ ...p, themes: themeState('light') })
    expect((screen.getByLabelText('Library sort order') as HTMLSelectElement).value).toBe('title')
    expect(save().disabled).toBe(false)
  })

  it('explains when themes cannot be loaded, while other settings still work', async () => {
    const p = props({ themes: null, themesStatus: 'error', themesError: 'Cannot reach the Rewind server.' })
    render(SettingsView, { props: p })
    expect(screen.getByRole('alert').textContent).toContain('Could not load themes: Cannot reach the Rewind server.')
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(p.onRetryThemes).toHaveBeenCalled()
    expect((screen.getByLabelText('Library sort order') as HTMLSelectElement).disabled).toBe(false)
  })
})
