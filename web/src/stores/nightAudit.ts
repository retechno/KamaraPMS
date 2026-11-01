import { defineStore } from 'pinia'
import { computed, onScopeDispose, ref } from 'vue'
import { api } from '@/api/client'
import { onApiMutation } from '@/api/mutationEvents'
import type { NightAuditPreview } from '@/api/types'
import { pendingSteps } from '@/utils/nightAuditReadiness'
import { touchesNightAudit } from '@/utils/nightAuditTriggers'

/** How long a preview is trusted by the places that only show a state (the chip and the Today card); the page itself always reads a fresh one. */
const FRESH_MS = 120_000

/**
 * The last night audit preview of the property, shared by the night audit page, the chip of the top bar and the Today card, so that all three say the same about "ready". The page
 * writes every preview it reads; the other two ask for one with `ensure`, which reads it only when there is none yet, or it is older than two minutes, or something that changes the steps
 * was done (`markStale`: a check-in, a check-out, a no-show, a shift opened or closed, on any screen). The preview dry-runs the room charges, so nobody polls it, and a read that is on its
 * way is shared by whoever asks meanwhile.
 */
export const useNightAuditStore = defineStore('nightAudit', () => {
  const preview = ref<NightAuditPreview | null>(null)
  const propertyId = ref<number | null>(null)
  const readAt = ref(0)
  /** Counts the times the preview went stale, so that a screen that shows the state can read it again at once. */
  const version = ref(0)
  let inflight: Promise<void> | null = null
  let dirty = false

  const pending = computed(() => (preview.value ? pendingSteps(preview.value) : null))

  function set(id: number, p: NightAuditPreview | null): void {
    preview.value = p
    propertyId.value = id
    readAt.value = p ? Date.now() : 0
  }

  function reset(): void {
    preview.value = null
    propertyId.value = null
    readAt.value = 0
  }

  /** What was shown may not be true any more: the next `ensure` reads again, and a read that is on its way is followed by one more. */
  function markStale(): void {
    readAt.value = 0
    version.value++
    if (inflight) dirty = true
  }

  async function ensure(id: number): Promise<void> {
    if (propertyId.value === id && preview.value && Date.now() - readAt.value < FRESH_MS) return
    if (inflight) return inflight
    inflight = (async () => {
      try {
        do {
          dirty = false
          const { data } = await api.GET('/api/v1/properties/{propertyId}/night-audit/preview', { params: { path: { propertyId: id } } })
          set(id, data ?? null)
        } while (dirty)
      } catch {
        // the state stays unknown: the chip says "checking" rather than a wrong "ready"
      } finally {
        inflight = null
        dirty = false
      }
    })()
    return inflight
  }

  // Any write that went through, from any screen, and changes the steps of the audit.
  const stop = onApiMutation((method, path) => {
    if (touchesNightAudit(method, path)) markStale()
  })
  onScopeDispose(stop)

  return { preview, propertyId, version, pending, set, reset, markStale, ensure }
})
