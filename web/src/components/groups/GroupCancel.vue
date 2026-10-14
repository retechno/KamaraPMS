<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GroupCancelResult, GroupMember } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { i18n, t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatBalance } from '@/utils/format'
import { GROUP_CHECKIN_CHUNK, chunk } from '@/utils/groupCheckin'
import { newIdempotencyKey } from '@/utils/reservations'

/**
 * Cancel the rooms of a group (docs/architecture/30-group-folio.md part F): the reservations that still have a room waiting are ticked, the person says why, and sees before anything is done how many rooms
 * will be cancelled and how many are skipped because they are checked in. The server cancels each reservation in its own transaction and answers for each; a reservation that failed stays ticked with its
 * reason. Cancelling moves no money: the deposits that are held are decided afterwards, one reservation at a time, in "What the cancelled reservations still hold".
 */
const props = defineProps<{ groupId: number; groupCode: string; members: GroupMember[] }>()
const emit = defineEmits<{ changed: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const can = (permission: string) => auth.can(permission, pid.value)

type Row = GroupCancelResult['rows'][number]
const cancellable = computed(() => props.members.filter((m) => m.rooms_cancellable > 0))
const cancelled = computed(() => props.members.filter((m) => m.display_status === 'CANCELLED'))
const visible = computed(() => can('group.cancel') && (cancellable.value.length > 0 || cancelled.value.length > 0 || Object.keys(outcome).length > 0))

const selected = ref<number[]>([])
const reason = ref('')
const confirming = ref(false)
const running = ref(false)
const progress = ref({ done: 0, total: 0 })
const notice = ref('')
const error = ref<ApiError | null>(null)
const outcome = reactive<Record<number, Row>>({})
const mode = ref<'cancel' | 'reinstate'>('cancel')
const totals = ref({ cancelled: 0, partly: 0, already: 0, skipped: 0, failed: 0 })

const isSelected = (m: GroupMember): boolean => selected.value.includes(m.reservation_id)
const chosen = computed(() => cancellable.value.filter(isSelected))
const roomsToCancel = computed(() => chosen.value.reduce((n, m) => n + m.rooms_cancellable, 0))
// the rooms of the ticked reservations that a cancellation leaves alone: those checked in or out and the no-shows
const roomsSkipped = computed(() => chosen.value.reduce((n, m) => n + m.rooms_in_house_or_done + m.rooms_no_show, 0))
const partly = computed(() => chosen.value.filter((m) => m.rooms_in_house_or_done > 0).length)
// the reservations that nothing can cancel: every room is in house or done
const untouched = computed(() => props.members.filter((m) => m.rooms_cancellable === 0 && m.display_status !== 'CANCELLED').length)
const canRun = computed(() => chosen.value.length > 0 && reason.value.trim() !== '' && !running.value)

function toggle(m: GroupMember, on: boolean): void {
  selected.value = on ? [...new Set([...selected.value, m.reservation_id])] : selected.value.filter((id) => id !== m.reservation_id)
}
const selectAll = (): void => {
  selected.value = cancellable.value.map((m) => m.reservation_id)
}

async function run(which: 'cancel' | 'reinstate'): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || running.value) return
  const list = which === 'cancel' ? chosen.value : cancelled.value
  if (!list.length) return
  running.value = true
  mode.value = which
  confirming.value = false
  error.value = null
  notice.value = ''
  totals.value = { cancelled: 0, partly: 0, already: 0, skipped: 0, failed: 0 }
  for (const k of Object.keys(outcome)) delete outcome[Number(k)]
  const parts = chunk([...list].sort((a, b) => a.reservation_id - b.reservation_id), GROUP_CHECKIN_CHUNK)
  progress.value = { done: 0, total: list.length }
  try {
    for (const part of parts) {
      // a request of its own for each part, with a key of its own: a repeat finds what the first did (a cancelled reservation answers ALREADY_CANCELLED)
      const rows = part.map((m) => ({ reservation_id: m.reservation_id, version: m.version }))
      const params = { path: { propertyId, id: props.groupId }, header: { 'Idempotency-Key': newIdempotencyKey() } }
      const { data } = which === 'cancel'
        ? await api.POST('/api/v1/properties/{propertyId}/groups/{id}/cancellations', { params, body: { reason: reason.value.trim(), rows } })
        : await api.POST('/api/v1/properties/{propertyId}/groups/{id}/reinstatements', { params, body: { rows } })
      for (const r of data?.rows ?? []) {
        outcome[r.reservation_id] = r
        if (r.status === 'CANCELLED' || r.status === 'REINSTATED') totals.value.cancelled++
        else if (r.status === 'PARTLY_CANCELLED') totals.value.partly++
        else if (r.status === 'ALREADY_CANCELLED') totals.value.already++
        else if (r.status === 'SKIPPED') totals.value.skipped++
        else totals.value.failed++
      }
      progress.value.done += part.length
    }
    selected.value = selected.value.filter((id) => outcome[id]?.status === 'FAILED')
    const tt = totals.value
    notice.value = which === 'cancel'
      ? t('groupCancel.summary', { c: tt.cancelled, p: tt.partly, a: tt.already, s: tt.skipped, f: tt.failed })
      : t('groupCancel.reinstateSummary', { c: tt.cancelled, s: tt.skipped, f: tt.failed })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    running.value = false
    emit('changed')
  }
}

