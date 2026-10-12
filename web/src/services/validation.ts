// Client-side checks that mirror the server's rules (internal/auth). The server
// remains authoritative; these only give faster feedback.

export const MIN_PASSWORD_LENGTH = 12
export const MAX_PASSWORD_LENGTH = 256

const USERNAME_PATTERN = /^[A-Za-z0-9._-]{1,64}$/
const SETUP_CODE_PATTERN = /^[A-Z2-7]{16}$/

/** Counts Unicode code points, matching the server's character count. */
function length(s: string): number {
  return [...s].length
}

export function usernameError(username: string): string | undefined {
  if (username === '') return 'Enter a username.'
  if (!USERNAME_PATTERN.test(username)) {
    return "Use 1–64 letters, digits, '.', '_' or '-'."
  }
  return undefined
}

export function newPasswordError(password: string, username: string): string | undefined {
  const n = length(password)
  if (n < MIN_PASSWORD_LENGTH) return `Use at least ${MIN_PASSWORD_LENGTH} characters.`
  if (n > MAX_PASSWORD_LENGTH) return `Use at most ${MAX_PASSWORD_LENGTH} characters.`
  if (username !== '' && password.toLowerCase() === username.toLowerCase()) {
    return 'The password must not be the same as the username.'
  }
  return undefined
}

export function normalizeSetupCode(code: string): string {
  return code.replace(/[\s-]/g, '').toUpperCase()
}

export function setupCodeError(code: string): string | undefined {
  if (code.trim() === '') return 'Enter the setup code.'
  if (!SETUP_CODE_PATTERN.test(normalizeSetupCode(code))) {
    return 'The setup code has 16 letters and digits, like ABCD-EFGH-IJKL-MNOP.'
  }
  return undefined
}

/** Capitalizes a server message for display. */
export function sentence(message: string): string {
  if (message === '') return message
  const s = message.charAt(0).toUpperCase() + message.slice(1)
  return /[.!?]$/.test(s) ? s : s + '.'
}
