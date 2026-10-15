import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import type { ThemeState } from '../services/api'
import { apiError, deferred, json, mockFetch } from '../test/fetch'
import { brokenTheme, themeState } from '../test/themes'
import ThemeSettings from './ThemeSettings.svelte'

function props(over: Record<string, unknown> = {}) {
  return {
    themes: themeState() as ThemeState | null,
    status: 'ready' as const,
    loadError: '',
    onRetry: vi.fn(),
    onChanged: vi.fn(),
    onPreview: vi.fn(),
    onUnauthorized: vi.fn(),
    ...over,
  }
}

const card = (name: string) => screen.getByRole('listitem', { name }) as HTMLElement
const button = (scope: HTMLElement, name: string | RegExp) => within(scope).getByRole('button', { name }) as HTMLButtonElement
const bodies = (fetch: ReturnType<typeof mockFetch>) =>
  fetch.mock.calls.filter(([, i]) => i?.method === 'PUT').map(([u, i]) => [String(u), JSON.parse(String(i?.body))])

describe('ThemeSettings: list', () => {
  it('lists every theme with its details, marking the active one in text', () => {
    render(ThemeSettings, { props: props({ themes: themeState('light') }) })
    const items = screen.getAllByRole('listitem')
    expect(items.map((li) => within(li).getByRole('heading').textContent)).toEqual(['Rewind Dark', 'Rewind Light', 'Rewind OLED'])
    const light = card('Rewind Light')
    expect(within(light).getByText('Active')).toBeTruthy()
    expect(within(light).getByText(/Bundled · v1\.0\.0 · Rewind contributors · light/)).toBeTruthy()
    expect(within(light).getByText(/Near-white paper background/)).toBeTruthy()
    expect(within(light).getByText('Compatible')).toBeTruthy()
    expect(within(card('Rewind Dark')).queryByText('Active')).toBeNull()
  })

  it('offers only the actions that apply to each theme', () => {
    render(ThemeSettings, {
      props: props({ themes: themeState('light', { 'oled-black': { enabled: false } }, [brokenTheme('broken', 'invalid', 'theme.json is missing')]) }),
    })
    const names = (li: HTMLElement) => within(li).queryAllByRole('button').map((b) => b.textContent?.trim())
    // Active: nothing to apply or preview.
    expect(names(card('Rewind Light'))).toEqual(['Customize', 'Disable'])
    // The default theme is the fallback and cannot be disabled.
    expect(names(card('Rewind Dark'))).toEqual(['Apply', 'Preview', 'Customize'])
    // Disabled: can be enabled or customized, not applied.
    expect(names(card('Rewind OLED'))).toEqual(['Customize', 'Enable'])
    expect(within(card('Rewind OLED')).getByText('Disabled')).toBeTruthy()
    // Invalid: no actions at all, and the reason.
    expect(names(card('broken'))).toEqual([])
    expect(within(card('broken')).getByText('Invalid')).toBeTruthy()
    expect(within(card('broken')).getByText(/theme\.json is missing/)).toBeTruthy()
    expect(within(card('broken')).getByText('No preview')).toBeTruthy()
  })

  it('shows incompatible themes and renders manifest text as plain text', () => {
    const evil = themeState('rewind-dark', {}, [brokenTheme('future', 'incompatible', 'written for theme API 2')])
    evil.items[1] = { ...evil.items[1], name: '<img src=x onerror=alert(1)>', description: '<b>bold</b>' }
    const { container } = render(ThemeSettings, { props: props({ themes: evil }) })
    expect(within(card('future')).getByText('Incompatible')).toBeTruthy()
    expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeTruthy()
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('b')).toBeNull()
  })

  it('draws each card preview with that theme’s tokens', () => {
    const { container } = render(ThemeSettings, { props: props() })
    const thumbs = [...container.querySelectorAll<HTMLElement>('.thumb')]
    expect(thumbs.map((t) => t.style.getPropertyValue('--rw-color-bg'))).toEqual(['#0b0b0b', '#f5f5f0', '#000000'])
  })

  it('explains how themes are installed, without offering an upload', () => {
    render(ThemeSettings, { props: props() })
    expect(screen.getByText(/installed by the server's administrator, not from this page/)).toBeTruthy()
    expect(screen.getByText(/No theme directory is configured/)).toBeTruthy()
    expect(document.querySelector('input[type="file"]')).toBeNull()
  })

  it('shows loading, error and notice states', async () => {
    const { rerender } = render(ThemeSettings, { props: props({ themes: null, status: 'loading' }) })
    expect(screen.getByText('Loading themes…')).toBeTruthy()
    const p = props({ themes: null, status: 'error', loadError: 'Cannot reach the Rewind server.' })
    await rerender(p)
    expect(screen.getByRole('alert').textContent).toContain('Could not load themes')
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(p.onRetry).toHaveBeenCalled()
    await rerender(props({ themes: { ...themeState(), notice: 'Your saved theme (sunset) is not available any more, so Rewind Dark is shown.' } }))
    expect(screen.getByText(/sunset\) is not available any more/)).toBeTruthy()
  })
})

