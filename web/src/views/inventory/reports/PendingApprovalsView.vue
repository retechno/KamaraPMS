<script setup lang="ts">
import { watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import type { InventoryPendingApprovalsReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { Button } from '@/components/ui/button'
import { useInventoryReport } from '@/composables/useInventoryReport'
import { t } from '@/i18n'
import { formatDateTime, formatMoney } from '@/utils/format'

/** The purchase requests and store requisitions still in the approval chain (docs/architecture/35-inventory.md 12.5, 22): the level they are at, who is awaited, how long they have been at that level (from the day it was entered), the value; the oldest first. */
const r = useInventoryReport<InventoryPendingApprovalsReport>('/api/v1/properties/{propertyId}/inventory/reports/pending-approvals', 'pending-approvals.csv', async (propertyId) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/reports/pending-approvals', { params: { path: { propertyId } } })
  return data ?? null
}, { single: true })
const print = (): void => window.print()
watch(r.pid, r.load, { immediate: true })
const link = (d: { doc_type: string; doc_id: number }): string => (d.doc_type === 'PR' ? `/inventory/requests/${d.doc_id}` : `/inventory/requisitions/${d.doc_id}`)
const age = (hours: number, days: number): string => (hours < 48 ? t('inventory.reports.pending.hours', { n: hours }) : t('inventory.reports.pending.days', { n: days }))
</script>

<template>
  <PageHeader :title="t('inventory.reports.pending.title')" :description="t('inventory.reports.pending.intro')" />
  <ReportPrintHeader />
  <ErrorNotice v-if="r.error.value" :error="r.error.value" inline />
  <p v-if="r.pid.value === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <div class="mb-4 flex flex-wrap gap-2 print:hidden" data-testid="report-period">
      <Button type="button" variant="outline" :disabled="r.busy.value" data-testid="report-show" @click="r.load">{{ t('inventory.reports.show') }}</Button>
      <Button type="button" variant="outline" data-testid="report-print" @click="print">{{ t('inventory.reports.print') }}</Button>
      <Button type="button" variant="outline" :disabled="r.busy.value" data-testid="report-csv" @click="r.csv()">{{ t('inventory.reports.csv') }}</Button>
    </div>
    <p v-if="r.loaded.value && !r.data.value?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="pending-empty">{{ t('inventory.reports.pending.none') }}</p>
    <template v-else-if="r.data.value">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[52rem] text-sm" data-testid="pending-table">
          <thead><tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.pending.document') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.filter.department') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.pending.level') }}</th>
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.pending.awaiting') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.pending.since') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.pending.age') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.value.value') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="d in r.data.value.rows" :key="`${d.doc_type}-${d.doc_id}`" class="border-t border-border" :data-testid="`pending-${d.number}`">
              <td class="py-1.5 pr-2"><RouterLink :to="link(d)" class="underline">{{ d.number }}</RouterLink><span class="block text-xs text-muted-foreground">{{ d.doc_type }} · {{ d.requester_name }}</span></td>
              <td class="py-1.5 pr-2">{{ d.department_name || t('inventory.reports.noDepartment') }}</td>
              <td class="py-1.5 pr-2">{{ d.level_no }} · {{ d.step_name }}</td><td class="py-1.5 pr-2">{{ d.awaiting }}</td>
              <td class="py-1.5 pr-2">{{ d.entered_at ? formatDateTime(d.entered_at) : '–' }}</td>
              <td class="py-1.5 pr-2 text-right font-semibold" data-testid="pending-age">{{ age(d.age_hours, d.age_days) }}</td><td class="py-1.5 text-right">{{ formatMoney(d.value) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="m-0 mt-3 text-right text-sm font-semibold" data-testid="pending-total">{{ t('inventory.reports.total') }}: {{ formatMoney(r.data.value.total) }}</p>
    </template>
  </template>
</template>
