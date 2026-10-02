<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GlAccount, GlAccountMapEntry, GlCodeReport } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { listAccounts } from './accountApi'

const auth = useAuthStore()
const property = usePropertyStore()

const entries = ref<GlAccountMapEntry[]>([])
const accounts = ref<GlAccount[]>([])
const report = ref<GlCodeReport | null>(null)
const choice = reactive<Record<string, number>>({})
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
// What each key may point at: an account that takes postings, is active, and is of the type the key needs.
const NEEDS: Record<string, string> = {
  CASH: 'ASSET', CARD: 'ASSET', BANK_TRANSFER: 'ASSET', OTHER_PAYMENT: 'ASSET', CITY_LEDGER: 'ASSET', GUEST_LEDGER: 'ASSET',
  ADVANCE_DEPOSITS: 'LIABILITY', TAX_PAYABLE: 'LIABILITY', SERVICE_PAYABLE: 'LIABILITY', RETAINED_EARNINGS: 'EQUITY', ACCOUNTS_PAYABLE: 'LIABILITY',
}
const options = (key: string) => accounts.value.filter((a) => a.is_postable && a.is_active && (!NEEDS[key] || a.account_type === NEEDS[key]))
const changed = computed(() => entries.value.filter((e) => choice[e.map_key] !== e.account_id))
const problemText = (p: string): string => t(`accounting.p_${p}` as 'accounting.p_NO_CODE')
const KIND: Record<string, { label: 'accounting.k_CHARGE_CODE' | 'accounting.k_TAX' | 'accounting.k_SERVICE_CHARGE'; to: string }> = {
  CHARGE_CODE: { label: 'accounting.k_CHARGE_CODE', to: '/setup/charge-codes' }, TAX: { label: 'accounting.k_TAX', to: '/setup/taxes' }, SERVICE_CHARGE: { label: 'accounting.k_SERVICE_CHARGE', to: '/setup/taxes' },
}
const kindLabel = (k: string): string => (KIND[k] ? t(KIND[k].label) : k)
type Issue = GlCodeReport['issues'][number]
const mapColumns = computed<Column<GlAccountMapEntry>[]>(() => [
  { key: 'meaning', label: t('accounting.amWhat') },
  { key: 'account', label: t('accounting.amAccount') },
])
const issueColumns = computed<Column<Issue>[]>(() => [
  { key: 'item', label: t('accounting.amItem') },
  { key: 'problem', label: t('accounting.amProblem') },
  { key: 'posted_to', label: t('accounting.amPostedTo') },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    const path = { path: { propertyId } }
    const [m, a, r] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/accounting/account-map', { params: path }),
      listAccounts(propertyId, { postable: true }),
      api.GET('/api/v1/properties/{propertyId}/accounting/unmapped', { params: path }),
    ])
    entries.value = m.data?.data ?? []
    accounts.value = a
    report.value = r.data ?? null
    for (const e of entries.value) choice[e.map_key] = e.account_id
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.PUT('/api/v1/properties/{propertyId}/accounting/account-map', {
      params: { path: { propertyId } }, body: { entries: changed.value.map((e) => ({ map_key: e.map_key, account_id: choice[e.map_key] as number })) },
    })
    notice.value = t('accounting.saved')
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => void load(), { immediate: true })
</script>

<template>
  <PageHeader :title="t('accounting.amTitle')">
    <template #actions><RouterLink to="/accounting/accounts" class="text-sm text-primary hover:underline">{{ t('accounting.amChart') }}</RouterLink></template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="map-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-if="error.fieldErrors?.length"><br /><span v-for="(f, i) in error.fieldErrors" :key="i" class="muted">{{ f.field }}: {{ f.message }}<br /></span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('accounting.noAccessView', { what: t('accounting.whatAccounting'), permission: 'accounting.view' }) }}</p>

  <template v-else>
    <Card class="mb-4">
      <CardContent class="pt-4">
        <p class="mb-3 mt-0 text-sm text-muted-foreground">{{ t('accounting.amIntro') }}</p>
        <DataTable :columns="mapColumns" :rows="entries" row-key="map_key" :row-test-id="(e) => `map-${e.map_key}`" :caption="t('accounting.amTitle')" data-testid="map">
          <template #cell-meaning="{ row }"><b>{{ row.meaning }}</b> <small class="text-muted-foreground">{{ row.map_key }}</small></template>
          <template #cell-account="{ row }">
            <Combobox v-if="can('accounting.manage')" v-model="choice[row.map_key]" class="max-w-sm" :name="`map_${row.map_key}`" :options="[...options(row.map_key).map((a) => ({ value: a.id, label: `${a.code} · ${a.name}` }))]" />
            <template v-else>{{ row.account_code }} · {{ row.account_name }}</template>
          </template>
        </DataTable>
        <div v-if="can('accounting.manage')" class="mt-4 flex justify-end">
          <Button type="button" :disabled="busy || !changed.length" data-testid="save-map" @click="save">{{ t('accounting.amSave') }}</Button>
        </div>
      </CardContent>
    </Card>

    <Card v-if="report" data-testid="unmapped">
      <CardHeader><CardTitle>{{ t('accounting.amItems') }}</CardTitle></CardHeader>
      <CardContent>
        <p v-if="!report.issues.length" class="m-0 text-sm text-muted-foreground" data-testid="all-mapped">{{ t('accounting.amAllMapped', { n: report.checked }) }}</p>
        <template v-else>
          <p class="mb-3 mt-0 text-sm text-muted-foreground">{{ t('accounting.amIssuesHint', { n: report.issues.length, total: report.checked }) }}</p>
          <DataTable :columns="issueColumns" :rows="report.issues" :row-key="(i) => `${i.kind}-${i.id}`" :row-test-id="(i) => `issue-${i.kind}-${i.code}`" :caption="t('accounting.amItems')">
            <template #cell-item="{ row }">{{ kindLabel(row.kind) }} <RouterLink :to="KIND[row.kind]?.to ?? '/'" class="text-primary hover:underline"><b>{{ row.code }}</b></RouterLink> {{ row.name }}</template>
            <template #cell-problem="{ row }">{{ row.gl_account_code ? `${row.gl_account_code}: ` : '' }}{{ problemText(row.problem) }}</template>
          </DataTable>
        </template>
      </CardContent>
    </Card>
  </template>
</template>
