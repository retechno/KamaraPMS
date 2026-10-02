import { ref, watch } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { listAccounts } from '@/views/accounting/accountApi'

/**
 * The names of the chart's accounts by code, for showing "4110 - Room revenue" where only a code is stored. Empty (and
 * no request) without accounting.view or when the chart cannot be loaded, so a code is then shown as it is.
 */
export function useAccountNames() {
  const auth = useAuthStore()
  const property = usePropertyStore()
  const names = ref<Record<string, string>>({})

  watch(() => property.currentId, async (id) => {
    names.value = {}
    if (id === null || !auth.can('accounting.view', id)) return
    try {
      names.value = Object.fromEntries((await listAccounts(id)).map((a) => [a.code, a.name]))
    } catch {
      names.value = {}
    }
  }, { immediate: true })

  /** "4110 - Room revenue", the bare code when the name is unknown, a dash when there is no code. */
  function label(code: string | null | undefined): string {
    if (!code) return '—'
    const name = names.value[code]
    return name ? `${code} - ${name}` : code
  }
  return { label }
}
