<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { TrialBalance } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'
import { downloadCsv, money } from './reportApi'

const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<TrialBalance | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const range = reactive({ from: '', to: '' })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const query = () => ({ from: range.from || undefined, to: range.to || undefined })

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/trial-balance', { params: { path: { propertyId }, query: query() } })
    report.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function exportCsv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !report.value) return
  try {
    await downloadCsv('/api/v1/properties/{propertyId}/accounting/trial-balance', { path: { propertyId }, query: query() }, `trial-balance-${report.value.to}.csv`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function showPdf(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    await openPdf(documentPath.accounting(propertyId, 'trial-balance', query()))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch(() => pid.value, () => {
  report.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('statements.trial')">
    <template #actions>
      <template v-if="report">
        <Button type="button" variant="outline" data-testid="pdf" @click="showPdf">{{ t('statements.pdf') }}</Button>
        <Button type="button" variant="outline" data-testid="export" @click="exportCsv">{{ t('statements.export') }}</Button>
      </template>
    </template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert" data-testid="report-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in error.fieldErrors ?? []" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('statements.noAccess', { what: t('statements.whatTrial'), permission: 'accounting.view' }) }}</p>
  <template v-else>
    <Card class="mb-4">
      <form class="flex flex-wrap items-end gap-4 p-4" novalidate @submit.prevent="load">
        <FormField :hint="$weekday(range.from)" :label="t('statements.from')"><template #default="{ id }"><Input :id="id" v-model="range.from" name="from" type="date" /></template></FormField>
        <FormField :hint="$weekday(range.to)" :label="t('statements.to')"><template #default="{ id }"><Input :id="id" v-model="range.to" name="to" type="date" /></template></FormField>
        <Button type="submit" variant="outline" :disabled="busy" data-testid="apply">{{ t('statements.show') }}</Button>
      </form>
    </Card>
    <Card v-if="report">
      <CardContent class="pt-4">
        <p class="mb-3 mt-0 text-sm text-muted-foreground" data-testid="range">{{ t('statements.tbRange', { from: $date(report.from), to: $date(report.to) }) }}</p>
        <EmptyState v-if="!report.rows.length" :title="t('statements.tbEmpty')" data-testid="empty" />
        <div v-else class="overflow-x-auto">
          <table class="w-full border-collapse text-sm" data-testid="trial-balance">
            <thead class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              <tr>
                <th rowspan="2" class="border-b border-border px-3 py-2 text-left">{{ t('statements.account') }}</th>
                <th colspan="2" class="px-3 pt-2 text-center">{{ t('statements.opening') }}</th>
                <th colspan="2" class="px-3 pt-2 text-center">{{ t('statements.movement') }}</th>
                <th colspan="2" class="px-3 pt-2 text-center">{{ t('statements.closing') }}</th>
              </tr>
              <tr class="border-b border-border">
                <template v-for="n in 3" :key="n">
                  <th class="px-3 py-1 text-right">{{ t('statements.debit') }}</th><th class="px-3 py-1 text-right">{{ t('statements.credit') }}</th>
                </template>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in report.rows" :key="r.account_id" class="border-b border-border hover:bg-accent/50" :data-testid="`row-${r.code}`">
                <td class="px-3 py-2">{{ r.code }} · {{ r.name }}</td>
                <td class="px-3 py-2 text-right tabular-nums">{{ money(r.opening_debit) }}</td><td class="px-3 py-2 text-right tabular-nums">{{ money(r.opening_credit) }}</td>
                <td class="px-3 py-2 text-right tabular-nums">{{ money(r.debit) }}</td><td class="px-3 py-2 text-right tabular-nums">{{ money(r.credit) }}</td>
                <td class="px-3 py-2 text-right tabular-nums">{{ money(r.closing_debit) }}</td><td class="px-3 py-2 text-right tabular-nums">{{ money(r.closing_credit) }}</td>
              </tr>
            </tbody>
            <tfoot>
              <tr data-testid="totals" class="font-semibold">
                <th class="px-3 py-2 text-left">{{ t('statements.total') }}</th>
                <th class="px-3 py-2 text-right tabular-nums">{{ money(report.totals.opening_debit) }}</th><th class="px-3 py-2 text-right tabular-nums">{{ money(report.totals.opening_credit) }}</th>
                <th class="px-3 py-2 text-right tabular-nums">{{ money(report.totals.debit) }}</th><th class="px-3 py-2 text-right tabular-nums">{{ money(report.totals.credit) }}</th>
                <th class="px-3 py-2 text-right tabular-nums">{{ money(report.totals.closing_debit) }}</th><th class="px-3 py-2 text-right tabular-nums">{{ money(report.totals.closing_credit) }}</th>
              </tr>
            </tfoot>
          </table>
        </div>
      </CardContent>
    </Card>
  </template>
</template>
