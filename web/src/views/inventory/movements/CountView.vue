<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryCount, InventoryCountLine, InventoryItem } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDateTime, formatMoney, formatTime } from '@/utils/format'
import { formatQty, isDecimal } from '@/utils/inventory'
import { countVariant } from '@/utils/movements'

/**
 * A stock count (docs/architecture/35-inventory.md 4.7, 5.5, mockup 26). For the people who count it is the sheet, made for a phone (390 px): the list of items with a box for the quantity found, blind (the book
 * quantity is not shown) and with the hour each item was counted (the server's clock); a quantity is saved when the box is left. For the person who reviews it (a submitted count) it is the table of the
 * variance: the book quantity at the freeze, what the held rows already took, what was found, the difference at cost; the approver approves it, which ends the freeze. A banner says the warehouse is frozen.
 */
const route = useRoute()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const countId = computed(() => Number(route.params.id))

const count = ref<InventoryCount | null>(null)
const items = ref<InventoryItem[]>([])
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const saved = ref<Record<number, boolean>>({})
const typed = reactive<Record<number, string>>({})
const unitOf = reactive<Record<number, number>>({})
const q = ref('')
const view = ref<'todo' | 'done'>('todo')

const sheetMode = computed(() => count.value?.status === 'OPEN' && can('inventory.count'))
const itemOf = (id: number): InventoryItem | undefined => items.value.find((i) => i.id === id)
const trimZeros = (v: string): string => (v.includes('.') ? v.replace(/\.?0+$/, '') : v)

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/counts/{id}', { params: { path: { propertyId, id: countId.value } } })
    count.value = data ?? null
    if (count.value) {
      for (const l of count.value.lines) {
        // a quantity entered in a unit of the item is shown in that unit; one that was not is shown in the stock unit
        typed[l.item_id] = l.counted_qty === null ? '' : trimZeros(l.entered_qty ?? l.counted_qty)
        unitOf[l.item_id] = l.entered_unit_id ?? itemOf(l.item_id)?.stock_unit_id ?? 0
      }
      if (count.value.status === 'OPEN' && can('inventory.count') && items.value.length === 0) {
        const r = await api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200 } } })
        items.value = r.data?.items ?? []
        for (const l of count.value.lines) if (!unitOf[l.item_id]) unitOf[l.item_id] = itemOf(l.item_id)?.stock_unit_id ?? 0 // the units are known now
      }
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, countId], load, { immediate: true })

const lines = computed<InventoryCountLine[]>(() => count.value?.lines ?? [])
const countedN = computed(() => lines.value.filter((l) => l.counted_qty !== null).length)
const shownLines = computed(() =>
  lines.value.filter((l) => (view.value === 'todo' ? l.counted_qty === null : l.counted_qty !== null) && (!q.value.trim() || `${l.item_code} ${l.item_name}`.toLowerCase().includes(q.value.trim().toLowerCase()))),
)
const groups = computed(() => {
  const by = new Map<string, InventoryCountLine[]>()
  for (const l of shownLines.value) by.set(l.category_name, [...(by.get(l.category_name) ?? []), l])
  return [...by.entries()]
})

/** The unit of an item in the sheet: the units of the item, the stock unit first. */
const unitsOf = (itemId: number) => itemOf(itemId)?.units ?? []

async function enter(l: InventoryCountLine): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !count.value) return
  const v = (typed[l.item_id] ?? '').trim()
  const was = l.counted_qty === null ? '' : trimZeros(l.entered_qty ?? l.counted_qty)
  if (v === was) return
  if (v !== '' && !isDecimal(v, 6)) return
  formError.value = null
  try {
    const unit = unitOf[l.item_id]
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/inventory/counts/{id}/lines', {
      params: { path: { propertyId, id: count.value.id } },
      body: { lines: [{ item_id: l.item_id, qty: v === '' ? null : v, unit_id: unit && unit !== itemOf(l.item_id)?.stock_unit_id ? unit : null }] },
    })
    if (data) {
      count.value = data
      saved.value[l.item_id] = true
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  }
}

