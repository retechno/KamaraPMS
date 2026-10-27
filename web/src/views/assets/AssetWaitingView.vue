<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetCategory, AssetLocation, AssetWaitingLine, AssetWaitingPage } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { toast } from '@/composables/useToast'
import { t, te } from '@/i18n'
import { formatDate, formatMoney } from '@/utils/format'
import AssetMakeDialog from './AssetMakeDialog.vue'

/** The waiting list of "make an asset" (docs/architecture/37-fixed-assets.md 4.2.1, mockup 53): the journal lines that debit a cost account and no asset covers yet. */
const PAGE = 100
const { pid, canRead, canManage } = useAssetAccess()
const page = ref<AssetWaitingPage | null>(null)
const rows = ref<AssetWaitingLine[]>([])
const categories = ref<AssetCategory[]>([])
const locations = ref<AssetLocation[]>([])
const loaded = ref(false)
const loadingMore = ref(false)
const error = ref<ApiError | null>(null)
const tab = ref<'waiting' | 'dismissed'>('waiting')
const filter = ref({ source: '', category_id: 0, q: '' })
const making = ref<AssetWaitingLine | null>(null)

function query(offset: number): Record<string, string | number | boolean> {
  const f = filter.value
  const q: Record<string, string | number | boolean> = { limit: PAGE, offset, dismissed: tab.value === 'dismissed' }
  if (f.source) q.source = f.source
  if (f.category_id) q.category_id = f.category_id
  if (f.q.trim()) q.q = f.q.trim()
  return q
}

async function load(more = false): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !canRead.value) {
    loaded.value = true
    return
  }
  if (more) loadingMore.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/waiting', { params: { path: { propertyId }, query: query(more ? rows.value.length : 0) as never } })
    page.value = data ?? null
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
    loadingMore.value = false
  }
}

async function loadMasters(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canRead.value) return
  try {
    const [c, l] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/assets/categories', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/assets/locations', { params: { path: { propertyId } } }),
    ])
    categories.value = c.data?.data ?? []
    locations.value = l.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(pid, () => { void loadMasters(); void load() }, { immediate: true })

function setTab(next: 'waiting' | 'dismissed'): void {
  tab.value = next
  void load()
}

const total = computed(() => (tab.value === 'dismissed' ? page.value?.counts.dismissed : page.value?.counts.waiting) ?? 0)
const hasMore = computed(() => rows.value.length < (page.value?.total ?? 0))
const notStarted = computed(() => loaded.value && page.value !== null && !page.value.register_from)

const sourceText = (r: AssetWaitingLine): string => (te(`assets.waiting.source.${r.source}`) ? t(`assets.waiting.source.${r.source}`) : r.source)
const docText = (r: AssetWaitingLine): string => (r.source === 'BILL' && r.bill_number ? `${r.bill_number}${r.supplier_name ? ` · ${r.supplier_name}` : ''}` : r.document_number || r.journal_number)
const vatNote = (r: AssetWaitingLine): string => (r.vat_treatment && te(`assets.waiting.vat.${r.vat_treatment}`) ? t(`assets.waiting.vat.${r.vat_treatment}`) : '')

const columns = computed<Column<AssetWaitingLine>[]>(() => [
  { key: 'source', label: t('assets.waiting.col.source'), card: 'primary' },
  { key: 'journal_date', label: t('assets.waiting.col.date') },
  { key: 'description', label: t('assets.waiting.col.description') },
  { key: 'account_code', label: t('assets.waiting.col.account') },
  { key: 'cost', label: t('assets.waiting.col.cost'), align: 'right', card: 'money' },
  { key: 'age_days', label: t('assets.waiting.col.age'), align: 'right' },
  { key: 'actions', label: '', align: 'right' },
])

