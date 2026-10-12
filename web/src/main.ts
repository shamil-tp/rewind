import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'
import { registerServiceWorker } from './services/pwa'
import { applyRememberedTheme } from './services/themes'

// Before the first render: the theme last used in this browser, until the
// signed-in user's saved preference is loaded (see services/themes.ts).
applyRememberedTheme()

const target = document.getElementById('app')
if (!target) {
  throw new Error('Rewind: #app element is missing from index.html')
}

// Only production builds have a service worker (see sw/plugin.ts).
if (import.meta.env.PROD) registerServiceWorker()

export default mount(App, { target })
