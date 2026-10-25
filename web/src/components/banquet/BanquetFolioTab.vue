<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetEvent, BanquetMoney } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { formatDate, formatDateShort, formatMoney } from '@/utils/format'
import BanquetPostDialog, { type PostTarget } from './BanquetPostDialog.vue'
import BanquetTransferDialog from './BanquetTransferDialog.vue'

/**
 * The tab "Folio" (docs/architecture/36-banquet.md 6.7): the bill of the event, the sessions posted and the ones that wait, when the sessions are posted (the property, or this event), and the transfer of the
 * bill to the master folio of a group. Posting is done once per session, by the close of the day in AUTO mode or by a person here.
 */
const props = defineProps<{ propertyId: number; event: BanquetEvent; money: BanquetMoney; businessDate: string | null; canManage: boolean }>()
const emit = defineEmits<{ changed: [] }>()

const error = ref<ApiError | null>(null)
const postTarget = ref<PostTarget | null>(null)
const postOpen = ref(false)
const transferOpen = ref(false)
// An event that is not linked to a group can be transferred too (the dialog links it); the button is off only without the permission or without a group that has an open master folio
const transferBlock = computed<'banquet.transfer.offPermission' | 'banquet.transfer.offNoGroup' | null>(() => (!props.canManage ? 'banquet.transfer.offPermission' : !props.money.groups.length && !props.event.booking_group_id ? 'banquet.transfer.offNoGroup' : null))
const posted = computed(() => new Map(props.money.postings.map((p) => [p.session_id, p])))
const sessions = computed(() => props.event.sessions.filter((s) => s.status === 'ACTIVE'))
const definite = computed(() => props.event.status === 'DEFINITE')
const due = (sessionDate: string): boolean => props.businessDate !== null && sessionDate <= props.businessDate
const mode = computed({ get: () => (props.money.posting_mode_own ? props.money.posting_mode : ''), set: (v: string) => void setMode(v) })

async function setMode(v: string): Promise<void> {
  error.value = null
  try {
    await api.PUT('/api/v1/properties/{propertyId}/banquet/events/{id}/posting-mode', { params: { path: { propertyId: props.propertyId, id: props.event.id } }, body: { posting_mode: v as 'AUTO' | 'MANUAL' | '' } })
    emit('changed')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
function post(sessionId: number): void {
  const s = props.event.sessions.find((x) => x.id === sessionId)
  if (!s) return
  postTarget.value = { event_id: props.event.id, session_id: s.id, event_number: props.event.number, event_name: props.event.name, session_name: s.name, session_date: s.session_date, room_name: s.room_name, pax_estimated: s.pax_estimated, pax_guaranteed: s.pax_guaranteed, pax_actual: s.pax_actual }
  postOpen.value = true
}
function wasPosted(total: string): void {
  toast.success(t('banquet.post.done', { total: formatMoney(total) }))
  emit('changed')
}
</script>

<template>
  <ErrorNotice v-if="error" :error="error" inline />
  <div class="flex flex-col gap-4">
    <Card class="p-4" data-testid="folio-card">
      <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
        <h3 class="m-0 text-base font-semibold">{{ t('banquet.folio.title') }}</h3>
        <div class="flex gap-2">
          <Button
            v-if="money.folio && money.folio.status === 'OPEN'" type="button" variant="outline" data-testid="transfer-open" :disabled="transferBlock !== null"
            :title="transferBlock === null ? undefined : t(transferBlock)" @click="transferOpen = true"
          >{{ t('banquet.transfer.open') }}</Button>
        </div>
      </div>
      <p v-if="!money.folio" class="m-0 text-sm text-muted-foreground" data-testid="folio-none">{{ t('banquet.folio.none') }}</p>
      <dl v-else class="m-0 grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
        <div><dt class="text-muted-foreground">{{ t('banquet.folio.number') }}</dt><dd class="m-0 font-medium"><RouterLink :to="`/folios/${money.folio.id}`" class="text-primary hover:underline" data-testid="folio-link">{{ money.folio.number }}</RouterLink></dd></div>
        <div><dt class="text-muted-foreground">{{ t('banquet.folio.debit') }}</dt><dd class="m-0">{{ formatMoney(money.folio.debit) }}</dd></div>
        <div><dt class="text-muted-foreground">{{ t('banquet.folio.credit') }}</dt><dd class="m-0">{{ formatMoney(money.folio.credit) }}</dd></div>
        <div><dt class="text-muted-foreground">{{ t('banquet.folio.balance') }}</dt><dd class="m-0 font-medium" data-testid="folio-balance">{{ formatMoney(money.folio.balance) }}</dd></div>
      </dl>
      <p v-if="money.cancelled_fee_item_id" class="m-0 mt-2 text-sm text-muted-foreground" data-testid="folio-fee-posted">{{ t('banquet.folio.feePosted') }}</p>
    </Card>

    <Card class="p-4" data-testid="postings-card">
      <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
        <h3 class="m-0 text-base font-semibold">{{ t('banquet.folio.sessions') }}</h3>
        <label v-if="canManage && (event.status === 'DEFINITE' || event.status === 'TENTATIVE')" class="flex items-center gap-2 text-sm">
          <span class="text-muted-foreground">{{ t('banquet.folio.postingMode') }}</span>
          <NativeSelect v-model="mode" name="posting_mode" class="w-auto" data-testid="posting-mode">
            <option value="">{{ t('banquet.folio.followProperty', { mode: t(`banquet.settings.posting_${money.posting_mode}` as never) }) }}</option>
            <option value="AUTO">{{ t('banquet.settings.posting_AUTO') }}</option>
            <option value="MANUAL">{{ t('banquet.settings.posting_MANUAL') }}</option>
          </NativeSelect>
        </label>
      </div>
      <ul class="m-0 flex list-none flex-col gap-2 p-0">
        <li v-for="s in sessions" :key="s.id" class="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border p-2 text-sm" :data-testid="`folio-session-${s.id}`">
          <span>{{ s.name }} · {{ formatDateShort(s.session_date) }} · {{ t('banquet.session.paxCount', { n: s.pax_actual ?? s.pax_guaranteed ?? s.pax_estimated }) }}</span>
          <span v-if="posted.get(s.id)" class="flex items-center gap-2" :data-testid="`folio-posted-${s.id}`">
            <Badge variant="closed">{{ t('banquet.line.posted') }}</Badge>
            <span class="text-muted-foreground">{{ formatDate(posted.get(s.id)!.business_date) }} · {{ t('banquet.folio.billed', { n: posted.get(s.id)!.pax_billed }) }} · {{ t(`banquet.post.basis_${posted.get(s.id)!.pax_basis}` as never) }}<template v-if="posted.get(s.id)!.auto"> · {{ t('banquet.folio.auto') }}</template></span>
          </span>
          <span v-else class="flex items-center gap-2">
            <Badge variant="outline">{{ t('banquet.folio.notPosted') }}</Badge>
            <Button v-if="canManage && definite && due(s.session_date)" type="button" size="sm" :data-testid="`folio-post-${s.id}`" @click="post(s.id)">{{ t('banquet.post.action') }}</Button>
          </span>
        </li>
      </ul>
    </Card>
  </div>

  <BanquetPostDialog v-model:open="postOpen" :property-id="propertyId" :target="postTarget" :business-date="businessDate" @posted="wasPosted" />
  <BanquetTransferDialog v-if="money.folio" v-model:open="transferOpen" :property-id="propertyId" :event="event" :folio="money.folio" :groups="money.groups" @done="emit('changed')" />
</template>
