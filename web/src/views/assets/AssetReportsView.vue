<script setup lang="ts">
import { RouterLink } from 'vue-router'
import PageHeader from '@/components/app/PageHeader.vue'
import { Card } from '@/components/ui/card'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { t } from '@/i18n'

/** The index of the reports of the fixed assets (docs/architecture/37-fixed-assets.md 4.12): each one has its CSV file and its print page. */
const { canRead } = useAssetAccess()
const reports = [
  { key: 'register', to: '/assets/reports/register' },
  { key: 'schedule', to: '/assets/reports/schedule' },
  { key: 'recap', to: '/assets/reports/recap' },
  { key: 'locations', to: '/assets/reports/locations' },
  { key: 'expiring', to: '/assets/reports/expiring' },
  { key: 'audit-differences', to: '/assets/reports/audit-differences' },
  { key: 'fiscal', to: '/assets/reports/fiscal' },
] as const
</script>

<template>
  <section class="flex flex-col gap-4" data-testid="asset-reports">
    <PageHeader :title="t('assets.reports.title')" :description="t('assets.reports.intro')" />
    <p v-if="!canRead" class="muted m-0" data-testid="reports-denied">{{ t('assets.reports.denied') }}</p>
    <div v-else class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      <RouterLink v-for="r in reports" :key="r.key" :to="r.to" class="no-underline" :data-testid="`report-${r.key}`">
        <Card class="flex h-full flex-col gap-1 p-4 hover:border-primary">
          <b class="text-sm">{{ t(`assets.reports.kind.${r.key}.name`) }}</b>
          <span class="text-xs text-muted-foreground">{{ t(`assets.reports.kind.${r.key}.about`) }}</span>
        </Card>
      </RouterLink>
    </div>
  </section>
</template>
