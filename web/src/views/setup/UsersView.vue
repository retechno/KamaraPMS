<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { t } from '@/i18n'

type User = components['schemas']['User']

const users = ref<User[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const columns = computed<Column<User>[]>(() => [
  { key: 'full_name', label: t('users.name'), sortable: true, filter: 'text' as const },
  { key: 'email', label: t('users.email'), sortable: true, filter: 'text' as const },
  { key: 'access', label: t('users.access') },
  { key: 'status', label: t('setup.status'), filter: 'select' as const, filterValue: (r: User) => (r.is_active ? t('setup.active') : t('setup.inactive')) },
])

onMounted(async () => {
  try {
    const { data } = await api.GET('/api/v1/users', { params: { query: { limit: 200 } } })
    users.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
})

function access(u: User): string {
  if (u.is_tenant_admin) return t('users.allProperties')
  if (!u.grants.length) return t('users.noAccess')
  return u.grants.map((g) => `${g.property_code}: ${g.role_name}`).join(', ')
}
</script>

<template>
  <PageHeader :title="t('users.title')">
    <template #actions>
      <RouterLink to="/setup/users/new" custom v-slot="{ navigate }">
        <Button type="button" @click="navigate">{{ t('users.new') }}</Button>
      </RouterLink>
    </template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert">{{ error.message }}</p>
  <Card>
    <DataTable :columns="columns" :rows="users" row-key="id" :loading="!loaded" :caption="t('users.title')">
      <template #cell-full_name="{ row }"><RouterLink :to="`/setup/users/${row.id}`" class="font-semibold text-primary hover:underline">{{ row.full_name }}</RouterLink></template>
      <template #cell-access="{ row }">{{ access(row) }}</template>
      <template #cell-status="{ row }"><Badge :variant="row.is_active ? 'success' : 'outline'">{{ row.is_active ? t('setup.active') : t('setup.inactive') }}</Badge></template>
    </DataTable>
  </Card>
</template>
