<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { TaxFilingLiability } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { money } from '@/views/accounting/reportApi'

const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<TaxFilingLiability | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const asOf = ref('')

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('tax.view')) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/liability', { params: { path: { propertyId }, query: { as_of: asOf.value || undefined } } })
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
    <h1 class="page-title">Tax owed</h1>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="liability-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('tax.view')" class="muted" data-testid="no-access">Your role at this property cannot see what is owed in tax: the <code>tax.view</code> permission is needed.</p>
  <template v-else>
    <form class="filters card" novalidate @submit.prevent="load">
      <label class="field"><span>As of</span><input v-model="asOf" name="as_of" type="date" /></label>
      <button type="submit" :disabled="busy" data-testid="apply">Show</button>
    </form>
    <section v-if="report" class="card">
      <p v-if="!report.taxes.length" class="muted" data-testid="empty">No tax is set up for filing yet.</p>
      <template v-else>
        <p class="muted" data-testid="owed-total">Owed to the tax authorities as of {{ report.as_of }}: <b>{{ report.owed }}</b></p>
        <table class="list" data-testid="taxes">
          <thead><tr><th>Tax</th><th class="num">Collected</th><th class="num">On returns</th><th class="num">Not filed yet</th><th class="num">Paid</th><th class="num">Owed</th><th>Overdue</th></tr></thead>
          <tbody>
            <tr v-for="t in report.taxes" :key="t.tax_id" :data-testid="`tax-${t.tax_code}`">
              <td><RouterLink :to="{ path: '/tax/returns', query: { tax: String(t.tax_id) } }">{{ t.tax_code }}</RouterLink> <small class="muted">· {{ t.authority }} · account {{ t.account_code }}</small></td>
              <td class="num">{{ t.collected }}</td><td class="num">{{ t.filed }}</td><td class="num">{{ money(t.unfiled) }}</td><td class="num">{{ t.paid }}</td><td class="num"><b>{{ t.owed }}</b></td>
              <td>
                <span v-if="t.overdue_unfiled_months" class="error-text" data-testid="overdue-unfiled">{{ t.overdue_unfiled_months }} month(s) not filed</span>
                <span v-if="Number(t.overdue_unpaid)" class="error-text" data-testid="overdue-unpaid">{{ t.overdue_unpaid }} unpaid</span>
              </td>
            </tr>
          </tbody>
        </table>
        <h2>Against the books</h2>
        <table class="list" data-testid="accounts">
          <thead><tr><th>Tax payable account</th><th class="num">Books</th><th class="num">Taxes say</th><th class="num">Difference</th></tr></thead>
          <tbody>
            <tr v-for="a in report.accounts" :key="a.account_code" :data-testid="`account-${a.account_code}`">
              <td>{{ a.account_code }}</td><td class="num">{{ a.books }}</td><td class="num">{{ a.owed }}</td>
              <td class="num" :class="{ bad: Number(a.difference) !== 0 }">{{ money(a.difference) }}</td>
            </tr>
          </tbody>
        </table>
        <p class="muted">A difference means a day has no journal yet (Journals → "Journal missing days"), or a tax payable account carries something other than collected tax.</p>
      </template>
    </section>
  </template>
</template>

<style scoped>
.num {
  text-align: right;
  white-space: nowrap;
}
.bad {
  color: var(--danger, #b91c1c);
}
</style>
