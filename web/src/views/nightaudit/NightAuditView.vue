<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { NightAuditPreview, NightAuditResult } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const preview = ref<NightAuditPreview | null>(null)
const result = ref<NightAuditResult | null>(null)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const confirmRun = ref(false)
const noShow = reactive({ selected: [] as number[], confirm: false, reason: '' })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const blockers = computed(() => preview.value?.blockers)
const arrivals = computed(() => blockers.value?.unresolved_arrivals ?? [])
const allSelected = computed(() => arrivals.value.length > 0 && noShow.selected.length === arrivals.value.length)

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
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/night-audit/no-shows', {
      params: { path: { propertyId } },
      body: { business_date: preview.value.business_date, reservation_room_ids: noShow.selected, confirm: noShow.confirm, reason: noShow.reason || undefined },
    })
    notice.value = `${data?.marked.length ?? 0} arrival(s) marked as no-show.`
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
  <div class="page-head">
    <h1 class="page-title">Night audit <span v-if="preview" class="muted">{{ preview.business_date }}</span></h1>
    <button type="button" :disabled="busy" data-testid="refresh" @click="load">Refresh</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('nightaudit.run')" class="muted" data-testid="no-access">Your role at this property does not allow running the night audit.</p>

  <template v-else>
    <section v-if="result" class="card" data-testid="result">
      <h2>Business day {{ result.closed_business_date }} closed</h2>
      <p>The business date is now <strong data-testid="new-date">{{ result.new_business_date }}</strong>. {{ result.room_charges_posted }} room charge(s) posted.</p>
      <dl class="summary" data-testid="summary">
        <div><dt>Occupied</dt><dd>{{ result.summary.rooms.occupied }} of {{ result.summary.rooms.total - result.summary.rooms.out_of_order }} ({{ result.summary.occupancy_percent }}%)</dd></div>
        <div><dt>Arrivals / departures / no-shows</dt><dd>{{ result.summary.arrivals }} / {{ result.summary.departures }} / {{ result.summary.no_shows }}</dd></div>
        <div><dt>Room revenue (net)</dt><dd>{{ result.summary.room_revenue.net }}</dd></div>
        <div><dt>ADR</dt><dd>{{ result.summary.adr }}</dd></div>
        <div><dt>RevPAR</dt><dd>{{ result.summary.revpar }}</dd></div>
        <div v-for="m in result.summary.payments_by_method" :key="m.method"><dt>Payments {{ m.method }}</dt><dd>{{ m.net }}</dd></div>
      </dl>
    </section>

    <template v-if="preview">
      <section class="card" data-testid="guard">
        <p v-if="preview.time_guard_ok">The business date can be closed now.</p>
        <p v-else class="alert warning" data-testid="too-early">
          The business date cannot be closed before {{ preview.night_audit_allowed_from }}. Property time: {{ preview.property_local_time }}.
        </p>
      </section>

      <section class="card" data-testid="arrivals">
        <h2>1. Unresolved arrivals <span class="muted">({{ arrivals.length }})</span></h2>
        <p v-if="!arrivals.length" class="muted" data-testid="no-arrivals">Every arrival is checked in or resolved.</p>
        <template v-else>
          <p class="muted">Check each one in at <RouterLink to="/arrivals">Arrivals</RouterLink>, or amend or cancel the reservation, or mark the ones that did not come as no-show.</p>
          <table class="list">
            <thead><tr><th><input type="checkbox" :checked="allSelected" aria-label="Select all" data-testid="select-all" @change="toggleAll" /></th><th>Reservation</th><th>Guest</th><th>Room</th><th>Arrival</th></tr></thead>
            <tbody>
              <tr v-for="a in arrivals" :key="a.reservation_room_id" :data-testid="`arrival-${a.reservation_room_id}`">
                <td><input v-model="noShow.selected" type="checkbox" :value="a.reservation_room_id" :aria-label="`Select ${a.confirmation_number}`" /></td>
                <td><RouterLink :to="`/reservations/${a.reservation_id}`">{{ a.confirmation_number }}</RouterLink></td>
                <td>{{ a.guest }}</td>
                <td>{{ a.room_type }}<template v-if="a.room"> · {{ a.room }}</template></td>
                <td>{{ a.arrival_date }}</td>
              </tr>
            </tbody>
          </table>
          <form v-if="can('nightaudit.no_show')" class="noshow" novalidate data-testid="noshow-form" @submit.prevent="markNoShows">
            <label class="field grow"><span>Reason</span><input v-model="noShow.reason" name="reason" maxlength="500" /></label>
            <label class="check"><input v-model="noShow.confirm" type="checkbox" name="confirm" /><span>I confirm these guests did not arrive</span></label>
            <button type="submit" :disabled="busy || !noShow.selected.length || !noShow.confirm" data-testid="mark-no-shows">Mark {{ noShow.selected.length }} as no-show</button>
          </form>
        </template>
      </section>

      <section class="card" data-testid="departures">
        <h2>2. Unresolved departures <span class="muted">({{ blockers?.unresolved_departures.length }})</span></h2>
        <p v-if="!blockers?.unresolved_departures.length" class="muted">Nobody is overdue to leave.</p>
        <template v-else>
          <p class="muted">Check the guest out, or extend the stay, from the stay page.</p>
          <table class="list">
            <thead><tr><th>Stay</th><th>Guest</th><th>Room</th><th>Departure</th></tr></thead>
            <tbody>
              <tr v-for="s in blockers.unresolved_departures" :key="s.stay_id" :data-testid="`departure-${s.stay_id}`">
                <td><RouterLink :to="`/stays/${s.stay_id}`">{{ s.stay_number }}</RouterLink></td>
                <td>{{ s.guest }}</td><td>{{ s.room || '—' }}</td><td>{{ s.departure_date }}</td>
              </tr>
            </tbody>
          </table>
        </template>
      </section>

      <section class="card" data-testid="charges">
        <h2>3. Room charges</h2>
        <p data-testid="charge-counts">
          {{ preview.missing_charges.count }} missing night(s) from earlier days,
          {{ preview.tonight_charges.count }} night(s) tonight (total {{ preview.tonight_charges.total }}). The run posts them;
          <RouterLink to="/room-charges">Room charges</RouterLink> posts them beforehand.
        </p>
        <div v-if="blockers?.charge_errors.length" class="alert" data-testid="charge-errors">
          <strong>{{ blockers.charge_errors.length }} night(s) cannot be charged:</strong>
          <ul>
            <li v-for="c in blockers.charge_errors" :key="`${c.stay_id}-${c.service_date}`">
              <RouterLink :to="`/stays/${c.stay_id}`">{{ c.stay_number }}</RouterLink> {{ c.service_date }}: {{ c.reason }}
            </li>
          </ul>
        </div>
        <div v-if="blockers?.invalid_charges.length" class="alert" data-testid="invalid-charges">
          <strong>{{ blockers.invalid_charges.length }} posted night(s) should not exist:</strong>
          <ul>
            <li v-for="c in blockers.invalid_charges" :key="c.folio_item_id">
              <RouterLink :to="`/stays/${c.stay_id}`">{{ c.stay_number }}</RouterLink> {{ c.service_date }} ({{ c.reason }}): reverse it on the folio.
            </li>
          </ul>
        </div>
      </section>

      <section v-if="preview.warnings.stale_drafts.length || preview.warnings.open_folios_of_cancelled_reservations.length || preview.warnings.blocks_ending.length" class="card" data-testid="warnings">
        <h2>Warnings <span class="muted">(do not stop the audit)</span></h2>
        <ul>
          <li v-for="d in preview.warnings.stale_drafts" :key="d.reservation_room_id">Draft {{ d.confirmation_number }} should have arrived on {{ d.arrival_date }}.</li>
          <li v-for="f in preview.warnings.open_folios_of_cancelled_reservations" :key="f.folio_id">
            Folio <RouterLink :to="`/folios/${f.folio_id}`">{{ f.folio_number }}</RouterLink> of {{ f.confirmation_number }} is open with a balance of {{ f.balance }}.
          </li>
          <li v-for="b in preview.warnings.blocks_ending" :key="b.block_id">{{ b.block_type }} block on room {{ b.room }} ends {{ b.end_date }}.</li>
        </ul>
      </section>

      <section class="card" data-testid="run">
        <h2>4. Run</h2>
        <p v-if="!preview.can_run" class="muted" data-testid="cannot-run">Resolve the items above first.</p>
        <label class="check"><input v-model="confirmRun" type="checkbox" name="confirm_run" :disabled="!preview.can_run" /><span>Close {{ preview.business_date }} and open the next business date</span></label>
        <div class="form-actions">
          <button type="button" class="btn-primary" :disabled="busy || !preview.can_run || !confirmRun" data-testid="run-audit" @click="run">Run night audit</button>
        </div>
      </section>
    </template>
  </template>
</template>

<style scoped>
.list {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}
.list th,
.list td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
}
.noshow {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  flex-wrap: wrap;
  margin-top: 12px;
}
.summary {
  display: grid;
  gap: 6px;
}
.summary div {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}
.summary dt {
  color: var(--muted, inherit);
}
</style>
