<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

const store = usePropertyStore()
const columns = computed<Column<(typeof store.properties)[number]>[]>(() => [
  { key: 'code', label: t('properties.code') },
  { key: 'name', label: t('properties.name') },
  { key: 'city', label: t('properties.city') },
  { key: 'timezone', label: t('properties.timezone') },
  { key: 'currency', label: t('properties.currency') },
  { key: 'status', label: t('setup.status') },
])
onMounted(() => {
  if (!store.loaded) void store.loadProperties()
})
</script>

<template>
  <PageHeader :title="t('properties.title')">
    <template #actions>
      <RouterLink to="/setup/properties/new" custom v-slot="{ navigate }">
        <Button type="button" @click="navigate">{{ t('properties.new') }}</Button>
      </RouterLink>
    </template>
  </PageHeader>

  <ErrorNotice v-if="store.error" :error="store.error" inline />

  <Card>
    <EmptyState v-if="store.loaded && !store.hasProperties" :title="t('properties.empty')" :description="t('properties.emptyHint')" data-testid="empty" />
    <DataTable v-else :columns="columns" :rows="store.properties" row-key="id" :caption="t('properties.title')">
      <template #cell-code="{ row }"><RouterLink :to="`/setup/properties/${row.id}`" class="font-semibold text-primary hover:underline">{{ row.code }}</RouterLink></template>
      <template #cell-currency="{ row }">{{ row.currency_code }} ({{ t('properties.decimals', { n: row.currency_decimals }) }})</template>
      <template #cell-status="{ row }"><StatusBadge domain="record" :status="String(row.status).toUpperCase()" /></template>
    </DataTable>
  </Card>
</template>
