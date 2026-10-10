<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { RateQuote, RatePlan, RoomType, YieldRule } from '@/api/types'
import { confirm } from '@/composables/useConfirm'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const rules = ref<YieldRule[]>([])
const plans = ref<RatePlan[]>([])
const types = ref<RoomType[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<YieldRule | 'new' | null>(null)

const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('rate.manage', pid.value))
const fieldError = (field: string) => error.value?.fieldMessage(field)

const DAYS = ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN'] as const
type Day = (typeof DAYS)[number]

const blank = () => ({
  code: '', name: '', rate_plan_id: '' as number | '', room_type_id: '' as number | '', stay_from: '', stay_to: '', weekdays: [] as Day[],
  occupancy_from: '', occupancy_to: '', lead_min: '', lead_max: '', stay_min: '', stay_max: '',
  adjustment_type: 'PERCENT' as 'PERCENT' | 'AMOUNT', adjustment_value: '', floor_amount: '', cap_amount: '', priority: '100', is_active: true,
})
const form = reactive(blank())

const columns = computed<Column<YieldRule>[]>(() => [
  { key: 'priority', label: t('yieldRules.colPriority'), align: 'right' },
  { key: 'rule', label: t('yieldRules.colRule') },
  { key: 'when', label: t('yieldRules.colWhen') },
  { key: 'adjusts', label: t('yieldRules.colAdjusts'), align: 'right' },
  { key: 'status', label: t('setup.status') },
  { key: 'actions', label: '', align: 'right' },
])

const orNull = (s: string): string | null => (s.trim() === '' ? null : s.trim())
const intOrNull = (s: string): number | null => (s.trim() === '' ? null : Number(s))

async function load(): Promise<void> {
  const propertyId = pid.value
  rules.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    const [r, p, rt] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/yield-rules', { params: path }),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { ...path, query: { limit: 200, cursor } } })),
    ])
    rules.value = r.data?.data ?? []
    plans.value = p
    types.value = rt
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

const planCode = (id: number | null) => (id === null ? t('yieldRules.allPlansShort') : (plans.value.find((p) => p.id === id)?.code ?? t('yieldRules.planN', { id })))
const typeCode = (id: number | null) => (id === null ? t('yieldRules.allTypesShort') : (types.value.find((x) => x.id === id)?.code ?? t('yieldRules.typeN', { id })))

// The conditions of a rule in a line a person can read.
function conditions(r: YieldRule): string {
  const parts: string[] = [`${planCode(r.rate_plan_id)}, ${typeCode(r.room_type_id)}`]
  if (r.stay_from || r.stay_to) parts.push(t('yieldRules.condStay', { from: r.stay_from ?? '…', to: r.stay_to ?? '…' }))
  if (r.weekdays?.length) parts.push(r.weekdays.join(' '))
  if (r.occupancy_from || r.occupancy_to) parts.push(t('yieldRules.condOccupancy', { from: r.occupancy_from ?? '0', to: r.occupancy_to ?? '100' }))
  if (r.lead_days_min !== null || r.lead_days_max !== null) parts.push(t('yieldRules.condLead', { from: r.lead_days_min ?? 0, to: r.lead_days_max ?? '…' }))
  if (r.stay_nights_min !== null || r.stay_nights_max !== null) parts.push(t('yieldRules.condNights', { from: r.stay_nights_min ?? 1, to: r.stay_nights_max ?? '…' }))
  return parts.join(' · ')
}

const adjustment = (r: YieldRule): string => {
  const v = Number(r.adjustment_value)
  return `${v > 0 ? '+' : ''}${r.adjustment_value}${r.adjustment_type === 'PERCENT' ? '%' : ''}`
}

function startNew(): void {
  Object.assign(form, blank())
  error.value = null
  editing.value = 'new'
}

function startEdit(r: YieldRule): void {
  Object.assign(form, blank(), {
    code: r.code, name: r.name, rate_plan_id: r.rate_plan_id ?? '', room_type_id: r.room_type_id ?? '', stay_from: r.stay_from ?? '', stay_to: r.stay_to ?? '',
    weekdays: [...(r.weekdays ?? [])], occupancy_from: r.occupancy_from ?? '', occupancy_to: r.occupancy_to ?? '',
    lead_min: r.lead_days_min?.toString() ?? '', lead_max: r.lead_days_max?.toString() ?? '', stay_min: r.stay_nights_min?.toString() ?? '', stay_max: r.stay_nights_max?.toString() ?? '',
    adjustment_type: r.adjustment_type, adjustment_value: r.adjustment_value, floor_amount: r.floor_amount ?? '', cap_amount: r.cap_amount ?? '',
    priority: String(r.priority), is_active: r.is_active,
  })
  error.value = null
  editing.value = r
}

