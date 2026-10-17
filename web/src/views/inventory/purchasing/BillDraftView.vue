<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, InventoryBillDraft, InventoryBillPreview, InventoryConfirmResult, InventoryMatch } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'
import { formatQty, isDecimal } from '@/utils/inventory'
import { draftVariant } from '@/utils/purchasing'

/**
 * A draft bill, matched against the supplier's invoice (docs/architecture/35-inventory.md 5.3.4, mockup 21). Three sources side by side for each line: the order (the price), the receipt (the quantity)
 * and the invoice. The server works the match out as the invoice is typed: the difference of price and how it splits between the stock still on hand and the purchase price variance, the VAT, and
 * whether the difference is above the tolerance and needs the approval of someone who holds inventory.approve. Lines that are not stock go on the bill and never into the stock.
 */
interface Row {
  receipt_id: number
  receipt_line_no: number
  receipt_number: string
  item: string
  code: string
  unit: string
  stockUnit: string
  received: string
  billable: string
  po_price: string
  qty: string
  price: string
  vat: string
}
interface Extra {
  account_id: number
  description: string
  amount: string
  vat: string
}

const route = useRoute()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const draftId = computed(() => Number(route.params.id))
const withIds = computed(() => String(route.query.with ?? '').split(',').filter(Boolean).map(Number))
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const canBill = computed(() => can('inventory.bill') && can('payables.post'))

const drafts = ref<InventoryBillDraft[]>([])
const accounts = ref<{ id: number; code: string; name: string }[]>([])
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const result = ref<InventoryConfirmResult | null>(null)

const head = reactive({ invoice: '', date: '', due: '' })
const rows = ref<Row[]>([])
const extras = ref<Extra[]>([])
const approver = reactive({ email: '', password: '' })
const preview = ref<InventoryBillPreview | null>(null)
const previewError = ref<ApiError | null>(null)
const keys = new Map<string, string>()

const supplierId = computed(() => drafts.value[0]?.supplier_id ?? 0)
const supplierName = computed(() => drafts.value[0]?.supplier_name ?? '')
const matches = computed<InventoryMatch[]>(() => drafts.value.flatMap((d) => d.matches))
const billed = computed(() => drafts.value.length > 0 && drafts.value.every((d) => d.status === 'BILLED' || d.status === 'DISCARDED'))

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  result.value = null
  if (propertyId === null) return
  try {
    const ids = [draftId.value, ...withIds.value]
    const got = await Promise.all(ids.map((id) => api.GET('/api/v1/properties/{propertyId}/inventory/bill-drafts/{id}', { params: { path: { propertyId, id } } })))
    drafts.value = got.map((g) => g.data).filter((d): d is InventoryBillDraft => !!d)
    head.date = property.clock?.business_date ?? ''
    head.invoice = ''
    head.due = ''
    rows.value = drafts.value.flatMap((d) =>
      d.lines
        .filter((l) => Number(l.billable_qty) > 0)
        .map((l) => ({ receipt_id: d.receipt_id, receipt_line_no: l.line_no, receipt_number: d.receipt_number, item: l.item_name, code: l.item_code, unit: l.unit_code, stockUnit: l.stock_unit_code, received: l.qty, billable: l.billable_qty,
          po_price: l.unit_price, qty: trimZeros(l.billable_qty), price: l.unit_price, vat: '' })),
    )
    extras.value = []
    approver.email = auth.me?.user.email ?? ''
    approver.password = ''
    if (canBill.value) {
      try {
        const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/accounts', { params: { path: { propertyId }, query: { account_type: 'EXPENSE', postable: true, active: true } } })
        accounts.value = (((data as unknown as { data?: { id: number; code: string; name: string }[] } | undefined)?.data) ?? []).map((a) => ({ id: a.id, code: a.code, name: a.name }))
      } catch {
        accounts.value = [] // a person who may not read the chart bills the stock lines only
      }
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, draftId, () => route.query.with], load, { immediate: true })
const trimZeros = (v: string): string => (v.includes('.') ? v.replace(/\.?0+$/, '') : v)

function addExtra(): void {
  extras.value.push({ account_id: accounts.value[0]?.id ?? 0, description: '', amount: '', vat: '' })
}

