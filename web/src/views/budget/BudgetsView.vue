<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Budget, FiscalYear } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addYears, yearLabel } from './budgetMath'

/** The budgets of the property: a list of versions by fiscal year, and the form that starts a draft (empty, or a copy of a version). */
const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()

const budgets = ref<Budget[]>([])
const fiscalYears = ref<FiscalYear[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const creating = ref(false)
const form = reactive({ year_start: '', name: '', description: '', copy_from_id: '' })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

const columns = computed<Column<Budget>[]>(() => [
  { key: 'year_label', label: t('budget.year') },
  { key: 'version', label: t('budget.version') },
  { key: 'name', label: t('budget.name') },
  { key: 'status', label: t('budget.statusLabel') },
  { key: 'total_revenue', label: t('budget.revenue'), align: 'right', format: 'money' as const },
  { key: 'total_expense', label: t('budget.expense'), align: 'right', format: 'money' as const },
  { key: 'total_result', label: t('budget.result'), align: 'right', format: 'money' as const },
  { key: 'actions', label: '', align: 'right' },
])

/** The fiscal years a draft can be made for: the years of the books and the two after the latest (the server allows two). */
const yearOptions = computed(() => {
  const starts = fiscalYears.value.map((y) => y.year_start)
  const newest = starts[0]
  if (newest) starts.unshift(addYears(newest, 2), addYears(newest, 1))
  return starts.map((s) => ({ value: s, label: yearLabel(s) }))
})
const copySource = computed(() => budgets.value.find((b) => String(b.id) === form.copy_from_id))
const label = (b: Budget) => t('budget.versionLabel', { year: b.year_label, version: b.version, name: b.name })

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('budget.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/budgets', { params: { path: { propertyId } } })
    budgets.value = data?.data ?? []
    if (can('accounting.view')) {
      const years = await api.GET('/api/v1/properties/{propertyId}/accounting/fiscal-years', { params: { path: { propertyId } } })
      fiscalYears.value = years.data?.data ?? []
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startCreating(): void {
  Object.assign(form, { year_start: fiscalYears.value[0]?.year_start ?? '', name: '', description: '', copy_from_id: '' })
  error.value = null
  creating.value = true
}

async function create(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const copy = copySource.value
    const { data } = await api.POST('/api/v1/properties/{propertyId}/budgets', {
      params: { path: { propertyId } },
      body: { name: form.name.trim(), description: form.description.trim(), ...(copy ? { copy_from_id: copy.id } : { year_start: form.year_start }) },
    })
    if (data) await router.push(`/budget/${data.id}`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  budgets.value = []
  loaded.value = false
  creating.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('budget.title')" :description="t('budget.intro')">
    <template #actions>
      <RouterLink v-if="can('budget.view')" to="/budget/vs-actual" class="text-sm text-primary hover:underline" data-testid="to-report">{{ t('budget.vsActualTitle') }}</RouterLink>
      <Button v-if="can('budget.manage') && !creating" type="button" data-testid="new-budget" @click="startCreating">{{ t('budget.new') }}</Button>
    </template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert" data-testid="budget-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in error.fieldErrors ?? []" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('budget.view')" class="muted" data-testid="no-access">{{ t('budget.noAccess', { permission: 'budget.view' }) }}</p>
  <template v-else>
    <Card v-if="creating" class="mb-4">
      <form v-autofocus novalidate data-testid="new-form" @submit.prevent="create">
        <CardHeader><CardTitle>{{ t('budget.newTitle') }}</CardTitle></CardHeader>
        <CardContent class="flex flex-col gap-4">
          <div class="grid gap-4 sm:grid-cols-2">
            <FormField :label="t('budget.year')" :hint="copySource ? t('budget.copyKeepsYear') : t('budget.yearHint')">
              <template #default="{ id }">
                <NativeSelect v-if="yearOptions.length" :id="id" v-model="form.year_start" name="year_start" :disabled="!!copySource">
                  <option v-for="y in yearOptions" :key="y.value" :value="y.value">{{ y.label }} ({{ y.value }})</option>
                </NativeSelect>
                <Input v-else :id="id" v-model="form.year_start" name="year_start" type="date" :disabled="!!copySource" />
              </template>
            </FormField>
            <FormField :label="t('budget.copyFrom')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.copy_from_id" name="copy_from_id">
                  <option value="">{{ t('budget.startEmpty') }}</option>
                  <option v-for="b in budgets" :key="b.id" :value="String(b.id)">{{ label(b) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('budget.name')" required>
              <template #default="{ id }"><Input :id="id" v-model="form.name" name="name" maxlength="100" /></template>
            </FormField>
            <FormField :label="t('budget.description')">
              <template #default="{ id }"><Input :id="id" v-model="form.description" name="description" maxlength="500" /></template>
            </FormField>
          </div>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="creating = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !form.name.trim() || (!form.year_start && !copySource)" data-testid="create">{{ t('budget.createDraft') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
    <Card>
      <EmptyState v-if="loaded && !budgets.length" :title="t('budget.empty')" :description="t('budget.emptyHint')" data-testid="empty" />
      <DataTable v-else :columns="columns" :rows="budgets" row-key="id" :loading="!loaded" :row-test-id="(b) => `budget-${b.id}`" :caption="t('budget.title')" data-testid="budgets">
        <template #cell-year_label="{ row }"><b>{{ row.year_label }}</b></template>
        <template #cell-version="{ row }">v{{ row.version }}</template>
        <template #cell-status="{ row }">
          <Badge :variant="row.status === 'ACTIVE' ? 'success' : row.status === 'DRAFT' ? 'secondary' : 'outline'" :data-status="row.status">{{ t(`budget.status.${row.status}`) }}</Badge>
        </template>
        <template #cell-actions="{ row }">
          <RouterLink :to="`/budget/${row.id}`" class="text-primary hover:underline" :data-testid="`open-${row.id}`">{{ row.status === 'DRAFT' && can('budget.manage') ? t('budget.edit') : t('budget.view') }}</RouterLink>
        </template>
      </DataTable>
    </Card>
  </template>
</template>
