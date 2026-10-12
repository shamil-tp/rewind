// SPDX-License-Identifier: Apache-2.0
//
// Renders the PWA icons in web/public/icons/ from web/public/favicon.svg using
// a locally installed Chromium-based browser (Chrome, Chromium or Edge) in
// headless mode. The generated PNGs are committed, so this only needs to run
// when the artwork changes; builds never run it.
//
// Usage: node scripts/render-icons.mjs [path-to-browser]
//        (or set BROWSER=/path/to/chromium)
//
// Needs Node.js 22 or later (built-in WebSocket). No npm packages.

import { spawn } from 'node:child_process'
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const favicon = readFileSync(join(root, 'web/public/favicon.svg'), 'utf8')
const outDir = join(root, 'web/public/icons')
const background = '#0b0b0b'

// The artwork without its outer <svg> element, for re-embedding.
const inner = favicon.replace(/^<svg[^>]*>/, '').replace(/<\/svg>\s*$/, '')

// Full-bleed icon: the favicon artwork at full size.
const standard = favicon
// Maskable icon: platforms crop to a circle or squircle inside the central 80%,
// so the artwork is scaled to 60% on a full-bleed background.
const maskable = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" fill="${background}"/><g transform="translate(6.4 6.4) scale(0.6)">${inner}</g></svg>`

const icons = [
  { file: 'icon-192.png', size: 192, svg: standard },
  { file: 'icon-512.png', size: 512, svg: standard },
  { file: 'icon-maskable-512.png', size: 512, svg: maskable },
  // iOS applies its own rounded mask and ignores transparency; use padding.
  { file: 'apple-touch-icon.png', size: 180, svg: maskable },
]

const candidates = [
  process.argv[2],
  process.env.BROWSER,
  'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
  'C:/Program Files/Google/Chrome/Application/chrome.exe',
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser',
  '/usr/bin/google-chrome',
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
].filter(Boolean)
const browser = candidates.find((p) => existsSync(p))
if (!browser) {
  console.error('No Chromium-based browser found; pass its path as the first argument or set BROWSER.')
  process.exit(1)
}

const profile = mkdtempSync(join(tmpdir(), 'rewind-icons-'))
const proc = spawn(browser, ['--headless=new', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--no-first-run', 'about:blank'], {
  stdio: 'ignore',
})
try {
  const portFile = join(profile, 'DevToolsActivePort')
  for (let i = 0; i < 100 && !existsSync(portFile); i++) await new Promise((r) => setTimeout(r, 100))
  const port = readFileSync(portFile, 'utf8').split('\n')[0]
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
  const page = targets.find((t) => t.type === 'page')
  const ws = new WebSocket(page.webSocketDebuggerUrl)
  await new Promise((r, j) => ((ws.onopen = r), (ws.onerror = j)))
  let id = 0
  const pending = new Map()
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data)
    if (m.id && pending.has(m.id)) {
      pending.get(m.id)(m)
      pending.delete(m.id)
    }
  }
  const send = (method, params = {}) =>
    new Promise((r, j) => {
      pending.set(++id, (m) => (m.error ? j(new Error(`${method}: ${m.error.message}`)) : r(m.result)))
      ws.send(JSON.stringify({ id, method, params }))
    })

  await send('Page.enable')
  for (const icon of icons) {
    const html = `<!doctype html><html><body style="margin:0;background:${background}">${icon.svg.replace('<svg ', `<svg width="${icon.size}" height="${icon.size}" style="display:block" `)}</body></html>`
    await send('Emulation.setDeviceMetricsOverride', { width: icon.size, height: icon.size, deviceScaleFactor: 1, mobile: false })
    await send('Page.navigate', { url: 'data:text/html;base64,' + Buffer.from(html).toString('base64') })
    await new Promise((r) => setTimeout(r, 300))
    const shot = await send('Page.captureScreenshot', {
      format: 'png',
      clip: { x: 0, y: 0, width: icon.size, height: icon.size, scale: 1 },
    })
    writeFileSync(join(outDir, icon.file), Buffer.from(shot.data, 'base64'))
    console.log(`wrote web/public/icons/${icon.file} (${icon.size}x${icon.size})`)
  }
  ws.close()
} finally {
  proc.kill()
  await new Promise((r) => setTimeout(r, 300))
  rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 })
}
