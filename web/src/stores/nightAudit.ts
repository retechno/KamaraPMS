import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import type { NightAuditPreview } from '@/api/types'
import { pendingSteps } from '@/utils/nightAuditReadiness'

/** How long a preview is trusted by the places that only show a state (the chip and the Today card); the page itself always reads a fresh one. */
const FRESH_MS = 120_000

/**
 * The last night audit preview of the property, shared by the night audit page, the chip of the top bar and the Today card, so that all three say the same about "ready". The page
 * writes every preview it reads; the other two ask for one with `ensure`, which reads it only when there is none yet or it is older than two minutes (the preview dry-runs the room charges,
 * so nobody polls it).
 */
export const useNightAuditStore = defineStore('nightAudit', () => {
  const preview = ref<NightAuditPreview | null>(null)
  const propertyId = ref<number | null>(null)
  const readAt = ref(0)
  let inflight: Promise<void> | null = null

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

  async function ensure(id: number): Promise<void> {
    if (propertyId.value === id && preview.value && Date.now() - readAt.value < FRESH_MS) return
    if (inflight) return inflight
    inflight = (async () => {
      try {
        const { data } = await api.GET('/api/v1/properties/{propertyId}/night-audit/preview', { params: { path: { propertyId: id } } })
        set(id, data ?? null)
      } catch {
        // the state stays unknown: the chip says "checking" rather than a wrong "ready"
      } finally {
        inflight = null
      }
    })()
    return inflight
  }

  return { preview, propertyId, pending, set, reset, ensure }
})
