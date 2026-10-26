<script setup lang="ts">
import { CalendarDays } from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import type { BanquetNotice } from '@/api/types'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { useSubscriptionStore } from '@/stores/subscription'
import { formatDate } from '@/utils/format'

/**
 * The bell of the banquet (docs/architecture/36-banquet.md 4.6, 5.7): the notices of the signed-in user (an option moved up, is due or was lost; a deposit is due; the BEO of an event was revised), how many are
 * not seen, and the way to the event. It refreshes when the window gets the focus and every minute. It is shown to whoever can read the banquet, where the module is in the plan.
 */
const auth = useAuthStore()
const property = usePropertyStore()
const subscription = useSubscriptionStore()
const pid = computed(() => property.currentId)
const takesPart = computed(() => ['banquet.manage', 'banquet.confirm', 'banquet.read'].some((p) => auth.can(p, pid.value)))
const shown = computed(() => pid.value !== null && subscription.modules !== null && subscription.has('BANQUET') && takesPart.value)

const notices = ref<BanquetNotice[]>([])
const open = ref(false)
const unseen = computed(() => notices.value.filter((n) => n.seen_at === null).length)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (!shown.value || propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/notices', { params: { path: { propertyId } } })
    if (data) notices.value = data.data
  } catch {
    // the bell is a convenience: an error here is not worth a message
  }
}

async function markSeen(ids?: number[]): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    await api.POST('/api/v1/properties/{propertyId}/banquet/notices/seen', { params: { path: { propertyId } }, body: { ids } })
  } catch {
    // see above
  }
  void load()
}
function seen(n: BanquetNotice): void {
  open.value = false
  if (n.seen_at === null) void markSeen([n.id])
}
const to = (n: BanquetNotice): string => (n.kind === 'BEO_REVISED' ? `/banquet/events/${n.event_id}?tab=beo` : n.kind === 'DEPOSIT_DUE' ? `/banquet/events/${n.event_id}?tab=deposit` : `/banquet/events/${n.event_id}`)

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
      <button type="button" class="relative grid size-9 shrink-0 cursor-pointer place-items-center rounded-md border border-border bg-background text-foreground hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring" :aria-label="t('banquet.bell.label', { n: unseen })" data-testid="banquet-bell">
        <CalendarDays class="size-4" aria-hidden="true" />
        <span v-if="unseen > 0" class="absolute -right-1 -top-1 grid min-w-4 place-items-center rounded-full bg-destructive px-1 text-[10px] font-bold leading-4 text-white" data-testid="banquet-bell-count">{{ unseen > 99 ? '99+' : unseen }}</span>
      </button>
    </PopoverTrigger>
    <PopoverContent class="w-80" data-testid="banquet-bell-content">
      <div class="flex items-center justify-between gap-2">
        <strong class="text-sm">{{ t('banquet.bell.title') }}</strong>
        <button v-if="unseen > 0" type="button" class="cursor-pointer border-0 bg-transparent p-0 text-xs text-primary hover:underline" data-testid="banquet-bell-seen-all" @click="markSeen()">{{ t('banquet.bell.seenAll') }}</button>
      </div>
      <ul class="m-0 mt-2 max-h-72 list-none overflow-y-auto p-0 text-sm">
        <li v-for="n in notices.slice(0, 10)" :key="n.id" class="border-t border-border first:border-0">
          <RouterLink :to="to(n)" class="block py-2 no-underline" :class="n.seen_at ? 'text-muted-foreground' : 'font-medium text-foreground'" :data-testid="`banquet-notice-${n.id}`" @click="seen(n)">
            {{ t(`banquet.bell.kind.${n.kind}`, { number: n.event_number, name: n.event_name, revision: n.revision_no ?? '' }) }}
            <span class="block text-xs font-normal text-muted-foreground">{{ formatDate(n.notice_date) }}</span>
          </RouterLink>
        </li>
        <li v-if="notices.length === 0" class="py-2 text-muted-foreground">{{ t('banquet.bell.empty') }}</li>
      </ul>
      <RouterLink to="/banquet/today" class="mt-2 block border-t border-border pt-2 text-sm text-primary hover:underline" data-testid="banquet-bell-today" @click="open = false">{{ t('banquet.bell.today') }}</RouterLink>
    </PopoverContent>
  </Popover>
</template>
