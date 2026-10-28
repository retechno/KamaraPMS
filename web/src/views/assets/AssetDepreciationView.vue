<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetDepreciationBlocker, AssetDepreciationMonth, AssetDepreciationOverview, AssetDepreciationPreview } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { toast } from '@/composables/useToast'
import { t, te } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatBusinessDate } from '@/utils/dates'
import { formatDate, formatMoney, formatMonth } from '@/utils/format'
import { fromMinor, toMinor } from './split'

/** The monthly depreciation (docs/architecture/37-fixed-assets.md 4.3 and 5.1, mockup 56): the months in order, the preview of the journal with what blocks it, the run and the reversal of the latest run. */
const { pid, canRead, canDepreciate } = useAssetAccess()
const property = usePropertyStore()
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const overview = ref<AssetDepreciationOverview | null>(null)
const preview = ref<AssetDepreciationPreview | null>(null)
const month = ref('') // YYYY-MM
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
let key = crypto.randomUUID()

const ym = (date: string): string => date.slice(0, 7)

async function loadOverview(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !canDepreciate.value) {
    loaded.value = true
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/depreciation', { params: { path: { propertyId } } })
    overview.value = data ?? null
    const pick = data?.next_month ?? data?.months[data.months.length - 1]?.month
    if (!month.value && pick) month.value = ym(pick)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
  if (month.value) await loadPreview()
}

async function loadPreview(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !month.value) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/depreciation/{month}/preview', { params: { path: { propertyId, month: month.value } } })
    preview.value = data ?? null
  } catch (e) {
    preview.value = null
    error.value = e instanceof ApiError ? e : null
  }
}
watch(pid, () => { month.value = ''; void loadOverview() }, { immediate: true })

async function choose(m: AssetDepreciationMonth): Promise<void> {
  month.value = ym(m.month)
  key = crypto.randomUUID()
  error.value = null
  await loadPreview()
}

const current = computed(() => overview.value?.months.find((m) => ym(m.month) === month.value) ?? null)

/** The journal as the ledger will hold it: one debit line per category and department, one credit line per accumulated account. */
const debits = computed(() => preview.value?.groups ?? [])
const credits = computed(() => {
  const sums = new Map<string, { name: string; v: bigint }>()
  for (const g of preview.value?.groups ?? []) {
    const e = sums.get(g.accumulated_account_code) ?? { name: g.accumulated_account_name, v: 0n }
    e.v += toMinor(g.amount, decimals.value)
    sums.set(g.accumulated_account_code, e)
  }
  return [...sums.entries()].map(([account, e]) => ({ account, name: e.name, amount: fromMinor(e.v, decimals.value) }))
})
/** The months, oldest first (the register reads like a calendar). */
const months = computed(() => [...(overview.value?.months ?? [])].sort((a, b) => a.month.localeCompare(b.month)))
/** What the page has checked when nothing blocks the run: the sentence names the checks, and the one about departments only when every line has one. */
const checks = computed(() => {
  const list = [t('assets.depr.check.order'), t('assets.depr.check.accounts')]
  if (debits.value.length && debits.value.every((g) => g.department_id != null)) list.push(t('assets.depr.check.departments'))
  else list.push(t('assets.depr.check.departmentsRequired'))
  return list.join(', ')
})
const assetsOf = (g: { category_id: number; department_id?: number | null }): string =>
  (preview.value?.lines ?? []).filter((l) => l.category_id === g.category_id && (l.department_id ?? null) === (g.department_id ?? null)).map((l) => l.asset_number).join(', ')

/** A blocker in the language of the page: by its code (and the reason it carries), the figures it is made of come from the server. */
function blockerText(b: AssetDepreciationBlocker): string {
  const params: Record<string, string> = { ...(b.params ?? {}) }
  for (const k of ['month', 'next', 'register_from']) if (params[k]) params[k] = /^\d{4}-\d{2}$/.test(params[k]) ? formatMonth(params[k]) : params[k]
  const reason = params.reason ? `${b.code}_${params.reason}` : ''
  const generic = b.code.startsWith('ACCOUNT_') ? 'ACCOUNT_INVALID' : ''
  for (const k of [reason, b.code, generic]) if (k && te(`assets.depr.blocker.${k}`)) return t(`assets.depr.blocker.${k}`, params)
  return b.message
}

