<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetUnposted } from '@/api/types'
import BanquetPostDialog, { type PostTarget } from '@/components/banquet/BanquetPostDialog.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDateShort, formatMoney } from '@/utils/format'

/**
 * The report "Sessions not posted" (docs/architecture/36-banquet.md 4.10, mockup 38): every session of a definite event dated the business date or earlier that has no posting, with the reason it waits (the event is
 * posted by hand, or there is no number of guests to bill). An old session that is posted now is dated the open business day. The close of the day only warns; it never blocks for this.
 */
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('banquet.manage', pid.value))
const rows = ref<BanquetUnposted[]>([])
const businessDate = ref<string | null>(null)
const error = ref<ApiError | null>(null)
const loaded = ref(false)
const target = ref<PostTarget | null>(null)
const postOpen = ref(false)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    const [un, td] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/banquet/reports/unposted', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/banquet/today', { params: { path: { propertyId } } }),
    ])
    rows.value = un.data?.data ?? []
    businessDate.value = td.data?.date ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const cause = (u: BanquetUnposted): string => (u.blocked ? t('banquet.unposted.noGuests') : u.posting_mode === 'MANUAL' ? t('banquet.unposted.manual') : t('banquet.unposted.waiting'))
function post(u: BanquetUnposted): void {
  target.value = { event_id: u.event_id, session_id: u.session_id, event_number: u.event_number, event_name: u.event_name, session_name: u.session_name, session_date: u.session_date, room_name: u.room_name, pax_estimated: u.pax_estimated, pax_guaranteed: u.pax_guaranteed, pax_actual: u.pax_actual }
  postOpen.value = true
}
function posted(total: string): void {
  toast.success(t('banquet.post.done', { total: formatMoney(total) }))
  void load()
}
</script>

<template>
  <PageHeader :title="t('banquet.unposted.title')" :description="t('banquet.unposted.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else-if="loaded">
    <EmptyState v-if="!rows.length" :title="t('banquet.unposted.none')" :description="t('emptyState.banquetUnposted')" action-label="" />
    <Card v-else class="p-4" data-testid="unposted-table">
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead><tr class="text-left text-muted-foreground"><th class="py-1 pr-3 font-normal">{{ t('banquet.unposted.sessionDate') }}</th><th class="py-1 pr-3 font-normal">{{ t('banquet.unposted.age', { n: '' }).trim() }}</th><th class="py-1 pr-3 font-normal">{{ t('banquet.today.event') }}</th><th class="py-1 pr-3 font-normal">{{ t('banquet.today.room') }}</th><th class="py-1 pr-3 text-right font-normal">{{ t('banquet.post.guaranteed') }}</th><th class="py-1 pr-3 text-right font-normal">{{ t('banquet.post.actual') }}</th><th class="py-1 pr-3 font-normal">{{ t('banquet.unposted.cause') }}</th><th class="py-1 font-normal" /></tr></thead>
          <tbody>
            <tr v-for="u in rows" :key="u.session_id" class="border-t border-border" :data-testid="`unposted-${u.session_id}`">
              <td class="py-1.5 pr-3">{{ formatDateShort(u.session_date) }}</td>
              <td class="py-1.5 pr-3">{{ u.days_late === 0 ? t('banquet.unposted.today') : t('banquet.unposted.age', { n: u.days_late }) }}</td>
              <td class="py-1.5 pr-3"><RouterLink :to="`/banquet/events/${u.event_id}`" class="font-medium text-primary hover:underline">{{ u.event_name }}</RouterLink><span class="block text-xs text-muted-foreground">{{ u.event_number }} · {{ u.session_name }} · {{ u.sales_name }}</span></td>
              <td class="py-1.5 pr-3">{{ u.room_name ?? '–' }}</td>
              <td class="py-1.5 pr-3 text-right">{{ u.pax_guaranteed ?? '–' }}</td>
              <td class="py-1.5 pr-3 text-right">{{ u.pax_actual ?? '–' }}</td>
              <td class="py-1.5 pr-3"><Badge :variant="u.blocked ? 'destructive' : 'outline'">{{ cause(u) }}</Badge></td>
              <td class="py-1.5 text-right"><Button v-if="canManage" type="button" size="sm" :data-testid="`unposted-post-${u.session_id}`" @click="post(u)">{{ t('banquet.post.action') }}</Button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="m-0 mt-2 text-xs text-muted-foreground">{{ t('banquet.unposted.hint') }}</p>
    </Card>
  </template>
  <BanquetPostDialog v-if="pid !== null" v-model:open="postOpen" :property-id="pid" :target="target" :business-date="businessDate" @posted="posted" />
</template>
