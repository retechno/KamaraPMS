<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { RateGrid, RatePlan, RoomType, Weekday } from '@/api/types'
import { ChevronLeft } from 'lucide-vue-next'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { i18n, t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { windowDates } from '@/utils/blocks'
import { addDays } from '@/utils/dates'

const auth = useAuthStore()
const property = usePropertyStore()

const plans = ref<RatePlan[]>([])
const types = ref<RoomType[]>([])
const planId = ref(0)
const grid = ref<RateGrid | null>(null)
const days = ref(14)
const windowStart = ref('')
const error = ref<ApiError | null>(null)
const notice = ref('')
const saving = ref(false)

const KEYS: Weekday[] = ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN']
// A weekday has one name per language: take it from a known Monday (2024-01-01) and the days after it.
const weekdayName = (i: number) => new Date(Date.UTC(2024, 0, 1 + i)).toLocaleDateString(i18n.global.locale.value, { weekday: 'short', timeZone: 'UTC' })
const WEEKDAYS = computed(() => KEYS.map((key, i) => ({ key, label: weekdayName(i) })))
const form = reactive({ from: '', to: '', weekdays: [] as Weekday[], amount: '', typeIds: [] as number[] })

const canManage = computed(() => auth.can('rate.manage', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')
const activeTypes = computed(() => [...types.value].filter((x) => x.is_active).sort((a, b) => a.sort_order - b.sort_order || a.code.localeCompare(b.code)))
const plan = computed(() => plans.value.find((p) => p.id === planId.value))
const dates = computed(() => (windowStart.value ? windowDates(windowStart.value, days.value) : []))
const fieldError = (field: string) => error.value?.fieldMessage(field)

// amount by "typeId/date"
const amounts = computed(() => new Map((grid.value?.rates ?? []).map((r) => [`${r.room_type_id}/${r.stay_date}`, r.amount])))
const cell = (typeId: number, date: string) => amounts.value.get(`${typeId}/${date}`)
const dayLabel = (d: string) => `${d.slice(8)}/${d.slice(5, 7)}`
const weekdayOf = (d: string) => WEEKDAYS.value[(new Date(`${d}T00:00:00Z`).getUTCDay() + 6) % 7]?.label ?? ''

async function loadBase(): Promise<void> {
  const propertyId = property.currentId
  plans.value = []
  types.value = []
  grid.value = null
  planId.value = 0
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    const [p, rt] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { ...path, query: { limit: 200, cursor } } })),
    ])
    plans.value = p.filter((x) => x.occupancy_kind !== 'COMPLIMENTARY' && x.occupancy_kind !== 'HOUSE_USE') // free plans have no rates
    types.value = rt
    planId.value = plans.value.find((x) => x.is_active)?.id ?? plans.value[0]?.id ?? 0
    form.typeIds = activeTypes.value.map((x) => x.id)
    await loadGrid()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadGrid(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !planId.value) return
  if (!windowStart.value) windowStart.value = businessDate.value
  if (!windowStart.value) return // the business date is still loading; the watcher below retries
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/rates', {
      params: { path: { propertyId }, query: { rate_plan_id: planId.value, from: windowStart.value, to: addDays(windowStart.value, days.value) } },
    })
    grid.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function shift(n: number): void {
  windowStart.value = addDays(windowStart.value, n)
  void loadGrid()
}

/** Clicking a night prepares a fill for that room type and night, starting from its current amount. */
function pick(typeId: number, date: string): void {
  if (!canManage.value) return
  form.typeIds = [typeId]
  form.from = date
  form.to = addDays(date, 1)
  form.weekdays = []
  form.amount = cell(typeId, date) ?? ''
  notice.value = ''
  error.value = null
}

async function apply(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  saving.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/rates', {
      params: { path: { propertyId } },
      body: { rate_plan_id: planId.value, room_type_ids: form.typeIds, from: form.from, to: form.to, weekdays: form.weekdays, amount: form.amount },
    })
    notice.value = data ? t('rateGrid.written', { updated: data.updated_nights, created: data.created_nights }) : ''
    await loadGrid()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

function toggle<T>(list: T[], v: T): void {
  const i = list.indexOf(v)
  if (i >= 0) list.splice(i, 1)
  else list.push(v)
}

watch(() => property.currentId, () => {
  windowStart.value = ''
  void loadBase()
}, { immediate: true })
watch(businessDate, (bd) => {
  if (bd && !windowStart.value) void loadGrid()
})
watch(planId, () => void loadGrid())
</script>

