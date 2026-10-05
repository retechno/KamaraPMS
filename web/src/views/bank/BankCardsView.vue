<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CardFeeRule, CardSettlement, ExpectedCardSettlements } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

type ExpectedLine = ExpectedCardSettlements['lines'][number]

const auth = useAuthStore()
const property = usePropertyStore()

const key = ref<'CARD' | 'OTHER_PAYMENT'>('CARD')
const expected = ref<ExpectedCardSettlements | null>(null)
const rules = ref<CardFeeRule[]>([])
const settlements = ref<CardSettlement[]>([])
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const loaded = ref(false)
const rule = reactive({ payment_method: 'CARD' as 'CARD' | 'OTHER', mdr_rate: '', vat_rate: '', settlement_days: '1', effective_from: '' })

const pid = computed(() => property.currentId)
const canView = computed(() => auth.can('bank.view', pid.value))
const canManage = computed(() => auth.can('bank.manage', pid.value))
const fieldError = (field: string) => error.value?.fieldMessage(field)

const lineColumns = computed<Column<ExpectedLine>[]>(() => [
  { key: 'journal_date', label: t('cards.date'), format: 'date' as const },
  { key: 'reference', label: t('cards.reference') },
  { key: 'amount', label: t('cards.amount'), align: 'right', format: 'money' as const },
  { key: 'mdr_rate', label: t('cards.rate'), align: 'right' },
  { key: 'expected_mdr', label: t('cards.expectedFee'), align: 'right', format: 'money' as const },
  { key: 'expected_vat', label: t('cards.expectedVat'), align: 'right' },
  { key: 'expected_net', label: t('cards.expectedNet'), align: 'right', format: 'money' as const },
  { key: 'expected_date', label: t('cards.due') },
])
const settlementColumns = computed<Column<CardSettlement>[]>(() => [
  { key: 'journal_date', label: t('cards.settlementDate'), format: 'date' as const },
  { key: 'journal_number', label: t('cards.settlementDate') },
  { key: 'gross', label: t('cards.settlementGross'), align: 'right', format: 'money' as const },
  { key: 'net', label: t('cards.settlementNet'), align: 'right', format: 'money' as const },
  { key: 'fee', label: t('cards.settlementFee'), align: 'right', format: 'money' as const },
  { key: 'expected_mdr', label: t('cards.settlementExpected'), align: 'right' },
  { key: 'mdr_variance', label: t('cards.variance'), align: 'right' },
  { key: 'payments', label: t('cards.payments'), align: 'right' },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canView.value) return
  error.value = null
  try {
    const path = { propertyId }
    const [e, r, s] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/bank/card-settlements/expected', { params: { path, query: { account_key: key.value } } }),
      api.GET('/api/v1/properties/{propertyId}/bank/card-fee-rules', { params: { path } }),
      api.GET('/api/v1/properties/{propertyId}/bank/card-settlements', { params: { path, query: { limit: 30 } } }),
    ])
    expected.value = e.data ?? null
    rules.value = r.data?.data ?? []
    settlements.value = s.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function addRule(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/bank/card-fee-rules', {
      params: { path: { propertyId } },
      body: { payment_method: rule.payment_method, mdr_rate: rule.mdr_rate.trim(), vat_rate: rule.vat_rate.trim() || undefined, settlement_days: Number(rule.settlement_days), effective_from: rule.effective_from },
    })
    notice.value = t('cards.ruleAdded')
    rule.mdr_rate = ''
    rule.vat_rate = ''
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => property.currentId, () => {
  expected.value = null
  loaded.value = false
  void load()
}, { immediate: true })
watch(key, () => void load())
</script>

