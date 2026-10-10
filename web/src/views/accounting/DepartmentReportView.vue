<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { DepartmentNode, DepartmentReport } from '@/api/types'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
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

/**
 * Revenue, expenses and departmental profit of a range by department. A department adds up its sub-departments; what has no department is shown
 * apart. A row opens to the accounts that were posted to it directly.
 */
const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<DepartmentReport | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const open = ref<Set<string>>(new Set())
const form = reactive({ from: '', to: '', department_id: null as number | null })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const query = () => ({ from: form.from || undefined, to: form.to || undefined, department_id: form.department_id ?? undefined })
const key = (n: DepartmentNode): string => String(n.id)
const toggle = (n: DepartmentNode): void => {
  const next = new Set(open.value)
  if (!next.delete(key(n))) next.add(key(n))
  open.value = next
}
const hasUnassigned = computed(() => (report.value?.unassigned.accounts.length ?? 0) > 0)
const rows = computed(() => {
  const out: { node: DepartmentNode; label: string; level: number }[] = []
  for (const d of report.value?.departments ?? []) {
    out.push({ node: d, label: `${d.code} · ${d.name}`, level: d.level })
    for (const c of d.children) out.push({ node: c, label: `${c.code} · ${c.name}`, level: 2 })
  }
  return out
})

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/department-report', { params: { path: { propertyId }, query: query() } })
    report.value = (data as DepartmentReport | undefined) ?? null
  } catch (e) {
    report.value = null
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
    loaded.value = true
  }
}

