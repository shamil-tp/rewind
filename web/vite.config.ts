/// <reference types="vitest/config" />
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'
import { defineConfig, loadEnv } from 'vite'
import { serviceWorker } from './sw/plugin.ts'

// In development, Vite serves the frontend and forwards /api to the Go server.
// The target defaults to the Go server's default address; override it with
// REWIND_DEV_API_TARGET (environment or web/.env.local) if REWIND_ADDR differs.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), 'REWIND_')
  const apiTarget = env.REWIND_DEV_API_TARGET || 'http://127.0.0.1:8090'

  return {
    plugins: mode === 'test' ? [svelte(), svelteTesting()] : [svelte(), serviceWorker()],
    server: {
      proxy: {
        '/api': { target: apiTarget },
      },
    },
    build: {
      outDir: 'dist',
      emptyOutDir: true,
    },
    test: {
      environment: 'jsdom',
      include: ['src/**/*.test.ts', 'sw/**/*.test.ts'],
      restoreMocks: true,
      unstubGlobals: true,
      // Tests read app.css as text (?raw) to check the theme tokens; other
      // CSS stays stubbed.
      css: { include: [/src\/app\.css/] },
    },
  }
})
