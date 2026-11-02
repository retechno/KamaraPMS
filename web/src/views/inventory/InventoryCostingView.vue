<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryItem, InventoryQueueList, InventorySettings, InventorySetupReport, InventoryStockCard, InventoryWarehouse } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DateInput } from '@/components/ui/date-input'
import { guardForm } from '@/utils/forms'
import { NativeSelect } from '@/components/ui/native-select'
import { t, te } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { formatQty } from '@/utils/inventory'
import { findingText } from '@/utils/findings'
import { downloadCsv } from '@/views/accounting/reportApi'

/**
 * Costing (docs/architecture/35-inventory.md 4, mockup 28): the four steps cost goes through, the method of the property (chosen before the first stock movement, then locked), the setup
 * check, and the stock card of any item in any warehouse with, under FIFO, the layers that are left and the stock sold below zero.
 */
const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('inventory.manage', pid.value))

const settings = ref<InventorySettings | null>(null)
const setup = ref<InventorySetupReport | null>(null)
const queue = ref<InventoryQueueList | null>(null)
const queueError = ref<ApiError | null>(null)
const queueBusy = ref<number | null>(null)
const warehouses = ref<InventoryWarehouse[]>([])
const items = ref<InventoryItem[]>([])
const card = ref<InventoryStockCard | null>(null)
const loaded = ref(false)
const saving = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const method = ref<'AVERAGE' | 'FIFO'>('AVERAGE')
const engineering = ref<number | null>(null)
const pick = reactive({ warehouse: 0, item: 0, from: '', to: '' })

/** The POS sales that are not in the stock yet: what a count holds and what failed (35-inventory.md 5.6). */
async function loadQueue(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/consumption-queue', { params: { path: { propertyId } } })
    queue.value = data && data.summary ? data : null
  } catch {
    queue.value = null // the person may not read the stock
  }
}

async function actOnRow(id: number, what: 'retry' | 'cancel'): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  queueBusy.value = id
  queueError.value = null
  try {
    const params = { params: { path: { propertyId, id } } }
    if (what === 'retry') await api.POST('/api/v1/properties/{propertyId}/inventory/consumption-queue/{id}/retry', params)
    else await api.POST('/api/v1/properties/{propertyId}/inventory/consumption-queue/{id}/cancel', params)
    notice.value = t(what === 'retry' ? 'inventory.queue.retried' : 'inventory.queue.cancelled')
    const c = await api.GET('/api/v1/properties/{propertyId}/inventory/setup-check', { params: { path: { propertyId } } })
    setup.value = c.data ?? null
    await loadQueue()
  } catch (e) {
    queueError.value = e instanceof ApiError ? e : null
  } finally {
    queueBusy.value = null
  }
}

