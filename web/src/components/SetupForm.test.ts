import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { apiError, deferred, json, mockFetch } from '../test/fetch'
import SetupForm from './SetupForm.svelte'

function fill(values: { code?: string; username?: string; password?: string; confirm?: string }) {
  const set = (label: string, value: string | undefined) => {
    if (value !== undefined) fireEvent.input(screen.getByLabelText(label), { target: { value } })
  }
  set('Setup code', values.code)
  set('Username', values.username)
  set('Password', values.password)
  set('Confirm password', values.confirm)
}

const valid = {
  code: 'abcd-efgh-ijkl-mnop',
  username: 'owner',
  password: 'correct horse battery staple',
  confirm: 'correct horse battery staple',
}

function renderForm() {
  const onComplete = vi.fn()
  const onAlreadyComplete = vi.fn()
  render(SetupForm, { props: { onComplete, onAlreadyComplete } })
  return { onComplete, onAlreadyComplete }
}

describe('SetupForm', () => {
  it('validates every field before calling the server', async () => {
    const fetch = mockFetch({})
    renderForm()
    fill({ code: 'nope', username: 'bad name', password: 'short', confirm: 'different' })
    await fireEvent.click(screen.getByRole('button', { name: 'Create account' }))

    expect(screen.getByText(/setup code has 16 letters/)).toBeTruthy()
    expect(screen.getByText(/Use 1–64 letters/)).toBeTruthy()
    expect(screen.getByText('Use at least 12 characters.')).toBeTruthy()
    expect(screen.getByText('The passwords do not match.')).toBeTruthy()
    expect(fetch).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(screen.getByLabelText('Setup code'))
  })

  it('submits the normalized code, shows loading, and completes', async () => {
    const pending = deferred<Response>()
    const fetch = mockFetch({ 'POST /api/auth/setup': () => pending.promise })
    const { onComplete } = renderForm()
    fill(valid)
    await fireEvent.click(screen.getByRole('button', { name: 'Create account' }))

    expect((screen.getByRole('button', { name: 'Creating account…' }) as HTMLButtonElement).disabled).toBe(true)
    const body = JSON.parse((fetch.mock.calls[0][1] as RequestInit).body as string)
    expect(body).toEqual({ setup_code: 'ABCDEFGHIJKLMNOP', username: 'owner', password: valid.password })

    pending.resolve(json(201, { user: { id: 1, username: 'owner' } }))
    await vi.waitFor(() => expect(onComplete).toHaveBeenCalledWith({ id: 1, username: 'owner' }))
  })

  it('marks the code field when the server rejects the code', async () => {
    mockFetch({ 'POST /api/auth/setup': () => apiError(403, 'invalid_setup_code', 'the setup code is not valid') })
    const { onComplete } = renderForm()
    fill(valid)
    await fireEvent.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByText(/This setup code is not valid/)).toBeTruthy()
    expect(screen.getByLabelText('Setup code').getAttribute('aria-invalid')).toBe('true')
    expect(onComplete).not.toHaveBeenCalled()
  })

  it('hands over to sign-in when setup was already completed', async () => {
    mockFetch({ 'POST /api/auth/setup': () => apiError(409, 'setup_complete', 'setup has already been completed') })
    const { onAlreadyComplete } = renderForm()
    fill(valid)
    await fireEvent.click(screen.getByRole('button', { name: 'Create account' }))
    await vi.waitFor(() => expect(onAlreadyComplete).toHaveBeenCalled())
  })

  it('shows server validation messages', async () => {
    mockFetch({
      'POST /api/auth/setup': () => apiError(422, 'validation_failed', 'password must not be the same as the username'),
    })
    renderForm()
    fill(valid)
    await fireEvent.click(screen.getByRole('button', { name: 'Create account' }))
    expect((await screen.findByRole('alert')).textContent).toBe('Password must not be the same as the username.')
  })
})
