<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventorySalesWithoutDeduction, PosOutlet } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DateInput } from '@/components/ui/date-input'
import { guardForm } from '@/utils/forms'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDate } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/**
 * POS lines with a recipe and no deduction from stock (docs/architecture/35-inventory.md 5.6): sales made while the stock module was off, or before the outlet had a warehouse. Switching the module on later
 * shows the gap and does not hide it: this report lists those lines by the business date they were posted.
 */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const rows = ref<InventorySalesWithoutDeduction[]>([])
const outlets = ref<PosOutlet[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const today = (): string => property.clock?.business_date ?? new Date().toISOString().slice(0, 10)
const startOfMonth = (d: string): string => `${d.slice(0, 8)}01`
const pick = reactive({ from: '', to: '', outlet: 0 })

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  if (!pick.to) pick.to = today()
  if (!pick.from) pick.from = startOfMonth(pick.to)
  try {
    if (outlets.value.length === 0) {
      try {
        outlets.value = (await api.GET('/api/v1/properties/{propertyId}/pos/outlets', { params: { path: { propertyId } } })).data?.data ?? []
      } catch {
        outlets.value = [] // the person may not read the outlets: the report is for all of them
      }
    }
    const query: { from: string; to: string; outlet_id?: number } = { from: pick.from, to: pick.to }
    if (pick.outlet) query.outlet_id = pick.outlet
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/pos-without-deduction', { params: { path: { propertyId }, query } })
    rows.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('inventory.posGap.title')" :description="t('inventory.posGap.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <form class="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4" novalidate data-testid="gap-filters" @submit.prevent="guardForm($event) && load()">
      <FormField :label="t('inventory.posGap.from')"><template #default="{ id }"><DateInput :id="id" v-model="pick.from" name="from" /></template></FormField>
      <FormField :label="t('inventory.posGap.to')"><template #default="{ id }"><DateInput :id="id" v-model="pick.to" name="to" /></template></FormField>
      <FormField v-if="outlets.length" :label="t('inventory.posGap.outlet')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model.number="pick.outlet" name="outlet" data-testid="gap-outlet">
            <option :value="0">{{ t('inventory.posGap.allOutlets') }}</option>
            <option v-for="o in outlets" :key="o.id" :value="o.id">{{ o.name }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <div class="flex items-end"><Button type="submit" variant="outline" data-testid="gap-show">{{ t('inventory.posGap.show') }}</Button></div>
    </form>
    <p v-if="loaded && rows.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="gap-empty">{{ t('inventory.posGap.empty') }}</p>
    <div v-else-if="rows.length" class="overflow-x-auto">
      <table class="w-full min-w-[40rem] text-sm" data-testid="gap-table">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.posGap.date') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.posGap.outlet') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.posGap.bill') }}</th>
            <th class="py-1 pr-2 font-medium">{{ t('inventory.posGap.product') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.posGap.quantity') }}</th><th />
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in rows" :key="`${r.source}-${r.bill_line_id ?? r.posting_line_id}`" class="border-t border-border" :data-testid="r.source === 'BANQUET' ? `gap-row-banquet-${r.posting_line_id}` : `gap-row-${r.bill_line_id}`">
            <td class="py-2 pr-2">{{ formatDate(r.sent_on) }}</td><td class="py-2 pr-2">{{ r.source === 'BANQUET' ? t('inventory.reports.outlet.banquet') : r.outlet_name }}</td><td class="py-2 pr-2">{{ r.bill_number }}</td>
            <td class="py-2 pr-2">{{ r.product_name }}</td><td class="py-2 pr-2 text-right">{{ formatQty(r.quantity) }}</td>
            <td class="py-2 text-right"><Badge v-if="r.voided" variant="outline">{{ t('inventory.posGap.voided') }}</Badge></td>
          </tr>
        </tbody>
      </table>
    </div>
  </template>
</template>
