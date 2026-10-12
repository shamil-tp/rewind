// SPDX-License-Identifier: Apache-2.0
//
// Build-time generation of dist/sw.js from sw/service-worker.js. No
// third-party PWA plugin: the policy is small and lives in one reviewable file.

import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import type { Plugin } from 'vite'

/** Files from public/ that make up the offline-capable shell. */
export const shellFiles = [
  '/offline.html',
  '/offline.css',
  '/manifest.webmanifest',
  '/favicon.svg',
  '/icons/icon-192.png',
  '/icons/icon-512.png',
  '/icons/icon-maskable-512.png',
  '/icons/apple-touch-icon.png',
]

const placeholder = /\/\*__REWIND_MANIFEST__\*\/ \{ version: 'dev', urls: \[\] \}/

export interface ShellEntry {
  url: string
  content: string | Uint8Array
}

/**
 * Fills the service-worker template with the URLs to cache and a version
 * that changes whenever the template or any cached file changes.
 */
export function buildServiceWorker(template: string, entries: ShellEntry[]): string {
  if (!placeholder.test(template)) throw new Error('service worker template is missing the manifest placeholder')
  const sorted = [...entries].sort((a, b) => a.url.localeCompare(b.url))
  for (const e of sorted) {
    if (!e.url.startsWith('/') || e.url.startsWith('/api/') || e.url.includes('?')) {
      throw new Error(`refusing to cache ${e.url}`)
    }
  }
  const hash = createHash('sha256').update(template)
  for (const e of sorted) hash.update('\0' + e.url + '\0').update(e.content)
  const manifest = { version: hash.digest('hex').slice(0, 16), urls: sorted.map((e) => e.url) }
  return template.replace(placeholder, () => JSON.stringify(manifest))
}

/** Emits sw.js in production builds. Development never registers a worker. */
export function serviceWorker(): Plugin {
  let publicDir = ''
  return {
    name: 'rewind-service-worker',
    apply: 'build',
    configResolved(config) {
      publicDir = config.publicDir
    },
    generateBundle(_options, bundle) {
      const entries: ShellEntry[] = []
      for (const [fileName, output] of Object.entries(bundle)) {
        // Only content-hashed build output; source maps are not needed offline.
        if (!fileName.startsWith('assets/') || fileName.endsWith('.map')) continue
        entries.push({ url: '/' + fileName, content: output.type === 'chunk' ? output.code : output.source })
      }
      for (const url of shellFiles) {
        // A missing shell file fails the build rather than shipping a broken worker.
        entries.push({ url, content: readFileSync(`${publicDir}${url}`) })
      }
      const template = readFileSync(fileURLToPath(new URL('./service-worker.js', import.meta.url)), 'utf8')
      this.emitFile({ type: 'asset', fileName: 'sw.js', source: buildServiceWorker(template, entries) })
    },
  }
}
