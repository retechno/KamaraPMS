<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, TaxInvoice, TaxInvoiceCoverage, TaxInvoiceExport, TaxInvoicePreview } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, downloadExport, openPdf } from '@/utils/documents'
import { newIdempotencyKey } from '@/utils/reservations'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()
const router = useRouter()

const invoices = ref<TaxInvoice[]>([])
const opened = ref<TaxInvoice | null>(null)
const exports = ref<TaxInvoiceExport[]>([])
const coverage = ref<TaxInvoiceCoverage | null>(null)
const preview = ref<TaxInvoicePreview | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const filter = reactive({ status: '', from: '', to: '', q: '' })
const buyer = reactive({ name: '', npwp: '', address: '' })
const range = reactive({ from: '', to: '' })
const djp = ref('')
const voiding = ref<{ reason: string; asking: boolean } | null>(null)
let issueKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const sourceType = computed(() => String(route.query.source_type ?? ''))
const sourceId = computed(() => Number(route.query.id) || 0)
const replacesId = computed(() => Number(route.query.replaces) || 0)
const statusVariant = (s: string) => (s === 'ISSUED' ? 'success' : 'outline') as 'success' | 'outline'
const columns = computed<Column<TaxInvoice>[]>(() => [
  { key: 'issue_date', label: t('taxInvoices.date'), format: 'date' as const },
  { key: 'invoice_ref', label: t('taxInvoices.ref') },
  { key: 'source', label: t('taxInvoices.source') },
  { key: 'buyer', label: t('taxInvoices.buyer') },
  { key: 'taxable_base', label: t('taxInvoices.base'), align: 'right', format: 'money' as const },
  { key: 'vat_amount', label: t('taxInvoices.vat'), align: 'right', format: 'money' as const },
  { key: 'status', label: t('taxInvoices.status') },
  { key: 'djp_number', label: t('taxInvoices.official') },
])
const blockerText = (code: string, fallback: string): string => {
  const key = `taxInvoices.blocker_${code}`
  const text = t(key as 'taxInvoices.blocker_NOT_PKP')
  return text === key ? fallback : text
}

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('tax.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/invoices', {
      params: {
        path: { propertyId },
        query: { status: (filter.status || undefined) as 'ISSUED' | undefined, from: filter.from || undefined, to: filter.to || undefined, q: filter.q.trim() || undefined },
      },
    })
    invoices.value = data?.data ?? []
    const res = await api.GET('/api/v1/properties/{propertyId}/tax/invoices/exports', { params: { path: { propertyId } } })
    exports.value = res.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function loadPreview(): Promise<void> {
  const propertyId = pid.value
  preview.value = null
  if (propertyId === null || !can('tax.invoice') || !sourceId.value || !['CITY_LEDGER_INVOICE', 'FOLIO'].includes(sourceType.value)) return
  try {
    const query: Record<string, string | number | undefined> = { source_type: sourceType.value, id: sourceId.value }
    if (sourceType.value === 'FOLIO') Object.assign(query, { buyer_name: buyer.name || undefined, buyer_npwp: buyer.npwp || undefined, buyer_address: buyer.address || undefined })
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/invoices/preview', { params: { path: { propertyId }, query: query as never } })
    preview.value = data ?? null
    if (data && sourceType.value === 'CITY_LEDGER_INVOICE') Object.assign(buyer, { name: data.buyer.name, npwp: data.buyer.npwp, address: data.buyer.address ?? '' })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function issue(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !preview.value) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const body = {
      source_type: sourceType.value as 'FOLIO',
      city_ledger_invoice_id: sourceType.value === 'CITY_LEDGER_INVOICE' ? sourceId.value : undefined,
      folio_id: sourceType.value === 'FOLIO' ? sourceId.value : undefined,
      buyer: sourceType.value === 'FOLIO' ? { name: buyer.name, npwp: buyer.npwp, address: buyer.address || undefined } : undefined,
      replaces_invoice_id: replacesId.value || undefined,
    }
    const { data } = await api.POST('/api/v1/properties/{propertyId}/tax/invoices', { params: { path: { propertyId }, header: { 'Idempotency-Key': issueKey } }, body })
    issueKey = newIdempotencyKey()
    notice.value = t('taxInvoices.issued', { ref: data?.invoice_ref ?? '' })
    preview.value = null
    await router.replace({ path: '/tax/invoices' })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (error.value && error.value.status !== 0) issueKey = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}

async function open(inv: TaxInvoice): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (opened.value?.id === inv.id) {
    opened.value = null
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/invoices/{id}', { params: { path: { propertyId, id: inv.id } } })
    opened.value = data ?? null
    voiding.value = null
    djp.value = ''
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function saveNumber(): Promise<void> {
  const propertyId = pid.value
  const inv = opened.value
  if (propertyId === null || !inv) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/tax/invoices/{id}/djp-number', { params: { path: { propertyId, id: inv.id } }, body: { number: djp.value.trim() } })
    opened.value = data ?? inv
    notice.value = t('taxInvoices.numberSaved')
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function voidInvoice(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const inv = opened.value
  if (propertyId === null || !inv || !voiding.value) return
  busy.value = true
  dialogError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/tax/invoices/{id}/void', { params: { path: { propertyId, id: inv.id } }, body: { reason: voiding.value.reason.trim(), approval } })
    notice.value = t('taxInvoices.voided', { ref: inv.invoice_ref })
    voiding.value = null
    opened.value = null
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function showPdf(inv: TaxInvoice): Promise<void> {
  if (pid.value === null) return
  error.value = null
  try {
    await openPdf(documentPath.taxInvoice(pid.value, inv.id))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function exportCsv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const res = await downloadExport(`/api/v1/properties/${propertyId}/tax/invoices/exports`, { from: range.from, to: range.to })
    notice.value = t('taxInvoices.exported', { n: res.invoices ?? '' })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function checkCoverage(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/invoices/coverage', { params: { path: { propertyId }, query: { from: range.from, to: range.to } } })
    coverage.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch(() => pid.value, () => {
  invoices.value = []
  opened.value = null
  loaded.value = false
  void load()
}, { immediate: true })
watch(() => [route.query.source_type, route.query.id], () => {
  Object.assign(buyer, { name: '', npwp: '', address: '' })
  issueKey = newIdempotencyKey()
  void loadPreview()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('taxInvoices.title')" :description="pid !== null && can('tax.view') ? t('taxInvoices.intro') : undefined" />
  <ErrorNotice v-if="error" :error="error" inline data-testid="invoice-error">
<template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 4)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
</ErrorNotice>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('tax.view')" class="muted" data-testid="no-access">{{ t('taxInvoices.noAccess', { permission: 'tax.view' }) }}</p>
  <template v-else>
    <Card v-if="preview" class="mb-4" data-testid="issue">
      <form novalidate @submit.prevent="issue">
        <CardHeader>
          <CardTitle>{{ t(preview.source_type === 'FOLIO' ? 'taxInvoices.issueFolio' : 'taxInvoices.issueCityLedger', { ref: preview.source_ref }) }}</CardTitle>
        </CardHeader>
        <CardContent>
          <p class="mt-0 text-sm text-muted-foreground">{{ t('taxInvoices.issueHint', { date: $date(preview.issue_date) }) }}</p>
          <div class="grid gap-4 sm:grid-cols-3">
            <FormField :label="t('taxInvoices.buyerName')" :error="fieldError('buyer.name')">
              <template #default="{ id }"><Input :id="id" v-model="buyer.name" name="buyer_name" maxlength="150" :disabled="preview.source_type !== 'FOLIO'" @change="loadPreview" /></template>
            </FormField>
            <FormField :label="t('taxInvoices.buyerNpwp')" :hint="t('taxInvoices.npwpHint')">
              <template #default="{ id }"><Input :id="id" v-model="buyer.npwp" name="buyer_npwp" maxlength="30" :disabled="preview.source_type !== 'FOLIO'" @change="loadPreview" /></template>
            </FormField>
            <FormField :label="t('taxInvoices.buyerAddress')">
              <template #default="{ id }"><Input :id="id" v-model="buyer.address" name="buyer_address" maxlength="400" :disabled="preview.source_type !== 'FOLIO'" @change="loadPreview" /></template>
            </FormField>
          </div>
          <table class="mt-4 w-full border-collapse text-sm" data-testid="preview-lines">
            <thead>
              <tr class="border-b border-border text-left text-xs text-muted-foreground">
                <th class="py-1 pr-3 font-medium">{{ t('taxInvoices.charge') }}</th><th class="px-3 text-right font-medium">{{ t('taxInvoices.rate') }}</th>
                <th class="px-3 text-right font-medium">{{ t('taxInvoices.base') }}</th><th class="pl-3 text-right font-medium">{{ t('taxInvoices.vat') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="l in preview.lines" :key="l.line_no" class="border-b border-border">
                <td class="py-1 pr-3">{{ l.charge_code }}<small class="text-muted-foreground"> · {{ l.description }}</small></td>
                <td class="px-3 text-right tabular-nums">{{ Number(l.rate) }}%</td><td class="px-3 text-right tabular-nums">{{ $money(l.base_amount) }}</td><td class="pl-3 text-right tabular-nums">{{ $money(l.vat_amount) }}</td>
              </tr>
            </tbody>
            <tfoot><tr class="font-semibold" data-testid="preview-totals"><th colspan="2" class="pt-2 text-left">{{ t('taxInvoices.total') }}</th><th class="px-3 pt-2 text-right tabular-nums">{{ $money(preview.taxable_base) }}</th><th class="pl-3 pt-2 text-right tabular-nums">{{ $money(preview.vat_amount) }}</th></tr></tfoot>
          </table>
          <ul v-if="preview.blockers.length" class="mb-2 mt-3 text-destructive" data-testid="blockers"><li v-for="b in preview.blockers" :key="b.code">{{ blockerText(b.code, b.message) }}</li></ul>
          <div class="mt-4 flex justify-end gap-2">
            <RouterLink to="/tax/invoices" class="inline-flex items-center px-3 text-sm text-primary hover:underline">{{ t('common.cancel') }}</RouterLink>
            <Button type="submit" :disabled="busy || !preview.ready" data-testid="issue-post">{{ t('taxInvoices.issueButton') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card class="mb-4">
      <CardContent class="pt-4">
        <form class="mb-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-5" novalidate @submit.prevent="load">
          <FormField :label="t('taxInvoices.status')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.status" name="status">
                <option value="">{{ t('taxInvoices.all') }}</option>
                <option value="ISSUED">{{ t('taxInvoices.st_ISSUED') }}</option>
                <option value="VOIDED">{{ t('taxInvoices.st_VOIDED') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('taxInvoices.from')"><template #default="{ id }"><Input :id="id" v-model="filter.from" name="from" type="date" /></template></FormField>
          <FormField :label="t('taxInvoices.to')"><template #default="{ id }"><Input :id="id" v-model="filter.to" name="to" type="date" /></template></FormField>
          <FormField :label="t('taxInvoices.search')"><template #default="{ id }"><Input :id="id" v-model="filter.q" name="q" type="search" :placeholder="t('taxInvoices.searchHint')" /></template></FormField>
          <div class="flex items-end"><Button type="submit" variant="outline" data-testid="apply">{{ t('taxInvoices.apply') }}</Button></div>
        </form>
        <EmptyState v-if="loaded && !invoices.length" :title="t('taxInvoices.empty')" data-testid="empty" />
        <DataTable
          v-else
          :columns="columns"
          :rows="invoices"
          row-key="id"
          clickable
          :row-test-id="(i) => `invoice-${i.invoice_ref}`"
          :row-class="(i) => (i.status === 'VOIDED' ? 'voided text-muted-foreground line-through' : opened?.id === i.id ? 'bg-accent' : undefined)"
          :is-expanded="(i) => opened?.id === i.id"
          :detail-test-id="() => 'invoice-detail'"
          :caption="t('taxInvoices.title')"
          data-testid="invoices"
          @row-click="open"
        >
          <template #cell-source="{ row }">{{ row.source_type === 'FOLIO' ? row.folio_number : row.city_ledger_invoice_number }}</template>
          <template #cell-buyer="{ row }">{{ row.buyer.name }}</template>
          <template #cell-status="{ row }"><Badge :variant="statusVariant(row.status)">{{ t(`taxInvoices.st_${row.status}`) }}</Badge></template>
          <template #cell-djp_number="{ row }">{{ row.djp_number ?? '—' }}</template>
          <template #detail="{ row }">
            <template v-if="opened && opened.id === row.id">
              <div class="grid gap-2 text-sm sm:grid-cols-2" @click.stop>
                <p class="m-0"><b>{{ t('taxInvoices.seller') }}</b>: {{ opened.seller.name }} · NPWP {{ opened.seller.npwp }}<template v-if="opened.seller.pkp_number"> · PKP {{ opened.seller.pkp_number }}</template></p>
                <p class="m-0"><b>{{ t('taxInvoices.buyer') }}</b>: {{ opened.buyer.name }} · NPWP {{ opened.buyer.npwp }}<template v-if="opened.buyer.address"> · {{ opened.buyer.address }}</template></p>
              </div>
              <table class="mt-2 w-full border-collapse text-sm" data-testid="invoice-lines">
                <thead><tr class="border-b border-border text-left text-xs text-muted-foreground"><th class="py-1 pr-3 font-medium">{{ t('taxInvoices.charge') }}</th><th class="px-3 text-right font-medium">{{ t('taxInvoices.rate') }}</th><th class="px-3 text-right font-medium">{{ t('taxInvoices.base') }}</th><th class="pl-3 text-right font-medium">{{ t('taxInvoices.vat') }}</th></tr></thead>
                <tbody>
                  <tr v-for="l in opened.lines" :key="l.line_no" class="border-b border-border">
                    <td class="py-1 pr-3">{{ l.charge_code }}<small class="text-muted-foreground"> · {{ l.description }}</small></td>
                    <td class="px-3 text-right tabular-nums">{{ Number(l.rate) }}%</td><td class="px-3 text-right tabular-nums">{{ $money(l.base_amount) }}</td><td class="pl-3 text-right tabular-nums">{{ $money(l.vat_amount) }}</td>
                  </tr>
                </tbody>
              </table>
              <p v-if="opened.void_reason" class="mb-0 mt-2 text-sm text-muted-foreground">{{ t('taxInvoices.voidedReason', { reason: opened.void_reason }) }}</p>
              <div class="mt-3 flex flex-wrap items-end gap-3" @click.stop>
                <Button type="button" variant="outline" size="sm" data-testid="pdf" @click="showPdf(opened)">{{ t('taxInvoices.pdf') }}</Button>
                <form v-if="can('tax.invoice') && opened.status === 'ISSUED' && !opened.djp_number" class="flex items-end gap-2" novalidate @submit.prevent="saveNumber">
                  <FormField class="w-64" :label="t('taxInvoices.officialNumber')" :error="fieldError('number')">
                    <template #default="{ id, invalid }"><Input :id="id" v-model="djp" name="djp_number" maxlength="40" :aria-invalid="invalid" /></template>
                  </FormField>
                  <Button type="submit" variant="outline" size="sm" :disabled="busy || !djp.trim()" data-testid="save-number">{{ t('taxInvoices.saveNumber') }}</Button>
                </form>
                <template v-if="can('tax.invoice') && opened.status === 'ISSUED'">
                  <Button v-if="!voiding" type="button" variant="outline" size="sm" data-testid="void" @click="voiding = { reason: '', asking: false }">{{ t('taxInvoices.voidEllipsis') }}</Button>
                  <form v-else class="flex items-end gap-2" novalidate @submit.prevent="voiding.asking = true">
                    <FormField class="w-72" :label="t('taxInvoices.reason')"><template #default="{ id }"><Input :id="id" v-model="voiding.reason" name="reason" maxlength="500" /></template></FormField>
                    <Button type="button" variant="outline" size="sm" @click="voiding = null">{{ t('common.cancel') }}</Button>
                    <Button type="submit" size="sm" :disabled="!voiding.reason.trim()" data-testid="void-ask">{{ t('taxInvoices.voidWithApproval') }}</Button>
                  </form>
                </template>
                <RouterLink
                  v-if="can('tax.invoice') && opened.status === 'VOIDED'"
                  :to="{ path: '/tax/invoices', query: { source_type: opened.source_type, id: String(opened.city_ledger_invoice_id ?? opened.folio_id), replaces: String(opened.id) } }"
                  class="text-sm text-primary hover:underline"
                  data-testid="replace"
                >{{ t('taxInvoices.replace') }}</RouterLink>
              </div>
            </template>
          </template>
        </DataTable>
      </CardContent>
    </Card>

    <Card v-if="can('tax.view')" class="mb-4" data-testid="range-card">
      <CardHeader><CardTitle>{{ t('taxInvoices.exportTitle') }}</CardTitle></CardHeader>
      <CardContent>
        <p class="mt-0 text-sm text-muted-foreground">{{ t('taxInvoices.exportHint') }}</p>
        <form class="grid gap-4 sm:grid-cols-4" novalidate @submit.prevent="checkCoverage">
          <FormField :label="t('taxInvoices.from')" :error="fieldError('from')"><template #default="{ id }"><Input :id="id" v-model="range.from" name="range_from" type="date" /></template></FormField>
          <FormField :label="t('taxInvoices.to')" :error="fieldError('to')"><template #default="{ id }"><Input :id="id" v-model="range.to" name="range_to" type="date" /></template></FormField>
          <div class="flex items-end gap-2">
            <Button type="submit" variant="outline" :disabled="!range.from || !range.to" data-testid="coverage-check">{{ t('taxInvoices.checkCoverage') }}</Button>
            <Button v-if="can('tax.invoice')" type="button" :disabled="busy || !range.from || !range.to" data-testid="export" @click="exportCsv">{{ t('taxInvoices.exportCsv') }}</Button>
          </div>
        </form>
        <div v-if="coverage" class="mt-4" data-testid="coverage">
          <p class="mb-2 mt-0 text-sm">
            {{ t('taxInvoices.collected') }} <b data-testid="coverage-collected">{{ $money(coverage.vat_collected) }}</b> ·
            {{ t('taxInvoices.invoiced') }} <b data-testid="coverage-invoiced">{{ $money(coverage.vat_invoiced) }}</b> ·
            {{ t('taxInvoices.difference') }} <b data-testid="coverage-difference">{{ $money(coverage.difference) }}</b>
          </p>
          <table v-if="coverage.uncovered.length" class="w-full max-w-xl border-collapse text-sm" data-testid="uncovered">
            <caption class="pb-1 text-left text-xs text-muted-foreground">{{ t('taxInvoices.uncovered') }}</caption>
            <tbody>
              <tr v-for="f in coverage.uncovered" :key="f.folio_id" class="border-b border-border">
                <td class="py-1 pr-3">{{ f.folio_number }}</td>
                <td class="px-3 text-right tabular-nums">{{ $money(f.vat) }}</td>
                <td class="pl-3 text-right"><RouterLink v-if="can('tax.invoice') && f.status === 'CLOSED'" :to="{ path: '/tax/invoices', query: { source_type: 'FOLIO', id: String(f.folio_id) } }" class="text-sm text-primary hover:underline">{{ t('taxInvoices.issueButton') }}</RouterLink></td>
              </tr>
            </tbody>
          </table>
        </div>
        <table v-if="exports.length" class="mt-4 w-full max-w-3xl border-collapse text-sm" data-testid="exports">
          <caption class="pb-1 text-left text-xs text-muted-foreground">{{ t('taxInvoices.exports') }}</caption>
          <tbody>
            <tr v-for="x in exports" :key="x.id" class="border-b border-border">
              <td class="py-1 pr-3">{{ x.file_name }}</td>
              <td class="px-3 text-right tabular-nums">{{ t('taxInvoices.nInvoices', { n: x.invoice_count }) }}</td>
              <td class="pl-3"><small class="text-muted-foreground" :title="x.sha256">{{ x.sha256.slice(0, 12) }}…</small></td>
            </tr>
          </tbody>
        </table>
      </CardContent>
    </Card>
  </template>
  <ApprovalDialog v-if="voiding?.asking" :title="t('taxInvoices.approveVoid')" :busy="busy" :error="dialogError" @approve="voidInvoice" @cancel="voiding = null; dialogError = null" />
</template>
