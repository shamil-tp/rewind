// @vitest-environment node
import { existsSync, readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { buildServiceWorker, shellFiles } from './plugin.ts'

const web = new URL('../', import.meta.url)
const read = (path: string) => readFileSync(new URL(path, web))

/** Width and height from a PNG's IHDR chunk. */
function pngSize(buf: Buffer): [number, number] {
  expect(buf.subarray(1, 4).toString('ascii')).toBe('PNG')
  return [buf.readUInt32BE(16), buf.readUInt32BE(20)]
}

interface ManifestIcon {
  src: string
  sizes: string
  type: string
  purpose: string
}

describe('web app manifest', () => {
  const manifest = JSON.parse(read('public/manifest.webmanifest').toString('utf8'))

  it('has the fields browsers need to offer installation', () => {
    expect(manifest.name).toBe('Rewind')
    expect(manifest.short_name).toBe('Rewind')
    expect(manifest.start_url).toBe('/')
    expect(manifest.scope).toBe('/')
    expect(manifest.id).toBe('/')
    expect(manifest.display).toBe('standalone')
    expect(manifest.background_color).toMatch(/^#[0-9a-f]{6}$/)
    expect(manifest.theme_color).toMatch(/^#[0-9a-f]{6}$/)
  })

  it('lists 192 and 512 pixel icons plus a maskable icon, all present with matching sizes', () => {
    const icons = manifest.icons as ManifestIcon[]
    const png = icons.filter((i) => i.type === 'image/png')
    expect(png.some((i) => i.sizes === '192x192' && i.purpose === 'any')).toBe(true)
    expect(png.some((i) => i.sizes === '512x512' && i.purpose === 'any')).toBe(true)
    expect(png.some((i) => i.sizes === '512x512' && i.purpose === 'maskable')).toBe(true)
    for (const icon of icons) {
      expect(existsSync(new URL('public' + icon.src, web)), icon.src).toBe(true)
      if (icon.type === 'image/png') {
        const [w, h] = pngSize(read('public' + icon.src))
        expect(`${w}x${h}`).toBe(icon.sizes)
      }
    }
    expect(pngSize(read('public/icons/apple-touch-icon.png'))).toEqual([180, 180])
  })

  it('only refers to same-origin paths', () => {
    for (const icon of manifest.icons as ManifestIcon[]) expect(icon.src.startsWith('/')).toBe(true)
  })
})

describe('application shell files', () => {
  it('links the manifest and touch icon from index.html', () => {
    const html = read('index.html').toString('utf8')
    expect(html).toContain('<link rel="manifest" href="/manifest.webmanifest" />')
    expect(html).toContain('<link rel="apple-touch-icon" href="/icons/apple-touch-icon.png" />')
  })

  it('has every file the service worker caches', () => {
    for (const url of shellFiles) expect(existsSync(new URL('public' + url, web)), url).toBe(true)
  })

  it('has an offline page that works under the Content Security Policy', () => {
    const html = read('public/offline.html').toString('utf8')
    // No inline scripts or styles: the CSP allows only same-origin files.
    expect(html).not.toMatch(/<script/i)
    expect(html).not.toMatch(/<style/i)
    expect(html).not.toMatch(/\sstyle=/i)
    expect(html).not.toMatch(/https?:\/\//)
    expect(html).toContain('href="/offline.css"')
    expect(html).toContain('href="/"')
  })

  it('refuses to cache API paths or URLs with queries', () => {
    const template = read('sw/service-worker.js').toString('utf8')
    expect(() => buildServiceWorker(template, [{ url: '/api/auth/me', content: '' }])).toThrow(/refusing/)
    expect(() => buildServiceWorker(template, [{ url: '/x.js?v=1', content: '' }])).toThrow(/refusing/)
    expect(() => buildServiceWorker('no placeholder', [])).toThrow(/placeholder/)
  })
})