<template>
  <PageHeader :title="t('cards.title')" :description="canView ? t('cards.intro') : undefined" />

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canView" class="muted" data-testid="no-access">{{ t('cards.noAccess', { permission: 'bank.view' }) }}</p>

  <template v-else>
    <Card class="mb-4" data-testid="expected">
      <CardHeader>
        <CardTitle>{{ t('cards.expectedTitle') }}</CardTitle>
      </CardHeader>
      <CardContent>
        <FormField class="mb-4 w-56" :label="t('cards.method')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="key" name="account_key">
              <option value="CARD">{{ t('cards.card') }}</option>
              <option value="OTHER_PAYMENT">{{ t('cards.ewallet') }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <template v-if="expected">
          <dl class="mb-3 flex flex-wrap gap-6 text-sm" data-testid="totals">
            <div><dt class="text-muted-foreground">{{ t('cards.gross') }}</dt><dd class="m-0 text-lg font-semibold tabular-nums">{{ $money(expected.gross) }}</dd></div>
            <div><dt class="text-muted-foreground">{{ t('cards.fee') }}</dt><dd class="m-0 text-lg font-semibold tabular-nums">{{ $money(expected.expected_mdr) }}</dd></div>
            <div><dt class="text-muted-foreground">{{ t('cards.vat') }}</dt><dd class="m-0 text-lg font-semibold tabular-nums" data-testid="expected-vat">{{ $money(expected.expected_vat) }}</dd></div>
            <div><dt class="text-muted-foreground">{{ t('cards.net') }}</dt><dd class="m-0 text-lg font-semibold tabular-nums">{{ $money(expected.expected_net) }}</dd></div>
          </dl>
          <p v-if="expected.late_count" class="mb-2 text-sm text-destructive" data-testid="late">{{ t('cards.lateHint', { count: expected.late_count, amount: $money(expected.late_gross) }) }}</p>
          <p v-if="expected.without_rate" class="mb-2 text-sm text-muted-foreground" data-testid="without-rate">{{ t('cards.withoutRate', { count: expected.without_rate }) }}</p>
          <p v-if="expected.without_vat_rate" class="mb-2 text-sm text-muted-foreground" data-testid="without-vat-rate">{{ t('cards.withoutVatRate', { count: expected.without_vat_rate }) }}</p>
          <EmptyState v-if="!expected.lines.length" :title="t('cards.nothing')" data-testid="empty" />
          <DataTable v-else :columns="lineColumns" :rows="expected.lines" row-key="journal_line_id" :row-test-id="(l) => `line-${l.reference}`" :caption="t('cards.expectedTitle')">
            <template #cell-mdr_rate="{ row }">{{ row.mdr_rate === null ? '-' : `${row.mdr_rate}%` }}</template>
            <template #cell-expected_vat="{ row }">
              <template v-if="row.mdr_rate === null">-</template>
              <Badge v-else-if="row.without_vat_rate" variant="outline" :data-testid="`no-vat-rate-${row.reference}`">{{ t('cards.withoutVatRateBadge') }}</Badge>
              <template v-else>{{ $money(row.expected_vat) }}<small v-if="row.vat_rate" class="text-muted-foreground"> ({{ row.vat_rate }}%)</small></template>
            </template>
            <template #cell-expected_date="{ row }">
              <template v-if="row.expected_date">{{ $date(row.expected_date) }} <Badge v-if="row.late" variant="destructive">{{ t('cards.late') }}</Badge></template>
              <template v-else>-</template>
            </template>
          </DataTable>
        </template>
      </CardContent>
    </Card>

    <Card class="mb-4" data-testid="rules">
      <CardHeader>
        <CardTitle>{{ t('cards.rulesTitle') }}</CardTitle>
        <p class="m-0 text-sm text-muted-foreground">{{ t('cards.rulesHint') }}</p>
      </CardHeader>
      <CardContent>
        <p v-if="loaded && !rules.length" class="mt-0 text-sm text-muted-foreground" data-testid="no-rules">{{ t('cards.noRules') }}</p>
        <ul v-else class="mt-0 list-none p-0 text-sm" data-testid="rule-list">
          <li v-for="r in rules" :key="r.id">{{ r.payment_method === 'CARD' ? t('cards.card') : t('cards.ewallet') }} · {{ r.mdr_rate }}%<template v-if="Number(r.vat_rate) > 0"> + {{ t('cards.vat') }} {{ r.vat_rate }}%</template> · {{ r.settlement_days }} · {{ $date(r.effective_from) }}</li>
        </ul>
        <form v-if="canManage" class="mt-3 flex flex-wrap items-end gap-3" novalidate data-testid="rule-form" @submit.prevent="addRule">
          <FormField class="w-40" :label="t('cards.ruleMethod')" :error="fieldError('payment_method')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="rule.payment_method" name="rule_method">
                <option value="CARD">{{ t('cards.card') }}</option>
                <option value="OTHER">{{ t('cards.ewallet') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField class="w-32" :label="t('cards.ruleRate')" :error="fieldError('mdr_rate')">
            <template #default="{ id }"><Input :id="id" v-model="rule.mdr_rate" name="mdr_rate" inputmode="decimal" /></template>
          </FormField>
          <FormField class="w-36" :label="t('cards.ruleVat')" :hint="t('cards.ruleVatHint')" :error="fieldError('vat_rate')">
            <template #default="{ id }"><Input :id="id" v-model="rule.vat_rate" name="vat_rate" inputmode="decimal" placeholder="0" /></template>
          </FormField>
          <FormField class="w-32" :label="t('cards.ruleDays')" :error="fieldError('settlement_days')">
            <template #default="{ id }"><Input :id="id" v-model="rule.settlement_days" name="settlement_days" inputmode="numeric" /></template>
          </FormField>
          <FormField class="w-44" :label="t('cards.ruleFrom')" :error="fieldError('effective_from')">
            <template #default="{ id }"><Input :id="id" v-model="rule.effective_from" name="effective_from" type="date" /></template>
          </FormField>
          <Button type="submit" :disabled="busy || !rule.mdr_rate.trim() || !rule.effective_from" data-testid="add-rule">{{ t('cards.addRule') }}</Button>
        </form>
      </CardContent>
    </Card>

    <Card data-testid="settlements">
      <CardHeader><CardTitle>{{ t('cards.settlementsTitle') }}</CardTitle></CardHeader>
      <CardContent>
        <EmptyState v-if="loaded && !settlements.length" :title="t('cards.noSettlements')" data-testid="no-settlements" />
        <DataTable v-else :columns="settlementColumns" :rows="settlements" row-key="id" :row-test-id="(s) => `settlement-${s.journal_number}`" :caption="t('cards.settlementsTitle')">
          <template #cell-expected_mdr="{ row }">{{ row.expected_mdr === null ? '-' : $money(row.expected_mdr) }}</template>
          <template #cell-mdr_variance="{ row }">{{ row.mdr_variance === null ? '-' : $money(row.mdr_variance) }}</template>
        </DataTable>
      </CardContent>
    </Card>
  </template>
</template>
