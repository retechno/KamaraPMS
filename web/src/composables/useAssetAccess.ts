import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/** The asset permissions of the person at the open property (docs/architecture/37-fixed-assets.md 7.1). The register is read by any of them. */
const ANY = ['asset.read', 'asset.manage', 'asset.depreciate', 'asset.dispose', 'asset.setup']

export function useAssetAccess() {
  const auth = useAuthStore()
  const property = usePropertyStore()
  const pid = computed(() => property.currentId)
  const canRead = computed(() => ANY.some((p) => auth.can(p, pid.value)))
  const canManage = computed(() => auth.can('asset.manage', pid.value))
  const canSetup = computed(() => auth.can('asset.setup', pid.value))
  return { pid, canRead, canManage, canSetup }
}
