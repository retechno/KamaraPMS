import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { platformApi } from '@/api/platformClient'
import { refreshPlatformSession, setPlatformToken, type PlatformSession } from '@/api/platformSession'
import type { components } from '@/api/schema'

export type PlatformOperator = components['schemas']['PlatformOperator']
export type PlatformChallenge = components['schemas']['PlatformChallenge']
export type PlatformEnrollStarted = components['schemas']['PlatformEnrollStarted']

/**
 * Who is signed in to the platform console. It never touches the hotel's `auth` store: an operator is not a user of any tenant, and the two sessions
 * (tokens, cookies, stores) are independent.
 */
export const usePlatformAuthStore = defineStore('platformAuth', () => {
  const status = ref<'unknown' | 'authenticated' | 'anonymous'>('unknown')
  const operator = ref<PlatformOperator | null>(null)
  let initializing: Promise<void> | null = null

  const isAuthenticated = computed(() => status.value === 'authenticated')
  const displayName = computed(() => operator.value?.full_name ?? '')

  function open(session: PlatformSession): void {
    setPlatformToken(session.access_token)
    operator.value = session.operator
    status.value = 'authenticated'
  }

  /** Restores the session from the platform refresh cookie (once per page load). */
  function init(): Promise<void> {
    if (status.value !== 'unknown') return Promise.resolve()
    initializing ??= (async () => {
      const session = await refreshPlatformSession()
      if (session) open(session)
      else status.value = 'anonymous'
    })().finally(() => {
      initializing = null
    })
    return initializing
  }

  /** Step 1: the password. The answer says what comes next (a code, or the enrolment). */
  async function login(email: string, password: string): Promise<PlatformChallenge> {
    const { data } = await platformApi.POST('/api/v1/platform/auth/login', { body: { email, password } })
    return data as PlatformChallenge
  }

  /** Step 2 with the authenticator, or with a recovery code. */
  async function verify(challenge: string, input: { code?: string; recoveryCode?: string }): Promise<void> {
    const { data } = await platformApi.POST('/api/v1/platform/auth/totp', { body: { challenge, code: input.code, recovery_code: input.recoveryCode } })
    open(data as PlatformSession)
  }

  async function enrollStart(challenge: string): Promise<PlatformEnrollStarted> {
    const { data } = await platformApi.POST('/api/v1/platform/auth/enroll/start', { body: { challenge } })
    return data as PlatformEnrollStarted
  }

  /**
   * Confirms the enrolment and answers the 10 recovery codes, for the caller only: they go to the screen and to nothing else (no store, no storage, no log).
   * The console is opened by the `open` it answers, once the person has saved them.
   */
  async function enrollConfirm(challenge: string, code: string): Promise<{ codes: string[]; open: () => void }> {
    const { data } = await platformApi.POST('/api/v1/platform/auth/enroll/confirm', { body: { challenge, code } })
    const session = data as PlatformSession
    // The access token is kept now (the session exists on the server), but the screens stay on the sign-in page until the codes are saved.
    setPlatformToken(session.access_token)
    const codes = session.recovery_codes ?? []
    const withoutCodes: PlatformSession = { ...session, recovery_codes: undefined } // what the store keeps never holds the codes
    return { codes, open: () => open(withoutCodes) }
  }

  async function logout(): Promise<void> {
    try {
      await platformApi.POST('/api/v1/platform/auth/logout')
    } finally {
      signedOut()
    }
  }

  /** Local sign-out (after logout, or when the server ended the session). */
  function signedOut(): void {
    setPlatformToken(null)
    operator.value = null
    status.value = 'anonymous'
  }

  return { status, operator, isAuthenticated, displayName, init, login, verify, enrollStart, enrollConfirm, logout, signedOut }
})
