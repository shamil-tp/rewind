import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { DEFAULT_PREFERENCES, type Preferences } from '../services/api'
import { apiError, deferred, json, mockFetch as baseMockFetch } from '../test/fetch'
import SettingsView from './SettingsView.svelte'

// Settings also shows plugin status; these tests are about preferences.
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
    onPreviewTheme: vi.fn(),
    onUnauthorized: vi.fn(),
    ...over,
  }
}

const save = () => screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement
const radio = (name: string) => screen.getByRole('radio', { name: new RegExp(name) }) as HTMLInputElement
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
    expect(radio('Light').matches(':disabled')).toBe(true) // via the disabled fieldset
    expect(save().disabled).toBe(true)
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(p.onRetry).toHaveBeenCalled()
  })

  it('shows a loading state', () => {
    render(SettingsView, { props: props({ status: 'loading' }) })
    expect(screen.getByText('Loading settings…')).toBeTruthy()
  })
})

describe('SettingsView: theme', () => {
  it('lists the built-in themes as one labelled radio group, marking the saved one in text', () => {
    render(SettingsView, { props: props({ preferences: prefs({ theme: 'light' }) }) })
    const group = screen.getByRole('group', { name: 'Theme' })
    const radios = within(group).getAllByRole('radio') as HTMLInputElement[]
    expect(radios.map((r) => r.labels?.[0].textContent?.replace(/\s+/g, ' ').trim())).toEqual(['Rewind Dark', 'Light Current', 'OLED Black'])
    expect(radios.map((r) => r.checked)).toEqual([false, true, false])
    // One name: browsers move between them with the arrow keys, and Tab enters at the checked one.
    expect(new Set(radios.map((r) => r.name)).size).toBe(1)
  })

  it('falls back to Rewind Dark for an unknown saved theme', () => {
    render(SettingsView, { props: props({ preferences: prefs({ theme: 'solarized' }) }) })
    expect(radio('Rewind Dark').checked).toBe(true)
  })

  it('applies a theme at once and reports it saved only after the server confirms', async () => {
    const pending = deferred<Response>()
    const fetch = mockFetch({ 'PATCH /api/preferences': () => pending.promise })
    const p = props()
    render(SettingsView, { props: p })
    await fireEvent.click(radio('OLED Black'))
    expect(p.onPreviewTheme).toHaveBeenLastCalledWith('oled-black')
    expect(radio('OLED Black').checked).toBe(true)
    expect(screen.getByText('Saving theme…')).toBeTruthy()
    expect(screen.queryByText(/Theme saved/)).toBeNull()

    pending.resolve(json(200, { preferences: prefs({ theme: 'oled-black' }) }))
    expect(await screen.findByText('Theme saved: OLED Black.')).toBeTruthy()
    expect(p.onSaved).toHaveBeenCalledWith(prefs({ theme: 'oled-black' }))
    expect(p.onPreviewTheme).toHaveBeenLastCalledWith(null)
    expect(patches(fetch)).toEqual([{ theme: 'oled-black' }])
  })

  it('returns to the saved theme when saving fails', async () => {
    mockFetch({ 'PATCH /api/preferences': () => json(500, { error: { code: 'internal_error', message: 'x' } }) })
    const p = props({ preferences: prefs({ theme: 'light' }) })
    render(SettingsView, { props: p })
    await fireEvent.click(radio('OLED Black'))
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Theme not saved: The server had a problem. Try again. The saved theme is shown again.',
    )
    expect(p.onPreviewTheme).toHaveBeenLastCalledWith(null)
    expect(radio('Light').checked).toBe(true)
    expect(p.onSaved).not.toHaveBeenCalled()
  })

  it('saves quick successive choices one at a time, the last one last', async () => {
    const first = deferred<Response>()
    let n = 0
    const fetch = mockFetch({
      'PATCH /api/preferences': (init) => {
        n++
        const body = JSON.parse(String(init?.body))
        return n === 1 ? first.promise : json(200, { preferences: prefs(body) })
      },
    })
    render(SettingsView, { props: props() })
    await fireEvent.click(radio('Light'))
    await fireEvent.click(radio('OLED Black'))
    expect(patches(fetch)).toEqual([{ theme: 'light' }]) // the second waits
    first.resolve(json(200, { preferences: prefs({ theme: 'light' }) }))
    expect(await screen.findByText('Theme saved: OLED Black.')).toBeTruthy()
    expect(patches(fetch)).toEqual([{ theme: 'light' }, { theme: 'oled-black' }])
  })

  it('keeps unsaved library edits when a theme is saved', async () => {
    mockFetch({ 'PATCH /api/preferences': () => json(200, { preferences: prefs({ theme: 'light' }) }) })
    const p = props()
    const { rerender } = render(SettingsView, { props: p })
    await fireEvent.change(screen.getByLabelText('Library sort order'), { target: { value: 'title' } })
    await fireEvent.click(radio('Light'))
    await screen.findByText('Theme saved: Light.')
    await rerender({ ...p, preferences: prefs({ theme: 'light' }) })
    expect((screen.getByLabelText('Library sort order') as HTMLSelectElement).value).toBe('title')
    expect(save().disabled).toBe(false)
  })
})

