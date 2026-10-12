import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App.svelte'
import { apiError, json, mockFetch } from './test/fetch'

const health = () => json(200, { status: 'ok', name: 'rewind', version: 'test' })
const owner = { id: 1, username: 'owner' }
// The signed-in view loads the first page of tracks.
const emptyLibrary = {
  'GET /api/tracks?page=1&limit=50&sort=artist&order=asc': () =>
    json(200, { items: [], pagination: { page: 1, limit: 50, total: 0, total_pages: 0 } }),
  'GET /api/preferences': () =>
    json(200, { preferences: { library_sort: 'artist', autoplay_next: true }, defaults: { library_sort: 'artist', autoplay_next: true } }),
}

describe('App', () => {
  beforeEach(() => localStorage.clear())

  it('shows an error, not the app, when the backend is unavailable', async () => {
    mockFetch({
      'GET /api/health': health,
      'GET /api/auth/status': () => {
        throw new TypeError('Failed to fetch')
      },
    })
    render(App)
    expect(await screen.findByText('Rewind is unavailable')).toBeTruthy()
    expect(screen.getByText('Cannot reach the Rewind server.')).toBeTruthy()
    expect(screen.queryByText(/Signed in as/)).toBeNull()
    expect(screen.queryByText('Library')).toBeNull()
  })

  it('retries after the backend comes back', async () => {
    let up = false
    mockFetch({
      'GET /api/health': health,
      'GET /api/auth/status': () => {
        if (!up) throw new TypeError('Failed to fetch')
        return json(200, { setup_required: false, authenticated: false })
      },
    })
    render(App)
    await screen.findByText('Rewind is unavailable')
    up = true
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeTruthy()
  })

  it('shows first-run setup when no account exists', async () => {
    mockFetch({ 'GET /api/health': health, 'GET /api/auth/status': () => json(200, { setup_required: true, authenticated: false }) })
    render(App)
    expect(await screen.findByRole('heading', { name: 'Create the owner account' })).toBeTruthy()
  })

  it('shows sign-in when not authenticated', async () => {
    mockFetch({ 'GET /api/health': health, 'GET /api/auth/status': () => json(200, { setup_required: false, authenticated: false }) })
    render(App)
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeTruthy()
  })

  it('signs in, shows the signed-in shell, and signs out', async () => {
    let authenticated = false
    mockFetch({
      'GET /api/health': health,
      'GET /api/auth/status': () => json(200, { setup_required: false, authenticated }),
      ...emptyLibrary,
      'POST /api/auth/login': () => {
        authenticated = true
        return json(200, { user: owner })
      },
      'POST /api/auth/logout': () => {
        authenticated = false
        return new Response(null, { status: 204 })
      },
    })
    render(App)
    await screen.findByRole('heading', { name: 'Sign in' })
    fireEvent.input(screen.getByLabelText('Username'), { target: { value: 'owner' } })
    fireEvent.input(screen.getByLabelText('Password'), { target: { value: 'correct horse battery staple' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByText('owner')).toBeTruthy()
    expect(await screen.findByRole('heading', { name: 'Library' })).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Sign out' }))
    expect(await screen.findByText('You have been signed out.')).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Sign in' })).toBeTruthy()
    expect(screen.queryByText(/Signed in as/)).toBeNull()
  })

  it('restores a signed-in session from the cookie', async () => {
    mockFetch({
      'GET /api/health': health,
      'GET /api/auth/status': () => json(200, { setup_required: false, authenticated: true }),
      ...emptyLibrary,
      'GET /api/auth/me': () => json(200, { user: owner }),
    })
    render(App)
    expect(await screen.findByRole('heading', { name: 'Library' })).toBeTruthy()
    expect(screen.getByText(/Signed in as/).textContent).toContain('owner')
  })

  it('falls back to sign-in if the session expires between requests', async () => {
    mockFetch({
      'GET /api/health': health,
      'GET /api/auth/status': () => json(200, { setup_required: false, authenticated: true }),
      'GET /api/auth/me': () => apiError(401, 'unauthenticated', 'sign in required'),
    })
    render(App)
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeTruthy()
  })

  it('keeps the user signed in and reports an error if sign-out fails', async () => {
    mockFetch({
      'GET /api/health': health,
      'GET /api/auth/status': () => json(200, { setup_required: false, authenticated: true }),
      ...emptyLibrary,
      'GET /api/auth/me': () => json(200, { user: owner }),
      'POST /api/auth/logout': () => {
        throw new TypeError('Failed to fetch')
      },
    })
    render(App)
    await screen.findByRole('heading', { name: 'Library' })
    await fireEvent.click(screen.getByRole('button', { name: 'Sign out' }))
    expect((await screen.findByRole('alert')).textContent).toBe('Cannot reach the Rewind server.')
  })

  it('never writes credentials to web storage', async () => {
    mockFetch({
      'GET /api/health': health,
      'GET /api/auth/status': () => json(200, { setup_required: false, authenticated: false }),
      ...emptyLibrary,
      'POST /api/auth/login': () => json(200, { user: owner }),
    })
    render(App)
    await screen.findByRole('heading', { name: 'Sign in' })
    fireEvent.input(screen.getByLabelText('Username'), { target: { value: 'owner' } })
    fireEvent.input(screen.getByLabelText('Password'), { target: { value: 'correct horse battery staple' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    await screen.findByRole('heading', { name: 'Library' })
    // The only thing ever stored is the theme hint (services/themes.ts): a
    // theme ID and an optional color, nothing about the account or session.
    await vi.waitFor(() => expect(localStorage.getItem('rewind.theme')).not.toBeNull())
    expect(Object.keys(localStorage)).toEqual(['rewind.theme'])
    expect(JSON.parse(localStorage.getItem('rewind.theme')!)).toEqual({ theme: 'rewind-dark', primary: '', secondary: '', accent: '' })
    const stored = localStorage.getItem('rewind.theme')!
    for (const secret of ['owner', 'correct horse', 'session', 'token']) expect(stored).not.toContain(secret)
    expect(sessionStorage.length).toBe(0)
  })
})
