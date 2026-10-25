<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetToday, BanquetUnposted } from '@/api/types'
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
import { formatDate, formatDateShort, formatMoney } from '@/utils/format'

/**
 * Events today (docs/architecture/36-banquet.md 6.2, mockup 38): the sessions of the business date with what is known of the guests, whether each is posted, and, below, the sessions of earlier days
 * that were never posted. The close of the day posts the ones in AUTO mode; a session in MANUAL mode, or without guests, waits for a person here.
 */
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('banquet.manage', pid.value))
const today = ref<BanquetToday | null>(null)
const unposted = ref<BanquetUnposted[]>([])
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
    const [td, un] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/banquet/today', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/banquet/reports/unposted', { params: { path: { propertyId } } }).catch(() => null),
    ])
    today.value = td.data ?? null
    unposted.value = un?.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const date = computed(() => today.value?.date ?? null)
const sessions = computed(() => (today.value?.sessions ?? []).filter((s) => s.event_status === 'DEFINITE' || s.event_status === 'COMPLETED').sort((a, b) => a.start_time.localeCompare(b.start_time)))
const waiting = computed(() => new Map(unposted.value.map((u) => [u.session_id, u])))
const older = computed(() => unposted.value.filter((u) => date.value !== null && u.session_date < date.value))
const guaranteed = computed(() => sessions.value.reduce((n, s) => n + (waiting.value.get(s.session_id)?.pax_guaranteed ?? 0), 0))
const range = (s: { start_time: string; end_time: string }): string => `${s.start_time.slice(0, 5)}–${s.end_time.slice(0, 5)}`

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
  <PageHeader :title="date ? t('banquet.today.titleDate', { date: formatDate(date) }) : t('banquet.today.title')" :description="t('banquet.today.intro')">
    <template #actions><Button as-child variant="outline" size="sm"><RouterLink to="/banquet/unposted" data-testid="to-unposted">{{ t('banquet.today.fullReport') }}</RouterLink></Button></template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else-if="loaded">
    <Card class="mb-4 p-4" data-testid="today-sessions">
      <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
        <h2 class="m-0 text-base font-semibold">{{ t('banquet.today.schedule') }}</h2>
        <span class="text-sm text-muted-foreground">{{ t('banquet.today.count', { n: sessions.length, pax: guaranteed }) }}</span>
      </div>
      <EmptyState v-if="!sessions.length" :title="t('banquet.today.none')" :description="t('emptyState.banquetToday')" action-label="" />
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead><tr class="text-left text-muted-foreground"><th class="py-1 pr-3 font-normal">{{ t('banquet.today.time') }}</th><th class="py-1 pr-3 font-normal">{{ t('banquet.today.room') }}</th><th class="py-1 pr-3 font-normal">{{ t('banquet.today.event') }}</th><th class="py-1 pr-3 text-right font-normal">{{ t('banquet.post.guaranteed') }}</th><th class="py-1 pr-3 text-right font-normal">{{ t('banquet.post.actual') }}</th><th class="py-1 font-normal">{{ t('banquet.today.bill') }}</th></tr></thead>
          <tbody>
            <tr v-for="s in sessions" :key="s.session_id" class="border-t border-border" :data-testid="`today-session-${s.session_id}`">
              <td class="py-1.5 pr-3">{{ range(s) }}</td>
              <td class="py-1.5 pr-3">{{ s.room_name }}<template v-if="s.setup_style"> · {{ t(`banquet.style.${s.setup_style}` as never) }}</template></td>
              <td class="py-1.5 pr-3"><RouterLink :to="`/banquet/events/${s.event_id}`" class="font-medium text-primary hover:underline">{{ s.event_name }}</RouterLink><span class="block text-xs text-muted-foreground">{{ s.event_number }} · {{ s.session_name }}</span></td>
              <td class="py-1.5 pr-3 text-right">{{ waiting.get(s.session_id)?.pax_guaranteed ?? '–' }}</td>
              <td class="py-1.5 pr-3 text-right">{{ waiting.get(s.session_id)?.pax_actual ?? '–' }}</td>
              <td class="py-1.5">
                <span v-if="waiting.get(s.session_id)" class="flex items-center gap-2">
                  <Badge variant="outline">{{ t('banquet.folio.notPosted') }}</Badge>
                  <Badge v-if="waiting.get(s.session_id)!.blocked" variant="destructive" :data-testid="`today-blocked-${s.session_id}`">{{ t('banquet.unposted.noGuests') }}</Badge>
                  <Button v-if="canManage" type="button" size="sm" :data-testid="`today-post-${s.session_id}`" @click="post(waiting.get(s.session_id)!)">{{ t('banquet.post.action') }}</Button>
                </span>
                <Badge v-else variant="closed">{{ t('banquet.line.posted') }}</Badge>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="[...waiting.values()].some((u) => date && u.session_date === date)" class="m-0 mt-2 text-xs text-muted-foreground" data-testid="today-close-note">{{ t('banquet.today.closeNote') }}</p>
    </Card>

    <Card class="p-4" data-testid="today-older">
      <h2 class="m-0 mb-2 text-base font-semibold">{{ t('banquet.today.older') }}</h2>
      <p v-if="!older.length" class="m-0 text-sm text-muted-foreground" data-testid="today-older-none">{{ t('banquet.unposted.none') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-2 p-0">
        <li v-for="u in older" :key="u.session_id" class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-2 text-sm" :data-testid="`older-${u.session_id}`">
          <span><RouterLink :to="`/banquet/events/${u.event_id}`" class="font-medium text-primary hover:underline">{{ u.event_name }}</RouterLink> <span class="text-muted-foreground">{{ u.event_number }} · {{ u.session_name }} · {{ formatDateShort(u.session_date) }} · {{ t('banquet.unposted.age', { n: u.days_late }) }}</span></span>
          <span class="flex items-center gap-2"><Badge variant="outline">{{ u.blocked ? t('banquet.unposted.noGuests') : u.posting_mode === 'MANUAL' ? t('banquet.unposted.manual') : t('banquet.unposted.waiting') }}</Badge><Button v-if="canManage" type="button" size="sm" @click="post(u)">{{ t('banquet.post.action') }}</Button></span>
        </li>
      </ul>
      <p class="m-0 mt-2 text-xs text-muted-foreground">{{ t('banquet.today.olderHint') }}</p>
    </Card>
  </template>
  <BanquetPostDialog v-if="pid !== null" v-model:open="postOpen" :property-id="pid" :target="target" :business-date="date" @posted="posted" />
</template>
