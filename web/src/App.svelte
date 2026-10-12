<script lang="ts">
  import { onMount } from 'svelte'
  import LoginForm from './components/LoginForm.svelte'
  import ServerStatus from './components/ServerStatus.svelte'
  import SetupForm from './components/SetupForm.svelte'
  import Shell from './components/Shell.svelte'
  import { ApiError, getAuthStatus, getMe, logout, type User } from './services/api'
  import { sentence } from './services/validation'

  type View =
    | { kind: 'loading' }
    | { kind: 'unavailable'; message: string }
    | { kind: 'setup' }
    | { kind: 'login'; notice?: string }
    | { kind: 'signed-in'; user: User }

  let view = $state<View>({ kind: 'loading' })
  let signOutError = $state('')
  let signingOut = $state(false)

  function sessionExpired() {
    view = { kind: 'login', notice: 'Your session has expired. Sign in again.' }
  }

  // Decides what to show from the server's answer. Any failure is shown as an
  // error; the user is never treated as signed in without the server saying so.
  async function refresh(notice?: string) {
    view = { kind: 'loading' }
    try {
      const status = await getAuthStatus()
      if (status.setup_required) {
        view = { kind: 'setup' }
      } else if (!status.authenticated) {
        view = { kind: 'login', notice }
      } else {
        view = { kind: 'signed-in', user: await getMe() }
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        view = { kind: 'login', notice }
      } else {
        view = { kind: 'unavailable', message: err instanceof Error ? sentence(err.message) : 'Unknown error.' }
      }
    }
  }

  async function signOut() {
    signOutError = ''
    signingOut = true
    try {
      await logout()
      view = { kind: 'login', notice: 'You have been signed out.' }
    } catch (err) {
      signOutError = err instanceof Error ? sentence(err.message) : 'Sign-out failed.'
    } finally {
      signingOut = false
    }
  }

  function signedIn(user: User) {
    signOutError = ''
    view = { kind: 'signed-in', user }
  }

  onMount(() => {
    refresh()
  })
</script>

{#if view.kind === 'signed-in'}
  <!-- Signed in: the music player shell (navigation, views, playback bar). -->
  <Shell user={view.user} {signingOut} {signOutError} onSignOut={signOut} onUnauthorized={sessionExpired} />
{:else}
<div class="shell">
  <header class="masthead">
    <div class="brand">
      <h1 class="wordmark">Rewind</h1>
      <p class="tagline">Your music. Your server.</p>
    </div>
  </header>

  <main class="content">
    {#if view.kind === 'loading'}
      <p class="rw-muted" role="status">Loading…</p>
    {:else if view.kind === 'unavailable'}
      <section class="rw-panel" aria-labelledby="unavailable-heading">
        <h2 id="unavailable-heading" class="rw-panel-title">Rewind is unavailable</h2>
        <p class="rw-alert" role="alert">{view.message}</p>
        <div><button class="rw-button" type="button" onclick={() => refresh()}>Try again</button></div>
      </section>
    {:else if view.kind === 'setup'}
      <SetupForm onComplete={signedIn} onAlreadyComplete={() => refresh()} />
    {:else if view.kind === 'login'}
      <LoginForm onSuccess={signedIn} notice={view.notice} />
    {/if}
  </main>

  <footer class="statusbar">
    <ServerStatus />
  </footer>
</div>
{/if}

<style>
  .shell {
    display: grid;
    grid-template-rows: auto 1fr auto;
    min-height: 100vh;
    min-height: 100dvh;
    max-width: var(--rw-content-max-width);
    margin: 0 auto;
    padding: var(--rw-space-4);
    gap: var(--rw-space-5);
  }

  .masthead {
    display: flex;
    flex-wrap: wrap;
    align-items: end;
    justify-content: space-between;
    gap: var(--rw-space-4);
    border-bottom: var(--rw-border);
    padding-bottom: var(--rw-space-4);
  }

  .wordmark {
    font-size: var(--rw-font-size-display);
    font-weight: var(--rw-font-weight-black);
    line-height: 0.9;
    letter-spacing: -0.04em;
    text-transform: uppercase;
    overflow-wrap: anywhere;
  }

  .tagline {
    margin-top: var(--rw-space-3);
    color: var(--rw-color-accent);
    font-family: var(--rw-font-mono);
    font-size: var(--rw-font-size-sm);
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }

  .content {
    display: grid;
    gap: var(--rw-space-4);
    align-self: start;
    width: 100%;
    max-width: 36rem;
  }

  .statusbar {
    border-top: var(--rw-border);
    padding-top: var(--rw-space-3);
  }

  @media (min-width: 48rem) {
    .shell {
      padding: var(--rw-space-6);
    }
  }
</style>
