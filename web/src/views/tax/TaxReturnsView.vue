<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, GlAccount, TaxFilingPeriod, TaxFilingProfile, TaxFilingReturn, TaxFilingWorksheet } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { i18n, t } from '@/i18n'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'
import { newIdempotencyKey } from '@/utils/reservations'
import { listAccounts } from '@/views/accounting/accountApi'
import { money } from '@/views/accounting/reportApi'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const profiles = ref<TaxFilingProfile[]>([])
const accounts = ref<GlAccount[]>([])
const periods = ref<TaxFilingPeriod[]>([])
const worksheet = ref<TaxFilingWorksheet | null>(null)
const filed = ref<TaxFilingReturn | null>(null)
const taxId = ref(Number(route.query.tax) || 0)
const openMonth = ref('')
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const fileForm = reactive({ filed_on: '', reference: '', notes: '' })
const payForm = reactive({ open: false, payment_date: '', amount: '', penalty: '', penalty_account_id: 0, department_id: null as number | null, method: 'BANK_TRANSFER', reference: '', remarks: '' })
const voiding = ref<{ kind: 'return' | 'payment'; id: number; reason: string; asking: boolean } | null>(null)
let fileKey = newIdempotencyKey()
let payKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const expenses = computed(() => accounts.value.filter((a) => a.is_postable && a.is_active && a.account_type === 'EXPENSE'))
const statusText = (s: string): string => t(`taxReturns.st_${s}` as 'taxReturns.st_OPEN')
const payText = (s: string): string => t(`taxReturns.pay_${s}` as 'taxReturns.pay_PAID')
const METHODS = ['BANK_TRANSFER', 'CASH', 'OTHER'] as const
const methodText = (m: string): string => t(`taxReturns.m_${m}` as 'taxReturns.m_CASH')
const periodColumns = computed<Column<TaxFilingPeriod>[]>(() => [
  { key: 'month', label: t('taxReturns.month') },
  { key: 'due_date', label: t('taxReturns.due'), format: 'date' as const },
  { key: 'tax_amount', label: t('taxReturns.taxAmount'), align: 'right', format: 'money' as const },
  { key: 'status', label: t('taxReturns.status') },
  { key: 'paid', label: t('taxReturns.paid'), align: 'right', format: 'money' as const },
  { key: 'outstanding', label: t('taxReturns.owed'), align: 'right', format: 'money' as const },
  { key: 'credit', label: t('taxReturns.credit'), align: 'right' },
])
/** The month on screen as the return filed for it, else as the worksheet: both carry the offset of the input VAT and the credit. */
const vat = computed(() => filed.value ?? worksheet.value)
const showOffset = computed(() => {
  const v = vat.value
  return !!v && (worksheet.value?.claims_input_vat === true || Number(v.input_claimed) !== 0 || Number(v.credit_brought_forward) !== 0 || Number(v.credit_carried_forward) !== 0)
})
const base = () => ({ path: { propertyId: pid.value as number } })
const monthLabel = (start: string): string => new Date(`${start}T00:00:00Z`).toLocaleDateString(i18n.global.locale.value, { month: 'long', year: 'numeric', timeZone: 'UTC' })

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('tax.view')) return
  try {
    if (!profiles.value.length) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/profiles', { params: base() })
      profiles.value = (data?.data ?? []).filter((p) => p.is_active)
      if (!taxId.value && profiles.value.length === 1) taxId.value = profiles.value[0]?.tax_id ?? 0
    }
    if (taxId.value) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/periods', { params: { ...base(), query: { tax_id: taxId.value } } })
      periods.value = data?.data ?? []
    }
    if (can('accounting.view') && !accounts.value.length) accounts.value = await listAccounts(propertyId, { active: true })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function open(per: TaxFilingPeriod): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (openMonth.value === per.period_start) {
    openMonth.value = ''
    worksheet.value = null
    filed.value = null
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/worksheet', { params: { ...base(), query: { tax_id: taxId.value, period: per.period_start } } })
    worksheet.value = data ?? null
    openMonth.value = per.period_start
    fileForm.filed_on = ''
    fileForm.reference = ''
    fileForm.notes = ''
    payForm.open = false
    voiding.value = null
    fileKey = newIdempotencyKey()
    filed.value = null
    if (data?.return) {
      const res = await api.GET('/api/v1/properties/{propertyId}/tax/returns/{id}', { params: { path: { propertyId, id: data.return.id } } })
      filed.value = res.data ?? null
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function reload(): Promise<void> {
  const month = openMonth.value
  openMonth.value = ''
  await load()
  const per = periods.value.find((p) => p.period_start === month)
  if (per) await open(per)
}

async function run<T>(fn: () => Promise<T>, done: string): Promise<boolean> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await fn()
    notice.value = done
    await reload()
    return true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    return false
  } finally {
    busy.value = false
  }
}

