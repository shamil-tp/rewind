<script lang="ts">
  import { ApiError, login, type User } from '../services/api'
  import { sentence } from '../services/validation'

  interface Props {
    onSuccess: (user: User) => void
    notice?: string
  }
  let { onSuccess, notice }: Props = $props()

  let username = $state('')
  let password = $state('')
  let errors = $state<{ username?: string; password?: string }>({})
  let formError = $state('')
  let busy = $state(false)

  let usernameInput: HTMLInputElement | undefined = $state()
  let passwordInput: HTMLInputElement | undefined = $state()

  async function submit(event: SubmitEvent) {
    event.preventDefault()
    formError = ''
    errors = {
      username: username.trim() === '' ? 'Enter your username.' : undefined,
      password: password === '' ? 'Enter your password.' : undefined,
    }
    if (errors.username) return usernameInput?.focus()
    if (errors.password) return passwordInput?.focus()

    busy = true
    try {
      const user = await login(username.trim(), password)
      password = ''
      onSuccess(user)
    } catch (err) {
      password = ''
      if (err instanceof ApiError && err.code === 'invalid_credentials') {
        formError = 'Incorrect username or password.'
      } else {
        formError = err instanceof Error ? sentence(err.message) : 'Sign-in failed.'
      }
      passwordInput?.focus()
    } finally {
      busy = false
    }
  }
</script>

<section class="rw-panel" aria-labelledby="login-heading">
  <h2 id="login-heading" class="rw-panel-title">Sign in</h2>

  {#if notice}
    <p class="rw-notice" role="status">{notice}</p>
  {/if}
  {#if formError}
    <p class="rw-alert" role="alert">{formError}</p>
  {/if}

  <form class="rw-form" novalidate onsubmit={submit} aria-busy={busy}>
    <div class="rw-field">
      <label class="rw-label" for="login-username">Username</label>
      <input
        id="login-username"
        class="rw-input"
        name="username"
        autocomplete="username"
        autocapitalize="none"
        spellcheck="false"
        bind:value={username}
        bind:this={usernameInput}
        aria-invalid={errors.username ? 'true' : undefined}
        aria-describedby={errors.username ? 'login-username-error' : undefined}
      />
      {#if errors.username}<p id="login-username-error" class="rw-field-error">{errors.username}</p>{/if}
    </div>

    <div class="rw-field">
      <label class="rw-label" for="login-password">Password</label>
      <input
        id="login-password"
        class="rw-input"
        type="password"
        name="password"
        autocomplete="current-password"
        bind:value={password}
        bind:this={passwordInput}
        aria-invalid={errors.password ? 'true' : undefined}
        aria-describedby={errors.password ? 'login-password-error' : undefined}
      />
      {#if errors.password}<p id="login-password-error" class="rw-field-error">{errors.password}</p>{/if}
    </div>

    <button class="rw-button" type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
  </form>
</section>
