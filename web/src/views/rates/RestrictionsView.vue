<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { EffectiveRestriction, FillRateRestrictionsRequest, RatePlan, RoomType, Weekday } from '@/api/types'
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

// The sales restriction grid: what each night is under for a room type and a rate plan (the effective view, resolved by the server), and a fill that sets or clears
// stop sell, closed to arrival, closed to departure, and the minimum and maximum stay. The precedence between rows is the server's; this screen never resolves it.

const auth = useAuthStore()
const property = usePropertyStore()

const plans = ref<RatePlan[]>([])
const types = ref<RoomType[]>([])
const planId = ref(0)
const effective = ref<Record<number, Map<string, EffectiveRestriction>>>({})
const days = ref(14)
const windowStart = ref('')
const error = ref<ApiError | null>(null)
const notice = ref('')
const saving = ref(false)

const KEYS: Weekday[] = ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN']
const weekdayName = (i: number) => new Date(Date.UTC(2024, 0, 1 + i)).toLocaleDateString(i18n.global.locale.value, { weekday: 'short', timeZone: 'UTC' })
const WEEKDAYS = computed(() => KEYS.map((key, i) => ({ key, label: weekdayName(i) })))

type Choice = '' | 'yes' | 'no' | 'clear'
const blank = () => ({
  from: '', to: '', weekdays: [] as Weekday[], typeIds: [] as number[], planIds: [] as number[],
  stopSell: '' as Choice, cta: '' as Choice, ctd: '' as Choice, minStay: '', maxStay: '', clearMin: false, clearMax: false, note: '', clearNote: false,
})
const form = reactive(blank())

const canManage = computed(() => auth.can('rate.manage', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')
const activeTypes = computed(() => [...types.value].filter((x) => x.is_active).sort((a, b) => a.sort_order - b.sort_order || a.code.localeCompare(b.code)))
const dates = computed(() => (windowStart.value ? windowDates(windowStart.value, days.value) : []))
const fieldError = (field: string) => error.value?.fieldMessage(field)
const dayLabel = (d: string) => `${d.slice(8)}/${d.slice(5, 7)}`
const weekdayOf = (d: string) => WEEKDAYS.value[(new Date(`${d}T00:00:00Z`).getUTCDay() + 6) % 7]?.label ?? ''

const day = (typeId: number, date: string): EffectiveRestriction | undefined => effective.value[typeId]?.get(date)

interface Mark {
  key: string
  label: string
  tone: string
}
/** The marks of a night: one small label for each restriction that is on. */
function marks(typeId: number, date: string): Mark[] {
  const e = day(typeId, date)
  if (!e) return []
  const out: Mark[] = []
  if (e.stop_sell) out.push({ key: 'stop_sell', label: t('restrictions.markStopSell'), tone: 'bg-destructive/15 text-destructive' })
  if (e.closed_to_arrival) out.push({ key: 'closed_to_arrival', label: t('restrictions.markCta'), tone: 'bg-warning/25' })
  if (e.closed_to_departure) out.push({ key: 'closed_to_departure', label: t('restrictions.markCtd'), tone: 'bg-warning/25' })
  if (e.min_stay != null) out.push({ key: 'min_stay', label: `≥${e.min_stay}`, tone: 'bg-muted' })
  if (e.max_stay != null) out.push({ key: 'max_stay', label: `≤${e.max_stay}`, tone: 'bg-muted' })
  return out
}

/** Where each mark of a night comes from, for the tooltip. */
function title(typeId: number, date: string): string {
  const e = day(typeId, date)
  if (!e) return date
  const parts = Object.entries(e.sources).map(([attr, s]) => `${t(`restrictions.attr_${attr}`)}: ${t(`restrictions.scope_${s.scope}`)}`)
  return parts.length ? `${date} · ${parts.join(' · ')}` : date
}

async function loadBase(): Promise<void> {
  const propertyId = property.currentId
  plans.value = []
  types.value = []
  effective.value = {}
  planId.value = 0
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    const [p, rt] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { ...path, query: { limit: 200, cursor } } })),
    ])
    plans.value = p
    types.value = rt
    planId.value = plans.value.find((x) => x.is_active)?.id ?? plans.value[0]?.id ?? 0 // the watcher of the plan reads the grid
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadGrid(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !planId.value) return
  if (!windowStart.value) windowStart.value = businessDate.value
  if (!windowStart.value) return // the business date is still loading; the watcher below retries
  const from = windowStart.value
  const to = addDays(from, days.value)
  try {
    const results = await Promise.all(activeTypes.value.map(async (rt) => {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/rate-restrictions/effective', {
        params: { path: { propertyId }, query: { room_type_id: rt.id, rate_plan_id: planId.value, from, to } },
      })
      return [rt.id, new Map((data?.data ?? []).map((e) => [e.date, e]))] as const
    }))
    effective.value = Object.fromEntries(results)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function shift(n: number): void {
  windowStart.value = addDays(windowStart.value, n)
  void loadGrid()
}