async function file(): Promise<void> {
  const ok = await run(
    () => api.POST('/api/v1/properties/{propertyId}/tax/returns', {
      params: { ...base(), header: { 'Idempotency-Key': fileKey } },
      body: { tax_id: taxId.value, period_start: openMonth.value, filed_on: fileForm.filed_on || null, filing_reference: fileForm.reference || undefined, notes: fileForm.notes || undefined },
    }),
    t('taxReturns.isFiled'),
  )
  if (ok) fileKey = newIdempotencyKey()
}

function startPay(): void {
  Object.assign(payForm, { open: true, payment_date: '', amount: filed.value?.outstanding ?? '', penalty: '', penalty_account_id: 0, department_id: null, method: 'BANK_TRANSFER', reference: '', remarks: '' })
  payKey = newIdempotencyKey()
  error.value = null
}

async function pay(): Promise<void> {
  const r = filed.value
  if (!r) return
  const ok = await run(
    () => api.POST('/api/v1/properties/{propertyId}/tax/returns/{id}/payments', {
      params: { path: { ...base().path, id: r.id }, header: { 'Idempotency-Key': payKey } },
      body: {
        payment_date: payForm.payment_date, amount: payForm.amount.trim(), penalty: payForm.penalty.trim() || undefined, penalty_account_id: payForm.penalty_account_id || undefined,
        department_id: payForm.penalty_account_id ? payForm.department_id : undefined,
        payment_method: payForm.method as 'CASH', reference_number: payForm.reference || undefined, remarks: payForm.remarks || undefined,
      },
    }),
    t('taxReturns.paymentRecorded'),
  )
  if (ok) payForm.open = false
}

