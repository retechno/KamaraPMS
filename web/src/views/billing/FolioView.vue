<script setup lang="ts">
import { Printer } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Approval, ChargeCode, Company, Folio, FolioItem, FolioSummary, PaymentMethod } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'
import { newIdempotencyKey } from '@/utils/reservations'

const props = defineProps<{ id: string }>()
const auth = useAuthStore()
const property = usePropertyStore()

const folio = ref<Folio | null>(null)
const codes = ref<ChargeCode[]>([])
const companies = ref<Company[]>([])
const companiesDenied = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const open = ref<number | null>(null) // item whose components are shown

const METHODS: PaymentMethod[] = ['CASH', 'CARD', 'BANK_TRANSFER', 'OTHER']
const charge = reactive({ codeId: 0, quantity: '1', unitPrice: '', description: '' })
const payment = reactive({ amount: '', method: 'CASH' as PaymentMethod, reference: '' })
const adjust = reactive({ codeId: 0, amount: '', reason: '' })
const transfer = reactive({ companyId: 0, amount: '', reference: '' })
// A correction waiting for its reason and its approval.
type Pending =
  | { kind: 'adjust' }
  | { kind: 'reverse'; item: FolioItem }
  | { kind: 'void'; item: FolioItem }
  | { kind: 'refund'; item: FolioItem }
  | { kind: 'move'; item: FolioItem }
const pending = ref<Pending | null>(null)
// The methods the property allows for a refund (cash unless configured otherwise).
const refundMethods = computed(() => (property.current?.refund_methods?.length ? property.current.refund_methods : ['CASH']))
const correction = reactive({ reason: '', amount: '', method: '', reference: '', targetFolio: 0 })
// The other open folios of this reservation that belong to a stay: where a charge can move to.
const siblings = ref<FolioSummary[]>([])
const approving = ref(false)
// One key per attempt; it is kept while a request may have been lost and renewed once the server has answered.
const keys: Record<string, string> = {}
function keyFor(name: string): string {
  return (keys[name] ??= newIdempotencyKey())
}

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const businessDate = computed(() => property.clock?.business_date ?? '')
const isOpen = computed(() => folio.value?.status === 'OPEN')
const chargeCodes = computed(() => codes.value.filter((c) => c.is_active && c.charge_type !== 'ROOM'))
// An adjustment corrects what is already posted: only the charge codes that have something posted on this folio (the net
// of their charges, adjustments and reversals above zero). The server enforces it; this keeps the list honest.
const postedNet = computed(() => {
  const net = new Map<string, number>()
  for (const i of folio.value?.items ?? []) {
    if (!i.charge_code || !['CHARGE', 'ADJUSTMENT', 'REVERSAL'].includes(i.transaction_type)) continue
    net.set(i.charge_code, Number(((net.get(i.charge_code) ?? 0) + Number(i.net_amount)).toFixed(3)))
  }
  return net
})
const adjustCodes = computed(() => codes.value.filter((c) => c.is_active && (postedNet.value.get(c.code) ?? 0) > 0))
const adjustPosted = computed(() => {
  const code = codes.value.find((c) => c.id === adjust.codeId)
  return code ? (postedNet.value.get(code.code) ?? 0) : 0
})
const fieldError = (field: string) => error.value?.fieldMessage(field)

const base = () => ({ path: { propertyId: pid.value as number, id: Number(props.id) } })

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('folio.read')) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/folios/{id}', { params: base() })
    folio.value = data ?? null
    codes.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/charge-codes', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
    charge.codeId ||= chargeCodes.value[0]?.id ?? 0
    if (!adjustCodes.value.some((c) => c.id === adjust.codeId)) adjust.codeId = adjustCodes.value[0]?.id ?? 0
    await loadCompanies(propertyId)
    await loadSiblings(propertyId)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadSiblings(propertyId: number): Promise<void> {
  siblings.value = []
  const f = folio.value
  if (!f || f.status !== 'OPEN' || !can('folio.reverse')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/folios', { params: { path: { propertyId }, query: { reservation_id: f.reservation_id, status: 'OPEN', limit: 50 } } })
    siblings.value = (data?.data ?? []).filter((s) => s.id !== f.id && s.stay_id !== null)
  } catch {
    siblings.value = [] // the move is an extra: the folio itself still works
  }
}

