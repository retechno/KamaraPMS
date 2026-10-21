<script setup lang="ts">
import PageHeader from '@/components/app/PageHeader.vue'
import ReportIndex from '@/components/inventory/ReportIndex.vue'
import { t } from '@/i18n'

/** "Inventory reports": every report of the inventory, grouped (docs/architecture/35-inventory.md 22). */
</script>

<template>
  <PageHeader :title="t('inventory.home.reports')" :description="t('inventory.home.reportsIntro')" />
  <ReportIndex />
</template>
