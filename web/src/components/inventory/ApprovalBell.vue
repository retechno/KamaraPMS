<script setup lang="ts">
import { Bell } from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import type { InventoryNoticeList } from '@/api/types'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { useSubscriptionStore } from '@/stores/subscription'
import { formatDate } from '@/utils/format'

/**
 * The bell of the approvals of the inventory (docs/architecture/35-inventory.md 5.8 item 9, D27): the notices of the signed-in user (a request waits for their decision, was approved, was returned, or its chain is
 * stuck), how many are not seen, and the way to the inbox "Perlu saya setujui". It refreshes when the window gets the focus and every minute. It is shown to whoever can take part in the approvals, where the module is in the plan.
 */
const auth = useAuthStore()
const property = usePropertyStore()
const subscription = useSubscriptionStore()
const pid = computed(() => property.currentId)
const takesPart = computed(() =>
  ['inventory.request', 'inventory.dept_approve', 'inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'].some((p) => auth.can(p, pid.value)),
)
const shown = computed(() => pid.value !== null && subscription.modules !== null && subscription.has('INVENTORY') && takesPart.value)

const list = ref<InventoryNoticeList>({ unseen: 0, data: [] })
const open = ref(false)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (!shown.value || propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/notices', { params: { path: { propertyId } } })
    if (data) list.value = data
  } catch {
    // the bell is a convenience: an error here is not worth a message
  }
}

async function seen(id: number): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  open.value = false
  const n = list.value.data.find((x) => x.id === id)
  if (!n || n.seen_at) return
  try {
    await api.POST('/api/v1/properties/{propertyId}/inventory/notices/{id}/seen', { params: { path: { propertyId, id } } })
  } catch {
    // see above
  }
  void load()
}
async function seenAll(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    await api.POST('/api/v1/properties/{propertyId}/inventory/notices/seen-all', { params: { path: { propertyId } } })
  } catch {
    // see above
  }
  void load()
}

let timer: ReturnType<typeof setInterval> | undefined
const onFocus = (): void => void load()
onMounted(() => {
  void load()
  timer = setInterval(() => void load(), 60_000)
  window.addEventListener('focus', onFocus)
})
onBeforeUnmount(() => {
  clearInterval(timer)
  window.removeEventListener('focus', onFocus)
})
watch([pid, shown], () => void load())
</script>

<template>
  <Popover v-if="shown" v-model:open="open">
    <PopoverTrigger as-child>
      <button type="button" class="relative grid size-9 shrink-0 cursor-pointer place-items-center rounded-md border border-border bg-background text-foreground hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring" :aria-label="t('inventory.bell.label', { n: list.unseen })" data-testid="approval-bell">
        <Bell class="size-4" aria-hidden="true" />
        <span v-if="list.unseen > 0" class="absolute -right-1 -top-1 grid min-w-4 place-items-center rounded-full bg-destructive px-1 text-[10px] font-bold leading-4 text-white" data-testid="bell-count">{{ list.unseen > 99 ? '99+' : list.unseen }}</span>
      </button>
    </PopoverTrigger>
    <PopoverContent class="w-80" data-testid="bell-content">
      <div class="flex items-center justify-between gap-2">
        <strong class="text-sm">{{ t('inventory.bell.title') }}</strong>
        <button v-if="list.unseen > 0" type="button" class="cursor-pointer border-0 bg-transparent p-0 text-xs text-primary hover:underline" data-testid="bell-seen-all" @click="seenAll">{{ t('inventory.bell.seenAll') }}</button>
      </div>
      <ul class="m-0 mt-2 max-h-72 list-none overflow-y-auto p-0 text-sm">
        <li v-for="n in list.data.slice(0, 8)" :key="n.id" class="border-t border-border first:border-0">
          <RouterLink :to="n.request_id ? `/inventory/requests/${n.request_id}` : n.requisition_id ? `/inventory/requisitions/${n.requisition_id}` : '/inventory/approvals'" class="block py-2 no-underline" :class="n.seen_at ? 'text-muted-foreground' : 'font-medium text-foreground'" :data-testid="`notice-${n.id}`" @click="seen(n.id)">
            {{ t(`inventory.bell.kind.${n.kind}`, { number: n.request_number ?? n.requisition_number ?? '', level: n.step_name ?? '' }) }}
            <span class="block text-xs font-normal text-muted-foreground">{{ formatDate(n.created_at.slice(0, 10)) }}</span>
          </RouterLink>
        </li>
        <li v-if="list.data.length === 0" class="py-2 text-muted-foreground">{{ t('inventory.bell.empty') }}</li>
      </ul>
      <RouterLink to="/inventory/approvals" class="mt-2 block border-t border-border pt-2 text-sm text-primary hover:underline" data-testid="bell-inbox" @click="open = false">{{ t('inventory.bell.inbox') }}</RouterLink>
    </PopoverContent>
  </Popover>
</template>