/** Clicking a night prepares a fill for that room type and night, for every rate plan (take plans off the list to narrow it). */
function pick(typeId: number, date: string): void {
  if (!canManage.value) return
  Object.assign(form, blank(), { typeIds: [typeId], from: date, to: addDays(date, 1) })
  notice.value = ''
  error.value = null
}

function toggle<T>(list: T[], v: T): void {
  const i = list.indexOf(v)
  if (i >= 0) list.splice(i, 1)
  else list.push(v)
}

type Attr = NonNullable<FillRateRestrictionsRequest['clear']>[number]

/** The request body of the fill as the form stands: a choice of yes or no sets, clear clears, empty leaves the attribute alone. */
function body(): FillRateRestrictionsRequest {
  const set: FillRateRestrictionsRequest['set'] = {}
  const clear: Attr[] = []
  const flag = (name: 'stop_sell' | 'closed_to_arrival' | 'closed_to_departure', c: Choice) => {
    if (c === 'yes') set[name] = true
    else if (c === 'no') set[name] = false
    else if (c === 'clear') clear.push(name)
  }
  flag('stop_sell', form.stopSell)
  flag('closed_to_arrival', form.cta)
  flag('closed_to_departure', form.ctd)
  if (form.clearMin) clear.push('min_stay')
  else if (form.minStay.trim() !== '') set.min_stay = Number(form.minStay)
  if (form.clearMax) clear.push('max_stay')
  else if (form.maxStay.trim() !== '') set.max_stay = Number(form.maxStay)
  if (form.clearNote) clear.push('note')
  else if (form.note.trim() !== '') set.note = form.note.trim()
  return {
    room_type_ids: form.typeIds.length ? form.typeIds : undefined,
    rate_plan_ids: form.planIds.length ? form.planIds : undefined,
    from: form.from, to: form.to, weekdays: form.weekdays, set, clear,
  }
}