async function exportCsv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    await downloadCsv('/api/v1/properties/{propertyId}/accounting/department-report', { path: { propertyId }, query: query() }, 'department-report.csv')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function showPdf(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const base = documentPath.accounting(propertyId, 'department-report', { from: form.from, to: form.to })
  const url = form.department_id === null ? base : `${base}${base.includes('?') ? '&' : '?'}department_id=${form.department_id}`
  try {
    await openPdf(url)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch(() => pid.value, () => {
  report.value = null
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('departments.reportTitle')" :description="t('departments.reportIntro')">
    <template #actions>
      <RouterLink to="/accounting/departments" class="text-sm text-primary hover:underline">{{ t('departments.title') }}</RouterLink>
      <template v-if="report">
        <Button type="button" variant="outline" data-testid="pdf" @click="showPdf">{{ t('statements.pdf') }}</Button>
        <Button type="button" variant="outline" data-testid="export" @click="exportCsv">{{ t('statements.export') }}</Button>
      </template>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline data-testid="report-error" />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('departments.noAccess', { permission: 'accounting.view' }) }}</p>
  <template v-else>
    <Card class="mb-4">
      <form class="flex flex-wrap items-end gap-x-4 gap-y-6 px-4 pb-8 pt-4" novalidate data-testid="filters" @submit.prevent="load">
        <FormField float-hint :hint="$weekday(form.from)" :label="t('statements.from')"><template #default="{ id }"><Input :id="id" v-model="form.from" name="from" type="date" /></template></FormField>
        <FormField float-hint :hint="$weekday(form.to)" :label="t('statements.to')"><template #default="{ id }"><Input :id="id" v-model="form.to" name="to" type="date" /></template></FormField>
        <FormField :label="t('departments.title')">
          <template #default="{ id }"><DepartmentSelect :id="id" v-model="form.department_id" name="department_id" :none-label="t('departments.all')" /></template>
        </FormField>
        <Button type="submit" variant="outline" :disabled="busy" data-testid="apply">{{ t('statements.show') }}</Button>
      </form>
    </Card>
    <Card v-if="loaded">
      <CardContent class="pt-4">
        <EmptyState v-if="!report" :title="t('departments.reportEmpty')" data-testid="empty" />
        <template v-else>
          <p class="mb-3 mt-0 text-sm text-muted-foreground" data-testid="range">{{ $date(report.from) }} – {{ $date(report.to) }} · {{ t('departments.reportBasis') }}</p>
          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-sm" data-testid="report">
              <thead>
                <tr class="border-b border-border text-xs uppercase tracking-wide text-muted-foreground">
                  <th class="py-2 pr-2 text-left">{{ t('departments.name') }}</th>
                  <th class="px-2 py-2 text-right">{{ t('departments.revenue') }}</th>
                  <th class="px-2 py-2 text-right">{{ t('departments.expense') }}</th>
                  <th class="px-2 py-2 text-right">{{ t('departments.profit') }}</th>
                </tr>
              </thead>
              <tbody>
                <template v-for="r in rows" :key="r.node.id">
                  <tr class="cursor-pointer border-b border-border" :class="r.level === 1 ? 'font-semibold' : ''" :data-testid="`dept-${r.node.code}`" @click="toggle(r.node)">
                    <td class="py-1.5 pr-2" :class="r.level === 2 ? 'pl-6' : ''">
                      <button type="button" class="mr-1 inline-block w-4 text-muted-foreground" :aria-expanded="open.has(key(r.node))" :aria-label="t('departments.showAccounts', { name: r.node.name })" :data-testid="`toggle-${r.node.code}`" @click.stop="toggle(r.node)">{{ open.has(key(r.node)) ? '▾' : '▸' }}</button>{{ r.label }}
                    </td>
                    <td class="px-2 text-right tabular-nums">{{ money(r.node.revenue) }}</td>
                    <td class="px-2 text-right tabular-nums">{{ money(r.node.expense) }}</td>
                    <td class="px-2 text-right tabular-nums" :class="Number(r.node.profit) < 0 ? 'text-destructive' : ''">{{ money(r.node.profit) }}</td>
                  </tr>
                  <tr v-for="a in open.has(key(r.node)) ? r.node.accounts : []" :key="`${r.node.id}-${a.account_id}`" class="text-muted-foreground" :data-testid="`account-${r.node.code}-${a.code}`">
                    <td class="py-1 pr-2" :class="r.level === 2 ? 'pl-14' : 'pl-8'"><span class="tabular-nums">{{ a.code }}</span> {{ a.name }}</td>
                    <td class="px-2 text-right tabular-nums">{{ a.account_type === 'REVENUE' ? money(a.amount) : '' }}</td>
                    <td class="px-2 text-right tabular-nums">{{ a.account_type === 'EXPENSE' ? money(a.amount) : '' }}</td>
                    <td />
                  </tr>
                </template>
                <template v-if="hasUnassigned">
                  <tr class="cursor-pointer border-b border-border italic" data-testid="dept-unassigned" @click="toggle(report.unassigned)">
                    <td class="py-1.5 pr-2">
                      <button type="button" class="mr-1 inline-block w-4 text-muted-foreground" :aria-expanded="open.has(key(report.unassigned))" :aria-label="t('departments.showAccounts', { name: t('departments.unassigned') })" data-testid="toggle-unassigned" @click.stop="toggle(report.unassigned)">{{ open.has(key(report.unassigned)) ? '▾' : '▸' }}</button>{{ t('departments.unassigned') }}
                    </td>
                    <td class="px-2 text-right tabular-nums">{{ money(report.unassigned.revenue) }}</td>
                    <td class="px-2 text-right tabular-nums">{{ money(report.unassigned.expense) }}</td>
                    <td class="px-2 text-right tabular-nums">{{ money(report.unassigned.profit) }}</td>
                  </tr>
                  <tr v-for="a in open.has(key(report.unassigned)) ? report.unassigned.accounts : []" :key="`u-${a.account_id}`" class="text-muted-foreground" :data-testid="`account-unassigned-${a.code}`">
                    <td class="py-1 pl-8 pr-2"><span class="tabular-nums">{{ a.code }}</span> {{ a.name }}</td>
                    <td class="px-2 text-right tabular-nums">{{ a.account_type === 'REVENUE' ? money(a.amount) : '' }}</td>
                    <td class="px-2 text-right tabular-nums">{{ a.account_type === 'EXPENSE' ? money(a.amount) : '' }}</td>
                    <td />
                  </tr>
                </template>
              </tbody>
              <tfoot>
                <tr class="border-t-2 border-foreground font-semibold" data-testid="totals">
                  <td class="py-1.5 pr-2">{{ t('departments.total') }}</td>
                  <td class="px-2 text-right tabular-nums">{{ money(report.totals.revenue) }}</td>
                  <td class="px-2 text-right tabular-nums">{{ money(report.totals.expense) }}</td>
                  <td class="px-2 text-right tabular-nums">{{ money(report.totals.profit) }}</td>
                </tr>
              </tfoot>
            </table>
          </div>
        </template>
      </CardContent>
    </Card>
  </template>
</template>