describe('SettingsView: accent color', () => {
  const hex = () => screen.getByLabelText('Accent color') as HTMLInputElement
  const use = () => screen.getByRole('button', { name: 'Use color' }) as HTMLButtonElement

  it("starts at the theme's accent", () => {
    render(SettingsView, { props: props() })
    expect(hex().value).toBe('#c6ff00')
    expect(use().disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Theme default' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('rejects invalid formats and CSS without contacting the server', async () => {
    const fetch = mockFetch({})
    render(SettingsView, { props: props() })
    for (const bad of ['orange', '#f80', 'rgb(1,2,3)', '#ff8800;background:url(x)', 'url(https://x)', '#gggggg']) {
      await fireEvent.input(hex(), { target: { value: bad } })
      await fireEvent.click(use())
      expect(screen.getByText('Use a color like #c6ff00 (six hex digits).'), bad).toBeTruthy()
      expect(hex().getAttribute('aria-invalid')).toBe('true')
    }
    expect(patches(fetch)).toEqual([])
  })

  it('rejects colors without enough contrast on the current theme', async () => {
    const fetch = mockFetch({})
    render(SettingsView, { props: props({ preferences: prefs({ theme: 'light' }) }) })
    await fireEvent.input(hex(), { target: { value: '#ff8800' } })
    await fireEvent.click(use())
    expect(screen.getByText(/Too little contrast with the Light background \(1\.9:1; at least 4\.5:1 is needed\)/)).toBeTruthy()
    expect(patches(fetch)).toEqual([])
  })

  it('saves a valid color (normalized to lower case) and resets to the theme accent', async () => {
    let stored = prefs()
    const fetch = mockFetch({
      'PATCH /api/preferences': (init) => {
        stored = prefs({ ...stored, ...JSON.parse(String(init?.body)) })
        return json(200, { preferences: stored })
      },
    })
    const p = props()
    const { rerender } = render(SettingsView, { props: p })
    await fireEvent.input(hex(), { target: { value: ' #FF8800 ' } })
    await fireEvent.click(use())
    expect(await screen.findByText('Accent color saved.')).toBeTruthy()
    await rerender({ ...p, preferences: stored })
    expect(hex().value).toBe('#ff8800')

    await fireEvent.click(screen.getByRole('button', { name: 'Theme default' }))
    expect(await screen.findByText('Using the Rewind Dark accent.')).toBeTruthy()
    await rerender({ ...p, preferences: stored })
    expect(hex().value).toBe('#c6ff00')
    expect(patches(fetch)).toEqual([{ accent_color: '#ff8800' }, { accent_color: '' }])
  })

  it('says when a saved accent is unreadable on the chosen theme', () => {
    render(SettingsView, { props: props({ preferences: prefs({ theme: 'light', accent_color: '#ff8800' }) }) })
    expect(screen.getByText(/Your accent color #ff8800 is too hard to read on Light/)).toBeTruthy()
  })

  it('reports a failed accent save', async () => {
    mockFetch({ 'PATCH /api/preferences': () => apiError(422, 'validation_failed', 'accent_color must be ""') })
    const p = props()
    render(SettingsView, { props: p })
    await fireEvent.input(hex(), { target: { value: '#ff8800' } })
    await fireEvent.click(use())
    expect((await screen.findByRole('alert')).textContent).toBe('Accent color not saved: Accent_color must be "".')
    expect(p.onSaved).not.toHaveBeenCalled()
  })
})
