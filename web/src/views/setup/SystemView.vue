<script setup lang="ts">
import { onMounted } from 'vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { t } from '@/i18n'
import { useSystemStore, type ComponentStatus } from '@/stores/system'

/**
 * Whether the API and the database answer: for the administrator (the route is for administrators), not for the front desk. Down is said only when nothing answered or the server
 * said it is not well; an answer that is not the one of a health check (a proxy that does not pass the route on) is "cannot be checked", which is not an outage.
 */
const system = useSystemStore()
onMounted(() => void system.check())

const label = (s: ComponentStatus): string => t(`system.${s}` as never)
const variant = (s: ComponentStatus) => (s === 'up' ? ('success' as const) : s === 'down' ? ('destructive' as const) : s === 'unverified' ? ('warning' as const) : ('outline' as const))
</script>

<template>
  <PageHeader :title="t('system.title')" :description="t('system.description')">
    <template #actions>
      <Button variant="outline" size="sm" :disabled="system.checking" data-testid="system-refresh" @click="system.check()">
        {{ system.checking ? t('system.checking') : t('common.refresh') }}
      </Button>
    </template>
  </PageHeader>

  <Card aria-labelledby="status-title" data-testid="system-status-card">
    <CardContent class="pt-6">
      <h2 id="status-title" class="sr-only">{{ t('system.title') }}</h2>
      <dl class="m-0 divide-y divide-border text-sm">
        <div class="flex items-center justify-between py-2">
          <dt class="font-medium">{{ t('system.api') }}</dt>
          <dd class="m-0"><Badge :variant="variant(system.apiStatus)" data-testid="api-status">{{ label(system.apiStatus) }}</Badge></dd>
        </div>
        <div class="flex items-center justify-between py-2">
          <dt class="font-medium">{{ t('system.database') }}</dt>
          <dd class="m-0"><Badge :variant="variant(system.databaseStatus)" data-testid="db-status">{{ label(system.databaseStatus) }}</Badge></dd>
        </div>
      </dl>
      <p v-if="system.lastError" class="m-0 mt-3 text-sm text-destructive" role="alert">
        {{ system.lastError.message }}
        <code>{{ system.lastError.code }}</code>
        <span v-if="system.lastError.requestId"> · {{ t('system.request', { id: system.lastError.requestId }) }}</span>
      </p>
      <p v-if="system.checkedAt" class="m-0 mt-3 text-sm text-muted-foreground">{{ t('system.lastChecked', { time: system.checkedAt.toLocaleTimeString() }) }}</p>
    </CardContent>
  </Card>
</template>
