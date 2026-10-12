import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { apiError, deferred, json, mockFetch } from '../test/fetch'
import LoginForm from './LoginForm.svelte'

function fill(username: string, password: string) {
  fireEvent.input(screen.getByLabelText('Username'), { target: { value: username } })
  fireEvent.input(screen.getByLabelText('Password'), { target: { value: password } })
}

describe('LoginForm', () => {
  it('validates required fields without calling the server', async () => {
    const fetch = mockFetch({})
    render(LoginForm, { props: { onSuccess: vi.fn() } })
    await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(screen.getByText('Enter your username.')).toBeTruthy()
    expect(screen.getByLabelText('Username').getAttribute('aria-invalid')).toBe('true')
    expect(fetch).not.toHaveBeenCalled()
  })

  it('shows a loading state while signing in, then reports success', async () => {
    const pending = deferred<Response>()
    mockFetch({ 'POST /api/auth/login': () => pending.promise })
    const onSuccess = vi.fn()
    render(LoginForm, { props: { onSuccess } })
    fill('owner', 'correct horse battery staple')
    await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))

    const busy = screen.getByRole('button', { name: 'Signing in…' }) as HTMLButtonElement
    expect(busy.disabled).toBe(true)

    pending.resolve(json(200, { user: { id: 1, username: 'owner' } }))
    await vi.waitFor(() => expect(onSuccess).toHaveBeenCalledWith({ id: 1, username: 'owner' }))
  })

  it('shows an authentication error and clears the password', async () => {
    mockFetch({ 'POST /api/auth/login': () => apiError(401, 'invalid_credentials', 'invalid username or password') })
    const onSuccess = vi.fn()
    render(LoginForm, { props: { onSuccess } })
    fill('owner', 'wrong password')
    await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))

    expect((await screen.findByRole('alert')).textContent).toBe('Incorrect username or password.')
    expect((screen.getByLabelText('Password') as HTMLInputElement).value).toBe('')
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it('shows rate limiting and unreachable-server errors', async () => {
    mockFetch({ 'POST /api/auth/login': () => apiError(429, 'rate_limited', 'too many attempts; try again in 12 seconds') })
    render(LoginForm, { props: { onSuccess: vi.fn() } })
    fill('owner', 'any password')
    await fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect((await screen.findByRole('alert')).textContent).toBe('Too many attempts; try again in 12 seconds.')
  })

  it('uses password-manager friendly autocomplete attributes', () => {
    mockFetch({})
    render(LoginForm, { props: { onSuccess: vi.fn() } })
    expect(screen.getByLabelText('Username').getAttribute('autocomplete')).toBe('username')
    expect(screen.getByLabelText('Password').getAttribute('autocomplete')).toBe('current-password')
    expect(screen.getByLabelText('Password').getAttribute('type')).toBe('password')
  })
})