// The companies a folio can be transferred to. Picking one needs reservation.read or cityledger.read; a role that
// has only cityledger.transfer is told so instead of seeing an empty list.
async function loadCompanies(propertyId: number): Promise<void> {
  if (!can('cityledger.transfer') || folio.value?.status !== 'OPEN') return
  try {
    const all = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/companies', { params: { path: { propertyId }, query: { limit: 200, cursor, active: true } } }))
    // a folio billed to a company can only be transferred to that company
    const payer = folio.value?.bill_to_company_id ?? null
    companies.value = payer === null ? all : all.filter((c) => c.id === payer)
    companiesDenied.value = false
    transfer.companyId = payer ?? (transfer.companyId || (all[0]?.id ?? 0))
  } catch (e) {
    companies.value = []
    companiesDenied.value = e instanceof ApiError && e.status === 403
  }
}

async function run(name: string, action: () => Promise<unknown>): Promise<boolean> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await action()
    delete keys[name]
    await load()
    return true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) delete keys[name]
    return false
  } finally {
    busy.value = false
  }
}

const postCharge = () => run('charge', async () => {
  await api.POST('/api/v1/properties/{propertyId}/folios/{id}/charges', {
    params: { ...base(), header: { 'Idempotency-Key': keyFor('charge') } },
    body: { charge_code_id: charge.codeId, quantity: charge.quantity, unit_price: charge.unitPrice || undefined, description: charge.description || undefined },
  })
  charge.unitPrice = ''
  charge.description = ''
  notice.value = t('folio.noticeCharge')
})

const postPayment = () => run('payment', async () => {
  await api.POST('/api/v1/properties/{propertyId}/folios/{id}/payments', {
    params: { ...base(), header: { 'Idempotency-Key': keyFor('payment') } },
    body: { amount: payment.amount, payment_method: payment.method, reference_number: payment.reference || undefined },
  })
  payment.amount = ''
  payment.reference = ''
  notice.value = t('folio.noticePayment')
})

const postTransfer = () => run('transfer', async () => {
  await api.POST('/api/v1/properties/{propertyId}/folios/{id}/city-ledger-transfers', {
    params: { ...base(), header: { 'Idempotency-Key': keyFor('transfer') } },
    body: { company_id: transfer.companyId, amount: transfer.amount, reference_number: transfer.reference || undefined },
  })
  transfer.amount = ''
  transfer.reference = ''
  notice.value = t('folio.noticeTransfer')
})

const closeFolio = () => run('close', async () => {
  await api.POST('/api/v1/properties/{propertyId}/folios/{id}/close', { params: base(), body: { version: folio.value?.version ?? 0 } })
  notice.value = t('folio.noticeClosed')
})

function startAdjust(): void {
  correction.reason = adjust.reason
  pending.value = { kind: 'adjust' }
  dialogError.value = null
  approving.value = true
}

function startCorrection(kind: 'reverse' | 'void' | 'refund' | 'move', item: FolioItem): void {
  pending.value = { kind, item }
  correction.reason = ''
  correction.targetFolio = siblings.value[0]?.id ?? 0
  correction.amount = kind === 'refund' ? item.credit : ''
  correction.method = refundMethods.value[0] ?? 'CASH'
  correction.reference = ''
  dialogError.value = null
  approving.value = false
  error.value = null
}

function cancelCorrection(): void {
  pending.value = null
  approving.value = false
}