const ready = computed(
  () =>
    canBill.value &&
    !billed.value &&
    head.invoice.trim() !== '' &&
    head.date !== '' &&
    rows.value.length > 0 &&
    rows.value.every((r) => isDecimal(r.qty, 6) && Number(r.qty) > 0 && isDecimal(r.price, decimals.value) && Number(r.price) > 0 && (r.vat === '' || isDecimal(r.vat, decimals.value))) &&
    extras.value.every((e) => e.account_id > 0 && isDecimal(e.amount, decimals.value) && Number(e.amount) > 0 && (e.vat === '' || isDecimal(e.vat, decimals.value))),
)
function inputBody() {
  return {
    supplier_id: supplierId.value,
    supplier_invoice_number: head.invoice.trim(),
    bill_date: head.date,
    due_date: head.due || null,
    lines: rows.value.map((r) => ({ receipt_id: r.receipt_id, receipt_line_no: r.receipt_line_no, qty: r.qty.trim(), unit_price: r.price.trim(), vat_amount: r.vat || '0' })),
    extra_lines: extras.value.map((e) => ({ account_id: e.account_id, description: e.description.trim(), amount: e.amount.trim(), vat_amount: e.vat || '0' })),
  }
}

// The match is worked out by the server as the invoice is typed (a short pause after the last key).
let timer: ReturnType<typeof setTimeout> | undefined
async function refresh(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !ready.value) {
    preview.value = null
    return
  }
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/bill-drafts/preview', { params: { path: { propertyId } }, body: inputBody() })
    preview.value = data ?? null
    previewError.value = null
  } catch (e) {
    preview.value = null
    previewError.value = e instanceof ApiError ? e : null
  }
}
watch(
  [() => head.invoice, () => head.date, () => head.due, rows, extras],
  () => {
    clearTimeout(timer)
    timer = setTimeout(() => void refresh(), 300)
  },
  { deep: true },
)

// "288 botol ditagih · 768 botol masih di gudang → seluruh selisih masuk nilai stok": what is billed and what is still on hand, in the unit of the stock, and where the difference goes.
function splitSentence(l: InventoryBillPreview['lines'][number]): string {
  const unit = rows.value.find((r) => r.receipt_id === l.receipt_id && r.receipt_line_no === l.receipt_line_no)?.stockUnit ?? ''
  const where = Number(l.ppv_part) === 0 ? 'splitAllStock' : Number(l.inventory_part) === 0 ? 'splitAllPpv' : 'splitPartStock'
  return t('inventory.bill.splitOnHand', { item: l.item_name, billed: formatQty(l.billed_qty_stock), on: formatQty(l.on_hand_qty_stock), unit, where: t(`inventory.bill.${where}`) })
}
const previewOf = (r: Row) => preview.value?.lines.find((l) => l.receipt_id === r.receipt_id && l.receipt_line_no === r.receipt_line_no)
const needsApproval = computed(() => !!preview.value?.approval_required)

