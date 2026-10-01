<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GlAccount, GlAccountMapEntry, GlCodeReport } from '@/api/types'
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
  ADVANCE_DEPOSITS: 'LIABILITY', TAX_PAYABLE: 'LIABILITY', SERVICE_PAYABLE: 'LIABILITY', RETAINED_EARNINGS: 'EQUITY',
}
const options = (key: string) => accounts.value.filter((a) => a.is_postable && a.is_active && (!NEEDS[key] || a.account_type === NEEDS[key]))
const changed = computed(() => entries.value.filter((e) => choice[e.map_key] !== e.account_id))
const PROBLEM: Record<string, string> = {
  NO_CODE: 'has no account code', UNKNOWN_ACCOUNT: 'names an account that is not in the chart', INACTIVE_ACCOUNT: 'names an inactive account',
  HEADER_ACCOUNT: 'names a header account', WRONG_TYPE: 'names an account of the wrong type',
}
const KIND: Record<string, { label: string; to: string }> = {
  CHARGE_CODE: { label: 'Charge code', to: '/setup/charge-codes' }, TAX: { label: 'Tax', to: '/setup/taxes' }, SERVICE_CHARGE: { label: 'Service charge', to: '/setup/taxes' },
}

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
    notice.value = 'Saved.'
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
  <div class="page-head">
    <h1 class="page-title">System accounts</h1>
    <RouterLink to="/accounting/accounts">Chart of accounts</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="map-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-if="error.fieldErrors?.length"><br /><span v-for="(f, i) in error.fieldErrors" :key="i" class="muted">{{ f.field }}: {{ f.message }}<br /></span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">Your role at this property cannot see accounting: the <code>accounting.view</code> permission is needed.</p>

  <template v-else>
    <section class="card">
      <p class="muted">The accounts the day close posts to. Payments are received into the account of their method; a transfer to a company goes to the city ledger; deposits stay in the deposit account until the guest checks out.</p>
      <table class="list" data-testid="map">
        <thead><tr><th>What</th><th>Account</th></tr></thead>
        <tbody>
          <tr v-for="e in entries" :key="e.map_key" :data-testid="`map-${e.map_key}`">
            <td><b>{{ e.meaning }}</b> <small class="muted">{{ e.map_key }}</small></td>
            <td>
              <select v-if="can('accounting.manage')" v-model.number="choice[e.map_key]" :name="`map_${e.map_key}`">
                <option v-for="a in options(e.map_key)" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
              </select>
              <template v-else>{{ e.account_code }} · {{ e.account_name }}</template>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-if="can('accounting.manage')" class="form-actions">
        <button type="button" class="btn-primary" :disabled="busy || !changed.length" data-testid="save-map" @click="save">Save changes</button>
      </div>
    </section>

    <section v-if="report" class="card" data-testid="unmapped">
      <h2>Charge codes, taxes and service charges</h2>
      <p v-if="!report.issues.length" class="muted" data-testid="all-mapped">All {{ report.checked }} active items are mapped to an account of the chart.</p>
      <template v-else>
        <p class="muted">These cannot be placed in the chart, so their amounts go to the account shown until their account code is fixed ({{ report.issues.length }} of {{ report.checked }}).</p>
        <table class="list">
          <thead><tr><th>Item</th><th>Problem</th><th>Posted to</th></tr></thead>
          <tbody>
            <tr v-for="i in report.issues" :key="`${i.kind}-${i.id}`" :data-testid="`issue-${i.kind}-${i.code}`">
              <td>{{ KIND[i.kind]?.label }} <RouterLink :to="KIND[i.kind]?.to ?? '/'"><b>{{ i.code }}</b></RouterLink> {{ i.name }}</td>
              <td>{{ i.gl_account_code ? `${i.gl_account_code}: ` : '' }}{{ PROBLEM[i.problem] }}</td>
              <td>{{ i.posted_to }}</td>
            </tr>
          </tbody>
        </table>
      </template>
    </section>
  </template>
</template>