// not an asset / restore
const dismissing = ref<AssetWaitingLine | null>(null)
const reason = ref('')
const busy = ref(false)
const formError = ref<ApiError | null>(null)
function openDismiss(r: AssetWaitingLine): void {
  dismissing.value = r
  reason.value = ''
  formError.value = null
}
async function dismiss(): Promise<void> {
  const propertyId = pid.value
  const r = dismissing.value
  if (propertyId === null || !r) return
  busy.value = true
  formError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/assets/waiting/dismiss', { params: { path: { propertyId } }, body: { journal_line_id: r.line_id, reason: reason.value.trim() } })
    dismissing.value = null
    toast.success(t('assets.waiting.dismissed'))
    await load()
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
async function restore(r: AssetWaitingLine): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/assets/waiting/restore', { params: { path: { propertyId } }, body: { journal_line_id: r.line_id } })
    toast.success(t('assets.waiting.restored'))
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function made(): Promise<void> {
  making.value = null
  await load()
}
</script>

<template>
  <RouterLink to="/assets" class="text-sm text-primary hover:underline">← {{ t('assets.register.title') }}</RouterLink>
  <PageHeader :title="t('assets.waiting.title')" :description="t('assets.waiting.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('assets.noAccess') }}</p>
  <template v-else>
    <p v-if="notStarted" class="alert warning" role="status" data-testid="not-started">{{ t('assets.waiting.notStarted') }}</p>
    <div class="flex flex-wrap items-center gap-2" role="tablist">
      <button type="button" role="tab" :aria-selected="tab === 'waiting'" class="min-h-9 rounded-full border px-3 text-sm" :class="tab === 'waiting' ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'" data-testid="tab-waiting" @click="setTab('waiting')">{{ t('assets.waiting.tabWaiting', { n: page?.counts.waiting ?? 0 }) }}</button>
      <button type="button" role="tab" :aria-selected="tab === 'dismissed'" class="min-h-9 rounded-full border px-3 text-sm" :class="tab === 'dismissed' ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'" data-testid="tab-dismissed" @click="setTab('dismissed')">{{ t('assets.waiting.tabDismissed', { n: page?.counts.dismissed ?? 0 }) }}</button>
      <span class="flex-1" />
      <NativeSelect v-model="filter.source" name="source" :aria-label="t('assets.waiting.col.source')" data-testid="filter-source" @change="load()">
        <option value="">{{ t('assets.waiting.allSources') }}</option>
        <option v-for="s in ['BILL', 'CASHIER', 'BANK', 'MANUAL', 'OTHER']" :key="s" :value="s">{{ t(`assets.waiting.source.${s}`) }}</option>
      </NativeSelect>
      <NativeSelect v-model.number="filter.category_id" name="category_id" :aria-label="t('assets.col.category')" data-testid="filter-category" @change="load()">
        <option :value="0">{{ t('assets.filter.allCategories') }}</option>
        <option v-for="c in categories" :key="c.id" :value="c.id">{{ c.name }}</option>
      </NativeSelect>
      <Input v-model="filter.q" name="q" class="w-56" :placeholder="t('assets.waiting.search')" :aria-label="t('assets.waiting.search')" data-testid="filter-q" @keyup.enter="load()" @change="load()" />
    </div>
    <DataTable
      :columns="columns" :rows="rows" row-key="line_id" :loading="!loaded" :empty-title="tab === 'dismissed' ? t('assets.waiting.emptyDismissed') : t('assets.waiting.empty')" :empty-description="tab === 'dismissed' ? '' : t('assets.waiting.emptyHint')"
      :row-test-id="(r) => `line-${r.line_id}`" :has-more="hasMore" :loading-more="loadingMore" :total="page?.total ?? 0" :count-label="t('assets.waiting.count', { n: total })" cards data-testid="waiting-table" @load-more="load(true)"
    >
      <template #cell-source="{ row }">
        <Badge variant="outline">{{ sourceText(row) }}</Badge>
        <div class="text-xs text-muted-foreground" data-testid="doc">{{ docText(row) }}</div>
      </template>
      <template #cell-journal_date="{ row }">{{ formatDate(row.journal_date) }}</template>
      <template #cell-description="{ row }">
        {{ row.description }}
        <div v-if="vatNote(row)" class="text-xs text-muted-foreground">{{ vatNote(row) }}</div>
        <div v-if="row.below_threshold" class="text-xs text-warning-text" data-testid="below-threshold">{{ t('assets.waiting.below', { threshold: formatMoney(page?.capitalization_threshold) }) }}</div>
        <div v-if="row.dismissed && row.dismissed_reason" class="text-xs text-muted-foreground">{{ t('assets.waiting.because', { reason: row.dismissed_reason }) }}</div>
      </template>
      <template #cell-account_code="{ row }">{{ row.account_code }} {{ row.account_name }}</template>
      <template #cell-cost="{ row }"><span class="tabular-nums">{{ formatMoney(row.cost) }}</span></template>
      <template #cell-age_days="{ row }"><span class="text-xs text-muted-foreground">{{ t('assets.waiting.days', { n: row.age_days }) }}</span></template>
      <template #cell-actions="{ row }">
        <div v-if="canManage" class="flex justify-end gap-2">
          <template v-if="!row.dismissed">
            <Button size="sm" :data-testid="`make-${row.line_id}`" @click="making = row">{{ row.category_kind === 'OE' ? t('assets.waiting.makeBatch') : t('assets.waiting.make') }}</Button>
            <Button size="sm" variant="outline" :data-testid="`dismiss-${row.line_id}`" @click="openDismiss(row)">{{ t('assets.waiting.notAsset') }}</Button>
          </template>
          <Button v-else size="sm" variant="outline" :data-testid="`restore-${row.line_id}`" @click="restore(row)">{{ t('assets.waiting.restore') }}</Button>
        </div>
      </template>
    </DataTable>
    <p v-if="page" class="m-0 flex justify-between gap-3 text-sm font-semibold tabular-nums" data-testid="waiting-total">
      <span>{{ tab === 'dismissed' ? t('assets.waiting.totalDismissed') : t('assets.waiting.totalWaiting') }}</span>
      <span>{{ formatMoney(tab === 'dismissed' ? page.counts.dismissed_cost : page.counts.waiting_cost) }}</span>
    </p>
    <p class="m-0 text-xs text-muted-foreground">{{ t('assets.waiting.note') }}</p>
  </template>

  <AssetMakeDialog v-if="pid !== null" :line="making" :categories="categories" :locations="locations" :threshold="page?.capitalization_threshold ?? '0'" :property-id="pid" @close="making = null" @done="made" />

  <Dialog :open="!!dismissing" @update:open="(v) => { if (!v) dismissing = null }">
    <DialogContent class="max-w-md p-5" data-testid="dismiss-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="dismiss">
        <DialogTitle>{{ t('assets.waiting.dismissTitle') }}</DialogTitle>
        <DialogDescription>{{ t('assets.waiting.dismissHint') }}</DialogDescription>
        <ErrorNotice v-if="formError" :error="formError" inline />
        <FormField :label="t('assets.asset.reason')" required :error="formError?.fieldMessage('reason')"><template #default="{ id }"><Input :id="id" v-model="reason" name="reason" maxlength="300" /></template></FormField>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="dismissing = null">{{ t('common.cancel') }}</Button><Button type="submit" :disabled="busy || !reason.trim()" data-testid="dismiss-confirm">{{ t('assets.waiting.notAsset') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>
</template>
