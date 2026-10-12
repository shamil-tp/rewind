/**
 * Registers the service worker built from sw/service-worker.js.
 *
 * Browsers only allow service workers in secure contexts (HTTPS, or
 * http://localhost). Over plain-HTTP LAN addresses registration is skipped
 * and Rewind works as a normal web page. Failure never affects the player.
 */
export function registerServiceWorker(win: Window = window): boolean {
  if (!win.isSecureContext || !('serviceWorker' in win.navigator)) return false
  const register = () => {
    win.navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch((err: unknown) => {
      console.warn('Rewind: service worker registration failed; the app still works without it.', err)
    })
  }
  if (win.document.readyState === 'complete') register()
  else win.addEventListener('load', register, { once: true })
  return true
}
