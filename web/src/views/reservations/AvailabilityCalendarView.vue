<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { ChevronLeft, SlidersHorizontal } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AvailabilityCalendar } from '@/api/types'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { i18n, t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { windowDates } from '@/utils/blocks'
import { addDays } from '@/utils/dates'

interface Night {
  date: string
  sellable: number
  blocked: number
  held: number
  available: number
  occupancy_percent: string
  in_house: number
  arrivals: number
  reservations: number
  complimentary: number
  house_use: number
  locked?: number
  bed_limited?: boolean
  restrictions?: string[]
  stop_sell_all?: boolean
}

/** What a cell can show. `available` alone is the compact view; more than one makes it the detailed view. */
const METRICS = ['available', 'held', 'in_house', 'arrivals', 'reservations', 'complimentary', 'house_use', 'blocked', 'occupancy_percent'] as const
type Metric = (typeof METRICS)[number]

/** One row group: a room type, one of its bed types, or the property total. */
interface Group {
  key: string
  code: string
  title: string
  sub: string
  level: 'type' | 'bed' | 'total'
  nights: Night[]
}

const STORAGE_KEY = 'availability-calendar-metrics'

const auth = useAuthStore()
const property = usePropertyStore()

const days = ref(14)
const start = ref('')
const calendar = ref<AvailabilityCalendar | null>(null)
const error = ref<ApiError | null>(null)
const byBed = ref(false) // one row per bed type under each room type
const metrics = ref<Metric[]>(readMetrics())

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')
const dates = computed(() => (start.value ? windowDates(start.value, days.value) : []))
const dayLabel = (d: string) => `${d.slice(8)}/${d.slice(5, 7)}`
// A calendar date has one weekday whatever the time zone: read it in UTC, in the language of the page.
const weekday = (d: string) => new Date(`${d}T00:00:00Z`).toLocaleDateString(i18n.global.locale.value, { weekday: 'short', timeZone: 'UTC' })
const isWeekend = (d: string) => [0, 6].includes(new Date(`${d}T00:00:00Z`).getUTCDay())

function readMetrics(): Metric[] {
  try {
    const saved = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '[]') as unknown
    const ok = Array.isArray(saved) ? METRICS.filter((m) => saved.includes(m)) : []
    if (ok.length) return ok
  } catch {
    // storage unavailable or damaged: use the compact view
  }
  return ['available']
}

function toggleMetric(m: Metric, on: boolean): void {
  const next = METRICS.filter((x) => (x === m ? on : metrics.value.includes(x)))
  if (!next.length) return // at least one metric stays on
  metrics.value = next
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  } catch {
    // a per-viewer convenience only
  }
}

const detailed = computed(() => metrics.value.length > 1)

const groups = computed<Group[]>(() => {
  const c = calendar.value
  if (!c) return []
  const out: Group[] = []
  for (const ty of c.room_types) {
    out.push({ key: ty.code, code: ty.code, title: ty.code, sub: `${ty.name} · ${t('availabilityCalendar.rooms', { n: ty.rooms_total })}`, level: 'type', nights: ty.nights })
    if (byBed.value) {
      for (const b of ty.beds ?? []) {
        out.push({ key: `${ty.code}-${b.code}`, code: `${ty.code}-${b.code}`, title: b.name, sub: t('availabilityCalendar.rooms', { n: b.rooms_total }), level: 'bed', nights: b.nights })
      }
    }
  }
  out.push({ key: 'total', code: 'total', title: t('availabilityCalendar.total'), sub: '', level: 'total', nights: c.totals })
  return out
})

const metricLabel = (m: Metric) => t(`availabilityCalendar.metrics.${m}`)

function value(n: Night, m: Metric): string {
  return m === 'occupancy_percent' ? `${n.occupancy_percent}%` : String(n[m])
}