function statusText(m: AssetDepreciationMonth): string {
  if (m.status === 'POSTED') return t('assets.depr.state.POSTED')
  if (m.runnable) return t('assets.depr.state.READY')
  if (m.is_next) return t('assets.depr.state.NOT_ENDED')
  return m.status === 'REVERSED' ? t('assets.depr.state.REVERSED') : '–'
}
const statusVariant = (m: AssetDepreciationMonth) => (m.status === 'POSTED' ? 'success' : m.runnable ? 'warning' : 'outline')

async function run(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !month.value) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/depreciation/{month}/run', { params: { path: { propertyId, month: month.value }, header: { 'Idempotency-Key': key } } })
    key = crypto.randomUUID()
    toast.success(t('assets.depr.done', { month: formatMonth(month.value), number: data?.run.journal_number ?? '' }))
    month.value = ''
    await loadOverview()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// reverse the latest run
const reversing = ref(false)
const reason = ref('')
const formError = ref<ApiError | null>(null)
async function reverse(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !month.value) return
  busy.value = true
  formError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/assets/depreciation/{month}/reverse', { params: { path: { propertyId, month: month.value } }, body: { reason: reason.value.trim() } })
    reversing.value = false
    toast.success(t('assets.depr.reversed', { month: formatMonth(month.value) }))
    await loadOverview()
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <RouterLink to="/assets" class="text-sm text-primary hover:underline">← {{ t('assets.register.title') }}</RouterLink>
  <PageHeader :title="t('assets.depr.title')" :description="overview ? t('assets.depr.intro', { date: formatBusinessDate(overview.business_date), from: overview.register_from ? formatMonth(ym(overview.register_from)) : '–' }) : t('assets.depr.introShort')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('assets.noAccess') }}</p>
  <p v-else-if="!canDepreciate" class="muted" data-testid="no-depreciate">{{ t('assets.depr.needPermission') }}</p>
  <template v-else-if="overview">
    <p v-if="!overview.register_from" class="alert warning" role="status" data-testid="not-started">{{ t('assets.depr.notStarted') }}</p>
    <Card v-else class="flex flex-col gap-2 p-4" data-testid="depr-months">
      <h2 class="m-0 text-base font-semibold">{{ t('assets.depr.months') }}</h2>
      <ul class="m-0 flex list-none flex-wrap gap-2 p-0">
        <li v-for="m in months" :key="m.month">
          <button type="button" class="flex min-h-11 flex-col rounded-lg border px-3 py-1 text-left text-sm" :class="ym(m.month) === month ? 'border-primary bg-primary/5' : 'border-border bg-card'" :data-testid="`month-${ym(m.month)}`" @click="choose(m)">
            <span class="font-medium">{{ formatMonth(ym(m.month)) }}</span>
            <Badge :variant="statusVariant(m)">{{ statusText(m) }}</Badge>
            <span v-if="m.status === 'POSTED'" class="text-xs tabular-nums text-muted-foreground">{{ formatMoney(m.total) }}</span>
          </button>
        </li>
      </ul>
      <p class="m-0 text-xs text-muted-foreground">{{ t('assets.depr.rule') }}</p>
    </Card>

    <Card v-if="preview" class="flex flex-col gap-3 p-4" data-testid="depr-preview">
      <div class="flex flex-wrap items-baseline justify-between gap-2">
        <h2 class="m-0 text-base font-semibold">{{ t('assets.depr.preview', { month: formatMonth(month), date: formatDate(preview.journal_date) }) }}</h2>
        <span class="text-sm text-muted-foreground" data-testid="depr-counts">{{ t('assets.depr.counts', { n: preview.asset_count }) }}<template v-if="preview.catch_up_count"> · {{ t('assets.depr.catchUp', { n: preview.catch_up_count }) }}</template></span>
      </div>
      <ul v-if="preview.blockers.length" class="m-0 flex list-none flex-col gap-1 p-0 text-sm text-destructive" role="alert" data-testid="depr-blockers">
        <li v-for="(b, i) in preview.blockers" :key="i" :data-testid="`blocker-${b.code}`">{{ blockerText(b) }}</li>
      </ul>
      <div v-if="debits.length" class="overflow-x-auto">
        <table class="w-full border-collapse text-sm" data-testid="depr-journal">
          <thead><tr class="text-left text-xs uppercase text-muted-foreground"><th class="p-2">{{ t('assets.depr.col.account') }}</th><th class="p-2">{{ t('assets.col.department') }}</th><th class="p-2">{{ t('assets.depr.col.assets') }}</th><th class="p-2 text-right">{{ t('assets.depr.col.debit') }}</th><th class="p-2 text-right">{{ t('assets.depr.col.credit') }}</th></tr></thead>
          <tbody>
            <tr v-for="(g, i) in debits" :key="`d${i}`" class="border-t border-border" :data-testid="`debit-${g.expense_account_code}`">
              <td class="p-2">{{ g.expense_account_code }} · {{ g.expense_account_name }} <span class="text-xs text-muted-foreground">({{ g.category_name }})</span></td>
              <td class="p-2">{{ g.department_name ?? '–' }}</td>
              <td class="p-2 text-xs text-muted-foreground">{{ assetsOf(g) }}</td>
              <td class="p-2 text-right tabular-nums">{{ formatMoney(g.amount) }}</td><td class="p-2" />
            </tr>
            <tr v-for="c in credits" :key="c.account" class="border-t border-border" :data-testid="`credit-${c.account}`">
              <td class="p-2" colspan="3">{{ c.account }} · {{ c.name }}</td><td class="p-2" /><td class="p-2 text-right tabular-nums">{{ formatMoney(c.amount) }}</td>
            </tr>
            <tr class="border-t border-border font-semibold">
              <td class="p-2" colspan="3">{{ t('assets.depr.total') }}</td><td class="p-2 text-right tabular-nums" data-testid="depr-total">{{ formatMoney(preview.total) }}</td><td class="p-2 text-right tabular-nums">{{ formatMoney(preview.total) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="!preview.blockers.length && debits.length" class="m-0 text-xs text-muted-foreground" data-testid="depr-clear">{{ t('assets.depr.clear', { checks }) }}</p>
      <div v-if="current?.status === 'POSTED'" class="flex flex-wrap items-center gap-2 text-sm" data-testid="depr-posted">
        <span>{{ t('assets.depr.postedAs', { number: current.journal_number ?? '' }) }}</span>
        <span class="flex-1" />
        <Button v-if="current.reversible" variant="outline" data-testid="depr-reverse" @click="reversing = true; reason = ''; formError = null">{{ t('assets.depr.reverse') }}</Button>
        <span v-else class="text-xs text-muted-foreground">{{ current.period_closed ? t('assets.depr.closedHint') : t('assets.depr.notLatest') }}</span>
      </div>
      <div v-else class="flex justify-end">
        <Button :disabled="busy || !preview.can_run" data-testid="depr-run" @click="run">{{ t('assets.depr.run', { month: formatMonth(month) }) }}</Button>
      </div>
    </Card>
  </template>

  <Dialog :open="reversing" @update:open="(v) => (reversing = v)">
    <DialogContent class="max-w-md p-5" data-testid="reverse-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="reverse">
        <DialogTitle>{{ t('assets.depr.reverseTitle', { month: formatMonth(month) }) }}</DialogTitle>
        <DialogDescription>{{ t('assets.depr.reverseHint') }}</DialogDescription>
        <ErrorNotice v-if="formError" :error="formError" inline />
        <FormField :label="t('assets.asset.reason')" required :error="formError?.fieldMessage('reason')"><template #default="{ id }"><Input :id="id" v-model="reason" name="reason" maxlength="500" /></template></FormField>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="reversing = false">{{ t('common.cancel') }}</Button><Button type="submit" variant="default" :disabled="busy || !reason.trim()" data-testid="reverse-confirm">{{ t('assets.depr.reverse') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>
</template>