describe('ThemeSettings: actions', () => {
  it('applies a theme and reports it only after the server confirms', async () => {
    const pending = deferred<Response>()
    const fetch = mockFetch({ 'PUT /api/themes/active': () => pending.promise })
    const p = props()
    render(ThemeSettings, { props: p })
    await fireEvent.click(button(card('Rewind OLED'), 'Apply'))
    expect(screen.getByText('Saving…')).toBeTruthy()
    expect(p.onChanged).not.toHaveBeenCalled()
    pending.resolve(json(200, themeState('oled-black')))
    expect(await screen.findByText('Applied: Rewind OLED.')).toBeTruthy()
    expect(p.onChanged).toHaveBeenCalledWith(themeState('oled-black'))
    expect(bodies(fetch)).toEqual([['/api/themes/active', { id: 'oled-black' }]])
  })

  it('reports a failed apply and keeps the saved theme', async () => {
    mockFetch({ 'PUT /api/themes/active': () => apiError(409, 'theme_disabled', 'this theme is disabled; enable it before applying it') })
    const p = props()
    render(ThemeSettings, { props: p })
    await fireEvent.click(button(card('Rewind Light'), 'Apply'))
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Theme not applied: This theme is disabled; enable it before applying it. The saved theme is shown.',
    )
    expect(p.onChanged).not.toHaveBeenCalled()
  })

  it('previews a theme across the app without saving, then applies or stops', async () => {
    const fetch = mockFetch({ 'PUT /api/themes/active': () => json(200, themeState('light')) })
    const p = props()
    render(ThemeSettings, { props: p })
    const preview = button(card('Rewind Light'), 'Preview')
    await fireEvent.click(preview)
    expect(p.onPreview).toHaveBeenLastCalledWith({ themeId: 'light' })
    expect(preview.getAttribute('aria-pressed')).toBe('true')
    const banner = screen.getByRole('region', { name: 'Theme preview' })
    expect(banner.textContent).toContain('Previewing Rewind Light across the app. Nothing is saved')
    expect(fetch).not.toHaveBeenCalled()

    await fireEvent.click(within(banner).getByRole('button', { name: 'Stop preview' }))
    expect(p.onPreview).toHaveBeenLastCalledWith(null)
    expect(screen.queryByRole('region', { name: 'Theme preview' })).toBeNull()

    await fireEvent.click(preview)
    await fireEvent.click(within(screen.getByRole('region', { name: 'Theme preview' })).getByRole('button', { name: 'Apply Rewind Light' }))
    await screen.findByText('Applied: Rewind Light.')
    expect(p.onPreview).toHaveBeenLastCalledWith(null)
  })

  it('disables the active theme and says which theme took over', async () => {
    const fetch = mockFetch({
      'PUT /api/themes/light/enabled': () => json(200, themeState('oled-black', { light: { enabled: false } })),
    })
    const p = props({ themes: themeState('light') })
    render(ThemeSettings, { props: p })
    await fireEvent.click(button(card('Rewind Light'), 'Disable'))
    expect(await screen.findByText('Disabled: Rewind Light. Rewind OLED is now active.')).toBeTruthy()
    expect(bodies(fetch)).toEqual([['/api/themes/light/enabled', { enabled: false }]])
  })

  it('enables a disabled theme', async () => {
    mockFetch({ 'PUT /api/themes/oled-black/enabled': () => json(200, themeState()) })
    render(ThemeSettings, { props: props({ themes: themeState('rewind-dark', { 'oled-black': { enabled: false } }) }) })
    await fireEvent.click(button(card('Rewind OLED'), 'Enable'))
    expect(await screen.findByText('Enabled: Rewind OLED.')).toBeTruthy()
  })

  it('reports an expired session', async () => {
    mockFetch({ 'PUT /api/themes/active': () => apiError(401, 'unauthenticated', 'sign in required') })
    const p = props()
    render(ThemeSettings, { props: p })
    await fireEvent.click(button(card('Rewind Light'), 'Apply'))
    await vi.waitFor(() => expect(p.onUnauthorized).toHaveBeenCalled())
  })

  it('opens the editor and returns focus to the card when it closes', async () => {
    render(ThemeSettings, { props: props() })
    const customize = button(card('Rewind Light'), 'Customize')
    await fireEvent.click(customize)
    const heading = await screen.findByRole('heading', { name: 'Customize Rewind Light' })
    expect(document.activeElement).toBe(heading)
    expect(customize.getAttribute('aria-expanded')).toBe('true')
    await fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    await vi.waitFor(() => expect(document.activeElement).toBe(customize))
    expect(screen.queryByRole('heading', { name: 'Customize Rewind Light' })).toBeNull()
  })

  it('asks before discarding unsaved edits to open another theme', async () => {
    render(ThemeSettings, { props: props() })
    await fireEvent.click(button(card('Rewind Light'), 'Customize'))
    await fireEvent.change(screen.getByLabelText('Font'), { target: { value: 'serif' } })
    await fireEvent.click(button(card('Rewind OLED'), 'Customize'))
    const dialog = screen.getByRole('alertdialog', { name: 'Discard unsaved changes?' })
    expect(dialog.textContent).toContain('Your changes to Rewind Light are not saved. Open Rewind OLED')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }))
    expect(screen.getByRole('heading', { name: 'Customize Rewind Light' })).toBeTruthy()
    expect((screen.getByLabelText('Font') as HTMLSelectElement).value).toBe('serif')

    await fireEvent.click(button(card('Rewind OLED'), 'Customize'))
    await fireEvent.click(screen.getByRole('button', { name: 'Discard and open' }))
    expect(await screen.findByRole('heading', { name: 'Customize Rewind OLED' })).toBeTruthy()
  })
})
