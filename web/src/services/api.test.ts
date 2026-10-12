import { describe, expect, it } from 'vitest'
import { apiError, json, mockFetch } from '../test/fetch'
import { ApiError, getAuthStatus, login, logout } from './api'
import { newPasswordError, normalizeSetupCode, setupCodeError, usernameError } from './validation'

describe('api client', () => {
  it('sends JSON with same-origin credentials and returns the user', async () => {
    const fetch = mockFetch({
      'POST /api/auth/login': () => json(200, { user: { id: 1, username: 'owner' } }),
    })
    await expect(login('owner', 'secret password')).resolves.toEqual({ id: 1, username: 'owner' })
    const init = fetch.mock.calls[0][1] as RequestInit
    expect(init.credentials).toBe('same-origin')
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json')
    expect(JSON.parse(init.body as string)).toEqual({ username: 'owner', password: 'secret password' })
  })

  it('turns API errors into ApiError with code and message', async () => {
    mockFetch({ 'POST /api/auth/login': () => apiError(401, 'invalid_credentials', 'invalid username or password') })
    const err = await login('owner', 'wrong').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 401, code: 'invalid_credentials', message: 'invalid username or password' })
  })

  it('hides server error details for 5xx responses', async () => {
    mockFetch({ 'GET /api/auth/status': () => apiError(500, 'internal_error', 'database is locked at row 7') })
    const err = await getAuthStatus().catch((e: unknown) => e)
    expect(err).toMatchObject({ status: 500, message: 'The server had a problem. Try again.' })
  })

  it('reports an unreachable server', async () => {
    mockFetch({
      'GET /api/auth/status': () => {
        throw new TypeError('Failed to fetch')
      },
    })
    const err = await getAuthStatus().catch((e: unknown) => e)
    expect(err).toMatchObject({ code: 'network_error', status: undefined, message: 'Cannot reach the Rewind server' })
  })

  it('reports a non-JSON error page', async () => {
    mockFetch({ 'GET /api/auth/status': () => new Response('<html>Bad Gateway</html>', { status: 502 }) })
    await expect(getAuthStatus()).rejects.toMatchObject({ status: 502 })
  })

  it('handles 204 No Content', async () => {
    mockFetch({ 'POST /api/auth/logout': () => new Response(null, { status: 204 }) })
    await expect(logout()).resolves.toBeUndefined()
  })
})

describe('validation mirrors the server rules', () => {
  it('checks usernames', () => {
    expect(usernameError('')).toBeDefined()
    expect(usernameError('has space')).toBeDefined()
    expect(usernameError('a'.repeat(65))).toBeDefined()
    expect(usernameError('owner.name_1-x')).toBeUndefined()
  })

  it('counts password length in characters, not UTF-16 units', () => {
    expect(newPasswordError('elevenchars', 'owner')).toBeDefined()
    expect(newPasswordError('twelve chars', 'owner')).toBeUndefined()
    expect(newPasswordError('🎵'.repeat(11), 'owner')).toBeDefined() // 11 characters, 22 UTF-16 units
    expect(newPasswordError('🎵'.repeat(12), 'owner')).toBeUndefined()
    expect(newPasswordError('x'.repeat(257), 'owner')).toBeDefined()
    expect(newPasswordError('OwnerOwner12', 'ownerowner12')).toBeDefined()
  })

  it('normalizes and checks setup codes', () => {
    expect(normalizeSetupCode(' abcd-efgh ijkl-mnop ')).toBe('ABCDEFGHIJKLMNOP')
    expect(setupCodeError('abcd-efgh-ijkl-mnop')).toBeUndefined()
    expect(setupCodeError('')).toBeDefined()
    expect(setupCodeError('ABCD-EFGH')).toBeDefined()
    expect(setupCodeError('ABCD-EFGH-IJKL-MN01')).toBeDefined() // 0 and 1 are not base32
  })
})
