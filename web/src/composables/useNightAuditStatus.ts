import { computed, onBeforeUnmount, watch } from 'vue'
import { type RouteLocationRaw, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useNightAuditStore } from '@/stores/nightAudit'
import { usePropertyStore } from '@/stores/property'
import type { AuditStep } from '@/utils/nightAuditReadiness'

export type NightAuditState = 'later' | 'overdue' | 'checking' | 'pending' | 'ready'

/**
 * The state of the night audit as the chip of the top bar and the Today card show it, from one rule: `ready` only when the audit can run now (the clock allows it and no step needs anyone).
 * Before its time it is `later`; once its time has come the preview says `pending` (with how many steps and a link to the first) or `ready`, and `checking` until the preview is read; past
 * the business date it is `overdue` (told at once, whatever the steps). Only who may run the audit sees a state (null), and the preview is read only once its time has come.
 */
export function useNightAuditStatus(options: { navigation?: boolean } = {}) {
  const auth = useAuthStore()
  const property = usePropertyStore()
  const store = useNightAuditStore()

  const visible = computed(() => !!property.clock && auth.can('nightaudit.run', property.currentId))
  const dueNow = computed(() => !!property.clock && property.clock.night_audit_allowed && !property.clock.night_audit_overdue) // overdue is told without a look at the steps

  /** Reads the preview if it is wanted and not known (or stale): only once the audit is due, only for who may run it; a read on its way is shared. */
  function refresh(): void {
    if (visible.value && dueNow.value && property.currentId !== null) void store.ensure(property.currentId)
  }

  watch([visible, dueNow, () => property.currentId, () => property.clock?.business_date, () => store.version], refresh, { immediate: true })

  // The bar of the top is on every page: it looks again when the person goes to another page (the store keeps what it read for two minutes), so it does not fall behind what was done elsewhere.
  if (options.navigation) {
    const stop = useRouter().afterEach(refresh)
    onBeforeUnmount(stop)
  }

  const state = computed<NightAuditState | null>(() => {
    const c = property.clock
    if (!visible.value || !c) return null
    if (c.night_audit_overdue) return 'overdue'
    if (!c.night_audit_allowed) return 'later'
    if (store.propertyId !== property.currentId || !store.pending) return 'checking'
    return store.pending.count > 0 ? 'pending' : 'ready'
  })
  const pending = computed(() => (store.propertyId === property.currentId ? (store.pending?.count ?? 0) : 0))
  /** Where the link leads: the page, at the first step that needs someone. */
  const to = computed<RouteLocationRaw>(() => {
    const first: AuditStep | null | undefined = state.value === 'pending' ? store.pending?.first : null
    return first ? { path: '/night-audit', hash: `#${first}` } : '/night-audit'
  })
  return { state, pending, to }
}
