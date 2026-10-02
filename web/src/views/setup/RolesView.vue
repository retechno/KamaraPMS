<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { t } from '@/i18n'

type Role = components['schemas']['Role']

const roles = ref<Role[]>([])
const error = ref<ApiError | null>(null)
const columns = computed<Column<Role>[]>(() => [
  { key: 'name', label: t('roles.name') },
  { key: 'description', label: t('roles.description') },
  { key: 'permissions', label: t('roles.permissions'), align: 'right' },
])

onMounted(async () => {
  try {
    roles.value = (await api.GET('/api/v1/roles')).data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
})
</script>

<template>
  <PageHeader :title="t('roles.title')">
    <template #actions>
      <RouterLink to="/setup/roles/new" custom v-slot="{ navigate }">
        <Button type="button" @click="navigate">{{ t('roles.new') }}</Button>
      </RouterLink>
    </template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert">{{ error.message }}</p>
  <Card>
    <EmptyState v-if="!roles.length" :title="t('roles.empty')" :description="t('roles.emptyHint')" />
    <DataTable v-else :columns="columns" :rows="roles" row-key="id" :caption="t('roles.title')">
      <template #cell-name="{ row }"><RouterLink :to="`/setup/roles/${row.id}`" class="font-semibold text-primary hover:underline">{{ row.name }}</RouterLink></template>
      <template #cell-permissions="{ row }">{{ row.permissions.length }}</template>
    </DataTable>
  </Card>
</template>