function tooltip(n: Night): string {
  const base = t('availabilityCalendar.cellTitle', { date: n.date, sellable: n.sellable, held: n.held, blocked: n.blocked, percent: n.occupancy_percent })
  const kept = n.locked ? ` ${t('availabilityCalendar.keptTitle', { n: n.locked })}` : ''
  const marks = n.restrictions?.length ? ` ${t(n.stop_sell_all ? 'availabilityCalendar.restrictedAll' : 'availabilityCalendar.restrictedSome', { list: n.restrictions.map((r) => t(`restrictions.badge_${r}` as never)).join(', ') })}` : ''
  return base + kept + (n.bed_limited ? ` ${t('availabilityCalendar.bedLimited')}` : '') + marks
}

const MARKS: Record<string, string> = { STOP_SELL: 'markStopSell', CLOSED_TO_ARRIVAL: 'markCta', CLOSED_TO_DEPARTURE: 'markCtd', MIN_STAY: 'markMin', MAX_STAY: 'markMax' }
/** The marks of a night (sales restrictions), as the grid of restrictions writes them. They say nothing about stock. */
const marksOf = (n: Night): string => (n.restrictions ?? []).map((r) => t(`restrictions.${MARKS[r]}` as never)).join(' ')

/** Colour of an `available` cell: oversold is red, a full type is amber, the rest is neutral. */
function tone(n: Night, m: Metric): string {
  if (m !== 'available') return ''
  if (n.available < 0) return 'bg-destructive/15 font-semibold text-destructive'
  if (n.bed_limited) return 'bg-warning/30 font-semibold' // the bed is sold out although the room type is not
  if (n.available === 0) return 'bg-warning/20 font-medium'
  return ''
}

function cellId(g: Group, m: Metric, d: string): string {
  const prefix = g.level === 'total' ? 'totals' : `cell-${g.code}`
  return detailed.value ? `${prefix}-${m}-${d}` : `${prefix}-${d}`
}

async function load(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  if (!start.value) start.value = businessDate.value
  if (!start.value) return // the business date is still loading; the watcher below retries
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/calendar', {
      params: { path: { propertyId }, query: { from: start.value, to: addDays(start.value, days.value), by_bed: byBed.value || undefined } },
    })
    calendar.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function shift(n: number): void {
  start.value = addDays(start.value, n)
  void load()
}

watch(() => property.currentId, () => {
  start.value = ''
  calendar.value = null
  void load()
}, { immediate: true })
watch(businessDate, () => {
  if (!calendar.value) void load()
})
</script>