async function post(action: 'submit' | 'approve' | 'cancel', message: string): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !count.value) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST(`/api/v1/properties/{propertyId}/inventory/counts/{id}/${action}` as '/api/v1/properties/{propertyId}/inventory/counts/{id}/submit', { params: { path: { propertyId, id: count.value.id } } })
    if (data) {
      count.value = data
      notice.value = message
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

const hasValue = (v: string | null | undefined): v is string => v !== null && v !== undefined
const totalVariance = computed(() => lines.value.reduce((a, l) => a + (hasValue(l.variance_value) ? Number(l.variance_value) : 0), 0))
const counted = (l: InventoryCountLine): boolean => l.counted_qty !== null
const variancePositive = (l: InventoryCountLine): boolean => hasValue(l.variance_qty) && Number(l.variance_qty) > 0
const varianceNegative = (l: InventoryCountLine): boolean => hasValue(l.variance_qty) && Number(l.variance_qty) < 0
</script>

<template>
  <PageHeader :title="t('inventory.count.viewTitle', { number: count?.cnt_number ?? '', warehouse: count?.warehouse_name ?? '' })" :description="count ? (count.blind && count.status === 'OPEN' ? t('inventory.count.blind') : '') : ''">
    <template v-if="count" #marks><Badge :variant="countVariant(count.status)" data-testid="count-status">{{ t(`inventory.countStatus.${count.status}`) }}</Badge></template>
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/counts" data-testid="back">{{ t('inventory.count.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <div v-else-if="loaded && count" class="flex flex-col gap-3">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="count-error" />
    <p v-if="count.status === 'OPEN' || count.status === 'SUBMITTED'" class="alert warning m-0" data-testid="freeze-banner">{{ t('inventory.count.frozen', { time: formatDateTime(count.started_at) }) }}</p>
    <ul v-if="count.held_documents.length" class="m-0 list-none rounded-lg border border-border bg-card p-3 text-sm" data-testid="held-documents">
      <li v-for="d in count.held_documents" :key="d.issue_id">{{ t('inventory.count.heldDocument', { number: d.iss_number, n: d.held_lines }) }}</li>
    </ul>

    <!-- the sheet of the people who count: cards for a phone -->
    <template v-if="sheetMode">
      <div class="flex flex-wrap items-center justify-between gap-2" data-testid="progress"><span class="text-sm font-medium">{{ t('inventory.count.progress', { done: countedN, total: lines.length }) }}</span><span class="text-xs text-muted-foreground">{{ t('inventory.count.autosave') }}</span></div>
      <input v-model="q" type="search" name="q" class="h-11 w-full rounded-md border border-border bg-card px-3 text-base" :placeholder="t('inventory.count.search')" :aria-label="t('inventory.count.search')" data-testid="count-search" />
      <div class="flex gap-2" role="tablist">
        <Button type="button" role="tab" size="sm" :aria-selected="view === 'todo'" :variant="view === 'todo' ? 'default' : 'outline'" data-testid="tab-todo" @click="view = 'todo'">{{ t('inventory.count.todo', { n: lines.length - countedN }) }}</Button>
        <Button type="button" role="tab" size="sm" :aria-selected="view === 'done'" :variant="view === 'done' ? 'default' : 'outline'" data-testid="tab-done" @click="view = 'done'">{{ t('inventory.count.done', { n: countedN }) }}</Button>
      </div>
      <section v-for="[category, group] in groups" :key="category" class="flex flex-col gap-2">
        <h2 class="m-0 mt-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ category }}</h2>
        <article v-for="l in group" :key="l.item_id" class="rounded-xl border border-border bg-card p-3" :data-testid="`item-${l.item_code}`">
          <div class="flex items-baseline justify-between gap-2"><strong class="text-sm">{{ l.item_name }}</strong><span class="text-xs text-muted-foreground" :data-testid="`when-${l.item_code}`">{{ l.counted_at ? t('inventory.count.countedAt', { time: formatTime(l.counted_at) }) : t('inventory.count.notYet') }}</span></div>
          <div class="mt-2 flex items-center gap-2">
            <QuantityInput v-model="typed[l.item_id]!" :name="`count_${l.item_code}`" class="h-11 w-32 text-base" :aria-label="t('inventory.count.found', { item: l.item_name })" @blur="enter(l)" />
            <NativeSelect v-model.number="unitOf[l.item_id]" :name="`unit_${l.item_code}`" class="h-11 w-28" :aria-label="t('inventory.request.unit')" @change="enter(l)">
              <option v-for="u in unitsOf(l.item_id)" :key="u.unit_id" :value="u.unit_id">{{ u.unit_code }}</option>
            </NativeSelect>
            <span v-if="saved[l.item_id]" class="text-xs text-success-text">{{ t('inventory.count.saved') }}</span>
          </div>
          <p v-if="!count.blind && l.book_qty !== null" class="m-0 mt-1 text-xs text-muted-foreground">{{ t('inventory.count.book', { qty: formatQty(l.book_qty), unit: l.stock_unit_code }) }}</p>
        </article>
      </section>
      <p v-if="groups.length === 0" class="muted text-sm">{{ view === 'todo' ? t('inventory.count.allDone') : t('inventory.count.noneDone') }}</p>
      <p class="muted text-xs">{{ t('inventory.count.untouched') }}</p>
      <div class="sticky bottom-0 flex flex-wrap justify-end gap-2 bg-background/90 py-2">
        <Button type="button" variant="ghost" :disabled="busy" data-testid="count-cancel" @click="post('cancel', t('inventory.count.cancelled'))">{{ t('inventory.count.cancel') }}</Button>
        <Button type="button" :disabled="busy || countedN === 0" data-testid="count-submit" @click="post('submit', t('inventory.count.submitted'))">{{ t('inventory.count.submit') }}</Button>
      </div>
    </template>

    <!-- the review of the variance: the book, what was found and the difference -->
    <template v-else>
      <p v-if="count.preview" class="muted m-0 text-sm" data-testid="preview-note">{{ t('inventory.count.previewNote') }}</p>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[48rem] text-sm" data-testid="count-review">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.count.bookQty') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.count.heldBefore') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.count.expected') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.count.foundQty') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.count.countedWhen') }}</th>
              <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.count.varianceQty') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.count.varianceValue') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="l in lines" :key="l.item_id" class="border-t border-border" :class="counted(l) ? '' : 'text-muted-foreground'" :data-testid="`review-${l.item_code}`">
              <td class="py-2 pr-2">{{ l.item_name }} <span class="text-xs text-muted-foreground">{{ l.item_code }}</span></td>
              <td class="py-2 pr-2 text-right">{{ l.book_qty !== null ? formatQty(l.book_qty) : '' }}</td>
              <td class="py-2 pr-2 text-right">{{ hasValue(l.held_before_qty) && Number(l.held_before_qty) !== 0 ? formatQty(l.held_before_qty) : '' }}</td>
              <td class="py-2 pr-2 text-right">{{ hasValue(l.expected_qty) ? formatQty(l.expected_qty) : '' }}</td>
              <td class="py-2 pr-2 text-right">{{ counted(l) ? formatQty(l.counted_qty) : t('inventory.count.notCounted') }} <span v-if="counted(l)" class="text-xs text-muted-foreground">{{ l.stock_unit_code }}</span></td>
              <td class="py-2 pr-2 text-right text-xs">{{ l.counted_at ? formatTime(l.counted_at) : '' }}</td>
              <td class="py-2 pr-2 text-right" :class="variancePositive(l) ? 'text-success-text' : varianceNegative(l) ? 'text-destructive' : ''" :data-testid="`variance-${l.item_code}`">{{ hasValue(l.variance_qty) ? (Number(l.variance_qty) > 0 ? '+' : '') + formatQty(l.variance_qty) : '' }}</td>
              <td class="py-2 text-right" :class="variancePositive(l) ? 'text-success-text' : varianceNegative(l) ? 'text-destructive' : ''">{{ hasValue(l.variance_value) ? (Number(l.variance_value) > 0 ? '+' : '') + formatMoney(l.variance_value) : '' }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr class="border-t border-border"><td colspan="7" class="py-2 pr-2 text-right font-medium">{{ t('inventory.count.totalVariance') }}</td>
              <td class="py-2 text-right font-semibold" data-testid="count-total">{{ count.total_variance_value !== null ? formatMoney(count.total_variance_value) : formatMoney(String(totalVariance)) }}</td></tr>
          </tfoot>
        </table>
      </div>
      <div v-if="count.status === 'SUBMITTED'" class="flex flex-wrap items-center justify-end gap-2">
        <span class="muted mr-auto text-sm">{{ t('inventory.count.submittedBy', { name: count.submitted_by_name ?? '' }) }}</span>
        <Button v-if="can('inventory.count')" type="button" variant="outline" :disabled="busy" data-testid="count-cancel" @click="post('cancel', t('inventory.count.cancelled'))">{{ t('inventory.count.cancel') }}</Button>
        <Button v-if="can('inventory.approve')" type="button" :disabled="busy" data-testid="count-approve" @click="post('approve', t('inventory.count.approved'))">{{ t('inventory.count.approve') }}</Button>
      </div>
      <p v-if="count.status === 'APPROVED'" class="muted m-0 text-sm" data-testid="approved-note">{{ t('inventory.count.approvedBy', { name: count.decided_by_name ?? '', when: formatDateTime(count.decided_at) }) }}</p>
    </template>
  </div>
</template>
