<script lang="ts">
  import { ApiError, setupAccount, type User } from '../services/api'
  import {
    MIN_PASSWORD_LENGTH,
    newPasswordError,
    normalizeSetupCode,
    sentence,
    setupCodeError,
    usernameError,
  } from '../services/validation'

  interface Props {
    onComplete: (user: User) => void
    /** Called when the server reports that setup was already completed. */
    onAlreadyComplete: () => void
  }
  let { onComplete, onAlreadyComplete }: Props = $props()

  type Field = 'code' | 'username' | 'password' | 'confirm'

  let code = $state('')
  let username = $state('')
  let password = $state('')
  let confirm = $state('')
  let errors = $state<Partial<Record<Field, string>>>({})
  let formError = $state('')
  let busy = $state(false)

  const inputs: Partial<Record<Field, HTMLInputElement>> = $state({})

  function validate(): Field | undefined {
    errors = {
      code: setupCodeError(code),
      username: usernameError(username),
      password: newPasswordError(password, username),
      confirm: confirm !== password ? 'The passwords do not match.' : undefined,
    }
    return (['code', 'username', 'password', 'confirm'] as Field[]).find((f) => errors[f])
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault()
    formError = ''
    const invalid = validate()
    if (invalid) {
      inputs[invalid]?.focus()
      return
    }

    busy = true
    try {
      const user = await setupAccount(normalizeSetupCode(code), username, password)
      password = ''
      confirm = ''
      onComplete(user)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'invalid_setup_code') {
        errors = { ...errors, code: 'This setup code is not valid. Check the setup-code file.' }
        inputs.code?.focus()
      } else if (err instanceof ApiError && err.code === 'setup_complete') {
        onAlreadyComplete()
      } else {
        formError = err instanceof Error ? sentence(err.message) : 'Setup failed.'
      }
    } finally {
      busy = false
    }
  }
</script>

<section class="rw-panel" aria-labelledby="setup-heading">
  <h2 id="setup-heading" class="rw-panel-title">Create the owner account</h2>
  <p class="rw-muted">
    Rewind has no account yet. Enter the one-time setup code from the file <code>setup-code</code> in
    Rewind's data directory. The server log shows its exact location.
  </p>

  {#if formError}
    <p class="rw-alert" role="alert">{formError}</p>
  {/if}

  <form class="rw-form" novalidate onsubmit={submit} aria-busy={busy}>
    <div class="rw-field">
      <label class="rw-label" for="setup-code">Setup code</label>
      <input
        id="setup-code"
        class="rw-input"
        name="setup-code"
        autocomplete="off"
        autocapitalize="characters"
        spellcheck="false"
        placeholder="XXXX-XXXX-XXXX-XXXX"
        bind:value={code}
        bind:this={inputs.code}
        aria-invalid={errors.code ? 'true' : undefined}
        aria-describedby={errors.code ? 'setup-code-error' : undefined}
      />
      {#if errors.code}<p id="setup-code-error" class="rw-field-error">{errors.code}</p>{/if}
    </div>

    <div class="rw-field">
      <label class="rw-label" for="setup-username">Username</label>
      <input
        id="setup-username"
        class="rw-input"
        name="username"
        autocomplete="username"
        autocapitalize="none"
        spellcheck="false"
        bind:value={username}
        bind:this={inputs.username}
        aria-invalid={errors.username ? 'true' : undefined}
        aria-describedby={errors.username ? 'setup-username-error' : 'setup-username-hint'}
      />
      {#if errors.username}
        <p id="setup-username-error" class="rw-field-error">{errors.username}</p>
      {:else}
        <p id="setup-username-hint" class="rw-hint">Letters, digits, '.', '_' or '-'.</p>
      {/if}
    </div>

    <div class="rw-field">
      <label class="rw-label" for="setup-password">Password</label>
      <input
        id="setup-password"
        class="rw-input"
        type="password"
        name="new-password"
        autocomplete="new-password"
        bind:value={password}
        bind:this={inputs.password}
        aria-invalid={errors.password ? 'true' : undefined}
        aria-describedby={errors.password ? 'setup-password-error' : 'setup-password-hint'}
      />
      {#if errors.password}
        <p id="setup-password-error" class="rw-field-error">{errors.password}</p>
      {:else}
        <p id="setup-password-hint" class="rw-hint">At least {MIN_PASSWORD_LENGTH} characters. A passphrase works well.</p>
      {/if}
    </div>

    <div class="rw-field">
      <label class="rw-label" for="setup-confirm">Confirm password</label>
      <input
        id="setup-confirm"
        class="rw-input"
        type="password"
        name="confirm-password"
        autocomplete="new-password"
        bind:value={confirm}
        bind:this={inputs.confirm}
        aria-invalid={errors.confirm ? 'true' : undefined}
        aria-describedby={errors.confirm ? 'setup-confirm-error' : undefined}
      />
      {#if errors.confirm}<p id="setup-confirm-error" class="rw-field-error">{errors.confirm}</p>{/if}
    </div>

    <button class="rw-button" type="submit" disabled={busy}>{busy ? 'Creating account…' : 'Create account'}</button>
  </form>
</section>
