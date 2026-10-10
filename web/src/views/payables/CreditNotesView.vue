<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, Bill, SupplierCreditNote, OpenBill, Supplier } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'
import { fromMilli, toMilli } from '@/views/accounting/accountMeta'

/**
 * The credit notes suppliers gave on bills. A credit note is made against a bill: each of its lines credits a line of the bill (the account, the department and the VAT treatment are the bill line's).
 * It takes what it credits off the bill as far as the bill still owes; what is left is a credit of the supplier that can be applied to other bills.
 */
const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const notes = ref<SupplierCreditNote[]>([])
const suppliers = ref<Supplier[]>([])
const bills = ref<Bill[]>([])
const opened = ref<SupplierCreditNote | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const filter = reactive({ supplier: Number(route.query.supplier) || 0, status: '', unapplied_only: false, q: '' })
const creating = ref(false)
const billOfForm = ref<Bill | null>(null)
const form = reactive({ bill_id: 0, number: '', date: '', reason: '', lines: [] as { bill_line_no: number; label: string; max_amount: string; max_vat: string; amount: string; vat_amount: string }[] })
const voiding = ref<{ reason: string; asking: boolean } | null>(null)
const applying = ref<{ open: OpenBill[]; amounts: Record<number, string> } | null>(null)
let postKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const statusVariant = (s: string) => ({ POSTED: 'success', VOIDED: 'destructive' })[s] as 'success' | 'destructive'
const total = computed(() => {
  let sum = 0n
  let valid = true
  for (const l of form.lines) {
    for (const v of [l.amount, l.vat_amount]) {
      if (v.trim() === '') continue
      const n = toMilli(v)
      if (n === null || n < 0n) valid = false
      else sum += n
    }
  }
  return { sum, valid }
})
const applyTotal = computed(() => {
  let sum = 0n
  if (applying.value) for (const v of Object.values(applying.value.amounts)) if (v.trim() !== '') sum += toMilli(v) ?? 0n
  return sum
})
const columns = computed<Column<SupplierCreditNote>[]>(() => [
  { key: 'credit_date', label: t('supplierCredits.date'), format: 'date' as const },
  { key: 'credit_number', label: t('supplierCredits.number') },
  { key: 'supplier', label: t('payables.supplier') },
  { key: 'supplier_credit_number', label: t('supplierCredits.theirNumber') },
  { key: 'bill_number', label: t('payables.bill') },
  { key: 'total', label: t('payables.total'), align: 'right', format: 'money' as const },
  { key: 'unapplied', label: t('supplierCredits.unapplied'), align: 'right', format: 'money' as const },
  { key: 'status', label: t('payables.status') },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('payables.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/credit-notes', {
      params: { path: { propertyId }, query: { supplier_id: filter.supplier || undefined, status: (filter.status || undefined) as 'POSTED' | undefined, unapplied_only: filter.unapplied_only || undefined, q: filter.q.trim() || undefined } },
    })
    notes.value = data?.data ?? []
    if (!suppliers.value.length) {
      const res = await api.GET('/api/v1/properties/{propertyId}/payables/suppliers', { params: { path: { propertyId } } })
      suppliers.value = res.data?.data ?? []
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function open(n: SupplierCreditNote): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (opened.value?.id === n.id) {
    opened.value = null
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/credit-notes/{id}', { params: { path: { propertyId, id: n.id } } })
    opened.value = data ?? null
    voiding.value = null
    applying.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadBills(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || bills.value.length) return
  const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/bills', { params: { path: { propertyId }, query: { status: 'POSTED' } } })
  bills.value = data?.data ?? []
}

/** The lines of the chosen bill are what the credit note can credit. */
async function chooseBill(): Promise<void> {
  const propertyId = pid.value
  billOfForm.value = null
  form.lines = []
  if (propertyId === null || !form.bill_id) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/bills/{id}', { params: { path: { propertyId, id: form.bill_id } } })
    billOfForm.value = data ?? null
    form.lines = (data?.lines ?? []).map((l) => ({
      bill_line_no: l.line_no, label: `${l.account_code} · ${l.account_name}${l.description ? ` · ${l.description}` : ''}`, max_amount: l.amount, max_vat: l.vat_amount ?? '0', amount: '', vat_amount: '',
    }))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function startNew(billId = 0): Promise<void> {
  Object.assign(form, { bill_id: billId, number: '', date: '', reason: '' })
  form.lines = []
  error.value = null
  postKey = newIdempotencyKey()
  creating.value = true
  try {
    await loadBills()
    if (billId) await chooseBill()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const credited = form.lines.filter((l) => l.amount.trim() !== '' || l.vat_amount.trim() !== '')
    const { data } = await api.POST('/api/v1/properties/{propertyId}/payables/credit-notes', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': postKey } },
      body: {
        bill_id: form.bill_id, supplier_credit_number: form.number.trim(), credit_date: form.date, reason: form.reason.trim(),
        lines: credited.map((l) => ({ bill_line_no: l.bill_line_no, amount: l.amount.trim() || '0', vat_amount: l.vat_amount.trim() || '0' })),
      },
    })
    postKey = newIdempotencyKey()
    creating.value = false
    notice.value = t('supplierCredits.entered', { number: data?.credit_number ?? '' })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function startApply(n: SupplierCreditNote): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/suppliers/{id}/open-bills', { params: { path: { propertyId, id: n.supplier_id } } })
    applying.value = { open: data?.data ?? [], amounts: {} }
    voiding.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function apply(): Promise<void> {
  const propertyId = pid.value
  const n = opened.value
  if (propertyId === null || n === null || applying.value === null) return
  busy.value = true
  error.value = null
  try {
    const allocations = Object.entries(applying.value.amounts).filter(([, v]) => v.trim() !== '').map(([bill, v]) => ({ bill_id: Number(bill), amount: v.trim() }))
    await api.POST('/api/v1/properties/{propertyId}/payables/credit-notes/{id}/apply', { params: { path: { propertyId, id: n.id } }, body: { allocations } })
    notice.value = t('supplierCredits.appliedNotice', { number: n.credit_number })
    applying.value = null
    opened.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function voidNote(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const n = opened.value
  if (propertyId === null || n === null || voiding.value === null) return
  busy.value = true
  dialogError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/payables/credit-notes/{id}/void', { params: { path: { propertyId, id: n.id } }, body: { reason: voiding.value.reason.trim(), approval } })
    notice.value = t('supplierCredits.voidedNotice', { number: n.credit_number })
    voiding.value = null
    opened.value = null
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  notes.value = []
  suppliers.value = []
  bills.value = []
  opened.value = null
  loaded.value = false
  void load().then(() => {
    const bill = Number(route.query.bill)
    if (bill > 0 && can('payables.post')) void startNew(bill)
  })
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('supplierCredits.title')" :description="can('payables.view') ? t('supplierCredits.intro') : undefined">
    <template #actions>
      <Button v-if="can('payables.post') && !creating" type="button" data-testid="new-credit" @click="startNew()">{{ t('supplierCredits.enter') }}</Button>
    </template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert" data-testid="credit-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 6)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('payables.view')" class="muted" data-testid="no-access">{{ t('supplierCredits.noAccess', { permission: 'payables.view' }) }}</p>
  <template v-else>
    <Card v-if="creating" class="mb-4">
      <form v-autofocus novalidate data-testid="credit-form" @submit.prevent="post">
        <CardHeader><CardTitle>{{ t('supplierCredits.formTitle') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <FormField class="sm:col-span-2" :label="t('payables.bill')" :error="fieldError('bill_id')">
              <template #default="{ id, invalid }">
                <Combobox :id="id" v-model="form.bill_id" name="bill_id" :aria-invalid="invalid" @update:model-value="chooseBill"
                  :options="[{ value: 0, label: t('supplierCredits.chooseBill') }, ...bills.map((b) => ({ value: b.id, label: `${b.bill_number} · ${b.supplier_name} · ${b.supplier_invoice_number}` }))]" />
              </template>
            </FormField>
            <FormField :label="t('supplierCredits.theirNumber')" :error="fieldError('supplier_credit_number')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.number" name="supplier_credit_number" maxlength="60" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('supplierCredits.date')" :error="fieldError('credit_date')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.date" name="credit_date" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField class="sm:col-span-2 lg:col-span-4" :label="t('supplierCredits.reason')" :error="fieldError('reason')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.reason" name="reason" maxlength="500" :aria-invalid="invalid" /></template>
            </FormField>
          </div>
          <p v-if="billOfForm" class="mb-0 mt-3 text-sm text-muted-foreground" data-testid="bill-owes">{{ t('supplierCredits.billOwes', { number: billOfForm.bill_number, owed: $money(billOfForm.outstanding) }) }}</p>
          <table v-if="form.lines.length" class="mt-3 w-full border-collapse text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs text-muted-foreground">
                <th class="py-1.5 pr-3 font-medium">{{ t('supplierCredits.billLine') }}</th>
                <th class="px-3 text-right font-medium">{{ t('supplierCredits.paidAmount') }}</th>
                <th class="px-3 text-right font-medium">{{ t('supplierCredits.creditAmount') }}</th>
                <th class="px-3 text-right font-medium">{{ t('supplierCredits.paidVat') }}</th>
                <th class="pl-3 text-right font-medium">{{ t('supplierCredits.creditVat') }}</th>
              </tr>
            </thead>
            <tbody class="[&_td]:py-1.5 [&_td]:pr-3 [&_td]:align-top">
              <tr v-for="(l, i) in form.lines" :key="l.bill_line_no" :data-testid="`credit-line-${i}`">
                <td>{{ l.bill_line_no }} · {{ l.label }}</td>
                <td class="text-right tabular-nums">{{ $money(l.max_amount) }}</td>
                <td>
                  <Input v-model="l.amount" class="text-right" :name="`amount_${i}`" inputmode="decimal" :aria-invalid="!!fieldError(`lines[${i}].amount`)" />
                  <small v-if="fieldError(`lines[${i}].amount`)" role="alert" class="text-xs text-destructive">{{ fieldError(`lines[${i}].amount`) }}</small>
                </td>
                <td class="text-right tabular-nums">{{ $money(l.max_vat) }}</td>
                <td>
                  <Input v-model="l.vat_amount" class="text-right" :name="`vat_${i}`" inputmode="decimal" :aria-invalid="!!fieldError(`lines[${i}].vat_amount`)" />
                  <small v-if="fieldError(`lines[${i}].vat_amount`)" role="alert" class="text-xs text-destructive">{{ fieldError(`lines[${i}].vat_amount`) }}</small>
                </td>
              </tr>
            </tbody>
            <tfoot>
              <tr class="border-t border-border">
                <td colspan="4" class="pt-2 text-right"><b>{{ t('payables.total') }}</b></td>
                <td class="pt-2 text-right tabular-nums" data-testid="credit-total"><b>{{ $money(fromMilli(total.sum)) }}</b></td>
              </tr>
            </tfoot>
          </table>
          <small v-if="fieldError('lines')" role="alert" class="text-xs text-destructive">{{ fieldError('lines') }}</small>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="creating = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !total.valid || total.sum <= 0n || !form.bill_id || !form.number.trim() || !form.date || !form.reason.trim()" data-testid="credit-post">{{ t('supplierCredits.post') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card>
      <CardContent class="pt-4">
        <form class="mb-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-5" novalidate @submit.prevent="load">
          <FormField :label="t('payables.supplier')">
            <template #default="{ id }">
              <Combobox :id="id" v-model="filter.supplier" name="supplier" :options="[{ value: 0, label: t('payables.all') }, ...suppliers.map((s) => ({ value: s.id, label: `${s.code} · ${s.name}` }))]" />
            </template>
          </FormField>
          <FormField :label="t('payables.search')"><template #default="{ id }"><Input :id="id" v-model="filter.q" name="q" type="search" :placeholder="t('supplierCredits.search')" /></template></FormField>
          <div class="flex items-end gap-3 lg:col-span-2">
            <label class="flex items-center gap-2 pb-2 text-sm">
              <input v-model="filter.unapplied_only" name="unapplied_only" type="checkbox" class="size-4 accent-primary" /><span>{{ t('supplierCredits.unappliedOnly') }}</span>
            </label>
            <Button type="submit" variant="outline" data-testid="apply-filter">{{ t('payables.apply') }}</Button>
          </div>
        </form>
        <EmptyState v-if="loaded && !notes.length" :title="t('supplierCredits.empty')" data-testid="empty" />
        <DataTable
          v-else
          :columns="columns"
          :rows="notes"
          row-key="id"
          clickable
          :row-test-id="(n) => `credit-${n.credit_number}`"
          :row-class="(n) => (n.status === 'VOIDED' ? 'voided text-muted-foreground line-through' : opened?.id === n.id ? 'bg-accent' : undefined)"
          :is-expanded="(n) => opened?.id === n.id"
          :detail-test-id="() => 'credit-detail'"
          :caption="t('supplierCredits.title')"
          data-testid="credits"
          @row-click="open"
        >
          <template #cell-supplier="{ row }">{{ row.supplier_code }} · {{ row.supplier_name }}</template>
          <template #cell-status="{ row }"><Badge :variant="statusVariant(row.status)">{{ row.status === 'POSTED' ? t('payables.statusEntered') : t('payables.st_VOIDED') }}</Badge></template>
          <template #detail="{ row }">
            <template v-if="opened">
              <p class="mb-2 mt-0 text-sm text-muted-foreground">{{ opened.reason }}</p>
              <table class="w-full border-collapse text-sm">
                <thead><tr class="border-b border-border text-left text-xs text-muted-foreground"><th class="py-1 pr-3 font-medium">#</th><th class="px-3 font-medium">{{ t('payables.account') }}</th><th class="px-3 font-medium">{{ t('payables.department') }}</th><th class="px-3 text-right font-medium">{{ t('payables.amount') }}</th><th class="pl-3 text-right font-medium">{{ t('payables.vat') }}</th></tr></thead>
                <tbody>
                  <tr v-for="l in opened.lines" :key="l.line_no" class="border-b border-border">
                    <td class="py-1 pr-3">{{ l.line_no }}</td>
                    <td class="px-3">{{ l.account_code }} · {{ l.account_name }}</td>
                    <td class="px-3"><small v-if="l.department_code" :title="l.department_name">{{ l.department_code }}</small></td>
                    <td class="px-3 text-right tabular-nums">{{ $money(l.amount) }}</td>
                    <td class="pl-3 text-right tabular-nums">
                      <template v-if="l.vat_treatment">{{ $money(l.vat_amount) }} <small class="text-muted-foreground">{{ t(`payables.vat_${l.vat_treatment}`) }}</small></template>
                      <template v-else>—</template>
                    </td>
                  </tr>
                </tbody>
              </table>
              <ul v-if="opened.allocations?.length" class="mt-2 list-none p-0 text-sm" data-testid="allocations">
                <li v-for="(a, i) in opened.allocations" :key="i">{{ t('supplierCredits.takenOff', { bill: a.bill_number, amount: $money(a.amount), date: $date(a.applied_on) }) }}</li>
              </ul>
              <p class="mb-0 mt-2 text-sm text-muted-foreground" @click.stop>
                {{ t('payables.journal', { number: opened.journal_number }) }}<template v-if="opened.void_reason"> · {{ t('payables.voidedReason', { reason: opened.void_reason }) }}</template>
              </p>
              <div v-if="can('payables.post') && opened.status === 'POSTED' && row.id === opened.id" class="mt-2 flex flex-col gap-3" @click.stop>
                <div class="flex gap-2">
                  <Button v-if="!applying && Number(opened.unapplied) > 0" type="button" variant="outline" size="sm" data-testid="start-apply" @click="startApply(opened)">{{ t('supplierCredits.applyEllipsis') }}</Button>
                  <Button v-if="!voiding" type="button" variant="outline" size="sm" data-testid="void" @click="voiding = { reason: '', asking: false }; applying = null">{{ t('payables.voidEllipsis') }}</Button>
                </div>
                <form v-if="applying" class="flex flex-col gap-2" novalidate data-testid="apply-form" @submit.prevent="apply">
                  <p class="m-0 text-sm text-muted-foreground">{{ t('supplierCredits.applyHint', { amount: $money(opened.unapplied) }) }}</p>
                  <p v-if="!applying.open.length" class="m-0 text-sm" data-testid="no-open-bills">{{ t('supplierCredits.noOpenBills') }}</p>
                  <div v-for="b in applying.open" :key="b.bill_id" class="flex items-center gap-3 text-sm" :data-testid="`open-${b.bill_number}`">
                    <span class="w-64">{{ b.bill_number }} · {{ b.supplier_invoice_number }}</span>
                    <span class="w-32 text-right tabular-nums">{{ $money(b.outstanding) }}</span>
                    <Input v-model="applying.amounts[b.bill_id]" class="w-40 text-right" :name="`apply_${b.bill_id}`" inputmode="decimal" :aria-label="b.bill_number" />
                  </div>
                  <div class="flex gap-2">
                    <Button type="button" variant="outline" @click="applying = null">{{ t('common.cancel') }}</Button>
                    <Button type="submit" :disabled="busy || applyTotal <= 0n" data-testid="apply-submit">{{ t('supplierCredits.apply') }}</Button>
                  </div>
                </form>
                <form v-if="voiding" class="flex flex-wrap items-end gap-3" novalidate @submit.prevent="voiding.asking = true">
                  <FormField class="w-80" :label="t('payables.reason')">
                    <template #default="{ id }"><Input :id="id" v-model="voiding.reason" name="void_reason" maxlength="500" /></template>
                  </FormField>
                  <Button type="button" variant="outline" @click="voiding = null">{{ t('common.cancel') }}</Button>
                  <Button type="submit" :disabled="!voiding.reason.trim()" data-testid="void-ask">{{ t('payables.voidWithApproval') }}</Button>
                </form>
              </div>
            </template>
          </template>
        </DataTable>
      </CardContent>
    </Card>
  </template>
  <ApprovalDialog v-if="voiding?.asking" :title="t('supplierCredits.approveVoid')" :busy="busy" :error="dialogError" @approve="voidNote" @cancel="voiding = null; dialogError = null" />
</template>
