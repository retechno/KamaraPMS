import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { components } from '@/api/schema'
import { refreshSession, setAccessToken } from '@/api/session'

export type Me = components['schemas']['Me']

const TENANT_KEY = 'kamarapms.lastTenantCode'

export function rememberedTenantCode(): string {
  try {
    return localStorage.getItem(TENANT_KEY) ?? ''
  } catch {
    return ''
  }
}

function rememberTenantCode(code: string): void {
  try {
    localStorage.setItem(TENANT_KEY, code.trim().toUpperCase())
  } catch {
    // not remembered; harmless
  }
}

/** Who is signed in and what they may do. */
export const useAuthStore = defineStore('auth', () => {
  const status = ref<'unknown' | 'authenticated' | 'anonymous'>('unknown')
  const me = ref<Me | null>(null)
  let initializing: Promise<void> | null = null

  const isAuthenticated = computed(() => status.value === 'authenticated')
  const isAdmin = computed(() => me.value?.user.is_tenant_admin ?? false)
  const displayName = computed(() => me.value?.user.full_name ?? '')

  /** Restores the session from the refresh cookie (once per page load). */
  function init(): Promise<void> {
    if (status.value !== 'unknown') return Promise.resolve()
    initializing ??= (async () => {
      const session = await refreshSession()
      if (session) {
        await loadMe()
        status.value = 'authenticated'
      } else {
        status.value = 'anonymous'
      }
    })().finally(() => {
      initializing = null
    })
    return initializing
  }

  async function login(tenantCode: string, email: string, password: string): Promise<void> {
    const { data } = await api.POST('/api/v1/auth/login', { body: { tenant_code: tenantCode, email, password } })
    setAccessToken(data?.access_token ?? null)
    rememberTenantCode(tenantCode)
    await loadMe()
    status.value = 'authenticated'
  }

  async function logout(): Promise<void> {
    try {
      await api.POST('/api/v1/auth/logout')
    } finally {
      signedOut()
    }
  }

  /** Local sign-out (after logout, or when the server ended the session). */
  function signedOut(): void {
    setAccessToken(null)
    me.value = null
    status.value = 'anonymous'
  }

  async function loadMe(): Promise<void> {
    const { data } = await api.GET('/api/v1/auth/me')
    me.value = data ?? null
  }

  /** Whether the caller holds a permission at a property (admins hold all). */
  function can(permission: string, propertyId: number | null): boolean {
    if (isAdmin.value) return true
    const p = me.value?.properties.find((x) => x.id === propertyId)
    return p?.permissions.includes(permission) ?? false
  }

  return { status, me, isAuthenticated, isAdmin, displayName, init, login, logout, signedOut, loadMe, can }
})