async function confirmBill(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  formError.value = null
  const fingerprint = JSON.stringify(inputBody())
  if (!keys.has(fingerprint)) keys.set(fingerprint, crypto.randomUUID())
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/bill-drafts/confirm', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': keys.get(fingerprint)! } },
      body: { ...inputBody(), ...(needsApproval.value ? { approval: { email: approver.email.trim(), password: approver.password } } : {}) },
    })
    approver.password = ''
    if (data) {
      result.value = data
      notice.value = t('inventory.bill.confirmed', { number: data.bill.bill_number })
      const { data: fresh } = await api.GET('/api/v1/properties/{propertyId}/inventory/bill-drafts/{id}', { params: { path: { propertyId, id: draftId.value } } })
      if (fresh) drafts.value = [fresh, ...drafts.value.slice(1)]
    }
  } catch (e) {
    approver.password = ''
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// ------------------------------------------------------------------------------------------ unmatch
const unmatching = ref<{ bill_id: number; bill_number: string } | null>(null)
const unmatchReason = ref('')
const unmatchAsk = ref(false)
async function unmatch(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const u = unmatching.value
  if (propertyId === null || !u) return
  busy.value = true
  formError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/inventory/bills/{id}/unmatch', { params: { path: { propertyId, id: u.bill_id } }, body: { reason: unmatchReason.value.trim(), approval } })
    unmatching.value = null
    unmatchAsk.value = false
    unmatchReason.value = ''
    notice.value = t('inventory.bill.unmatched', { number: u.bill_number })
    await load()
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const billsOfMatches = computed(() => {
  const by = new Map<number, { bill_id: number; bill_number: string; invoice: string; cost: number; variance: number; credit: boolean }>()
  for (const m of matches.value) {
    const b = by.get(m.bill_id) ?? { bill_id: m.bill_id, bill_number: m.bill_number, invoice: m.supplier_invoice_number, cost: 0, variance: 0, credit: false }
    b.cost += Number(m.bill_cost)
    b.variance += Number(m.variance)
    b.credit = b.credit || m.credit_note_id !== null
    by.set(m.bill_id, b)
  }
  return [...by.values()]
})
</script>

<template>
  <PageHeader :title="t('inventory.bill.title', { receipts: drafts.map((d) => d.receipt_number).join(', ') })" :description="supplierName">
    <template v-if="drafts[0]" #marks><Badge :variant="draftVariant(drafts[0].status)" data-testid="draft-status">{{ t(`inventory.draftStatus.${drafts[0].status}`) }}</Badge></template>
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/bill-drafts" data-testid="back">{{ t('inventory.bill.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <div v-else-if="loaded && drafts.length" class="grid gap-4 2xl:grid-cols-[minmax(0,1fr)_22rem]">
    <section class="flex min-w-0 flex-col gap-4">
      <ErrorNotice v-if="formError" :error="formError" inline data-testid="bill-error" />

      <template v-if="!billed && canBill">
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('inventory.bill.invoiceNumber')" required><template #default="{ id }"><Input :id="id" v-model="head.invoice" name="invoice_number" maxlength="60" data-testid="invoice-number" /></template></FormField>
          <FormField :label="t('inventory.bill.billDate')" required><template #default="{ id }"><Input :id="id" v-model="head.date" type="date" name="bill_date" /></template></FormField>
          <FormField :label="t('inventory.bill.dueDate')" :hint="t('inventory.bill.dueHint')"><template #default="{ id }"><Input :id="id" v-model="head.due" type="date" name="due_date" /></template></FormField>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[44rem] text-sm" data-testid="bill-lines">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.bill.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.bill.received') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.bill.poPrice') }}</th>
                <th class="py-1 pr-2 font-medium">{{ t('inventory.bill.invoiceQty') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.bill.invoicePrice') }} / {{ t('inventory.bill.difference') }}</th>
                <th class="py-1 font-medium">{{ t('inventory.bill.vat') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(r, i) in rows" :key="`${r.receipt_id}-${r.receipt_line_no}`" class="border-t border-border align-top" :data-testid="`bill-line-${i}`">
                <td class="py-2 pr-2">{{ r.item }}<div class="text-xs text-muted-foreground">{{ r.unit }} · {{ r.receipt_number }}</div></td>
                <td class="py-2 pr-2 text-right">{{ formatQty(r.billable) }}<div v-if="r.billable !== r.received" class="text-xs text-muted-foreground">{{ t('inventory.bill.ofReceived', { qty: formatQty(r.received) }) }}</div></td>
                <td class="py-2 pr-2 text-right">{{ formatMoney(r.po_price) }}</td>
                <td class="py-2 pr-2"><QuantityInput v-model="r.qty" :name="`qty_${i}`" class="w-24" :aria-label="t('inventory.bill.invoiceQty')" /></td>
                <td class="py-2 pr-2">
                  <MoneyInput v-model="r.price" :name="`price_${i}`" :decimals="decimals" class="w-32" :aria-label="t('inventory.bill.invoicePrice')" />
                  <div class="mt-0.5 text-xs" :data-testid="`line-diff-${i}`"><span v-if="previewOf(r)" :class="Number(previewOf(r)!.price_variance) === 0 ? 'text-muted-foreground' : 'font-medium'">{{ t('inventory.bill.difference') }}: {{ Number(previewOf(r)!.price_variance) > 0 ? '+' : '' }}{{ formatMoney(previewOf(r)!.price_variance) }}</span></div>
                </td>
                <td class="py-2"><MoneyInput v-model="r.vat" :name="`vat_${i}`" :decimals="decimals" class="w-28" :aria-label="t('inventory.bill.vat')" /></td>
              </tr>
              <tr v-for="(e, i) in extras" :key="`x${i}`" class="border-t border-border align-top" :data-testid="`extra-${i}`">
                <td class="py-2 pr-2" colspan="2">
                  <Input v-model="e.description" :name="`extra_desc_${i}`" :placeholder="t('inventory.bill.extraDescription')" :aria-label="t('inventory.bill.extraDescription')" />
                  <div class="mt-1 text-xs text-muted-foreground">{{ t('inventory.bill.extraHint') }}</div>
                </td>
                <td class="py-2 pr-2" colspan="2">
                  <NativeSelect v-model.number="e.account_id" :name="`extra_account_${i}`" :aria-label="t('inventory.bill.extraAccount')"><option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option></NativeSelect>
                </td>
                <td class="py-2 pr-2"><MoneyInput v-model="e.amount" :name="`extra_amount_${i}`" :decimals="decimals" class="w-32" :aria-label="t('inventory.bill.extraAmount')" /></td>
                <td class="py-2"><MoneyInput v-model="e.vat" :name="`extra_vat_${i}`" :decimals="decimals" class="w-28" :aria-label="t('inventory.bill.vat')" /> <Button type="button" variant="ghost" size="sm" @click="extras.splice(i, 1)">{{ t('common.remove') }}</Button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="accounts.length"><Button type="button" variant="outline" size="sm" data-testid="extra-add" @click="addExtra">{{ t('inventory.bill.addExtra') }}</Button></div>
        <p v-if="previewError" class="alert warning" data-testid="preview-error">{{ previewError.message }}</p>

        <section v-if="needsApproval && preview" class="rounded-xl border border-border bg-card p-4" data-testid="approval-box">
          <h3 class="m-0 text-sm font-semibold">{{ t('inventory.bill.approvalTitle') }}</h3>
          <p class="muted mt-1 text-sm">{{ t('inventory.bill.approvalHint', { amount: formatMoney(preview.approval_amount), tolerance: formatMoney(preview.tolerance_amount) }) }}</p>
          <div class="mt-2 grid gap-3 sm:grid-cols-2">
            <FormField :label="t('inventory.bill.approver')"><template #default="{ id }"><Input :id="id" v-model="approver.email" name="approver_email" autocomplete="off" /></template></FormField>
            <FormField :label="t('inventory.bill.password')"><template #default="{ id }"><Input :id="id" v-model="approver.password" type="password" name="approver_password" autocomplete="new-password" data-testid="approver-password" /></template></FormField>
          </div>
        </section>
        <div class="flex justify-end gap-2">
          <Button type="button" :disabled="busy || !ready || (needsApproval && (!approver.email.trim() || !approver.password))" data-testid="bill-confirm" @click="confirmBill">{{ t('inventory.bill.confirm') }}</Button>
        </div>
      </template>
      <p v-else-if="!canBill" class="muted" data-testid="no-bill-permission">{{ t('inventory.bill.noPermission') }}</p>

      <!-- what was billed of these receipts -->
      <section v-if="billsOfMatches.length" class="rounded-xl border border-border bg-card p-4" data-testid="billed-box">
        <h2 class="m-0 text-base font-semibold">{{ t('inventory.bill.billedTitle') }}</h2>
        <ul class="m-0 mt-2 list-none p-0 text-sm">
          <li v-for="b in billsOfMatches" :key="b.bill_id" class="flex flex-wrap items-center justify-between gap-2 border-t border-border py-1.5" :data-testid="`bill-${b.bill_number}`">
            <span>{{ b.bill_number }} · {{ b.invoice }}</span><span>{{ formatMoney(String(b.cost)) }}</span><span class="text-muted-foreground">{{ t('inventory.receipt.variance', { amount: formatMoney(String(b.variance)) }) }}</span>
            <Button v-if="canBill && !b.credit" type="button" variant="outline" size="sm" :data-testid="`unmatch-${b.bill_number}`" @click="unmatching = { bill_id: b.bill_id, bill_number: b.bill_number }">{{ t('inventory.bill.unmatch') }}</Button>
          </li>
        </ul>
        <div v-if="unmatching" class="mt-2 flex flex-wrap items-end gap-2">
          <FormField class="max-w-md flex-1" :label="t('inventory.bill.unmatchReason')"><template #default="{ id }"><Input :id="id" v-model="unmatchReason" name="unmatch_reason" maxlength="500" data-testid="unmatch-reason" /></template></FormField>
          <Button type="button" :disabled="!unmatchReason.trim()" data-testid="unmatch-continue" @click="unmatchAsk = true">{{ t('inventory.bill.unmatchContinue') }}</Button>
          <Button type="button" variant="ghost" @click="unmatching = null">{{ t('common.cancel') }}</Button>
        </div>
      </section>
      <section v-if="result" class="alert" data-testid="bill-result">
        <strong>{{ t('inventory.bill.confirmed', { number: result.bill.bill_number }) }}</strong>
        <div class="text-sm">{{ t('inventory.bill.resultTotal', { total: formatMoney(result.bill.total) }) }} · <RouterLink to="/payables/bills" class="text-primary hover:underline">{{ t('inventory.bill.openBill') }}</RouterLink></div>
      </section>
    </section>

    <!-- the three sources, the summary and where the difference goes -->
    <aside v-if="!billed" class="flex min-w-0 flex-col gap-4" data-testid="bill-aside">
      <section class="rounded-xl border border-border bg-card p-4">
        <h3 class="m-0 text-sm font-semibold">{{ t('inventory.bill.threeWay') }}</h3>
        <ol class="m-0 mt-2 list-none p-0 text-sm">
          <li class="border-b border-border py-1.5"><strong>1 · {{ t('inventory.bill.sourceOrder') }}</strong><div class="text-muted-foreground">{{ t('inventory.bill.sourceOrderHint') }}</div></li>
          <li class="border-b border-border py-1.5"><strong>2 · {{ t('inventory.bill.sourceReceipt') }}</strong><div class="text-muted-foreground">{{ t('inventory.bill.sourceReceiptHint') }}</div></li>
          <li class="py-1.5"><strong>3 · {{ t('inventory.bill.sourceInvoice') }}</strong><div class="text-muted-foreground">{{ preview ? t('inventory.bill.sourceInvoiceTotal', { total: formatMoney(preview.bill_total) }) : t('inventory.bill.sourceInvoiceWait') }}</div></li>
        </ol>
      </section>
      <section v-if="preview" class="rounded-xl border border-border bg-card p-4" data-testid="bill-summary">
        <h3 class="m-0 text-sm font-semibold">{{ t('inventory.bill.summary') }}</h3>
        <dl class="m-0 mt-2 grid grid-cols-[1fr_auto] gap-x-3 gap-y-1 text-sm">
          <dt class="text-muted-foreground">{{ t('inventory.bill.receiptValue') }}</dt><dd class="m-0 text-right">{{ formatMoney(preview.receipt_total) }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.bill.priceDifference') }}</dt><dd class="m-0 text-right" data-testid="sum-variance">{{ Number(preview.variance) > 0 ? '+' : '' }}{{ formatMoney(preview.variance) }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.bill.nonStock') }}</dt><dd class="m-0 text-right">{{ formatMoney(preview.extra_total) }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.bill.vatTotal') }}</dt><dd class="m-0 text-right">{{ formatMoney(preview.vat_total) }}</dd>
          <dt class="font-semibold">{{ t('inventory.bill.billTotal') }}</dt><dd class="m-0 text-right font-semibold" data-testid="sum-total">{{ formatMoney(preview.bill_total) }}</dd>
        </dl>
      </section>
      <section v-if="preview && Number(preview.variance) !== 0" class="rounded-xl border border-border bg-card p-4" data-testid="bill-split">
        <h3 class="m-0 text-sm font-semibold">{{ t('inventory.bill.splitTitle', { amount: formatMoney(preview.variance) }) }}</h3>
        <ul v-for="l in preview.lines.filter((x) => Number(x.variance) !== 0)" :key="`${l.receipt_id}-${l.receipt_line_no}`" class="m-0 mt-1 list-none p-0 text-sm">
          <li class="text-muted-foreground" :data-testid="`split-line-${l.receipt_line_no}`">{{ splitSentence(l) }}</li>
        </ul>
        <dl class="m-0 mt-2 grid grid-cols-[1fr_auto] gap-x-3 gap-y-1 text-sm">
          <dt class="text-muted-foreground">{{ t('inventory.bill.intoStock') }}</dt><dd class="m-0 text-right" data-testid="split-stock">{{ formatMoney(preview.inventory_part) }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.bill.intoPpv') }}</dt><dd class="m-0 text-right" data-testid="split-ppv">{{ formatMoney(preview.ppv_part) }}</dd>
        </dl>
        <p v-if="needsApproval" class="alert warning mt-2 text-sm" data-testid="needs-approval">{{ t('inventory.bill.aboveTolerance') }}</p>
      </section>
    </aside>
  </div>

  <ApprovalDialog v-if="unmatching && unmatchAsk" :title="t('inventory.bill.unmatchTitle', { number: unmatching.bill_number })" :message="t('inventory.bill.unmatchHint')" kind="PAYABLES" :busy="busy" :error="formError" @approve="unmatch" @cancel="unmatchAsk = false" />
</template>
