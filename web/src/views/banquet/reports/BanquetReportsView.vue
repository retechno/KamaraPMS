<script setup lang="ts">
import { RouterLink } from 'vue-router'
import PageHeader from '@/components/app/PageHeader.vue'
import { Card } from '@/components/ui/card'
import { t } from '@/i18n'

/**
 * The index of the reports of the Banquet module (docs/architecture/36-banquet.md 4.10): one card for each report, with what it answers. The report of the sessions not posted is the one of the day close.
 */
const cards = [
  { id: 'forecast', to: '/banquet/reports/forecast' },
  { id: 'rooms', to: '/banquet/reports/rooms' },
  { id: 'guests', to: '/banquet/reports/guests' },
  { id: 'sales', to: '/banquet/reports/sales' },
  { id: 'lost', to: '/banquet/reports/lost' },
  { id: 'schedule', to: '/banquet/reports/schedule' },
  { id: 'unposted', to: '/banquet/unposted' },
] as const
</script>

<template>
  <PageHeader :title="t('banquet.reports.title')" :description="t('banquet.reports.intro')" />
  <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3" data-testid="banquet-reports">
    <RouterLink v-for="c in cards" :key="c.id" :to="c.to" class="block no-underline" :data-testid="`report-card-${c.id}`">
      <Card class="h-full p-4 transition-colors hover:bg-muted/50">
        <h2 class="m-0 mb-1 text-base font-semibold text-foreground">{{ t(`banquet.reports.cards.${c.id}.title`) }}</h2>
        <p class="m-0 text-sm text-muted-foreground">{{ t(`banquet.reports.cards.${c.id}.text`) }}</p>
      </Card>
    </RouterLink>
  </div>
</template>
