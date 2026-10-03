<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CityLedgerOverdue, CityLedgerOverdueCompany } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'
import { newIdempotencyKey } from '@/utils/reservations'

type OverdueInvoice = CityLedgerOverdueCompany['invoices'][number]

const auth = useAuthStore()
const property = usePropertyStore()

const overdue = ref<CityLedgerOverdue | null>(null)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const fee = reactive({ monthly_rate: '0', grace_days: '0' })
// A reminder being made for one company: the level, the note and the invoices on the letter.
const reminding = ref<{ company: CityLedgerOverdueCompany; level: number; note: string; picked: number[] } | null>(null)
// One key per attempt: kept while a request may have been lost, renewed once the server has answered.
let reminderKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const canRead = computed(() => auth.can('cityledger.read', pid.value))
const canRemind = computed(() => auth.can('cityledger.reminder', pid.value))
const fieldError = (field: string) => error.value?.fieldMessage(field)

const columns = computed<Column<OverdueInvoice>[]>(() => [
  { key: 'invoice_number', label: t('clOverdue.invoice') },
  { key: 'due_date', label: t('clOverdue.due'), format: 'date' as const },
  { key: 'days_overdue', label: t('clOverdue.days'), align: 'right' },
  { key: 'bucket', label: t('clOverdue.bucket') },
  { key: 'outstanding', label: t('clOverdue.outstanding'), align: 'right', format: 'money' as const },
  { key: 'interest', label: t('clOverdue.interest'), align: 'right', format: 'money' as const },
  { key: 'last_reminder', label: t('clOverdue.lastReminder') },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canRead.value) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/city-ledger/overdue', { params: { path: { propertyId } } })
    overdue.value = data ?? null
    fee.monthly_rate = data?.late_fee.monthly_rate ?? '0'
    fee.grace_days = String(data?.late_fee.grace_days ?? 0)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function saveLateFee(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.PUT('/api/v1/properties/{propertyId}/city-ledger/settings/late-fee', {
      params: { path: { propertyId } },
      body: { monthly_rate: fee.monthly_rate.trim(), grace_days: Number(fee.grace_days) },
    })
    notice.value = t('clOverdue.lateFeeSaved')
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

function startReminder(company: CityLedgerOverdueCompany): void {
  reminding.value = { company, level: company.next_level, note: '', picked: company.invoices.map((i) => i.invoice_id) }
  reminderKey = newIdempotencyKey()
  error.value = null
  notice.value = ''
}

function toggle(id: number, on: boolean): void {
  const r = reminding.value
  if (!r) return
  r.picked = on ? [...r.picked, id] : r.picked.filter((x) => x !== id)
}

async function sendReminder(): Promise<void> {
  const r = reminding.value
  const propertyId = pid.value
  if (!r || propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/reminders', {
      params: { path: { propertyId, id: r.company.company_id }, header: { 'Idempotency-Key': reminderKey } },
      body: { level: r.level, note: r.note.trim() || undefined, invoice_ids: r.picked },
    })
    reminderKey = newIdempotencyKey()
    reminding.value = null
    notice.value = t('clOverdue.recorded', { number: data?.number ?? '' })
    await load()
    if (data) await openPdf(documentPath.reminder(propertyId, data.id))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) reminderKey = newIdempotencyKey() // the server answered: the next submit is a new attempt
  } finally {
    busy.value = false
  }
}

