// Hand-off for the IdentityConfirm proof token. ActionBus handlers receive the
// grant (`ctx.confirmation.token`); HttpForgeClient reads it through its
// `confirmToken` option. This tiny holder connects the two without changing the
// ForgeClient contract: a handler wraps its write in `withConfirmToken`, and the
// default client factory passes `currentConfirmToken` as the option.

let current: string | undefined

export function currentConfirmToken(): string | undefined {
  return current
}

/** Make `token` visible to the client for the duration of one write. Clears afterwards. */
export async function withConfirmToken<T>(token: string | undefined, run: () => Promise<T>): Promise<T> {
  current = token
  try {
    return await run()
  } finally {
    if (current === token) current = undefined
  }
}