async function approve(approval: Approval): Promise<void> {
  const p = pending.value
  if (!p) return
  busy.value = true
  dialogError.value = null
  try {
    const propertyId = pid.value as number
    if (p.kind === 'adjust') {
      await api.POST('/api/v1/properties/{propertyId}/folios/{id}/adjustments', {
        params: { ...base(), header: { 'Idempotency-Key': keyFor('adjust') } },
        body: { charge_code_id: adjust.codeId, amount: adjust.amount, reason: adjust.reason, approval },
      })
      adjust.amount = ''
      adjust.reason = ''
      notice.value = t('folio.noticeAdjust')
    } else if (p.kind === 'reverse') {
      await api.POST('/api/v1/properties/{propertyId}/folio-items/{id}/reverse', { params: { path: { propertyId, id: p.item.id } }, body: { reason: correction.reason, approval } })
      notice.value = t('folio.noticeReverse')
    } else if (p.kind === 'move') {
      await api.POST('/api/v1/properties/{propertyId}/folio-items/{id}/transfer', { params: { path: { propertyId, id: p.item.id } }, body: { folio_id: correction.targetFolio, reason: correction.reason, approval } })
      notice.value = t('folio.noticeMove')
    } else if (p.kind === 'void') {
      await api.POST('/api/v1/properties/{propertyId}/payments/{id}/void', { params: { path: { propertyId, id: p.item.payment_id as number } }, body: { reason: correction.reason, approval } })
      notice.value = t('folio.noticeVoid')
    } else {
      await api.POST('/api/v1/properties/{propertyId}/payments/{id}/refunds', {
        params: { path: { propertyId, id: p.item.payment_id as number }, header: { 'Idempotency-Key': keyFor(`refund-${p.item.id}`) } },
        body: {
          amount: correction.amount,
          reason: correction.reason,
          approval,
          payment_method: correction.method as PaymentMethod,
          ...(correction.reference.trim() ? { reference_number: correction.reference.trim() } : {}),
        },
      })
      delete keys[`refund-${p.item.id}`]
      notice.value = t('folio.noticeRefund')
    }
    delete keys.adjust
    pending.value = null
    approving.value = false
    await load()
  } catch (e) {
    // The dialog stays open with the reason, so a mistyped password can be retried.
    dialogError.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) delete keys.adjust
  } finally {
    busy.value = false
  }
}

const reversible = (i: FolioItem) => isOpen.value && can('folio.reverse') && ['CHARGE', 'ADJUSTMENT'].includes(i.transaction_type) && i.business_date === businessDate.value && !i.reversed_by_item_id
const movable = (i: FolioItem) => isOpen.value && can('folio.reverse') && siblings.value.length > 0 && i.transaction_type === 'CHARGE' && !i.reversed_by_item_id
const folioLabel = (f: FolioSummary): string => (f.folio_type === 'COMPANY' ? `${f.folio_number} · ${t('billingInstructions.company')}` : f.folio_number)
const voidable = (i: FolioItem) => isOpen.value && can('payment.void') && i.transaction_type === 'PAYMENT' && i.business_date === businessDate.value && !i.reversed_by_item_id
// A transfer to a company is settled by the company's receipt, never refunded (its description names the method).
const isTransfer = (i: FolioItem) => i.description.includes('(CITY_LEDGER)')
const refundable = (i: FolioItem) => isOpen.value && can('payment.refund') && i.transaction_type === 'PAYMENT' && !i.reversed_by_item_id && !isTransfer(i)
const dialogTitle = computed(() => {
  switch (pending.value?.kind) {
    case 'adjust': return t('folio.approveAdjust')
    case 'reverse': return t('folio.approveReverse')
    case 'void': return t('folio.approveVoid')
    case 'refund': return t('folio.approveRefund')
    case 'move': return t('folio.approveMove')
    default: return ''
  }
})

const methodLabel = (m: string): string => t(`cashier.${m}` as never)

const itemColumns = computed<Column<FolioItem>[]>(() => [
  { key: 'business_date', label: t('folio.date'), format: 'date' as const },
  { key: 'description', label: t('folio.description') },
  { key: 'charge_code', label: t('folio.code') },
  { key: 'debit', label: t('folio.debit'), align: 'right', class: 'tabular-nums', format: 'money' as const },
  { key: 'credit', label: t('folio.credit'), align: 'right', class: 'tabular-nums', format: 'money' as const },
  { key: 'actions', label: '', align: 'right' },
])

