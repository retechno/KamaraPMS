<script setup lang="ts">
import { CheckCircle2, RefreshCw } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { NightAuditPreview, NightAuditResult } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StepCard from '@/components/app/StepCard.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const preview = ref<NightAuditPreview | null>(null)
const result = ref<NightAuditResult | null>(null)
const error = ref<ApiError | null>(null)
/** The failure of the run itself stays on the page: closing the day is the one refusal that has to be read and acted on, where the steps below show what blocks it. The other failures are toasts. */
const failedRun = ref(false)
const notice = ref('')
const busy = ref(false)
const confirmRun = ref(false)
const noShow = reactive({ selected: [] as number[], confirm: false, reason: '' })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const blockers = computed(() => preview.value?.blockers)
const arrivals = computed(() => blockers.value?.unresolved_arrivals ?? [])
const departures = computed(() => blockers.value?.unresolved_departures ?? [])
const accountingBlockers = computed(() => blockers.value?.accounting_readiness ?? [])
const chargeIssues = computed(() => (blockers.value?.charge_errors.length ?? 0) + (blockers.value?.invalid_charges.length ?? 0) + accountingBlockers.value.length)
const allSelected = computed(() => arrivals.value.length > 0 && noShow.selected.length === arrivals.value.length)

// How many steps still need someone: the clock, the arrivals, the departures and the room charges.
const attention = computed(() => (preview.value && !preview.value.time_guard_ok ? 1 : 0) + (arrivals.value.length ? 1 : 0) + (departures.value.length ? 1 : 0) + (chargeIssues.value ? 1 : 0))

const arrivalColumns = computed<Column<(typeof arrivals.value)[number]>[]>(() => [
  { key: 'select', label: '', class: 'w-8' },
  { key: 'confirmation_number', label: t('nightAudit.reservation') },
  { key: 'guest', label: t('nightAudit.guest') },
  { key: 'room_type', label: t('nightAudit.room') },
  { key: 'arrival_date', label: t('nightAudit.arrival'), format: 'date' as const },
])
const departureColumns = computed<Column<(typeof departures.value)[number]>[]>(() => [
  { key: 'stay_number', label: t('nightAudit.stay') },
  { key: 'guest', label: t('nightAudit.guest') },
  { key: 'room', label: t('nightAudit.room') },
  { key: 'departure_date', label: t('nightAudit.departure'), format: 'date' as const },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('nightaudit.run')) return
  busy.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/night-audit/preview', { params: { path: { propertyId } } })
    preview.value = data ?? null
    // The selection offered is what the staff sees now; a line that has gone is dropped.
    noShow.selected = noShow.selected.filter((id) => arrivals.value.some((a) => a.reservation_room_id === id))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

function toggleAll(): void {
  noShow.selected = allSelected.value ? [] : arrivals.value.map((a) => a.reservation_room_id)
}

async function markNoShows(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !preview.value) return
  busy.value = true
  error.value = null
  failedRun.value = false
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/night-audit/no-shows', {
      params: { path: { propertyId } },
      body: { business_date: preview.value.business_date, reservation_room_ids: noShow.selected, confirm: noShow.confirm, reason: noShow.reason || undefined },
    })
    notice.value = t('nightAudit.marked', { n: data?.marked.length ?? 0 })
    noShow.selected = []
    noShow.confirm = false
    noShow.reason = ''
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
  await load() // after a NO_SHOW_SET_CHANGED the list is refreshed, nothing was marked
}

async function run(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !preview.value) return
  busy.value = true
  error.value = null
  failedRun.value = true
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/night-audit/run', {
      params: { path: { propertyId } }, body: { business_date: preview.value.business_date },
    })
    result.value = data ?? null
    confirmRun.value = false
    await property.refreshClock()
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    confirmRun.value = false
    await load()
  } finally {
    busy.value = false
  }
}

