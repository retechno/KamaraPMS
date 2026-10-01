<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PayablesAging } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { money } from '@/views/accounting/reportApi'

const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<PayablesAging | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const asOf = ref('')
const expanded = ref<number | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const BUCKETS = [
  { key: 'CURRENT', label: 'Not due' }, { key: 'DAYS_1_30', label: '1–30 days' }, { key: 'DAYS_31_60', label: '31–60 days' },
  { key: 'DAYS_61_90', label: '61–90 days' }, { key: 'DAYS_OVER_90', label: 'Over 90 days' },
] as const

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('payables.view')) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/aging', { params: { path: { propertyId }, query: { as_of: asOf.value || undefined } } })
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
    <h1 class="page-title">Payables aging</h1>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="aging-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('payables.view')" class="muted" data-testid="no-access">Your role at this property cannot see payables: the <code>payables.view</code> permission is needed.</p>
  <template v-else>
    <form class="filters card" novalidate @submit.prevent="load">
      <label class="field"><span>As of</span><input v-model="asOf" name="as_of" type="date" /></label>
      <button type="submit" :disabled="busy" data-testid="apply">Show</button>
    </form>
    <section v-if="report" class="card">
      <p class="muted" data-testid="range">What is owed as of {{ report.as_of }}, by days past the due date.</p>
      <p v-if="!report.suppliers.length" class="muted" data-testid="empty">Nothing is owed.</p>
      <table v-else class="list" data-testid="aging">
        <thead><tr><th>Supplier</th><th v-for="b in BUCKETS" :key="b.key" class="num">{{ b.label }}</th><th class="num">Total</th></tr></thead>
        <tbody>
          <template v-for="s in report.suppliers" :key="s.supplier_id">
            <tr class="clickable" :data-testid="`supplier-${s.supplier_code}`" @click="expanded = expanded === s.supplier_id ? null : s.supplier_id">
              <td>{{ s.supplier_code }} · {{ s.supplier_name }}</td>
              <td v-for="b in BUCKETS" :key="b.key" class="num">{{ money(s.buckets[b.key]) }}</td>
              <td class="num"><b>{{ s.total }}</b></td>
            </tr>
            <tr v-if="expanded === s.supplier_id" class="detail" :data-testid="`bills-${s.supplier_code}`">
              <td :colspan="BUCKETS.length + 2">
                <table class="list inner">
                  <thead><tr><th>Bill</th><th>Invoice</th><th>Bill date</th><th>Due</th><th class="num">Days late</th><th class="num">Owed</th></tr></thead>
                  <tbody>
                    <tr v-for="bill in s.bills" :key="bill.bill_id">
                      <td>{{ bill.bill_number }}</td><td>{{ bill.supplier_invoice_number }}</td><td>{{ bill.bill_date }}</td><td>{{ bill.due_date }}</td>
                      <td class="num">{{ bill.days_overdue || '' }}</td><td class="num">{{ bill.outstanding }}</td>
                    </tr>
                  </tbody>
                </table>
              </td>
            </tr>
          </template>
        </tbody>
        <tfoot>
          <tr data-testid="totals">
            <th>Total</th>
            <th v-for="b in BUCKETS" :key="b.key" class="num">{{ money(report.buckets[b.key]) }}</th>
            <th class="num">{{ report.total }}</th>
          </tr>
        </tfoot>
      </table>
    </section>
  </template>
</template>

<style scoped>
.num {
  text-align: right;
  white-space: nowrap;
}
.clickable {
  cursor: pointer;
}
</style>