// The posting forms, one tab each, only for what the role may do. All forms stay in the page (hidden when not chosen).
const postTab = ref('')
const postTabs = computed(() => [
  ...(can('folio.post_charge') ? [{ value: 'charge', label: t('folio.tabCharge') }] : []),
  ...(can('payment.post') ? [{ value: 'payment', label: t('folio.tabPayment') }] : []),
  ...(can('cityledger.transfer') ? [{ value: 'transfer', label: t('folio.tabTransfer') }] : []),
  ...(can('folio.adjust') ? [{ value: 'adjust', label: t('folio.tabAdjust') }] : []),
])
const activeTab = computed(() => (postTabs.value.some((x) => x.value === postTab.value) ? postTab.value : (postTabs.value[0]?.value ?? '')))

const canPrint = computed(() => can('reservation.read')) // the document names the reservation and the guest

async function print(path: string): Promise<void> {
  error.value = null
  try {
    await openPdf(path)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch(() => [pid.value, props.id], () => void load(), { immediate: true })
</script>

<template>
  <PageHeader :title="`${t('folio.title')}${folio ? ` ${folio.folio_number}` : ''}`">
    <template v-if="folio" #marks>
      <StatusBadge domain="record" :status="folio.status" data-testid="folio-status" />
      <Badge v-if="folio.bill_to_company_name" variant="outline" data-testid="folio-company">{{ t('billingInstructions.folioFor', { company: folio.bill_to_company_name }) }}</Badge>
    </template>
    <template #actions>
      <Button v-if="folio" as-child variant="outline" size="sm">
        <RouterLink :to="`/reservations/${folio.reservation_id}`">{{ t('folio.reservation') }}</RouterLink>
      </Button>
      <Button as-child variant="outline" size="sm">
        <RouterLink to="/folios">{{ t('folio.foliosLink') }}</RouterLink>
      </Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('folio.selectProperty') }}</p>
  <p v-else-if="!can('folio.read')" class="muted" data-testid="no-access">{{ t('folio.noAccess') }}</p>

  <template v-else-if="folio">
    <!-- the totals stay in view while the ledger scrolls -->
    <Card class="sticky top-[3.75rem] z-20 mb-4 flex flex-wrap items-center gap-x-8 gap-y-2 px-5 py-3" data-testid="summary">
      <div>
        <p class="m-0 text-xs text-muted-foreground">{{ t('folio.debit') }}</p>
        <p class="m-0 font-semibold tabular-nums"><span data-testid="debit">{{ $money(folio.totals.debit) }}</span></p>
      </div>
      <div>
        <p class="m-0 text-xs text-muted-foreground">{{ t('folio.credit') }}</p>
        <p class="m-0 font-semibold tabular-nums"><span data-testid="credit">{{ $money(folio.totals.credit) }}</span></p>
      </div>
      <div>
        <p class="m-0 text-xs text-muted-foreground">{{ t('folio.balance') }}</p>
        <p class="m-0 text-2xl font-semibold tabular-nums tracking-tight"><span data-testid="balance">{{ $money(folio.balance) }}</span></p>
      </div>
      <div class="ml-auto flex flex-wrap gap-2">
        <Button v-if="isOpen && can('folio.post_charge') && !folio.stay_id" variant="outline" size="sm" :disabled="busy" data-testid="close" @click="closeFolio">{{ t('folio.closeFolio') }}</Button>
        <RouterLink v-if="!isOpen && can('tax.invoice')" :to="{ path: '/tax/invoices', query: { source_type: 'FOLIO', id: String(folio.id) } }" class="inline-flex h-8 items-center rounded-md border border-border px-3 text-sm hover:bg-accent" data-testid="tax-invoice">{{ t('folio.taxInvoice') }}</RouterLink>
        <Button v-if="canPrint && pid !== null" variant="outline" size="sm" data-testid="print-invoice" @click="print(documentPath.invoice(pid, folio.id))">
          <Printer />{{ isOpen ? t('folio.printBill') : t('folio.printInvoice') }}
        </Button>
      </div>
    </Card>

    <Card class="mb-4" data-testid="items">
      <DataTable
        :columns="itemColumns"
        :rows="folio.items"
        row-key="id"
        :row-test-id="(i) => `item-${i.id}`"
        :row-class="(i) => (i.reversed_by_item_id ? 'struck text-muted-foreground line-through' : undefined)"
        :is-expanded="(i) => open === i.id"
        :detail-test-id="(i) => `detail-${i.id}`"
        :caption="t('folio.title')"
      >
        <template #cell-description="{ row: i }">
          <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-left text-primary underline-offset-2 hover:underline" :data-testid="`toggle-${i.id}`" :aria-expanded="open === i.id" @click="open = open === i.id ? null : i.id">{{ i.description }}</button>
          <small class="ml-1 text-muted-foreground">{{ i.transaction_type }}</small>
        </template>
        <template #cell-debit="{ row: i }"><span :class="Number(i.debit) === 0 && 'text-muted-foreground'">{{ $money(i.debit) }}</span></template>
        <template #cell-credit="{ row: i }"><span :class="Number(i.credit) === 0 && 'text-muted-foreground'">{{ $money(i.credit) }}</span></template>
        <template #cell-actions="{ row: i }">
          <span class="inline-flex flex-wrap justify-end gap-1.5 no-underline">
            <Button v-if="reversible(i)" variant="outline" size="sm" :data-testid="`reverse-${i.id}`" @click="startCorrection('reverse', i)">{{ t('folio.reverse') }}</Button>
            <Button v-if="movable(i)" variant="outline" size="sm" :data-testid="`move-${i.id}`" @click="startCorrection('move', i)">{{ t('folio.move') }}</Button>
            <Button v-if="voidable(i)" variant="outline" size="sm" :data-testid="`void-${i.id}`" @click="startCorrection('void', i)">{{ t('folio.void') }}</Button>
            <Button v-if="refundable(i)" variant="outline" size="sm" :data-testid="`refund-${i.id}`" @click="startCorrection('refund', i)">{{ t('folio.refund') }}</Button>
            <Button v-if="i.payment_id && canPrint && pid !== null" variant="ghost" size="sm" :data-testid="`receipt-${i.id}`" @click="print(documentPath.receipt(pid, i.payment_id))">{{ t('folio.receipt') }}</Button>
          </span>
        </template>
        <template #detail="{ row: i }">
          <span class="text-sm text-muted-foreground">
            {{ t('folio.detailLine', { quantity: i.quantity, unitPrice: $money(i.unit_price), mode: i.price_mode.toLowerCase(), net: $money(i.net_amount) }) }}<template v-if="i.reason"> · {{ i.reason }}</template>
          </span>
          <ul v-if="i.components.length" class="m-0 mt-1 list-disc pl-5 text-sm">
            <li v-for="c in i.components" :key="`${c.component_type}-${c.sequence}`">{{ t('folio.componentLine', { name: c.name, rate: c.rate, base: $money(c.base_amount), amount: $money(c.amount) }) }}</li>
          </ul>
        </template>
        <template #empty><EmptyState :title="t('folio.nothing')" data-testid="empty" /></template>
      </DataTable>
    </Card>

    <Card v-if="pending && pending.kind !== 'adjust' && !approving" class="mb-4 border-primary/50">
      <form novalidate data-testid="correction-form" @submit.prevent="approving = true">
        <CardHeader><CardTitle>{{ t(`folio.${pending.kind}` as never) }}: {{ pending.item.description }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2">
            <FormField v-if="pending.kind === 'refund'" :label="t('folio.amount')">
              <template #default="{ id }"><Input :id="id" v-model="correction.amount" name="refund_amount" inputmode="decimal" /></template>
            </FormField>
            <FormField v-if="pending.kind === 'refund'" :label="t('folio.refundBy')" :hint="t('folio.refundByHint')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="correction.method" name="refund_method" data-testid="refund-method">
                  <option v-for="m in refundMethods" :key="m" :value="m">{{ methodLabel(m) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField v-if="pending.kind === 'refund'" :label="t('folio.referenceOptional')">
              <template #default="{ id }"><Input :id="id" v-model="correction.reference" name="refund_reference" maxlength="100" /></template>
            </FormField>
            <FormField v-if="pending.kind === 'move'" :label="t('folio.moveTo')" :hint="t('folio.moveHint')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model.number="correction.targetFolio" name="move_folio" data-testid="move-folio">
                  <option v-for="s in siblings" :key="s.id" :value="s.id">{{ folioLabel(s) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('folio.reason')">
              <template #default="{ id }"><Input :id="id" v-model="correction.reason" name="reason" maxlength="500" /></template>
            </FormField>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="cancelCorrection">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="!correction.reason.trim()">{{ t('folio.continueApproval') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <ApprovalDialog v-if="approving && pending" :title="dialogTitle" :busy="busy" :error="dialogError" @approve="approve" @cancel="cancelCorrection" />

    <Tabs v-if="isOpen && postTabs.length" :model-value="activeTab" @update:model-value="postTab = String($event)">
      <TabsList data-testid="post-tabs">
        <TabsTrigger v-for="tb in postTabs" :key="tb.value" :value="tb.value" :data-testid="`tab-${tb.value}`">{{ tb.label }}</TabsTrigger>
      </TabsList>

      <TabsContent value="charge" force-mount :class="activeTab !== 'charge' && 'hidden'">
        <Card v-if="can('folio.post_charge')">
          <form novalidate data-testid="charge-form" @submit.prevent="postCharge">
            <CardHeader><CardTitle>{{ t('folio.postChargeTitle') }}</CardTitle></CardHeader>
            <CardContent>
              <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <FormField :label="t('folio.chargeCode')" :error="fieldError('charge_code_id')">
                  <template #default="{ id }">
                    <Combobox :id="id" v-model="charge.codeId" name="charge_code" :options="[...chargeCodes.map((c) => ({ value: c.id, label: `${c.code} · ${c.name}` }))]" />
                  </template>
                </FormField>
                <FormField :label="t('folio.quantity')" :error="fieldError('quantity')">
                  <template #default="{ id, invalid }"><Input :id="id" v-model="charge.quantity" name="quantity" inputmode="decimal" :aria-invalid="invalid" /></template>
                </FormField>
                <FormField :label="t('folio.unitPrice')" :error="fieldError('unit_price')">
                  <template #default="{ id, invalid }"><Input :id="id" v-model="charge.unitPrice" name="unit_price" inputmode="decimal" :aria-invalid="invalid" /></template>
                </FormField>
                <FormField :label="t('folio.description')">
                  <template #default="{ id }"><Input :id="id" v-model="charge.description" name="description" /></template>
                </FormField>
              </div>
              <div class="mt-4 flex justify-end"><Button type="submit" :disabled="busy">{{ t('folio.postCharge') }}</Button></div>
            </CardContent>
          </form>
        </Card>
      </TabsContent>

      <TabsContent value="payment" force-mount :class="activeTab !== 'payment' && 'hidden'">
        <Card v-if="can('payment.post')">
          <form novalidate data-testid="payment-form" @submit.prevent="postPayment">
            <CardHeader><CardTitle>{{ t('folio.takePaymentTitle') }}</CardTitle></CardHeader>
            <CardContent>
              <div class="grid gap-4 sm:grid-cols-3">
                <FormField :label="t('folio.amount')" :error="fieldError('amount')">
                  <template #default="{ id, invalid }"><Input :id="id" v-model="payment.amount" name="amount" inputmode="decimal" :aria-invalid="invalid" /></template>
                </FormField>
                <FormField :label="t('folio.method')">
                  <template #default="{ id }">
                    <NativeSelect :id="id" v-model="payment.method" name="payment_method">
                      <option v-for="m in METHODS" :key="m" :value="m">{{ methodLabel(m) }}</option>
                    </NativeSelect>
                  </template>
                </FormField>
                <FormField :label="t('folio.reference')">
                  <template #default="{ id }"><Input :id="id" v-model="payment.reference" name="reference" /></template>
                </FormField>
              </div>
              <div class="mt-4 flex justify-end"><Button type="submit" :disabled="busy">{{ t('folio.postPayment') }}</Button></div>
            </CardContent>
          </form>
        </Card>
      </TabsContent>

      <TabsContent value="transfer" force-mount :class="activeTab !== 'transfer' && 'hidden'">
        <Card v-if="can('cityledger.transfer')">
          <form novalidate data-testid="transfer-form" @submit.prevent="postTransfer">
            <CardHeader><CardTitle>{{ t('folio.transferTitle') }}</CardTitle></CardHeader>
            <CardContent>
              <p v-if="companiesDenied" class="m-0 text-sm text-muted-foreground" data-testid="transfer-denied">{{ t('folio.transferDenied') }}</p>
              <p v-else-if="!companies.length" class="m-0 text-sm text-muted-foreground" data-testid="transfer-none">{{ t('folio.transferNone') }}</p>
              <div v-else class="grid gap-4 sm:grid-cols-3">
                <FormField :label="t('folio.company')" :error="fieldError('company_id')">
                  <template #default="{ id }">
                    <Combobox :id="id" v-model="transfer.companyId" name="transfer_company" :options="[...companies.map((c) => ({ value: c.id, label: `${c.code} · ${c.name}` }))]" />
                  </template>
                </FormField>
                <FormField :label="t('folio.transferAmount', { balance: folio?.balance ?? '' })" :error="fieldError('amount')">
                  <template #default="{ id, invalid }"><Input :id="id" v-model="transfer.amount" name="transfer_amount" inputmode="decimal" :aria-invalid="invalid" /></template>
                </FormField>
                <FormField :label="t('folio.transferReference')">
                  <template #default="{ id }"><Input :id="id" v-model="transfer.reference" name="transfer_reference" /></template>
                </FormField>
              </div>
              <div v-if="companies.length" class="mt-4 flex justify-end"><Button type="submit" :disabled="busy">{{ t('folio.transferButton') }}</Button></div>
            </CardContent>
          </form>
        </Card>
      </TabsContent>

      <TabsContent value="adjust" force-mount :class="activeTab !== 'adjust' && 'hidden'">
        <Card v-if="can('folio.adjust')">
          <form novalidate data-testid="adjust-form" @submit.prevent="startAdjust">
            <CardHeader>
              <CardTitle>{{ t('folio.adjustTitle') }}</CardTitle>
              <p class="m-0 text-sm text-muted-foreground">{{ t('folio.adjustHint') }}</p>
            </CardHeader>
            <CardContent>
              <p v-if="!adjustCodes.length" class="m-0 text-sm text-muted-foreground" data-testid="adjust-nothing">{{ t('folio.adjustNothing') }}</p>
              <div v-else class="grid gap-4 sm:grid-cols-3">
                <FormField :label="t('folio.chargeCode')" :hint="t('folio.adjustPosted', { net: adjustPosted })">
                  <template #default="{ id }">
                    <Combobox :id="id" v-model="adjust.codeId" name="adjust_code" :options="[...adjustCodes.map((c) => ({ value: c.id, label: `${c.code} · ${c.name}` }))]" />
                  </template>
                </FormField>
                <FormField :label="t('folio.adjustAmount')" :error="fieldError('amount')">
                  <template #default="{ id, invalid }"><Input :id="id" v-model="adjust.amount" name="adjust_amount" inputmode="decimal" :aria-invalid="invalid" /></template>
                </FormField>
                <FormField :label="t('folio.reason')">
                  <template #default="{ id }"><Input :id="id" v-model="adjust.reason" name="adjust_reason" maxlength="500" /></template>
                </FormField>
              </div>
              <div v-if="adjustCodes.length" class="mt-4 flex justify-end"><Button type="submit" variant="outline" :disabled="busy || !adjust.amount || !adjust.reason.trim()">{{ t('folio.continueApproval') }}</Button></div>
            </CardContent>
          </form>
        </Card>
      </TabsContent>
    </Tabs>
  </template>
</template>
