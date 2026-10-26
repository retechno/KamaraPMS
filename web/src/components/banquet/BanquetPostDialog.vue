<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetStockNote } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { billedPax } from '@/utils/banquetMoney'
import { formatDateShort } from '@/utils/format'

/**
 * Posts a session to the EVENT folio by hand (docs/architecture/36-banquet.md 4.5). The guests billed are the larger of the guaranteed and the actual number; a session with neither can be billed on the estimate
 * only when the person says so. The posting is dated the open business day and happens once: a later change of the guests is an adjustment on the folio.
 */
export interface PostTarget {
  event_id: number
  session_id: number
  event_number: string
  event_name: string
  session_name: string
  session_date: string
  room_name: string | null
  pax_estimated: number
  pax_guaranteed: number | null
  pax_actual: number | null
}
const props = defineProps<{ open: boolean; propertyId: number; target: PostTarget | null; businessDate: string | null }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; posted: [total: string] }>()

const actual = ref('')
const useEstimate = ref(false)
const error = ref<ApiError | null>(null)
const busy = ref(false)
// what the posting takes out of the warehouse (4.9): only said when the plan has the stock and the banquet has a warehouse
const stock = ref<BanquetStockNote | null>(null)
async function loadStock(): Promise<void> {
  stock.value = null
  const tg = props.target
  if (!tg) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/events/{id}/lines', { params: { path: { propertyId: props.propertyId, id: tg.event_id } } })
    stock.value = data?.stock ?? null
  } catch {
    stock.value = null // the note is a help; the posting does not depend on it
  }
}
watch(() => props.open, (open) => {
  if (!open) return
  void loadStock()
  error.value = null
  useEstimate.value = false
  actual.value = props.target?.pax_actual != null ? String(props.target.pax_actual) : ''
})
const entered = computed(() => (actual.value.trim() === '' ? null : Number(actual.value)))
const billed = computed(() => billedPax(props.target?.pax_guaranteed ?? null, Number.isInteger(entered.value) ? entered.value : null))
const noNumber = computed(() => billed.value.basis === 'NONE')

async function post(): Promise<void> {
  const tg = props.target
  if (!tg) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/sessions/{sid}/post', {
      params: { path: { propertyId: props.propertyId, id: tg.event_id, sid: tg.session_id } },
      body: { pax_actual: entered.value, use_estimate: noNumber.value && useEstimate.value },
    })
    emit('posted', data?.total ?? '0')
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="open && target !== null" @update:open="(v) => emit('update:open', v)">
    <DialogContent v-if="target" class="max-w-lg p-5" data-testid="post-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="post">
        <DialogTitle>{{ t('banquet.post.title') }}</DialogTitle>
        <DialogDescription>{{ target.event_number }} · {{ target.event_name }} · {{ target.session_name }} · {{ formatDateShort(target.session_date) }}<template v-if="target.room_name"> · {{ target.room_name }}</template></DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <dl class="m-0 grid grid-cols-3 gap-3 text-sm">
          <div><dt class="text-muted-foreground">{{ t('banquet.post.guaranteed') }}</dt><dd class="m-0 font-medium" data-testid="post-guaranteed">{{ target.pax_guaranteed ?? '–' }}</dd></div>
          <div>
            <dt class="text-muted-foreground">{{ t('banquet.post.actual') }}</dt>
            <dd class="m-0"><Input v-model="actual" name="pax_actual" type="number" min="0" :aria-label="t('banquet.post.actual')" /></dd>
          </div>
          <div><dt class="text-muted-foreground">{{ t('banquet.post.billed') }}</dt><dd class="m-0 font-medium" data-testid="post-billed">{{ noNumber ? '–' : billed.pax }}</dd></div>
        </dl>
        <p v-if="!noNumber" class="m-0 text-sm text-muted-foreground" data-testid="post-basis">{{ t(`banquet.post.basis_${billed.basis}`) }}</p>
        <label v-else class="flex cursor-pointer items-start gap-2 rounded-md border border-warning p-3 text-sm" data-testid="post-estimate">
          <input v-model="useEstimate" type="checkbox" name="use_estimate" class="mt-0.5 size-4 accent-primary" />
          <span>{{ t('banquet.post.useEstimate', { n: target.pax_estimated }) }}</span>
        </label>
        <p class="m-0 text-sm text-muted-foreground">{{ t('banquet.post.date', { date: businessDate ? formatDateShort(businessDate) : '' }) }}</p>
        <p v-if="stock?.enabled && stock.lines > 0" class="m-0 rounded-md border border-border p-2 text-sm" data-testid="post-stock">{{ t('banquet.post.stock', { n: stock.lines, warehouse: stock.warehouse_name ?? '' }) }}</p>
        <p class="m-0 text-xs text-muted-foreground">{{ t('banquet.post.once') }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || (noNumber && !useEstimate)" data-testid="post-save">{{ t('banquet.post.action') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