async function confirmVoid(approval: Approval): Promise<void> {
  const v = voiding.value
  if (!v) return
  busy.value = true
  dialogError.value = null
  try {
    const body = { reason: v.reason.trim(), approval }
    if (v.kind === 'return') await api.POST('/api/v1/properties/{propertyId}/tax/returns/{id}/void', { params: { path: { ...base().path, id: v.id } }, body })
    else await api.POST('/api/v1/properties/{propertyId}/tax/payments/{id}/void', { params: { path: { ...base().path, id: v.id } }, body })
    notice.value = v.kind === 'return' ? t('taxReturns.returnVoided') : t('taxReturns.paymentVoided')
    voiding.value = null
    await reload()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function showPdf(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !openMonth.value) return
  try {
    await openPdf(filed.value ? documentPath.taxReturn(propertyId, filed.value.id) : documentPath.taxWorksheet(propertyId, taxId.value, openMonth.value))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch([() => pid.value, taxId], () => {
  periods.value = []
  worksheet.value = null
  filed.value = null
  openMonth.value = ''
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('taxReturns.title')" />
  <p v-if="error" class="alert" role="alert" data-testid="tax-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 4)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('tax.view')" class="muted" data-testid="no-access">{{ t('taxReturns.noAccess', { permission: 'tax.view' }) }}</p>
  <template v-else>
    <Card class="mb-4">
      <form class="flex items-end gap-3 p-4" novalidate @submit.prevent>
        <FormField class="w-72" :label="t('taxReturns.tax')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="taxId" name="tax">
              <option :value="0">{{ t('taxReturns.chooseTax') }}</option>
              <option v-for="p in profiles" :key="p.tax_id" :value="p.tax_id">{{ p.tax_code }} · {{ p.tax_name }}</option>
            </NativeSelect>
          </template>
        </FormField>
      </form>
    </Card>
    <p v-if="loaded && !profiles.length" class="muted" data-testid="no-profiles">{{ t('taxReturns.noProfiles') }}</p>

    <Card v-if="taxId">
      <CardContent class="pt-4">
        <p v-if="loaded && !periods.length" class="m-0 text-sm text-muted-foreground" data-testid="empty">{{ t('taxReturns.empty') }}</p>
        <DataTable
          v-else
          :columns="periodColumns"
          :rows="periods"
          row-key="period_start"
          clickable
          :row-test-id="(per) => `period-${per.period_start}`"
          :row-class="(per) => (openMonth === per.period_start ? 'bg-accent' : undefined)"
          :is-expanded="(per) => openMonth === per.period_start && !!worksheet"
          :detail-test-id="() => 'worksheet'"
          :caption="t('taxReturns.title')"
          data-testid="periods"
          @row-click="open"
        >
          <template #cell-month="{ row }"><span :class="row.overdue ? 'text-destructive' : undefined">{{ monthLabel(row.period_start) }}</span></template>
          <template #cell-due_date="{ row }">{{ $date(row.due_date) }}<small v-if="row.overdue" class="text-destructive"> · {{ t('taxReturns.overdue') }}</small></template>
          <template #cell-status="{ row }"><Badge :variant="row.status === 'FILED' ? 'success' : row.status === 'READY' ? 'warning' : 'outline'">{{ statusText(row.status) }}</Badge></template>
          <template #cell-paid="{ row }">{{ row.status === 'FILED' ? row.paid : '' }}</template>
          <template #cell-outstanding="{ row }">{{ row.status === 'FILED' ? row.outstanding : '' }}</template>
          <template #cell-credit="{ row }">{{ row.status === 'FILED' && Number(row.credit_carried_forward) ? $money(row.credit_carried_forward) : '' }}</template>
          <template #detail>
            <template v-if="worksheet">
              <table class="w-full border-collapse text-sm" data-testid="lines">
                <thead>
                  <tr class="border-b border-border text-left text-xs text-muted-foreground">
                    <th class="py-1 pr-3 font-medium">{{ t('taxReturns.charge') }}</th><th class="px-3 text-right font-medium">{{ t('taxReturns.rate') }}</th><th class="px-3 text-right font-medium">{{ t('taxReturns.items') }}</th>
                    <th class="px-3 text-right font-medium">{{ t('taxReturns.base') }}</th><th class="pl-3 text-right font-medium">{{ t('taxReturns.taxAmount') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="l in worksheet.lines" :key="`${l.charge_code}-${l.rate}`" class="border-b border-border">
                    <td class="py-1 pr-3"><template v-if="l.charge_code === 'CREDIT_NOTE'">{{ t('taxReturns.creditNotes') }}</template><template v-else>{{ l.charge_code }}<small v-if="l.charge_name" class="text-muted-foreground"> · {{ l.charge_name }}</small></template></td>
                    <td class="px-3 text-right tabular-nums">{{ Number(l.rate) }}%</td><td class="px-3 text-right tabular-nums">{{ l.items }}</td><td class="px-3 text-right tabular-nums">{{ $money(l.base_amount) }}</td><td class="pl-3 text-right tabular-nums">{{ $money(l.tax_amount) }}</td>
                  </tr>
                  <tr v-if="!worksheet.lines.length"><td colspan="5" class="py-1 text-muted-foreground">{{ t('taxReturns.nothing') }}</td></tr>
                </tbody>
                <tfoot><tr data-testid="totals" class="font-semibold"><th colspan="3" class="pt-2 text-left">{{ t('taxReturns.total') }}</th><th class="px-3 pt-2 text-right tabular-nums">{{ $money(worksheet.base_amount) }}</th><th class="pl-3 pt-2 text-right tabular-nums">{{ $money(worksheet.tax_amount) }}</th></tr></tfoot>
              </table>
              <div v-if="showOffset && vat" class="mt-3" data-testid="offset" @click.stop>
                <h3 class="mb-1 text-sm font-semibold">{{ t('taxReturns.offsetTitle') }}</h3>
                <table class="w-full max-w-xl border-collapse text-sm">
                  <tbody class="[&_td]:py-1 [&_td:last-child]:text-right [&_td:last-child]:tabular-nums">
                    <tr class="border-b border-border"><td>{{ t('taxReturns.outputVat') }}</td><td data-testid="offset-output">{{ $money(vat.tax_amount) }}</td></tr>
                    <tr class="border-b border-border"><td>{{ t('taxReturns.inputClaimed') }}</td><td data-testid="offset-input">{{ $money(vat.input_claimed) }}</td></tr>
                    <tr class="border-b border-border"><td>{{ t('taxReturns.creditBroughtForward') }}</td><td data-testid="offset-bf">{{ $money(vat.credit_brought_forward) }}</td></tr>
                    <tr class="border-b border-border"><td>{{ t('taxReturns.offsetAmount') }}</td><td data-testid="offset-offset">{{ $money(vat.offset) }}</td></tr>
                    <tr class="border-b border-border font-semibold"><td>{{ t('taxReturns.payable') }}</td><td data-testid="offset-payable">{{ $money(vat.payable) }}</td></tr>
                    <tr><td>{{ t('taxReturns.creditCarriedForward') }}</td><td data-testid="offset-cf">{{ $money(vat.credit_carried_forward) }}</td></tr>
                  </tbody>
                </table>
                <table v-if="(filed?.input ?? worksheet.input).length" class="mt-2 w-full max-w-xl border-collapse text-sm" data-testid="input-claims">
                  <caption class="pb-1 text-left text-xs text-muted-foreground">{{ t('taxReturns.inputClaims') }}</caption>
                  <tbody>
                    <tr v-for="c in (filed?.input ?? worksheet.input)" :key="`${c.source}-${c.bill_id}-${c.line_no}-${c.credit_id}-${c.credit_line_no}-${c.settlement_id}-${c.reversal}`" class="border-b border-border">
                      <td class="py-1 pr-3">{{ c.bill_number }} · {{ c.supplier_name }}<small class="text-muted-foreground"> · {{ c.supplier_invoice_number }} · {{ $date(c.bill_date) }}</small><small v-if="c.source === 'SETTLEMENT'" class="text-muted-foreground"> · {{ t('taxReturns.settlementClaim') }}</small><small v-else-if="c.source === 'CREDIT_NOTE'" class="text-muted-foreground"> · {{ t(c.reversal ? 'taxReturns.creditNoteGivenBack' : 'taxReturns.creditNoteClaim') }}</small><small v-else-if="c.reversal" class="text-destructive"> · {{ t('taxReturns.takenBack') }}</small></td>
                      <td class="pl-3 text-right tabular-nums">{{ $money(c.amount) }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p class="mb-2 mt-3 text-sm text-muted-foreground" data-testid="books-check" @click.stop>
                {{ t('taxReturns.booksCredited', { amount: $money(worksheet.gl_collected) }) }}<template v-if="Number(worksheet.difference) !== 0"> · <b class="text-destructive">{{ t('taxReturns.difference', { amount: money(worksheet.difference) }) }}</b></template><template v-else> · {{ t('taxReturns.agree') }}</template>
                · {{ t('taxReturns.daysClosed', { posted: worksheet.posted_days, days: worksheet.days }) }}
              </p>
              <Button type="button" variant="outline" size="sm" data-testid="pdf" @click="showPdf">{{ t('taxReturns.pdf') }}</Button>

              <template v-if="filed">
                <h3 class="mb-2 mt-4 text-sm font-semibold" data-testid="filed-header">
                  {{ t('taxReturns.filedHeader', { number: filed.return_number, date: $date(filed.filed_on) }) }}<small v-if="filed.filing_reference" class="text-muted-foreground"> · {{ filed.filing_reference }}</small> ·
                  {{ payText(filed.payment_status) }} · {{ t('taxReturns.owedInfo', { amount: $money(filed.outstanding) }) }}
                </h3>
                <table v-if="filed.payments?.length" class="w-full border-collapse text-sm" data-testid="payments">
                  <thead>
                    <tr class="border-b border-border text-left text-xs text-muted-foreground">
                      <th class="py-1 pr-3 font-medium">{{ t('taxReturns.date') }}</th><th class="px-3 font-medium">{{ t('taxReturns.payment') }}</th><th class="px-3 font-medium">{{ t('taxReturns.reference') }}</th>
                      <th class="px-3 text-right font-medium">{{ t('taxReturns.amount') }}</th><th class="px-3 text-right font-medium">{{ t('taxReturns.penalty') }}</th><th />
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="p in filed.payments" :key="p.id" :class="['border-b border-border', p.status === 'VOIDED' && 'voided text-muted-foreground line-through']" :data-testid="`payment-${p.payment_number}`">
                      <td class="py-1 pr-3">{{ $date(p.payment_date) }}</td><td class="px-3">{{ p.payment_number }}</td><td class="px-3">{{ p.reference_number ?? '' }}</td>
                      <td class="px-3 text-right tabular-nums">{{ $money(p.amount) }}</td><td class="px-3 text-right tabular-nums">{{ money(p.penalty) }}</td>
                      <td><Button v-if="can('tax.file') && p.status === 'POSTED'" type="button" variant="outline" size="sm" :data-testid="`void-payment-${p.payment_number}`" @click="voiding = { kind: 'payment', id: p.id, reason: '', asking: false }">{{ t('taxReturns.voidEllipsis') }}</Button></td>
                    </tr>
                  </tbody>
                </table>
                <div v-if="can('tax.file') && filed.status === 'FILED'" class="my-3 flex gap-2">
                  <Button v-if="!payForm.open && Number(filed.outstanding) > 0" type="button" data-testid="pay-open" @click="startPay">{{ t('taxReturns.recordPayment') }}</Button>
                  <Button v-if="!Number(filed.paid)" type="button" variant="outline" data-testid="void-return" @click="voiding = { kind: 'return', id: filed.id, reason: '', asking: false }">{{ t('taxReturns.voidReturn') }}</Button>
                </div>
                <form v-if="payForm.open" class="mt-2 rounded-lg border border-border bg-card p-4" novalidate data-testid="pay-form" @submit.prevent="pay">
                  <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                    <FormField :label="t('taxReturns.paymentDate')" :error="fieldError('payment_date')">
                      <template #default="{ id, invalid }"><Input :id="id" v-model="payForm.payment_date" name="payment_date" type="date" :aria-invalid="invalid" /></template>
                    </FormField>
                    <FormField :label="t('taxReturns.amount')" :error="fieldError('amount')">
                      <template #default="{ id, invalid }"><Input :id="id" v-model="payForm.amount" name="amount" inputmode="decimal" :aria-invalid="invalid" /></template>
                    </FormField>
                    <FormField :label="t('taxReturns.paidFrom')">
                      <template #default="{ id }">
                        <NativeSelect :id="id" v-model="payForm.method" name="method">
                          <option v-for="m in METHODS" :key="m" :value="m">{{ methodText(m) }}</option>
                        </NativeSelect>
                      </template>
                    </FormField>
                    <FormField :label="t('taxReturns.billingCode')">
                      <template #default="{ id }"><Input :id="id" v-model="payForm.reference" name="reference" maxlength="100" /></template>
                    </FormField>
                    <FormField :label="t('taxReturns.penalty')" :error="fieldError('penalty')">
                      <template #default="{ id, invalid }"><Input :id="id" v-model="payForm.penalty" name="penalty" inputmode="decimal" :aria-invalid="invalid" /></template>
                    </FormField>
                    <FormField v-if="payForm.penalty.trim() && Number(payForm.penalty) > 0" :label="t('taxReturns.penaltyAccount')" :error="fieldError('penalty_account_id')">
                      <template #default="{ id, invalid }">
                        <Combobox :id="id" v-model="payForm.penalty_account_id" name="penalty_account" :aria-invalid="invalid" :options="[{ value: 0, label: `${t('taxReturns.chooseAccount')}` }, ...expenses.map((a) => ({ value: a.id, label: `${a.code} · ${a.name}` }))]" />
                      </template>
                    </FormField>
                    <FormField v-if="payForm.penalty.trim() && Number(payForm.penalty) > 0" :label="t('departments.field')">
                      <template #default="{ id }"><DepartmentSelect :id="id" v-model="payForm.department_id" name="penalty_department" /></template>
                    </FormField>
                  </div>
                  <div class="mt-4 flex justify-end gap-2">
                    <Button type="button" variant="outline" @click="payForm.open = false">{{ t('common.cancel') }}</Button>
                    <Button type="submit" :disabled="busy || !payForm.payment_date || !payForm.amount.trim()" data-testid="pay-post">{{ t('taxReturns.recordThePayment') }}</Button>
                  </div>
                </form>
              </template>

              <template v-else>
                <ul v-if="worksheet.blockers.length" class="mb-2 mt-3 text-destructive" data-testid="blockers"><li v-for="b in worksheet.blockers" :key="b">{{ b }}</li></ul>
                <form v-if="can('tax.file') && worksheet.ready" class="mt-3 rounded-lg border border-border bg-card p-4" novalidate data-testid="file-form" @submit.prevent="file">
                  <h3 class="m-0 text-sm font-semibold">{{ t('taxReturns.fileTitle', { month: monthLabel(worksheet.period_start) }) }}</h3>
                  <p class="mb-3 mt-1 text-sm text-muted-foreground">{{ t('taxReturns.fileHint', { date: $date(worksheet.due_date), authority: worksheet.profile.authority }) }}</p>
                  <div class="grid gap-4 sm:grid-cols-2">
                    <FormField :label="t('taxReturns.filedOn')" :hint="t('taxReturns.filedOnHint')" :error="fieldError('filed_on')">
                      <template #default="{ id, invalid }"><Input :id="id" v-model="fileForm.filed_on" name="filed_on" type="date" :aria-invalid="invalid" /></template>
                    </FormField>
                    <FormField :label="t('taxReturns.filingReference')">
                      <template #default="{ id }"><Input :id="id" v-model="fileForm.reference" name="reference" maxlength="100" /></template>
                    </FormField>
                    <FormField class="sm:col-span-2" :label="t('taxReturns.notes')">
                      <template #default="{ id }"><Input :id="id" v-model="fileForm.notes" name="notes" maxlength="500" /></template>
                    </FormField>
                  </div>
                  <div class="mt-4 flex justify-end"><Button type="submit" :disabled="busy" data-testid="file-post">{{ t('taxReturns.fileTheReturn') }}</Button></div>
                </form>
              </template>
            </template>
          </template>
        </DataTable>
      </CardContent>
    </Card>
  </template>
  <Card v-if="voiding && !voiding.asking" class="mt-4" data-testid="void-form">
    <CardHeader><CardTitle>{{ voiding.kind === 'return' ? t('taxReturns.voidTitleReturn') : t('taxReturns.voidTitlePayment') }}</CardTitle></CardHeader>
    <CardContent>
      <FormField class="max-w-md" :label="t('taxReturns.reason')">
        <template #default="{ id }"><Input :id="id" v-model="voiding.reason" name="reason" maxlength="500" /></template>
      </FormField>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="voiding = null">{{ t('common.cancel') }}</Button>
        <Button type="button" :disabled="!voiding.reason.trim()" data-testid="void-ask" @click="voiding.asking = true">{{ t('taxReturns.continue') }}</Button>
      </div>
    </CardContent>
  </Card>
  <ApprovalDialog v-if="voiding?.asking" :title="t('taxReturns.approveVoid')" :busy="busy" :error="dialogError" @approve="confirmVoid" @cancel="voiding = null; dialogError = null" />
</template>
