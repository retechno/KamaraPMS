<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CityLedgerAccount } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const accounts = ref<CityLedgerAccount[]>([])
const nextCursor = ref<string | undefined>()
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const owingOnly = ref(true)
const q = ref('')

const columns = computed<Column<CityLedgerAccount>[]>(() => [
  { key: 'company', label: t('cityLedger.company') },
  { key: 'transferred', label: t('cityLedger.transferred'), align: 'right' },
  { key: 'received', label: t('cityLedger.received'), align: 'right' },
  { key: 'balance', label: t('cityLedger.balance'), align: 'right' },
  { key: 'credit_limit', label: t('cityLedger.creditLimit'), align: 'right' },
  { key: 'available', label: t('cityLedger.available'), align: 'right' },
])

const canRead = computed(() => auth.can('cityledger.read', property.currentId))

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts', {
      params: { path: { propertyId }, query: { limit: 50, cursor: more ? nextCursor.value : undefined, owing: owingOnly.value ? true : undefined, q: q.value.trim() || undefined } },
    })
    accounts.value = more ? [...accounts.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

watch(() => property.currentId, () => {
  accounts.value = []
  loaded.value = false
  void load()
}, { immediate: true })
watch(owingOnly, () => void load())
</script>

<template>
  <PageHeader :title="t('cityLedger.title')" :description="canRead ? t('cityLedger.intro') : undefined" />

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('cityLedger.noAccess', { permission: 'cityledger.read' }) }}</p>

  <Card v-else>
    <CardContent class="pt-4">
      <form class="mb-4 flex flex-wrap items-end gap-3" novalidate data-testid="filters" @submit.prevent="load()">
        <FormField class="w-64" :label="t('cityLedger.company')">
          <template #default="{ id }"><Input :id="id" v-model="q" name="q" type="search" :placeholder="t('cityLedger.searchPlaceholder')" /></template>
        </FormField>
        <label class="flex items-center gap-2 pb-2 text-sm">
          <input v-model="owingOnly" type="checkbox" name="owing" class="size-4 accent-primary" />
          <span>{{ t('cityLedger.owingOnly') }}</span>
        </label>
        <Button type="submit" variant="outline">{{ t('cityLedger.search') }}</Button>
      </form>
      <EmptyState v-if="loaded && !accounts.length" :title="owingOnly ? t('cityLedger.emptyOwing') : t('cityLedger.empty')" data-testid="empty" />
      <DataTable v-else-if="accounts.length" :columns="columns" :rows="accounts" row-key="company_id" :row-test-id="(a) => `account-${a.code}`" :caption="t('cityLedger.title')">
        <template #cell-company="{ row }">
          <RouterLink :to="`/city-ledger/${row.company_id}`" class="text-primary hover:underline"><b>{{ row.code }}</b></RouterLink> {{ row.name }}
          <small v-if="!row.is_active" class="text-muted-foreground">{{ t('cityLedger.inactive') }}</small>
        </template>
        <template #cell-balance="{ row }"><b>{{ row.balance }}</b></template>
        <template #cell-credit_limit="{ row }">{{ row.credit_limit ?? t('cityLedger.noLimit') }}</template>
        <template #cell-available="{ row }">{{ row.available ?? '-' }}</template>
      </DataTable>
      <div v-if="nextCursor" class="mt-3 flex justify-center">
        <Button type="button" variant="outline" data-testid="more" @click="load(true)">{{ t('cityLedger.more') }}</Button>
      </div>
    </CardContent>
  </Card>
</template>