watch(pid, () => {
  preview.value = null
  result.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('nightAudit.title')">
    <template v-if="preview" #marks>
      <Badge variant="secondary" data-testid="audit-date">{{ $date(preview.business_date) }}</Badge>
      <Badge v-if="!result" :variant="attention ? 'warning' : 'success'" data-testid="readiness">
        {{ attention ? t('nightAudit.notReady', { n: attention }) : t('nightAudit.ready') }}
      </Badge>
    </template>
    <template #actions>
      <Button variant="outline" size="sm" :disabled="busy" data-testid="refresh" @click="load">
        <RefreshCw :class="busy && 'animate-spin'" />{{ t('common.refresh') }}
      </Button>
    </template>
  </PageHeader>

  <ErrorNotice :error="error" :inline="failedRun" />
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('nightAudit.selectProperty') }}</p>
  <p v-else-if="!can('nightaudit.run')" class="muted" data-testid="no-access">{{ t('nightAudit.noAccess') }}</p>

  <template v-else>
    <Card v-if="result" class="mb-4 border-success/40" data-testid="result">
      <CardHeader class="flex-row items-center gap-3">
        <CheckCircle2 class="size-6 text-success-text" aria-hidden="true" />
        <div>
          <CardTitle>{{ t('nightAudit.closedTitle', { date: $date(result.closed_business_date) }) }}</CardTitle>
          <p class="m-0 mt-0.5 text-sm text-muted-foreground">
            {{ t('nightAudit.newDateIs') }} <strong class="text-foreground" data-testid="new-date">{{ $date(result.new_business_date) }}</strong>. {{ t('nightAudit.chargesPosted', { n: result.room_charges_posted }) }}
          </p>
        </div>
      </CardHeader>
      <CardContent>
        <div class="grid grid-cols-[repeat(auto-fill,minmax(11rem,1fr))] gap-3" data-testid="summary">
          <KpiCard
            :label="t('nightAudit.occupied')"
            :value="`${result.summary.occupancy_percent}%`"
            :hint="t('nightAudit.occupiedValue', { occupied: result.summary.rooms.occupied, available: result.summary.rooms.total - result.summary.rooms.out_of_order, pct: result.summary.occupancy_percent })"
          />
          <KpiCard :label="t('nightAudit.movements')" :value="`${result.summary.arrivals} / ${result.summary.departures} / ${result.summary.no_shows}`" />
          <KpiCard :label="t('nightAudit.revenue')" :value="result.summary.room_revenue.net" />
          <KpiCard :label="t('nightAudit.adr')" :value="result.summary.adr" />
          <KpiCard :label="t('nightAudit.revpar')" :value="result.summary.revpar" />
          <KpiCard v-for="m in result.summary.payments_by_method" :key="m.method" :label="t('nightAudit.payments', { method: m.method })" :value="m.net" />
        </div>
      </CardContent>
    </Card>

    <template v-if="preview">
      <StepCard :step="1" :title="t('nightAudit.stepTime')" :state="preview.time_guard_ok ? 'ok' : 'blocked'" data-testid="guard">
        <p v-if="preview.time_guard_ok" class="m-0 text-sm">{{ t('nightAudit.timeOk') }}</p>
        <p v-else class="alert warning m-0" data-testid="too-early">
          {{ t('nightAudit.tooEarly', { from: preview.night_audit_allowed_from, now: preview.property_local_time }) }}
        </p>
      </StepCard>

      <StepCard :step="2" :title="t('nightAudit.stepArrivals')" :state="arrivals.length ? 'blocked' : 'ok'" :summary="`(${arrivals.length})`" data-testid="arrivals">
        <p v-if="!arrivals.length" class="m-0 text-sm text-muted-foreground" data-testid="no-arrivals">{{ t('nightAudit.allArrived') }}</p>
        <template v-else>
          <p class="mb-3 mt-0 text-sm text-muted-foreground">
            {{ t('nightAudit.arrivalsHelpBefore') }} <RouterLink to="/arrivals">{{ t('frontDesk.page.arrivals') }}</RouterLink>{{ t('nightAudit.arrivalsHelpAfter') }}
          </p>
          <DataTable :columns="arrivalColumns" :rows="arrivals" row-key="reservation_room_id" :row-test-id="(a) => `arrival-${a.reservation_room_id}`">
            <template #header-select>
              <input type="checkbox" :checked="allSelected" :aria-label="t('nightAudit.selectAll')" data-testid="select-all" @change="toggleAll" />
            </template>
            <template #cell-select="{ row }">
              <input v-model="noShow.selected" type="checkbox" :value="row.reservation_room_id" :aria-label="t('nightAudit.selectOne', { number: row.confirmation_number })" />
            </template>
            <template #cell-confirmation_number="{ row }"><RouterLink :to="`/reservations/${row.reservation_id}`">{{ row.confirmation_number }}</RouterLink></template>
            <template #cell-room_type="{ row }">{{ row.room_type }}<template v-if="row.room"> · {{ row.room }}</template></template>
          </DataTable>
          <form v-if="can('nightaudit.no_show')" class="mt-4 flex flex-wrap items-end gap-4" novalidate data-testid="noshow-form" @submit.prevent="markNoShows">
            <FormField class="min-w-56 flex-1" :label="t('nightAudit.noShowReason')">
              <template #default="{ id }"><Input :id="id" v-model="noShow.reason" name="reason" maxlength="500" /></template>
            </FormField>
            <label class="flex items-center gap-2 pb-2 text-sm"><input v-model="noShow.confirm" type="checkbox" name="confirm" /><span>{{ t('nightAudit.noShowConfirm') }}</span></label>
            <Button type="submit" variant="outline" :disabled="busy || !noShow.selected.length || !noShow.confirm" data-testid="mark-no-shows">
              {{ t('nightAudit.markNoShow', { n: noShow.selected.length }) }}
            </Button>
          </form>
        </template>
      </StepCard>

      <StepCard :step="3" :title="t('nightAudit.stepDepartures')" :state="departures.length ? 'blocked' : 'ok'" :summary="`(${departures.length})`" data-testid="departures">
        <p v-if="!departures.length" class="m-0 text-sm text-muted-foreground">{{ t('nightAudit.nobodyOverdue') }}</p>
        <template v-else>
          <p class="mb-3 mt-0 text-sm text-muted-foreground">{{ t('nightAudit.departuresHelp') }}</p>
          <DataTable :columns="departureColumns" :rows="departures" row-key="stay_id" :row-test-id="(s) => `departure-${s.stay_id}`">
            <template #cell-stay_number="{ row }"><RouterLink :to="`/stays/${row.stay_id}`">{{ row.stay_number }}</RouterLink></template>
            <template #cell-room="{ row }">{{ row.room || '—' }}</template>
          </DataTable>
        </template>
      </StepCard>

      <StepCard :step="4" :title="t('nightAudit.stepCharges')" :state="chargeIssues ? 'blocked' : 'ok'" data-testid="charges">
        <p class="m-0 text-sm" data-testid="charge-counts">
          {{ t('nightAudit.chargeCountsBefore', { missing: preview.missing_charges.count, tonight: preview.tonight_charges.count, total: $money(preview.tonight_charges.total) }) }}
          <RouterLink to="/room-charges">{{ t('nav.items.roomCharges') }}</RouterLink> {{ t('nightAudit.chargeCountsAfter') }}
        </p>
        <div v-if="blockers?.charge_errors.length" class="alert mb-0 mt-3" data-testid="charge-errors">
          <strong>{{ t('nightAudit.chargeErrors', { n: blockers.charge_errors.length }) }}</strong>
          <ul class="m-0 mt-1 pl-5">
            <li v-for="c in blockers.charge_errors" :key="`${c.stay_id}-${c.service_date}`">
              <RouterLink :to="`/stays/${c.stay_id}`">{{ c.stay_number }}</RouterLink> {{ $date(c.service_date) }}: {{ c.reason }}
            </li>
          </ul>
        </div>
        <div v-if="blockers?.invalid_charges.length" class="alert mb-0 mt-3" data-testid="invalid-charges">
          <strong>{{ t('nightAudit.invalidCharges', { n: blockers.invalid_charges.length }) }}</strong>
          <ul class="m-0 mt-1 pl-5">
            <li v-for="c in blockers.invalid_charges" :key="c.folio_item_id">
              <RouterLink :to="`/stays/${c.stay_id}`">{{ c.stay_number }}</RouterLink> {{ $date(c.service_date) }} ({{ c.reason }}): {{ t('nightAudit.reverseOnFolio') }}
            </li>
          </ul>
        </div>
        <div v-if="accountingBlockers.length" class="alert mb-0 mt-3" data-testid="accounting-readiness">
          <strong>{{ t('nightAudit.accountingNotReady', { n: accountingBlockers.length }) }}</strong>
          <ul class="m-0 mt-1 pl-5">
            <li v-for="b in accountingBlockers" :key="`${b.code}-${b.ref ?? ''}`"><code>{{ b.code }}</code> {{ b.message }}</li>
          </ul>
        </div>
      </StepCard>

      <Card v-if="preview.warnings.stale_drafts.length || preview.warnings.open_folios_of_cancelled_reservations.length || preview.warnings.blocks_ending.length" class="mb-4" data-testid="warnings">
        <CardHeader><CardTitle>{{ t('nightAudit.warnings') }} <small class="font-normal text-muted-foreground">{{ t('nightAudit.warningsNote') }}</small></CardTitle></CardHeader>
        <CardContent>
          <ul class="m-0 pl-5 text-sm">
            <li v-for="d in preview.warnings.stale_drafts" :key="d.reservation_room_id">{{ t('nightAudit.staleDraft', { number: d.confirmation_number, date: $date(d.arrival_date) }) }}</li>
            <li v-for="f in preview.warnings.open_folios_of_cancelled_reservations" :key="f.folio_id">
              {{ t('nightAudit.openFolioBefore') }} <RouterLink :to="`/folios/${f.folio_id}`">{{ f.folio_number }}</RouterLink> {{ t('nightAudit.openFolioAfter', { number: f.confirmation_number, balance: $money(f.balance) }) }}
            </li>
            <li v-for="b in preview.warnings.blocks_ending" :key="b.block_id">{{ t('nightAudit.blockEnds', { type: b.block_type, room: b.room, date: $date(b.end_date) }) }}</li>
          </ul>
        </CardContent>
      </Card>

      <StepCard :step="5" :title="t('nightAudit.stepRun')" :state="preview.can_run ? 'pending' : 'blocked'" data-testid="run">
        <p v-if="!preview.can_run" class="mb-3 mt-0 text-sm text-muted-foreground" data-testid="cannot-run">{{ t('nightAudit.resolveFirst') }}</p>
        <label class="flex items-center gap-2 text-sm">
          <input v-model="confirmRun" type="checkbox" name="confirm_run" :disabled="!preview.can_run" />
          <span>{{ t('nightAudit.confirmRun', { date: $date(preview.business_date) }) }}</span>
        </label>
        <div class="mt-4 flex justify-end">
          <Button :disabled="busy || !preview.can_run || !confirmRun" data-testid="run-audit" @click="run">{{ t('nightAudit.run') }}</Button>
        </div>
      </StepCard>
    </template>
  </template>
</template>
