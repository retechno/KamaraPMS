<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetDeposit } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'

/** The deposit schedule of one event: each row is a percent of the contract total or a fixed amount, due on a date. Saving replaces the rows; a row is paid when the deposits received reach its cumulative due. */
type Row = { label: string; due_date: string; percent: string; amount: string }
const props = defineProps<{ open: boolean; propertyId: number; eventId: number; schedule: BanquetDeposit[] }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; saved: [] }>()

const rows = ref<Row[]>([])
const error = ref<ApiError | null>(null)
const busy = ref(false)
watch(() => props.open, (open) => {
  if (!open) return
  error.value = null
  rows.value = props.schedule.map((r) => ({ label: r.label, due_date: r.due_date, percent: r.percent ?? '', amount: r.percent ? '' : r.amount }))
})
const add = (): void => { rows.value = [...rows.value, { label: '', due_date: '', percent: '', amount: '' }] }
const remove = (i: number): void => { rows.value = rows.value.filter((_, at) => at !== i) }

async function save(): Promise<void> {
  busy.value = true
  error.value = null
  const body = { rows: rows.value.map((r) => ({ label: r.label.trim(), due_date: r.due_date, percent: r.percent.trim() || null, amount: r.percent.trim() ? null : r.amount.trim() || null })) }
  try {
    await api.PUT('/api/v1/properties/{propertyId}/banquet/events/{id}/deposit-schedule', { params: { path: { propertyId: props.propertyId, id: props.eventId } }, body })
    emit('saved')
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-h-[90dvh] max-w-3xl overflow-y-auto p-5" data-testid="schedule-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="save">
        <DialogTitle>{{ t('banquet.deposit.editTitle') }}</DialogTitle>
        <DialogDescription>{{ t('banquet.deposit.editHint') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <div v-for="(r, i) in rows" :key="i" class="grid grid-cols-[1.4fr_1fr_0.7fr_1fr_auto] items-end gap-2" :data-testid="`schedule-row-${i}`">
          <label class="text-xs text-muted-foreground">{{ t('banquet.deposit.stage') }}<Input v-model="r.label" :name="`row_label_${i}`" maxlength="100" /></label>
          <label class="text-xs text-muted-foreground">{{ t('banquet.deposit.due') }}<Input v-model="r.due_date" type="date" :name="`row_due_${i}`" /></label>
          <label class="text-xs text-muted-foreground">{{ t('banquet.deposit.percent') }}<Input v-model="r.percent" inputmode="decimal" :name="`row_percent_${i}`" /></label>
          <label class="text-xs text-muted-foreground">{{ t('banquet.deposit.amount') }}<Input v-model="r.amount" inputmode="decimal" :name="`row_amount_${i}`" :disabled="r.percent.trim() !== ''" /></label>
          <Button type="button" variant="outline" size="sm" @click="remove(i)">{{ t('common.remove') }}</Button>
        </div>
        <div><Button type="button" variant="outline" size="sm" data-testid="schedule-add" @click="add">{{ t('banquet.deposit.addRow') }}</Button></div>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="schedule-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