const rowError = (code: string | null, message: string | null): string => (code && te(`errors.${code}`) ? t(`errors.${code}`) : (message ?? ''))

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    const [s, c, w, i] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/inventory/settings', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/setup-check', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path),
      api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200 } } }),
    ])
    settings.value = s.data ?? null
    method.value = (s.data?.costing_method as 'AVERAGE' | 'FIFO' | undefined) ?? 'AVERAGE'
    engineering.value = s.data?.engineering_department_id ?? null
    setup.value = c.data ?? null
    await loadQueue()
    warehouses.value = w.data?.data ?? []
    items.value = i.data?.items ?? []
    pick.warehouse = Number(route.query.warehouse) || pick.warehouse
    pick.item = Number(route.query.item) || pick.item
    await showCard()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function showCard(): Promise<void> {
  const propertyId = pid.value
  card.value = null
  if (propertyId === null || !pick.warehouse || !pick.item) return
  try {
    const query: { warehouse: number; item: number; from?: string; to?: string } = { warehouse: pick.warehouse, item: pick.item }
    if (pick.from) query.from = pick.from
    if (pick.to) query.to = pick.to
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/stock-card', { params: { path: { propertyId }, query } })
    card.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
async function choose(): Promise<void> {
  await router.replace({ query: pick.warehouse && pick.item ? { warehouse: String(pick.warehouse), item: String(pick.item) } : {} })
  await showCard()
}

async function saveMethod(): Promise<void> {
  const propertyId = pid.value
  const s = settings.value
  if (propertyId === null || !s) return
  saving.value = true
  formError.value = null
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/inventory/settings', {
      params: { path: { propertyId } },
      body: {
        costing_method: method.value, price_tolerance_pct: s.price_tolerance_pct, qty_tolerance_pct: s.qty_tolerance_pct, over_receipt_pct: s.over_receipt_pct, po_requires_pr: s.po_requires_pr,
        direct_receipt_limit: s.direct_receipt_limit, count_blocks_night_audit: s.count_blocks_night_audit, count_blind: s.count_blind, engineering_department_id: engineering.value, version: s.version,
      },
    })
    settings.value = data ?? s
    notice.value = t('inventory.costing.saved')
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}
async function cardCsv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !pick.warehouse || !pick.item) return
  try {
    await downloadCsv('/api/v1/properties/{propertyId}/inventory/stock-card', { path: { propertyId }, query: { warehouse: pick.warehouse, item: pick.item, from: pick.from || undefined, to: pick.to || undefined } }, 'stock-card.csv')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(pid, load, { immediate: true })

const locked = computed(() => settings.value?.costing_locked_on != null)
const dirty = computed(() => settings.value !== null && (method.value !== settings.value.costing_method || engineering.value !== (settings.value.engineering_department_id ?? null)))
const steps = ['1', '2', '3', '4'] as const
</script>

<template>
  <PageHeader :title="t('inventory.costing.title')" :description="t('inventory.costing.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <div v-else-if="loaded" class="flex flex-col gap-6">
    <section aria-labelledby="steps-h" data-testid="steps">
      <h2 id="steps-h" class="m-0 mb-2 text-sm font-semibold">{{ t('inventory.costing.steps') }}</h2>
      <ol class="m-0 grid list-none gap-3 p-0 sm:grid-cols-2 xl:grid-cols-4">
        <li v-for="n in steps" :key="n" class="rounded-xl border border-border bg-card p-3">
          <span class="mb-1 inline-flex size-6 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">{{ n }}</span>
          <strong class="block text-sm">{{ t(`inventory.costing.step${n}`) }}</strong>
          <span class="text-xs text-muted-foreground">{{ t(`inventory.costing.step${n}d`) }}</span>
        </li>
      </ol>
    </section>

    <section aria-labelledby="method-h" data-testid="method">
      <h2 id="method-h" class="m-0 mb-2 text-sm font-semibold">{{ t('inventory.costing.methodTitle') }}</h2>
      <ErrorNotice v-if="formError" :error="formError" inline data-testid="method-error" />
      <div class="grid gap-3 sm:grid-cols-2">
        <label v-for="m in (['AVERAGE', 'FIFO'] as const)" :key="m" class="flex cursor-pointer items-start gap-2 rounded-xl border border-border bg-card p-3" :class="method === m ? 'ring-2 ring-ring' : ''">
          <input v-model="method" type="radio" name="costing_method" :value="m" class="mt-1 accent-primary" :disabled="locked || !canManage" :data-testid="`method-${m}`" />
          <span><strong class="block text-sm">{{ t(`inventory.costing.${m}`) }}</strong><span class="text-xs text-muted-foreground">{{ t(`inventory.costing.${m}d`) }}</span></span>
        </label>
      </div>
      <p v-if="locked && settings?.costing_locked_on" class="m-0 mt-2 text-sm" data-testid="locked">{{ t('inventory.costing.lockedSince', { date: formatDate(settings.costing_locked_on) }) }}</p>
      <div v-else class="mt-2 flex flex-wrap items-center gap-3">
        <span class="text-xs text-muted-foreground">{{ t('inventory.costing.freeNote') }}</span>
        <Button v-if="canManage" type="button" size="sm" :disabled="saving || !dirty" data-testid="method-save" @click="saveMethod">{{ t('inventory.costing.saveMethod') }}</Button>
      </div>
      <div v-if="canManage" class="mt-4 max-w-md" data-testid="engineering">
        <FormField :label="t('inventory.costing.engineering')" :hint="t('inventory.costing.engineeringHint')">
          <template #default="{ id }"><DepartmentSelect :id="id" v-model="engineering" name="engineering_department_id" :none-label="t('inventory.costing.engineeringNone')" data-testid="engineering-select" /></template>
        </FormField>
        <Button v-if="locked" type="button" size="sm" class="mt-2" :disabled="saving || !dirty" data-testid="engineering-save" @click="saveMethod">{{ t('inventory.costing.saveEngineering') }}</Button>
      </div>
    </section>

    <section aria-labelledby="setup-h" data-testid="setup">
      <h2 id="setup-h" class="m-0 mb-2 text-sm font-semibold">{{ t('inventory.costing.setupTitle') }}</h2>
      <p v-if="setup && setup.issues.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="setup-ok">{{ t('inventory.costing.setupOk') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
        <li v-for="(i, n) in setup?.issues ?? []" :key="n" class="flex items-start gap-2" :data-testid="`issue-${i.code}`">
          <Badge :variant="i.severity === 'ERROR' ? 'destructive' : 'warning'">{{ i.code }}</Badge><span>{{ findingText(i, i.severity === 'ERROR' ? 'blocker' : 'warning') }}</span>
        </li>
      </ul>
    </section>

    <section aria-labelledby="queue-h" data-testid="queue">
      <h2 id="queue-h" class="m-0 mb-1 text-sm font-semibold">{{ t('inventory.queue.title') }}</h2>
      <p class="m-0 mb-2 text-xs text-muted-foreground">{{ t('inventory.queue.intro') }}</p>
      <div v-if="queue" class="mb-2 flex flex-wrap gap-2 text-xs" data-testid="queue-summary">
        <Badge variant="outline">{{ t('inventory.queue.pending', { n: queue.summary.pending }) }}</Badge>
        <Badge :variant="queue.summary.held ? 'warning' : 'outline'">{{ t('inventory.queue.held', { n: queue.summary.held }) }}</Badge>
        <Badge :variant="queue.summary.failed ? 'destructive' : 'outline'">{{ t('inventory.queue.failed', { n: queue.summary.failed }) }}</Badge>
      </div>
      <ErrorNotice v-if="queueError" :error="queueError" inline />
      <p v-if="queue && queue.rows.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="queue-ok">{{ t('inventory.queue.allIn') }}</p>
      <div v-else-if="queue" class="overflow-x-auto">
        <table class="w-full min-w-[44rem] text-sm" data-testid="queue-table">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.queue.product') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.queue.outlet') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.queue.bill') }}</th>
              <th class="py-1 pr-2 font-medium">{{ t('inventory.queue.kind') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.queue.status') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.queue.reason') }}</th><th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in queue.rows" :key="r.id" class="border-t border-border align-top" :data-testid="`queue-row-${r.id}`">
              <td class="py-2 pr-2">{{ r.product_name }} <span class="text-xs text-muted-foreground">× {{ r.quantity }}</span></td>
              <td class="py-2 pr-2">{{ r.outlet_name }}</td>
              <td class="py-2 pr-2">{{ r.bill_number }}</td>
              <td class="py-2 pr-2">{{ t(`inventory.queue.kinds.${r.kind}`) }}</td>
              <td class="py-2 pr-2"><Badge :variant="r.status === 'FAILED' ? 'destructive' : 'warning'">{{ t(`inventory.queue.statuses.${r.status}`) }}</Badge></td>
              <td class="py-2 pr-2 text-xs">{{ r.status === 'FAILED' ? rowError(r.error_code, r.error_message) : t('inventory.queue.heldWhy') }}</td>
              <td class="py-2 text-right whitespace-nowrap">
                <Button v-if="canManage && r.status === 'FAILED'" type="button" variant="outline" size="sm" :disabled="queueBusy === r.id" :data-testid="`queue-retry-${r.id}`" @click="actOnRow(r.id, 'retry')">{{ t('inventory.queue.retry') }}</Button>
                <Button v-if="canManage && r.status !== 'APPLIED'" type="button" variant="ghost" size="sm" :disabled="queueBusy === r.id" :data-testid="`queue-cancel-${r.id}`" @click="actOnRow(r.id, 'cancel')">{{ t('inventory.queue.cancel') }}</Button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section aria-labelledby="card-h" data-testid="card">
      <h2 id="card-h" class="m-0 mb-2 text-sm font-semibold">{{ t('inventory.costing.cardTitle') }}</h2>
      <form class="mb-6 grid gap-x-3 gap-y-6 sm:grid-cols-2 xl:grid-cols-5" @submit.prevent="guardForm($event) && choose()">
        <FormField :label="t('inventory.costing.warehouse')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="pick.warehouse" name="warehouse" data-testid="card-warehouse">
              <option :value="0" disabled>{{ t('common.choose') }}</option>
              <option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField class="xl:col-span-2" :label="t('inventory.costing.item')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="pick.item" name="item" data-testid="card-item">
              <option :value="0" disabled>{{ t('common.choose') }}</option>
              <option v-for="i in items" :key="i.id" :value="i.id">{{ i.code }} · {{ i.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('inventory.costing.from')"><template #default="{ id }"><DateInput :id="id" v-model="pick.from" name="from" /></template></FormField>
        <FormField :label="t('inventory.costing.to')"><template #default="{ id }"><DateInput :id="id" v-model="pick.to" name="to" /></template></FormField>
        <div class="flex items-end"><Button type="submit" variant="outline" data-testid="card-show">{{ t('inventory.costing.show') }}</Button></div>
      </form>

      <p v-if="!card" class="m-0 text-sm text-muted-foreground" data-testid="card-pick">{{ t('inventory.costing.pick') }}</p>
      <template v-else>
        <div class="mb-2 flex flex-wrap items-center gap-2 text-sm">
          <strong>{{ card.item.name }}</strong><span class="text-muted-foreground">{{ card.warehouse }} · {{ card.item.stock_unit_code }}</span>
          <Badge variant="outline">{{ t(`inventory.method.${card.costing_method}`) }}</Badge>
          <Button type="button" variant="outline" size="sm" class="ml-auto" data-testid="card-csv" @click="cardCsv">{{ t('inventory.reports.csv') }}</Button>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[44rem] text-sm" data-testid="card-table">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.costing.date') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.costing.movement') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.costing.qty') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.costing.moveValue') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.costing.balanceQty') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.costing.balanceValue') }}</th>
                <th class="py-1 text-right font-medium">{{ t('inventory.costing.avg') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="card.from" class="border-t border-border text-muted-foreground">
                <td class="py-2 pr-2">{{ formatDate(card.from) }}</td><td class="py-2 pr-2">{{ t('inventory.costing.opening') }}</td><td /><td />
                <td class="py-2 pr-2 text-right">{{ formatQty(card.opening_qty) }}</td><td class="py-2 pr-2 text-right">{{ formatMoney(card.opening_value) }}</td><td />
              </tr>
              <tr v-for="l in card.lines" :key="l.movement_id" class="border-t border-border" :data-testid="`move-${l.movement_id}`">
                <td class="py-2 pr-2 whitespace-nowrap">{{ formatDate(l.business_date) }}</td>
                <td class="py-2 pr-2">{{ t(`inventory.kind.${l.kind}`) + (l.document_number ? ' ' + l.document_number : '') }}<span v-if="l.cost_estimated" class="ml-1 text-xs text-muted-foreground">({{ t('inventory.costing.estimated') }})</span>
                  <span v-if="l.kind === 'COGS_TO_WASTE'" class="block text-xs text-muted-foreground" data-testid="cost-to-waste">{{ t('inventory.costing.costToWaste') }}</span></td>
                <td class="py-2 pr-2 text-right" :class="l.qty.startsWith('-') ? 'text-destructive' : ''">{{ l.kind === 'COGS_TO_WASTE' ? '–' : l.qty === '0' ? '' : (l.qty.startsWith('-') ? '' : '+') + formatQty(l.qty) }}</td>
                <td class="py-2 pr-2 text-right">{{ l.kind === 'COGS_TO_WASTE' ? '–' : formatMoney(l.value) }}</td>
                <td class="py-2 pr-2 text-right">{{ formatQty(l.balance_qty) }}</td>
                <td class="py-2 pr-2 text-right">{{ formatMoney(l.balance_value) }}</td>
                <td class="py-2 text-right">{{ l.average_cost !== '0' ? formatMoney(l.average_cost) : '—' }}</td>
              </tr>
              <tr v-if="card.lines.length === 0" class="border-t border-border"><td colspan="7" class="py-3 text-muted-foreground">{{ t('inventory.costing.noMoves') }}</td></tr>
            </tbody>
          </table>
        </div>
        <p v-if="card.truncated" class="m-0 mt-2 text-xs text-muted-foreground">{{ t('inventory.costing.truncated') }}</p>
        <p v-if="card.costing_method === 'AVERAGE'" class="m-0 mt-2 text-xs text-muted-foreground">{{ t('inventory.costing.formula') }}</p>

        <div v-if="card.costing_method === 'FIFO'" class="mt-4" data-testid="layers">
          <h3 class="m-0 mb-1 text-sm font-semibold">{{ t('inventory.costing.layersTitle') }}</h3>
          <table class="w-full min-w-[34rem] text-sm">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.costing.layerDate') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.costing.layerIn') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.costing.layerLeft') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.costing.layerCost') }}</th>
                <th class="py-1 text-right font-medium">{{ t('inventory.costing.layerValue') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="l in card.layers" :key="l.id" class="border-t border-border">
                <td class="py-2 pr-2">{{ formatDate(l.received_on) }}</td><td class="py-2 pr-2 text-right">{{ formatQty(l.qty_in) }}</td><td class="py-2 pr-2 text-right">{{ formatQty(l.qty_remaining) }}</td>
                <td class="py-2 pr-2 text-right">{{ formatMoney(l.unit_cost) }}</td><td class="py-2 text-right">{{ formatMoney(l.value_remaining) }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="card.shortfalls.length" class="mt-4" data-testid="shortfalls">
          <h3 class="m-0 mb-1 text-sm font-semibold">{{ t('inventory.costing.shortTitle') }}</h3>
          <p class="m-0 mb-1 text-xs text-muted-foreground">{{ t('inventory.costing.shortHint') }}</p>
          <ul class="m-0 list-none p-0 text-sm">
            <li v-for="s in card.shortfalls" :key="s.id" class="flex justify-between gap-2 border-t border-border py-1">
              <span>{{ formatDate(s.business_date) }} · {{ t('inventory.costing.shortQty') }} {{ formatQty(s.qty_open) }}</span><span>{{ t('inventory.costing.shortEst') }} {{ formatMoney(s.est_unit_cost) }}</span>
            </li>
          </ul>
        </div>
      </template>
    </section>
  </div>
</template>