<template>
  <PageHeader :title="t('availabilityCalendar.title')">
    <template #actions>
      <Button variant="outline" size="sm" @click="shift(-7)"><ChevronLeft />{{ t('availabilityCalendar.week') }}</Button>
      <Button variant="outline" size="sm" @click="start = businessDate; load()">{{ t('availabilityCalendar.businessDate') }}</Button>
      <Button variant="outline" size="sm" @click="shift(7)">{{ t('availabilityCalendar.week') }} &rarr;</Button>
      <label class="flex items-center gap-2 text-sm"><input v-model="byBed" name="by_bed" type="checkbox" class="size-4 accent-primary" @change="load" /><span>{{ t('availabilityCalendar.showBeds') }}</span></label>
      <Popover>
        <PopoverTrigger as-child>
          <Button variant="outline" size="sm" data-testid="metrics-trigger"><SlidersHorizontal />{{ t('availabilityCalendar.show') }} ({{ metrics.length }})</Button>
        </PopoverTrigger>
        <PopoverContent data-testid="metrics-popover">
          <p class="m-0 mb-2 text-xs font-medium text-muted-foreground">{{ t('availabilityCalendar.showHint') }}</p>
          <label v-for="m in METRICS" :key="m" class="flex items-center gap-2 py-1">
            <input
              type="checkbox"
              class="size-4 accent-primary"
              :name="`metric_${m}`"
              :checked="metrics.includes(m)"
              @change="toggleMetric(m, ($event.target as HTMLInputElement).checked)"
            />
            <span>{{ metricLabel(m) }}</span>
          </label>
        </PopoverContent>
      </Popover>
      <NativeSelect v-model.number="days" class="w-28" :aria-label="t('availabilityCalendar.daysShown')" @change="load">
        <option :value="14">{{ t('availabilityCalendar.days', { n: 14 }) }}</option>
        <option :value="28">{{ t('availabilityCalendar.days', { n: 28 }) }}</option>
      </NativeSelect>
      <Button as-child variant="ghost" size="sm"><RouterLink to="/reservations/tape">{{ t('availabilityCalendar.tapeChart') }}</RouterLink></Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="property.currentId === null" class="muted">{{ t('availabilityCalendar.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('availabilityCalendar.noAccess') }}</p>

  <template v-else-if="calendar">
    <p class="mb-3 text-xs text-muted-foreground" data-testid="legend">{{ t('availabilityCalendar.legend') }}</p>
    <p v-if="byBed" class="notice" data-testid="bed-note">{{ t('availabilityCalendar.bedNote') }}</p>
    <p v-if="calendar.room_types.length === 0" class="muted" data-testid="empty">{{ t('availabilityCalendar.empty') }}</p>

    <Card v-else class="overflow-x-auto">
      <table class="w-full min-w-[720px] border-collapse text-xs" data-testid="calendar">
        <thead>
          <tr>
            <th class="sticky left-0 z-10 min-w-32 border-b border-border bg-card" />
            <th
              v-for="d in dates"
              :key="d"
              :class="cn('min-w-11 border-b border-l border-border px-1 py-1.5 font-medium text-muted-foreground', isWeekend(d) && 'bg-muted/60', d === businessDate && 'bg-primary/10 text-primary')"
            >
              <span class="block text-[10px] uppercase">{{ weekday(d) }}</span>{{ dayLabel(d) }}
            </th>
          </tr>
        </thead>
        <tbody>
          <template v-for="g in groups" :key="g.key">
            <!-- one metric: the values sit in the group's own row; several: a title row, then one row per metric -->
            <tr v-if="detailed" :data-testid="`group-${g.code}`">
              <th :class="cn('sticky left-0 z-10 border-b border-border bg-card px-2 py-1.5 text-left', g.level === 'bed' ? 'pl-5 text-[11px] font-normal' : 'font-medium', g.level === 'total' && 'border-t-2')">
                {{ g.title }}
                <span v-if="g.sub" class="block text-[10px] font-normal text-muted-foreground">{{ g.sub }}</span>
              </th>
              <td :colspan="dates.length" :class="cn('border-b border-l border-border', g.level === 'total' && 'border-t-2')" />
            </tr>
            <tr v-for="m in metrics" :key="`${g.key}-${m}`" :data-testid="detailed ? `row-${g.code}-${m}` : `row-${g.code}`">
              <th
                :class="cn('sticky left-0 z-10 border-b border-border bg-card px-2 py-1 text-left', detailed ? 'pl-5 text-[11px] font-normal text-muted-foreground' : 'py-1.5 font-medium', !detailed && g.level === 'bed' && 'pl-5 text-[11px] font-normal', !detailed && g.level === 'total' && 'border-t-2 font-semibold')"
              >
                <template v-if="detailed">{{ metricLabel(m) }}</template>
                <template v-else>
                  {{ g.title }}
                  <span v-if="g.sub" class="block text-[10px] font-normal text-muted-foreground">{{ g.sub }}</span>
                </template>
              </th>
              <td
                v-for="n in g.nights"
                :key="n.date"
                :title="tooltip(n)"
                :data-testid="cellId(g, m, n.date)"
                :class="cn('border-b border-l border-border px-1 py-1 text-center tabular-nums', g.level === 'bed' && 'bg-muted/20 text-muted-foreground', g.level === 'total' && 'border-t-2 font-semibold', !detailed && g.level === 'type' && 'py-1.5', tone(n, m))"
              >
                {{ value(n, m) }}
                <span v-if="m === metrics[0] && n.restrictions?.length" :class="cn('block text-[10px] font-semibold', n.stop_sell_all ? 'text-destructive' : 'text-warning-foreground')" :data-testid="`marks-${g.code}-${n.date}`">{{ marksOf(n) }}</span>
                <span v-if="!detailed && g.level === 'total'" class="block text-[10px] font-normal text-muted-foreground" :data-testid="`occupancy-${n.date}`">{{ n.occupancy_percent }}%</span>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </Card>
  </template>
</template>
