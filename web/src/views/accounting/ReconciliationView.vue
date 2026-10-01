<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Reconciliation } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { money } from './reportApi'

const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<Reconciliation | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const asOf = ref('')

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/reconciliation', { params: { path: { propertyId }, query: { as_of: asOf.value || undefined } } })
    report.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  report.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Control accounts</h1>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="report-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">Your role at this property cannot see this report: the <code>accounting.view</code> permission is needed.</p>
  <template v-else>
    <p class="muted">Compares the guest ledger, advance deposit and city ledger accounts of the books with what the folios and the city ledger say.</p>
    <form class="filters card" novalidate @submit.prevent="load">
      <label class="field"><span>As of</span><input v-model="asOf" name="as_of" type="date" /></label>
      <button type="submit" :disabled="busy" data-testid="apply">Check</button>
    </form>
    <section v-if="report" class="card">
      <p v-if="report.reconciled" class="notice" data-testid="reconciled">Reconciled as of {{ report.as_of }}: the books agree with the folios.</p>
      <p v-else class="alert" data-testid="not-reconciled">Not reconciled as of {{ report.as_of }}.</p>
      <p v-if="report.pending_days" class="alert" data-testid="pending">
        {{ report.pending_days }} closed business day(s) have no journal yet: use "Journal missing days" on the Journals screen.
      </p>
      <p v-if="report.includes_open_day" class="muted" data-testid="open-day">The open business day is not in the books until the night audit closes it, so activity of today shows as a difference.</p>
      <table class="list" data-testid="controls">
        <thead><tr><th>Account</th><th class="num">Books</th><th class="num">Folios</th><th class="num">Difference</th></tr></thead>
        <tbody>
          <tr v-for="c in report.controls" :key="c.key" :data-testid="`control-${c.key}`" :class="{ off: Number(c.difference) !== 0 }">
            <td>{{ c.title }}<br /><small class="muted">{{ c.account }} · {{ c.basis }}</small></td>
            <td class="num">{{ c.ledger }}</td>
            <td class="num">{{ c.source }}</td>
            <td class="num">{{ money(c.difference) }}</td>
          </tr>
        </tbody>
      </table>
      <p class="muted">Accounting starts on {{ report.start_date }}: folio activity before that date is not in the books.</p>
    </section>
  </template>
</template>

<style scoped>
.num {
  text-align: right;
  white-space: nowrap;
}
.off td {
  color: var(--danger, #b91c1c);
}
</style>