async function apply(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  saving.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/rate-restrictions', { params: { path: { propertyId } }, body: body() })
    notice.value = data ? t('restrictions.written', { created: data.created, updated: data.updated, deleted: data.deleted }) : ''
    await loadGrid()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
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
  <PageHeader :title="t('restrictions.title')" :description="t('restrictions.intro')">
    <template #actions>
      <Button variant="outline" size="sm" data-testid="week-prev" @click="shift(-7)"><ChevronLeft />{{ t('restrictions.week') }}</Button>
      <Button variant="outline" size="sm" @click="windowStart = businessDate; loadGrid()">{{ t('restrictions.businessDate') }}</Button>
      <Button variant="outline" size="sm" data-testid="week-next" @click="shift(7)">{{ t('restrictions.week') }} &rarr;</Button>
      <NativeSelect v-model.number="days" class="w-28" :aria-label="t('restrictions.daysShown')" @change="loadGrid">
        <option :value="14">{{ t('restrictions.days', { n: 14 }) }}</option>
        <option :value="28">{{ t('restrictions.days', { n: 28 }) }}</option>
      </NativeSelect>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!plans.length" class="muted" data-testid="no-plans">{{ t('restrictions.noPlans') }}</p>

  <template v-else>
    <p v-if="!canManage" class="muted" data-testid="read-only">{{ t('restrictions.readOnly', { permission: 'rate.manage' }) }}</p>
    <FormField class="mb-4 max-w-md" :label="t('restrictions.ratePlan')">
      <template #default="{ id }">
        <NativeSelect :id="id" v-model.number="planId" name="rate_plan">
          <option v-for="p in plans" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
        </NativeSelect>
        <small class="text-xs text-muted-foreground">{{ t('restrictions.planHint') }}</small>
      </template>
    </FormField>

    <Card class="mb-2 overflow-x-auto p-2">
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
                class="flex min-h-9 w-full cursor-pointer flex-wrap items-center justify-center gap-0.5 rounded-none border-0 bg-transparent px-0.5 py-1 hover:bg-accent disabled:cursor-default disabled:hover:bg-transparent"
                :disabled="!canManage"
                :title="title(rt.id, d)"
                :data-testid="`cell-${rt.code}-${d}`"
                @click="pick(rt.id, d)"
              >
                <span v-for="m in marks(rt.id, d)" :key="m.key" :class="cn('rounded px-1 text-[10px] font-medium leading-4', m.tone)" :data-testid="`mark-${m.key}`">{{ m.label }}</span>
                <span v-if="!marks(rt.id, d).length" class="text-muted-foreground">·</span>
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </Card>
    <p class="mb-4 text-xs text-muted-foreground" data-testid="legend">{{ t('restrictions.legend') }}</p>

    <Card v-if="canManage" class="mb-4">
      <form novalidate data-testid="fill-form" @submit.prevent="apply">
        <CardHeader><CardTitle>{{ t('restrictions.setTitle') }}</CardTitle></CardHeader>
        <CardContent>
          <p v-if="notice" class="mb-3 text-sm text-muted-foreground" role="status" data-testid="notice">{{ notice }}</p>
          <div class="grid gap-4 sm:grid-cols-2">
            <FormField :label="t('restrictions.from')" :error="fieldError('from')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.from" name="from" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('restrictions.until')" :error="fieldError('to')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.to" name="to" type="date" :aria-invalid="invalid" /></template>
            </FormField>
          </div>
          <fieldset class="mt-4 flex flex-wrap gap-x-4 gap-y-1 rounded-md border border-border px-3 py-2">
            <legend class="px-1 text-sm font-medium">{{ t('restrictions.roomTypes') }}</legend>
            <label v-for="rt in activeTypes" :key="rt.id" class="flex items-center gap-1.5 text-sm">
              <input type="checkbox" class="size-4 accent-primary" :checked="form.typeIds.includes(rt.id)" :name="`type-${rt.code}`" @change="toggle(form.typeIds, rt.id)" />
              <span>{{ rt.code }}</span>
            </label>
            <small class="w-full text-xs text-muted-foreground">{{ t('restrictions.noneIsAll') }}</small>
            <small v-if="fieldError('room_type_ids')" role="alert" class="w-full text-xs text-destructive">{{ fieldError('room_type_ids') }}</small>
          </fieldset>
          <fieldset class="mt-3 flex flex-wrap gap-x-4 gap-y-1 rounded-md border border-border px-3 py-2">
            <legend class="px-1 text-sm font-medium">{{ t('restrictions.ratePlans') }}</legend>
            <label v-for="p in plans" :key="p.id" class="flex items-center gap-1.5 text-sm">
              <input type="checkbox" class="size-4 accent-primary" :checked="form.planIds.includes(p.id)" :name="`plan-${p.code}`" @change="toggle(form.planIds, p.id)" />
              <span>{{ p.code }}</span>
            </label>
            <small class="w-full text-xs text-muted-foreground">{{ t('restrictions.noneIsAllPlans') }}</small>
          </fieldset>
          <fieldset class="mt-3 flex flex-wrap gap-x-4 gap-y-1 rounded-md border border-border px-3 py-2">
            <legend class="px-1 text-sm font-medium">{{ t('restrictions.onlyDays') }}</legend>
            <label v-for="w in WEEKDAYS" :key="w.key" class="flex items-center gap-1.5 text-sm">
              <input type="checkbox" class="size-4 accent-primary" :checked="form.weekdays.includes(w.key)" :name="`weekday-${w.key}`" @change="toggle(form.weekdays, w.key)" />
              <span>{{ w.label }}</span>
            </label>
            <small v-if="fieldError('weekdays')" role="alert" class="w-full text-xs text-destructive">{{ fieldError('weekdays') }}</small>
          </fieldset>

          <h3 class="mb-2 mt-5 text-sm font-semibold">{{ t('restrictions.what') }}</h3>
          <div class="grid gap-4 sm:grid-cols-3">
            <FormField :label="t('restrictions.stopSell')" :hint="t('restrictions.stopSellHint')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.stopSell" name="stop_sell">
                  <option value="">{{ t('restrictions.noChange') }}</option>
                  <option value="yes">{{ t('restrictions.closed') }}</option>
                  <option value="no">{{ t('restrictions.open') }}</option>
                  <option value="clear">{{ t('restrictions.clear') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('restrictions.closedToArrival')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.cta" name="closed_to_arrival">
                  <option value="">{{ t('restrictions.noChange') }}</option>
                  <option value="yes">{{ t('restrictions.closed') }}</option>
                  <option value="no">{{ t('restrictions.open') }}</option>
                  <option value="clear">{{ t('restrictions.clear') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('restrictions.closedToDeparture')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.ctd" name="closed_to_departure">
                  <option value="">{{ t('restrictions.noChange') }}</option>
                  <option value="yes">{{ t('restrictions.closed') }}</option>
                  <option value="no">{{ t('restrictions.open') }}</option>
                  <option value="clear">{{ t('restrictions.clear') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('restrictions.minStay')" :hint="t('restrictions.stayHint')" :error="fieldError('set.min_stay')">
              <template #default="{ id, invalid }">
                <Input :id="id" v-model="form.minStay" name="min_stay" inputmode="numeric" :disabled="form.clearMin" :aria-invalid="invalid" />
                <label class="mt-1 flex items-center gap-1.5 text-xs"><input v-model="form.clearMin" type="checkbox" name="clear_min_stay" class="size-4 accent-primary" /><span>{{ t('restrictions.clear') }}</span></label>
              </template>
            </FormField>
            <FormField :label="t('restrictions.maxStay')" :error="fieldError('set.max_stay')">
              <template #default="{ id, invalid }">
                <Input :id="id" v-model="form.maxStay" name="max_stay" inputmode="numeric" :disabled="form.clearMax" :aria-invalid="invalid" />
                <label class="mt-1 flex items-center gap-1.5 text-xs"><input v-model="form.clearMax" type="checkbox" name="clear_max_stay" class="size-4 accent-primary" /><span>{{ t('restrictions.clear') }}</span></label>
              </template>
            </FormField>
            <FormField :label="t('restrictions.note')" :error="fieldError('set.note')">
              <template #default="{ id, invalid }">
                <Input :id="id" v-model="form.note" name="note" :disabled="form.clearNote" :aria-invalid="invalid" />
                <label class="mt-1 flex items-center gap-1.5 text-xs"><input v-model="form.clearNote" type="checkbox" name="clear_note" class="size-4 accent-primary" /><span>{{ t('restrictions.clear') }}</span></label>
              </template>
            </FormField>
          </div>
          <small v-if="fieldError('set')" role="alert" class="mt-2 block text-xs text-destructive">{{ fieldError('set') }}</small>
          <div class="mt-4 flex justify-end"><Button type="submit" :disabled="saving" data-testid="apply">{{ t('restrictions.apply') }}</Button></div>
        </CardContent>
      </form>
    </Card>
  </template>
</template>
