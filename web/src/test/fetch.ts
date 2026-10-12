import { vi } from 'vitest'

type Route = (init: RequestInit | undefined) => Response | Promise<Response>

/** A JSON response like the Rewind API returns. */
export function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

export function apiError(status: number, code: string, message: string): Response {
  return json(status, { error: { code, message } })
}

/**
 * Replaces global fetch with a router keyed by "METHOD /path". Unknown
 * requests fail the test. Returns the mock so calls can be inspected.
 */
export function mockFetch(routes: Record<string, Route>) {
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${String(input)}`
    const route = routes[key]
    if (!route) throw new Error(`unexpected request: ${key}`)
    return route(init)
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

/** A promise whose resolution the test controls, for observing loading states. */
export function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}
