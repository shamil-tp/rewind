import { describe, expect, it, vi } from 'vitest'
import { registerServiceWorker } from './pwa'

function fakeWindow(opts: { secure?: boolean; sw?: boolean; ready?: DocumentReadyState; register?: () => Promise<unknown> }) {
  const listeners: Record<string, () => void> = {}
  const register = vi.fn(opts.register ?? (() => Promise.resolve({})))
  const navigator = opts.sw === false ? {} : { serviceWorker: { register } }
  const win = {
    isSecureContext: opts.secure ?? true,
    navigator,
    document: { readyState: opts.ready ?? 'complete' },
    addEventListener: (type: string, fn: () => void) => (listeners[type] = fn),
  } as unknown as Window
  return { win, register, listeners }
}

describe('registerServiceWorker', () => {
  it('registers /sw.js for the whole app in a secure context', () => {
    const { win, register } = fakeWindow({})
    expect(registerServiceWorker(win)).toBe(true)
    expect(register).toHaveBeenCalledWith('/sw.js', { scope: '/' })
  })

  it('waits for the page to load first', () => {
    const { win, register, listeners } = fakeWindow({ ready: 'loading' })
    registerServiceWorker(win)
    expect(register).not.toHaveBeenCalled()
    listeners.load()
    expect(register).toHaveBeenCalledTimes(1)
  })

  it('does nothing over plain HTTP on a LAN address (not a secure context)', () => {
    const { win, register } = fakeWindow({ secure: false })
    expect(registerServiceWorker(win)).toBe(false)
    expect(register).not.toHaveBeenCalled()
  })

  it('does nothing when the browser has no service worker support', () => {
    const { win } = fakeWindow({ sw: false })
    expect(registerServiceWorker(win)).toBe(false)
  })

  it('reports a failed registration without throwing', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { win } = fakeWindow({ register: () => Promise.reject(new Error('blocked')) })
    expect(registerServiceWorker(win)).toBe(true)
    await vi.waitFor(() => expect(warn).toHaveBeenCalled())
  })
})