<template>
  <PageHeader :title="t('rateGrid.title')">
    <template #actions>
      <Button variant="outline" size="sm" data-testid="week-prev" @click="shift(-7)"><ChevronLeft />{{ t('rateGrid.week') }}</Button>
      <Button variant="outline" size="sm" @click="windowStart = businessDate; loadGrid()">{{ t('rateGrid.businessDate') }}</Button>
      <Button variant="outline" size="sm" data-testid="week-next" @click="shift(7)">{{ t('rateGrid.week') }} &rarr;</Button>
      <NativeSelect v-model.number="days" class="w-28" :aria-label="t('rateGrid.daysShown')" @change="loadGrid">
        <option :value="14">{{ t('rateGrid.days', { n: 14 }) }}</option>
        <option :value="28">{{ t('rateGrid.days', { n: 28 }) }}</option>
      </NativeSelect>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!plans.length" class="muted" data-testid="no-plans">{{ t('rateGrid.noPlans') }}</p>

  <template v-else>
    <FormField class="mb-4 max-w-md" :label="t('rateGrid.ratePlan')">
      <template #default="{ id }">
        <NativeSelect :id="id" v-model.number="planId" name="rate_plan">
          <option v-for="p in plans" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}{{ p.is_active ? '' : ` (${t('setup.inactive').toLowerCase()})` }}</option>
        </NativeSelect>
        <small v-if="plan" class="text-xs text-muted-foreground" data-testid="price-mode">
          {{ plan.price_mode === 'INCLUSIVE' ? t('rateGrid.amountsInclusive', { code: plan.room_charge_code }) : t('rateGrid.amountsExclusive', { code: plan.room_charge_code }) }}
        </small>
      </template>
    </FormField>

    <Card class="mb-4 overflow-x-auto p-2">
      <table class="w-full min-w-[640px] border-collapse text-xs" data-testid="grid">
        <thead>
          <tr>
            <th />
            <th
              v-for="d in dates"
              :key="d"
              :class="cn('border border-border p-1 text-center font-medium leading-tight text-muted-foreground', d === businessDate && 'bg-primary/10 text-primary')"
            >
              {{ weekdayOf(d) }}<br />{{ dayLabel(d) }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="rt in activeTypes" :key="rt.id" :data-testid="`row-${rt.code}`">
            <th scope="row" class="border border-border px-2 py-1 text-left font-semibold">{{ rt.code }}</th>
            <td v-for="d in dates" :key="d" :class="cn('border border-border p-0 text-center', d === businessDate && 'bg-primary/10')">
              <button
                type="button"
                :class="cn('w-full cursor-pointer rounded-none border-0 bg-transparent px-1 py-1.5 tabular-nums hover:bg-accent disabled:cursor-default disabled:hover:bg-transparent', cell(rt.id, d) === undefined && 'text-muted-foreground')"
                :disabled="!canManage"
                :data-testid="`cell-${rt.code}-${d}`"
                @click="pick(rt.id, d)"
              >
                {{ cell(rt.id, d) ?? '—' }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </Card>

    <Card v-if="canManage" class="mb-4">
      <form novalidate data-testid="fill-form" @submit.prevent="apply">
        <CardHeader><CardTitle>{{ t('rateGrid.setRates') }}</CardTitle></CardHeader>
        <CardContent>
          <p v-if="notice" class="mb-3 text-sm text-muted-foreground" role="status" data-testid="notice">{{ notice }}</p>
          <div class="grid gap-4 sm:grid-cols-3">
            <FormField :label="t('rateGrid.from')" :error="fieldError('from')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.from" name="from" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('rateGrid.until')" :error="fieldError('to')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.to" name="to" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('rateGrid.amount')" :error="fieldError('amount')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.amount" name="amount" inputmode="decimal" :aria-invalid="invalid" /></template>
            </FormField>
          </div>
          <fieldset class="mt-4 flex flex-wrap gap-x-4 gap-y-1 rounded-md border border-border px-3 py-2">
            <legend class="px-1 text-sm font-medium">{{ t('rateGrid.roomTypes') }}</legend>
            <label v-for="rt in activeTypes" :key="rt.id" class="flex items-center gap-1.5 text-sm">
              <input type="checkbox" class="size-4 accent-primary" :checked="form.typeIds.includes(rt.id)" :name="`type-${rt.code}`" @change="toggle(form.typeIds, rt.id)" />
              <span>{{ rt.code }}</span>
            </label>
            <small v-if="fieldError('room_type_ids')" role="alert" class="w-full text-xs text-destructive">{{ fieldError('room_type_ids') }}</small>
          </fieldset>
          <fieldset class="mt-3 flex flex-wrap gap-x-4 gap-y-1 rounded-md border border-border px-3 py-2">
            <legend class="px-1 text-sm font-medium">{{ t('rateGrid.onlyDays') }}</legend>
            <label v-for="w in WEEKDAYS" :key="w.key" class="flex items-center gap-1.5 text-sm">
              <input type="checkbox" class="size-4 accent-primary" :checked="form.weekdays.includes(w.key)" :name="`weekday-${w.key}`" @change="toggle(form.weekdays, w.key)" />
              <span>{{ w.label }}</span>
            </label>
            <small v-if="fieldError('weekdays')" role="alert" class="w-full text-xs text-destructive">{{ fieldError('weekdays') }}</small>
          </fieldset>
          <div class="mt-4 flex justify-end"><Button type="submit" :disabled="saving">{{ t('rateGrid.apply') }}</Button></div>
        </CardContent>
      </form>
    </Card>
  </template>
</template>