watch(() => property.currentId, () => {
  overdue.value = null
  reminding.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('clOverdue.title')" :description="canRead ? t('clOverdue.intro') : undefined" />

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('clOverdue.noAccess', { permission: 'cityledger.read' }) }}</p>

  <template v-else-if="overdue">
    <Card class="mb-4" data-testid="overdue-summary">
      <CardContent class="flex flex-wrap gap-6 pt-4">
        <div><small class="text-muted-foreground">{{ t('clOverdue.outstanding') }}</small><div class="text-lg font-semibold">{{ $money(overdue.outstanding) }}</div></div>
        <div><small class="text-muted-foreground">{{ t('clOverdue.interest') }}</small><div class="text-lg font-semibold">{{ $money(overdue.interest) }}</div></div>
        <div class="text-sm text-muted-foreground">{{ t('clOverdue.asOf', { date: $date(overdue.as_of) }) }}</div>
      </CardContent>
    </Card>

    <Card v-if="canRemind" class="mb-4" data-testid="late-fee">
      <form novalidate @submit.prevent="saveLateFee">
        <CardHeader>
          <CardTitle>{{ t('clOverdue.lateFee') }}</CardTitle>
          <p class="text-sm text-muted-foreground">{{ t('clOverdue.lateFeeHint') }}</p>
        </CardHeader>
        <CardContent class="flex flex-wrap items-end gap-3">
          <FormField class="w-40" :label="t('clOverdue.monthlyRate')" :error="fieldError('monthly_rate')">
            <template #default="{ id }"><Input :id="id" v-model="fee.monthly_rate" name="monthly_rate" inputmode="decimal" /></template>
          </FormField>
          <FormField class="w-40" :label="t('clOverdue.graceDays')" :error="fieldError('grace_days')">
            <template #default="{ id }"><Input :id="id" v-model="fee.grace_days" name="grace_days" inputmode="numeric" /></template>
          </FormField>
          <Button type="submit" :disabled="busy">{{ t('clOverdue.saveLateFee') }}</Button>
        </CardContent>
      </form>
    </Card>

    <Card v-if="reminding" class="mb-4" data-testid="reminder-form">
      <form novalidate @submit.prevent="sendReminder">
        <CardHeader><CardTitle>{{ t('clOverdue.reminderTitle', { company: reminding.company.name }) }}</CardTitle></CardHeader>
        <CardContent class="flex flex-col gap-3">
          <FormField class="w-64" :label="t('clOverdue.level')" :error="fieldError('level')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="reminding.level" name="level">
                <option :value="1">{{ t('clOverdue.level1') }}</option>
                <option :value="2">{{ t('clOverdue.level2') }}</option>
                <option :value="3">{{ t('clOverdue.level3') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('clOverdue.note')" :error="fieldError('note')">
            <template #default="{ id }"><Input :id="id" v-model="reminding.note" name="note" maxlength="500" /></template>
          </FormField>
          <fieldset class="text-sm">
            <legend class="mb-1 font-medium">{{ t('clOverdue.invoicesPicked') }}</legend>
            <label v-for="inv in reminding.company.invoices" :key="inv.invoice_id" class="mr-4 inline-flex items-center gap-2">
              <input type="checkbox" class="size-4 accent-primary" :name="`pick-${inv.invoice_number}`" :checked="reminding.picked.includes(inv.invoice_id)" @change="toggle(inv.invoice_id, ($event.target as HTMLInputElement).checked)" />
              {{ inv.invoice_number }}
            </label>
          </fieldset>
          <div class="flex gap-2">
            <Button type="submit" :disabled="busy || !reminding.picked.length" data-testid="send-reminder">{{ t('clOverdue.record') }}</Button>
            <Button type="button" variant="outline" @click="reminding = null">{{ t('clOverdue.cancel') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <EmptyState v-if="!overdue.companies.length" :title="t('clOverdue.empty')" data-testid="empty" />
    <Card v-for="c in overdue.companies" :key="c.company_id" class="mb-4" :data-testid="`overdue-${c.code}`">
      <CardHeader>
        <CardTitle>
          <RouterLink :to="`/city-ledger/${c.company_id}`" class="text-primary hover:underline">{{ c.code }}</RouterLink> {{ c.name }}
        </CardTitle>
        <p class="text-sm text-muted-foreground">
          {{ t('clOverdue.outstanding') }} <b>{{ $money(c.outstanding) }}</b> · {{ t('clOverdue.interest') }} {{ $money(c.interest) }} · {{ t('clOverdue.daysOverdue', { days: c.oldest_days_overdue }) }}
        </p>
      </CardHeader>
      <CardContent>
        <DataTable :columns="columns" :rows="c.invoices" row-key="invoice_id" :row-test-id="(i) => `invoice-${i.invoice_number}`" :caption="c.name">
          <template #cell-last_reminder="{ row }">{{ row.last_reminder ? `${row.last_reminder.number} (${row.last_reminder.level})` : t('clOverdue.never') }}</template>
        </DataTable>
        <div v-if="canRemind" class="mt-3">
          <Button type="button" variant="outline" :data-testid="`remind-${c.code}`" @click="startReminder(c)">{{ t('clOverdue.remind') }}</Button>
        </div>
      </CardContent>
    </Card>
  </template>
</template>
