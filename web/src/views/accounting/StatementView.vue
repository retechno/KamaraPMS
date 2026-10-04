<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CashFlow, StatementLine } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'
import { downloadCsv } from './reportApi'
import StatementTable from './StatementTable.vue'

/** The income statement (USALI layout, over a range) and the balance sheet (as of a date): one screen, told apart by the route. */
const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const income = computed(() => route.name === 'accounting-income-statement')
const cashFlow = computed(() => route.name === 'accounting-cash-flow')
const ranged = computed(() => income.value || cashFlow.value)
const title = computed(() => (income.value ? t('statements.income') : cashFlow.value ? t('statements.cashFlow') : t('statements.balance')))
const lines = ref<StatementLine[]>([])
const heading = ref('')
const imbalance = ref('')
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const form = reactive({ from: '', to: '', as_of: '', method: 'INDIRECT' })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  busy.value = true
  error.value = null
  try {
    if (cashFlow.value) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/cash-flow', {
        params: { path: { propertyId }, query: { from: form.from || undefined, to: form.to || undefined, method: form.method === 'DIRECT' ? 'DIRECT' : undefined } },
      })
      const cf = data as CashFlow | undefined
      lines.value = cf?.lines ?? []
      heading.value = cf ? t('statements.rangeIncome', { from: cf.from, to: cf.to }) : ''
      imbalance.value = cf && !cf.reconciled ? cf.difference : ''
    } else if (income.value) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/income-statement', {
        params: { path: { propertyId }, query: { from: form.from || undefined, to: form.to || undefined } },
      })
      lines.value = data?.lines ?? []
      heading.value = data ? t('statements.rangeIncome', { from: data.from, to: data.to }) : ''
      imbalance.value = ''
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/balance-sheet', { params: { path: { propertyId }, query: { as_of: form.as_of || undefined } } })
      lines.value = data?.lines ?? []
      heading.value = data ? t('statements.rangeBalance', { date: data.as_of }) : ''
      imbalance.value = data && Number(data.difference) !== 0 ? data.difference : ''
    }
  } catch (e) {
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
    if (cashFlow.value) {
      await downloadCsv('/api/v1/properties/{propertyId}/accounting/cash-flow', { path: { propertyId }, query: { from: form.from || undefined, to: form.to || undefined, method: form.method === 'DIRECT' ? 'DIRECT' : undefined } }, 'cash-flow.csv')
    } else if (income.value) {
      await downloadCsv('/api/v1/properties/{propertyId}/accounting/income-statement', { path: { propertyId }, query: { from: form.from || undefined, to: form.to || undefined } }, 'income-statement.csv')
    } else {
      await downloadCsv('/api/v1/properties/{propertyId}/accounting/balance-sheet', { path: { propertyId }, query: { as_of: form.as_of || undefined } }, 'balance-sheet.csv')
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function showPdf(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    if (cashFlow.value) await openPdf(documentPath.accounting(propertyId, 'cash-flow', { from: form.from, to: form.to, method: form.method === 'DIRECT' ? 'DIRECT' : undefined }))
    else if (income.value) await openPdf(documentPath.accounting(propertyId, 'income-statement', { from: form.from, to: form.to }))
    else await openPdf(documentPath.accounting(propertyId, 'balance-sheet', { as_of: form.as_of }))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch([() => pid.value, income, cashFlow], () => {
  lines.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="title">
    <template #actions>
      <template v-if="loaded && lines.length">
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
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('statements.noAccess', { what: t('statements.whatStatements'), permission: 'accounting.view' }) }}</p>
  <template v-else>
    <Card class="mb-4">
      <form class="flex flex-wrap items-end gap-4 p-4" novalidate @submit.prevent="load">
        <template v-if="ranged">
          <FormField :label="t('statements.from')"><template #default="{ id }"><Input :id="id" v-model="form.from" name="from" type="date" /></template></FormField>
          <FormField :label="t('statements.to')"><template #default="{ id }"><Input :id="id" v-model="form.to" name="to" type="date" /></template></FormField>
          <FormField v-if="cashFlow" :label="t('statements.method')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.method" name="method">
                <option value="INDIRECT">{{ t('statements.methodIndirect') }}</option>
                <option value="DIRECT">{{ t('statements.methodDirect') }}</option>
              </NativeSelect>
            </template>
          </FormField>
        </template>
        <FormField v-else :label="t('statements.asOf')"><template #default="{ id }"><Input :id="id" v-model="form.as_of" name="as_of" type="date" /></template></FormField>
        <Button type="submit" variant="outline" :disabled="busy" data-testid="apply">{{ t('statements.show') }}</Button>
      </form>
    </Card>
    <Card v-if="loaded">
      <CardContent class="pt-4">
        <p class="mb-3 mt-0 text-sm text-muted-foreground" data-testid="range">{{ heading }}<template v-if="income"> · {{ t('statements.usali') }}</template><template v-if="cashFlow"> · {{ form.method === 'DIRECT' ? t('statements.direct') : t('statements.indirect') }}</template></p>
        <p v-if="imbalance" class="alert" data-testid="imbalance">{{ cashFlow ? t('statements.cashDifference', { amount: imbalance }) : t('statements.imbalance', { amount: imbalance }) }}</p>
        <EmptyState v-if="!lines.length" :title="t('statements.empty')" data-testid="empty" />
        <StatementTable v-else :lines="lines" />
        <p v-if="!ranged" class="mb-0 mt-3 text-sm text-muted-foreground">{{ t('statements.equityNote') }}</p>
      </CardContent>
    </Card>
  </template>
</template>