function body(isActive = form.is_active) {
  return {
    code: form.code, name: form.name, rate_plan_id: form.rate_plan_id === '' ? null : Number(form.rate_plan_id),
    room_type_id: form.room_type_id === '' ? null : Number(form.room_type_id), stay_from: orNull(form.stay_from), stay_to: orNull(form.stay_to),
    weekdays: form.weekdays.length ? form.weekdays : null, occupancy_from: orNull(form.occupancy_from), occupancy_to: orNull(form.occupancy_to),
    lead_days_min: intOrNull(form.lead_min), lead_days_max: intOrNull(form.lead_max), stay_nights_min: intOrNull(form.stay_min), stay_nights_max: intOrNull(form.stay_max),
    adjustment_type: form.adjustment_type, adjustment_value: form.adjustment_value, floor_amount: orNull(form.floor_amount), cap_amount: orNull(form.cap_amount),
    priority: Number(form.priority), is_active: isActive,
  }
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/yield-rules', { params: { path: { propertyId } }, body: body() })
    } else {
      await api.PUT('/api/v1/properties/{propertyId}/yield-rules/{id}', { params: { path: { propertyId, id: editing.value.id } }, body: body() })
    }
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

async function toggle(r: YieldRule): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  startEdit(r)
  error.value = null
  try {
    await api.PUT('/api/v1/properties/{propertyId}/yield-rules/{id}', { params: { path: { propertyId, id: r.id } }, body: body(!r.is_active) })
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function remove(r: YieldRule): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !(await confirm({ title: t('yieldRules.deleteTitle', { code: r.code }), description: t('yieldRules.deleteHint'), destructive: true }))) return
  error.value = null
  try {
    await api.DELETE('/api/v1/properties/{propertyId}/yield-rules/{id}', { params: { path: { propertyId, id: r.id } } })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

// ---- price check: what a stay costs today, rule by rule
const check = reactive({ plan: '' as number | '', type: '' as number | '', arrival: '', departure: '' })
const quote = ref<RateQuote | null>(null)
const quoteError = ref<ApiError | null>(null)
const quoting = ref(false)

async function runQuote(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || check.plan === '' || check.type === '') return
  quoting.value = true
  quoteError.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/rate-quotes', {
      params: { path: { propertyId }, query: { rate_plan_id: Number(check.plan), room_type_id: Number(check.type), arrival_date: check.arrival, departure_date: check.departure } },
    })
    quote.value = data ?? null
  } catch (e) {
    quote.value = null
    quoteError.value = e instanceof ApiError ? e : null
  } finally {
    quoting.value = false
  }
}