// The reason of a row that failed, in the language of the page when there is a text for its code (the answer of the server is in English)
function why(code?: string, message?: string): string {
  const key = `errors.${code}`
  return i18n.global.locale.value !== 'en' && code && i18n.global.te(key, i18n.global.locale.value) ? t(key as never) : (message ?? '')
}
const rowOf = (m: GroupMember): Row | undefined => outcome[m.reservation_id]
const outcomeRows = computed(() => props.members.filter((m) => outcome[m.reservation_id]))
const hasFailed = computed(() => Object.values(outcome).some((r) => r.status === 'FAILED'))

const columns = computed<Column<GroupMember>[]>(() => [
  { key: 'select', label: '' },
  { key: 'confirmation_number', label: t('groupDetail.confirmation'), card: 'primary' as const },
  { key: 'guest_name', label: t('groupDetail.booker'), card: 'secondary' as const },
  { key: 'rooms', label: t('groupCancel.rooms') },
  { key: 'status', label: t('setup.status'), card: 'badge' as const },
])
</script>

<template>
  <Card v-if="visible" class="mt-4" data-testid="group-cancel">
    <CardHeader><CardTitle>{{ t('groupCancel.title') }}</CardTitle></CardHeader>
    <CardContent>
      <p class="m-0 mb-3 text-sm text-muted-foreground">{{ t('groupCancel.hint') }}</p>
      <ErrorNotice v-if="error" :error="error" inline data-testid="group-cancel-error" />
      <p v-if="notice" :class="hasFailed ? 'alert warning' : 'notice'" role="status" data-testid="group-cancel-notice">{{ notice }}</p>

      <template v-if="cancellable.length">
        <div class="mb-3 flex flex-wrap items-center gap-2">
          <Button type="button" variant="outline" size="sm" :disabled="running" data-testid="cancel-select-all" @click="selectAll">{{ t('groupCancel.selectAll', { n: cancellable.length }) }}</Button>
          <Button v-if="selected.length" type="button" variant="ghost" size="sm" data-testid="cancel-select-none" @click="selected = []">{{ t('groupCheckIn.clear') }}</Button>
        </div>
        <DataTable :columns="columns" :rows="cancellable" row-key="reservation_id" :row-test-id="(m) => `cancel-row-${m.confirmation_number}`" cards>
          <template #cell-select="{ row: m }">
            <input type="checkbox" class="size-4 accent-primary" :checked="isSelected(m)" :disabled="running" :aria-label="t('groupCancel.pick')" :name="`cancel_${m.reservation_id}`" :data-testid="`cancel-pick-${m.reservation_id}`" @change="toggle(m, ($event.target as HTMLInputElement).checked)" />
          </template>
          <template #cell-confirmation_number="{ row: m }"><RouterLink :to="`/reservations/${m.reservation_id}`" class="text-primary hover:underline">{{ m.confirmation_number }}</RouterLink></template>
          <template #cell-rooms="{ row: m }">
            {{ t('groupCancel.roomsWaiting', { n: m.rooms_cancellable }, m.rooms_cancellable) }}
            <Badge v-if="m.rooms_in_house_or_done > 0" variant="warning" class="ml-2" :data-testid="`cancel-inhouse-${m.reservation_id}`">{{ t('groupCancel.roomsInHouse', { n: m.rooms_in_house_or_done }) }}</Badge>
          </template>
          <template #cell-status="{ row: m }"><StatusBadge domain="reservation" :status="m.display_status" /></template>
        </DataTable>
        <p v-if="untouched > 0" class="m-0 mt-2 text-sm text-muted-foreground" data-testid="cancel-untouched">{{ t('groupCancel.untouched', { n: untouched }) }}</p>

        <div class="mt-3 flex flex-wrap items-end gap-3">
          <FormField class="min-w-64 flex-1" :label="t('groupCancel.reason')">
            <template #default="{ id }"><Input :id="id" v-model="reason" name="cancel_reason" maxlength="500" data-testid="cancel-reason" /></template>
          </FormField>
          <Button type="button" variant="destructive" :disabled="!canRun" data-testid="cancel-open" @click="confirming = true">{{ t('groupCancel.open', { n: chosen.length }) }}</Button>
        </div>
        <p class="m-0 mt-2 text-xs text-muted-foreground" data-testid="cancel-progress">
          <template v-if="running">{{ t('groupCancel.progress', { done: progress.done, total: progress.total }) }}</template>
          <template v-else-if="chosen.length && !reason.trim()">{{ t('groupCancel.reasonMissing') }}</template>
        </p>
      </template>
      <p v-else-if="!cancelled.length" class="m-0 text-sm text-muted-foreground" data-testid="cancel-nothing">{{ t('groupCancel.nothing') }}</p>

      <div v-if="cancelled.length && can('reservation.reinstate')" class="mt-4 flex flex-wrap items-center gap-3 border-t border-border pt-3">
        <Button type="button" variant="outline" size="sm" :disabled="running" data-testid="reinstate-run" @click="run('reinstate')">{{ t('groupCancel.reinstate', { n: cancelled.length }) }}</Button>
        <span class="text-xs text-muted-foreground">{{ t('groupCancel.reinstateHint') }}</span>
      </div>

      <ul v-if="outcomeRows.length" class="m-0 mt-3 list-none space-y-1 p-0 text-sm" data-testid="group-cancel-result">
        <li v-for="m in outcomeRows" :key="m.reservation_id" :data-testid="`cancel-result-${m.reservation_id}`" class="flex flex-wrap items-baseline gap-x-2">
          <RouterLink :to="`/reservations/${m.reservation_id}`" class="font-medium text-primary hover:underline">{{ m.confirmation_number }}</RouterLink>
          <Badge :variant="rowOf(m)?.status === 'FAILED' ? 'destructive' : rowOf(m)?.status === 'SKIPPED' || rowOf(m)?.status === 'PARTLY_CANCELLED' ? 'warning' : 'success'">{{ t(`groupCancel.status.${rowOf(m)?.status}` as never) }}</Badge>
          <span v-if="rowOf(m)?.status === 'FAILED'" class="text-xs text-destructive">{{ rowOf(m)?.code }}: {{ why(rowOf(m)?.code, rowOf(m)?.message) }}</span>
          <span v-else-if="rowOf(m)?.status === 'SKIPPED'" class="text-xs text-muted-foreground">{{ t(`groupCancel.skip.${rowOf(m)?.code}` as never) }}</span>
          <span v-else-if="rowOf(m)?.status === 'PARTLY_CANCELLED'" class="text-xs text-muted-foreground">{{ t('groupCancel.partlyNote') }}</span>
          <span v-if="(rowOf(m)?.deposits?.length ?? 0) > 0" class="text-xs text-muted-foreground" :data-testid="`cancel-held-${m.reservation_id}`">{{ t('groupCancel.heldNote', { amount: formatBalance(m.held_balance) }) }}</span>
        </li>
      </ul>
      <p v-if="hasFailed" class="m-0 mt-2 text-sm text-muted-foreground" data-testid="group-cancel-failed">{{ t('groupCancel.failedHint') }}</p>
    </CardContent>
  </Card>

  <Dialog v-if="confirming" :open="true" @update:open="(v) => { if (!v) confirming = false }">
    <DialogContent class="max-w-md p-5" data-testid="group-cancel-dialog" @interact-outside.prevent>
      <DialogTitle>{{ t('groupCancel.confirmTitle', { n: chosen.length, group: groupCode }) }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('groupCancel.confirmHint') }}</DialogDescription>
      <ul class="m-0 list-none space-y-1 p-0 text-sm" data-testid="group-cancel-counts">
        <li data-testid="count-cancel"><strong>{{ t('groupCancel.countCancel', { n: roomsToCancel }, roomsToCancel) }}</strong></li>
        <li v-if="roomsSkipped > 0" class="text-warning-foreground" data-testid="count-skip">{{ t('groupCancel.countSkip', { n: roomsSkipped, r: partly }, roomsSkipped) }}</li>
        <li class="text-muted-foreground">{{ t('groupCancel.countReason', { reason: reason.trim() }) }}</li>
        <li class="text-muted-foreground">{{ t('groupCancel.countMoney') }}</li>
      </ul>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" data-testid="group-cancel-back" @click="confirming = false">{{ t('groupCancel.back') }}</Button>
        <Button type="button" variant="destructive" data-testid="group-cancel-confirm" @click="run('cancel')">{{ t('groupCancel.confirm') }}</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
