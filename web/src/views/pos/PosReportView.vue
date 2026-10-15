<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosOutletReport } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { usePosOutlet } from '@/composables/usePosOutlet'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'

/**
 * The report of an outlet for a range of business dates (docs/architecture/34-pos.md 13.2): sales, service and tax; payments by method; voids, discounts and complimentary with who gave and who approved;
 * cashiers and waiters. Everything is the ledger of the folios of the bills, never a second set of totals.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const { outlets, outletId, loaded } = usePosOutlet()

const from = ref('')
const to = ref('')
const report = ref<PosOutletReport | null>(null)
const error = ref<ApiError | null>(null)
const loading = ref(false)

watch(() => property.current?.business_date, (d) => {
  if (d && !from.value) from.value = to.value = d
}, { immediate: true })

async function load(): Promise<void> {
  const propertyId = pid.value
  const id = outletId.value
  if (propertyId === null || id === null || !from.value || !to.value) return
  loading.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}/report', { params: { path: { propertyId, id }, query: { from: from.value, to: to.value } } })
    report.value = data ?? null
    error.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}
watch([outletId, from, to], () => void load(), { immediate: true })

function csv(): void {
  const r = report.value
  if (!r) return
  const lines = [['business_date', 'bills', 'net', 'service', 'tax', 'rounding', 'total'], ...r.days.map((d) => [d.business_date, d.bills, d.net, d.service, d.tax, d.rounding, d.total])]
  const text = lines.map((l) => l.join(',')).join('\n')
  const a = document.createElement('a')
  a.href = URL.createObjectURL(new Blob([text], { type: 'text/csv' }))
  a.download = `outlet-${r.outlet_id}-${r.from}-${r.to}.csv`
  a.click()
}
</script>

<template>
  <PageHeader :title="t('posReport.title')" :description="t('posReport.intro')" />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <EmptyState v-else-if="loaded && outlets.length === 0" :title="t('posFloor.noOutlet')" :description="t('posFloor.noOutletHint')" />
  <template v-else>
    <form class="mb-4 flex flex-wrap items-end gap-3" novalidate @submit.prevent="load">
      <FormField :label="t('posFloor.outlet')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="outletId" name="outlet" data-testid="report-outlet">
            <option v-for="o in outlets" :key="o.id" :value="o.id">{{ o.name }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField :label="t('posReport.from')"><template #default="{ id }"><Input :id="id" v-model="from" type="date" name="from" data-testid="report-from" /></template></FormField>
      <FormField :label="t('posReport.to')"><template #default="{ id }"><Input :id="id" v-model="to" type="date" name="to" data-testid="report-to" /></template></FormField>
      <Button type="button" variant="outline" :disabled="!report" data-testid="report-csv" @click="csv">{{ t('posReport.csv') }}</Button>
    </form>
    <ErrorNotice v-if="error" :error="error" inline />

    <template v-if="report">
      <dl class="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-5" data-testid="report-totals">
        <div v-for="k in (['net', 'service', 'tax', 'rounding', 'total'] as const)" :key="k" class="rounded-xl border border-border bg-card p-3">
          <dt class="text-xs text-muted-foreground">{{ t(`posReport.${k}`) }}</dt>
          <dd class="m-0 text-lg font-semibold tabular-nums" :data-testid="`total-${k}`">{{ formatMoney(report.totals[k]) }}</dd>
        </div>
      </dl>

      <section class="mb-4 grid gap-4 lg:grid-cols-2">
        <div class="rounded-xl border border-border bg-card p-3" data-testid="report-days">
          <h2 class="m-0 mb-2 text-base font-semibold">{{ t('posReport.byDay') }}</h2>
          <EmptyState v-if="report.days.length === 0" :title="t('posReport.none')" :description="t('posReport.noneHint')" />
          <table v-else class="w-full text-sm">
            <thead><tr class="text-left text-xs text-muted-foreground"><th>{{ t('posReport.date') }}</th><th class="text-right">{{ t('posReport.bills') }}</th><th class="text-right">{{ t('posReport.total') }}</th></tr></thead>
            <tbody><tr v-for="d in report.days" :key="d.business_date" class="border-t border-border"><td>{{ formatDate(d.business_date) }}</td><td class="text-right tabular-nums">{{ d.bills }}</td><td class="text-right tabular-nums">{{ formatMoney(d.total) }}</td></tr></tbody>
          </table>
        </div>
        <div class="rounded-xl border border-border bg-card p-3" data-testid="report-methods">
          <h2 class="m-0 mb-2 text-base font-semibold">{{ t('posReport.byMethod') }}</h2>
          <EmptyState v-if="report.methods.length === 0" :title="t('posReport.none')" :description="t('posReport.noneHint')" />
          <table v-else class="w-full text-sm">
            <tbody><tr v-for="m in report.methods" :key="m.method" class="border-t border-border first:border-t-0"><td>{{ t(`posPay.method_${m.method === 'CITY_LEDGER' ? 'COMPANY' : m.method}`) }}</td><td class="text-right tabular-nums">{{ m.payments }}</td><td class="text-right tabular-nums">{{ formatMoney(m.amount) }}</td></tr></tbody>
          </table>
          <p class="m-0 mt-2 text-xs text-muted-foreground" data-testid="report-room">{{ t('posReport.roomCharges', { n: report.room_charges.count, amount: formatMoney(report.room_charges.amount) }) }}</p>
          <p class="m-0 text-xs text-muted-foreground" data-testid="report-open">{{ t('posReport.openBills', { n: report.open_bills.count, amount: formatMoney(report.open_bills.balance) }) }}</p>
        </div>
      </section>

      <section class="mb-4 rounded-xl border border-border bg-card p-3" data-testid="report-products">
        <h2 class="m-0 mb-2 text-base font-semibold">{{ t('posReport.byProduct') }}</h2>
        <EmptyState v-if="report.products.length === 0" :title="t('posReport.none')" :description="t('posReport.noneHint')" />
        <table v-else class="w-full text-sm">
          <thead><tr class="text-left text-xs text-muted-foreground"><th>{{ t('posReport.product') }}</th><th class="text-right">{{ t('posReport.quantity') }}</th><th class="text-right">{{ t('posReport.net') }}</th><th class="text-right">{{ t('posReport.total') }}</th></tr></thead>
          <tbody>
            <tr v-for="p in report.products" :key="p.product_id" class="border-t border-border">
              <td>{{ p.name }}<span class="ml-2 text-xs text-muted-foreground">{{ p.category }}</span></td>
              <td class="text-right tabular-nums">{{ Number(p.quantity) }}</td><td class="text-right tabular-nums">{{ formatMoney(p.net) }}</td><td class="text-right tabular-nums">{{ formatMoney(p.total) }}</td>
            </tr>
          </tbody>
        </table>
      </section>

      <section class="grid gap-4 lg:grid-cols-3">
        <div v-for="block in ([['voids', report.voids], ['discounts', report.discounts], ['comps', report.comps]] as const)" :key="block[0]" class="rounded-xl border border-border bg-card p-3" :data-testid="`report-${block[0]}`">
          <h2 class="m-0 mb-2 text-base font-semibold">{{ t(`posReport.${block[0]}`) }}</h2>
          <EmptyState v-if="block[1].length === 0" :title="t('posReport.none')" :description="t('posReport.noneHint')" />
          <ul v-else class="m-0 list-none p-0 text-sm">
            <li v-for="(e, i) in block[1]" :key="i" class="border-t border-border py-1.5 first:border-t-0">
              <strong>{{ e.bill_number }}</strong> · {{ e.what }} <span class="float-right tabular-nums">{{ formatMoney(e.amount) }}</span>
              <span class="block text-xs text-muted-foreground">{{ formatDate(e.business_date) }}<template v-if="e.reason"> · {{ e.reason }}</template><template v-if="e.by"> · {{ e.by }}</template><template v-if="e.approved_by"> · {{ t('posReport.approvedBy', { who: e.approved_by }) }}</template></span>
            </li>
          </ul>
        </div>
      </section>
    </template>
  </template>
</template>