watch(pid, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('yieldRules.title')" :description="t('yieldRules.intro')">
    <template #actions>
      <Button v-if="canManage && !editing" type="button" data-testid="new-rule" @click="startNew">{{ t('yieldRules.newRule') }}</Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canManage" class="muted" data-testid="read-only">{{ t('yieldRules.readOnly', { permission: 'rate.manage' }) }}</p>

  <Card v-if="editing" class="mb-4">
    <form v-autofocus="editing" novalidate data-testid="rule-form" @submit.prevent="save">
      <CardHeader><CardTitle>{{ editing === 'new' ? t('yieldRules.formNew') : t('yieldRules.formEdit', { code: form.code }) }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-3">
          <FormField :label="t('yieldRules.code')" :error="fieldError('code')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('yieldRules.name')" :error="fieldError('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('yieldRules.priority')" :hint="t('yieldRules.priorityHint')" :error="fieldError('priority')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.priority" name="priority" inputmode="numeric" :aria-invalid="invalid" /></template>
          </FormField>
        </div>

        <h3 class="mb-2 mt-5 text-sm font-semibold">{{ t('yieldRules.when') }}</h3>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <FormField :label="t('yieldRules.ratePlan')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.rate_plan_id" name="rate_plan_id">
                <option value="">{{ t('yieldRules.allPlans') }}</option>
                <option v-for="p in plans" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('yieldRules.roomType')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.room_type_id" name="room_type_id">
                <option value="">{{ t('yieldRules.allTypes') }}</option>
                <option v-for="rt in types" :key="rt.id" :value="rt.id">{{ rt.code }} · {{ rt.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('yieldRules.stayFrom')">
            <template #default="{ id }"><Input :id="id" v-model="form.stay_from" name="stay_from" type="date" /></template>
          </FormField>
          <FormField :label="t('yieldRules.stayTo')" :error="fieldError('stay_to')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.stay_to" name="stay_to" type="date" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('yieldRules.occFrom')" :hint="t('yieldRules.occFromHint')" :error="fieldError('occupancy_from')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.occupancy_from" name="occupancy_from" inputmode="decimal" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('yieldRules.occTo')" :hint="t('yieldRules.occToHint')" :error="fieldError('occupancy_to')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.occupancy_to" name="occupancy_to" inputmode="decimal" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('yieldRules.leadMin')">
            <template #default="{ id }"><Input :id="id" v-model="form.lead_min" name="lead_min" inputmode="numeric" /></template>
          </FormField>
          <FormField :label="t('yieldRules.leadMax')" :error="fieldError('lead_days_max')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.lead_max" name="lead_max" inputmode="numeric" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('yieldRules.stayMin')">
            <template #default="{ id }"><Input :id="id" v-model="form.stay_min" name="stay_min" inputmode="numeric" /></template>
          </FormField>
          <FormField :label="t('yieldRules.stayMax')">
            <template #default="{ id }"><Input :id="id" v-model="form.stay_max" name="stay_max" inputmode="numeric" /></template>
          </FormField>
        </div>
        <fieldset class="mt-3 flex flex-wrap gap-x-4 gap-y-1 rounded-md border border-border px-3 py-2" data-testid="weekdays">
          <legend class="px-1 text-sm font-medium">{{ t('yieldRules.weekdays') }}</legend>
          <label v-for="d in DAYS" :key="d" class="flex items-center gap-1.5 text-sm">
            <input v-model="form.weekdays" type="checkbox" name="weekdays" :value="d" class="size-4 accent-primary" /><span>{{ d }}</span>
          </label>
        </fieldset>

        <h3 class="mb-2 mt-5 text-sm font-semibold">{{ t('yieldRules.then') }}</h3>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <FormField :label="t('yieldRules.adjustBy')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.adjustment_type" name="adjustment_type">
                <option value="PERCENT">{{ t('yieldRules.percent') }}</option>
                <option value="AMOUNT">{{ t('yieldRules.amountType') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('yieldRules.value')" :error="fieldError('adjustment_value')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.adjustment_value" name="adjustment_value" inputmode="decimal" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('yieldRules.floor')" :error="fieldError('floor_amount')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.floor_amount" name="floor_amount" inputmode="decimal" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('yieldRules.cap')" :error="fieldError('cap_amount')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.cap_amount" name="cap_amount" inputmode="decimal" :aria-invalid="invalid" /></template>
          </FormField>
        </div>
        <label class="mt-4 flex items-center gap-2 text-sm">
          <input v-model="form.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" /><span>{{ t('yieldRules.activeCheck') }}</span>
        </label>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="editing = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="saving">{{ saving ? t('yieldRules.saving') : t('common.save') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>

  <Card class="mb-4">
    <EmptyState v-if="loaded && !rules.length" :description="t('emptyState.yieldRules')" :action-label="canManage && !editing ? t('yieldRules.newRule') : ''" @action="startNew" :title="t('yieldRules.empty')" data-testid="empty" />
    <DataTable
      v-else-if="rules.length"
      :columns="columns"
      :rows="rules"
      row-key="id"
      :row-test-id="(r) => `rule-${r.code}`"
      :row-class="(r) => (r.is_active ? undefined : 'opacity-55')"
      :caption="t('yieldRules.title')"
      data-testid="rules"
    >
      <template #cell-priority="{ row }">{{ row.priority }}</template>
      <template #cell-rule="{ row }"><b>{{ row.code }}</b> <small class="text-muted-foreground">{{ row.name }}</small></template>
      <template #cell-when="{ row }"><small>{{ conditions(row) }}</small></template>
      <template #cell-adjusts="{ row }">
        <b>{{ adjustment(row) }}</b><small v-if="row.floor_amount || row.cap_amount" class="text-muted-foreground"> ({{ row.floor_amount ?? '–' }} to {{ row.cap_amount ?? '–' }})</small>
      </template>
      <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('yieldRules.on') : t('yieldRules.off') }}</Badge></template>
      <template #cell-actions="{ row }">
        <div v-if="canManage" class="flex justify-end gap-1.5">
          <Button type="button" variant="outline" size="sm" :data-testid="`edit-${row.code}`" @click="startEdit(row)">{{ t('common.edit') }}</Button>
          <Button type="button" variant="outline" size="sm" :data-testid="`toggle-${row.code}`" @click="toggle(row)">{{ row.is_active ? t('yieldRules.turnOff') : t('yieldRules.turnOn') }}</Button>
          <Button type="button" variant="outline" size="sm" :data-testid="`delete-${row.code}`" @click="remove(row)">{{ t('common.delete') }}</Button>
        </div>
      </template>
    </DataTable>
  </Card>

  <Card>
    <form novalidate data-testid="quote-form" @submit.prevent="runQuote">
      <CardHeader>
        <CardTitle>{{ t('yieldRules.checkTitle') }}</CardTitle>
        <p class="m-0 text-sm text-muted-foreground">{{ t('yieldRules.checkHint') }}</p>
      </CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <FormField :label="t('yieldRules.ratePlan')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="check.plan" name="quote_plan"><option v-for="p in plans" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option></NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('yieldRules.roomType')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="check.type" name="quote_type"><option v-for="rt in types" :key="rt.id" :value="rt.id">{{ rt.code }} · {{ rt.name }}</option></NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('yieldRules.arrival')">
            <template #default="{ id }"><Input :id="id" v-model="check.arrival" name="quote_arrival" type="date" /></template>
          </FormField>
          <FormField :label="t('yieldRules.departure')">
            <template #default="{ id }"><Input :id="id" v-model="check.departure" name="quote_departure" type="date" /></template>
          </FormField>
        </div>
        <div class="mt-4 flex justify-end">
          <Button type="submit" :disabled="quoting || check.plan === '' || check.type === ''" data-testid="quote-run">{{ t('yieldRules.checkRun') }}</Button>
        </div>
        <ErrorNotice v-if="quoteError" :error="quoteError" inline class="mt-3" data-testid="quote-error" />
        <template v-if="quote">
          <table class="mt-4 w-full border-collapse text-sm" data-testid="quote">
            <thead>
              <tr class="border-b border-border text-left text-xs text-muted-foreground">
                <th class="py-1.5 pr-3 font-medium">{{ t('yieldRules.night') }}</th>
                <th class="px-3 text-right font-medium">{{ t('yieldRules.grid') }}</th>
                <th class="px-3 text-right font-medium">{{ t('yieldRules.full') }}</th>
                <th class="px-3 font-medium">{{ t('yieldRules.rules') }}</th>
                <th class="pl-3 text-right font-medium">{{ t('yieldRules.price') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="n in quote.nights" :key="n.date" class="border-b border-border">
                <td class="py-1.5 pr-3">{{ $date(n.date) }}</td>
                <td class="px-3 text-right tabular-nums">{{ n.grid_rate ? $money(n.grid_rate) : t('yieldRules.notSet') }}</td>
                <td class="px-3 text-right tabular-nums">{{ n.occupancy_percent }}%</td>
                <td class="px-3">
                  <small v-for="s in n.steps" :key="s.code" class="block">{{ s.code }} {{ $money(s.before) }} → {{ $money(s.after) }}</small>
                  <small v-if="!n.steps.length" class="text-muted-foreground">{{ t('yieldRules.none') }}</small>
                </td>
                <td class="pl-3 text-right tabular-nums"><b>{{ n.amount ? $money(n.amount) : '–' }}</b></td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <td colspan="4" class="pt-2">{{ quote.price_mode === 'INCLUSIVE' ? t('yieldRules.totalInclusive', { grid: $money(quote.grid_total) }) : t('yieldRules.totalExclusive', { grid: $money(quote.grid_total) }) }}</td>
                <td class="pl-3 pt-2 text-right tabular-nums"><b data-testid="quote-total">{{ $money(quote.total) }}</b></td>
              </tr>
            </tfoot>
          </table>
          <p v-if="quote.missing_nights" class="mt-2 text-sm text-muted-foreground">{{ t('yieldRules.missing', { n: quote.missing_nights }) }}</p>
        </template>
      </CardContent>
    </form>
  </Card>
</template>
