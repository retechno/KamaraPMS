<script setup lang="ts">
import { CalendarPlus, DoorOpen, MoonStar } from 'lucide-vue-next'
import { computed, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { useSystemStore, type ComponentStatus } from '@/stores/system'
import { formatBusinessDate, wallClock } from '@/utils/dates'
import ManagerDashboard from '@/views/dashboard/ManagerDashboard.vue'

const system = useSystemStore()
const property = usePropertyStore()
const auth = useAuthStore()
onMounted(() => system.check())

const pid = computed(() => property.currentId)
const can = (permission: string) => pid.value !== null && auth.can(permission, pid.value)

const local = computed(() => (property.clock ? wallClock(property.clock.property_local_time) : null))
const auditFrom = computed(() => {
  const c = property.clock
  if (!c || !property.current) return ''
  // Shown in the property's zone: the rule is "from HH:MM local on the business date".
  return t('dashboard.page.allowedFrom', { time: property.current.night_audit_earliest_time, date: formatBusinessDate(c.business_date) })
})

const label = (s: ComponentStatus): string => t(`dashboard.page.${s}` as never)
const variant = (s: ComponentStatus) => (s === 'up' ? ('success' as const) : s === 'down' ? ('destructive' as const) : ('outline' as const))
</script>

<template>
  <PageHeader :title="t('dashboard.page.title')">
    <template v-if="property.current" #marks>
      <Badge variant="secondary" data-testid="property-name">{{ property.current.name }}</Badge>
    </template>
    <template #actions>
      <Button v-if="can('reservation.create')" as-child size="sm" data-testid="quick-new-reservation">
        <RouterLink to="/reservations/new"><CalendarPlus />{{ t('dashboard.page.newReservation') }}</RouterLink>
      </Button>
      <Button v-if="can('frontdesk.checkin')" as-child size="sm" variant="outline" data-testid="quick-walk-in">
        <RouterLink to="/walk-in"><DoorOpen />{{ t('dashboard.page.walkIn') }}</RouterLink>
      </Button>
      <Button v-if="can('nightaudit.run')" as-child size="sm" variant="outline" data-testid="quick-night-audit">
        <RouterLink to="/night-audit"><MoonStar />{{ t('dashboard.page.runNightAudit') }}</RouterLink>
      </Button>
    </template>
  </PageHeader>

  <ManagerDashboard />

  <div class="grid gap-4 md:grid-cols-2">
    <Card v-if="property.clock && property.current" data-testid="business-day-card">
      <CardHeader><CardTitle>{{ t('dashboard.page.businessDay') }} · {{ property.current.name }}</CardTitle></CardHeader>
      <CardContent>
        <p class="m-0 mb-3 text-2xl font-semibold tracking-tight">{{ formatBusinessDate(property.clock.business_date) }}</p>
        <dl class="m-0 divide-y divide-border text-sm">
          <div class="flex justify-between gap-3 py-2">
            <dt class="font-medium">{{ t('dashboard.page.localTime') }}</dt>
            <dd class="m-0 text-right">{{ local ? `${formatBusinessDate(local.date)} ${local.time}` : '—' }} ({{ property.clock.timezone }})</dd>
          </div>
          <div class="flex justify-between gap-3 py-2">
            <dt class="font-medium">{{ t('dashboard.page.nightAudit') }}</dt>
            <dd class="m-0 text-right">{{ property.clock.night_audit_allowed ? t('dashboard.page.canRunNow') : auditFrom }}</dd>
          </div>
        </dl>
      </CardContent>
    </Card>
    <Card v-else-if="property.loaded && !property.hasProperties">
      <CardHeader><CardTitle>{{ t('dashboard.page.setupTitle') }}</CardTitle></CardHeader>
      <CardContent>
        <p class="m-0 mb-3 text-sm text-muted-foreground">{{ t('dashboard.page.setupBody') }}</p>
        <RouterLink to="/setup/properties/new">{{ t('dashboard.page.createProperty') }}</RouterLink>
      </CardContent>
    </Card>

    <Card aria-labelledby="status-title">
      <CardHeader class="flex-row items-center justify-between">
        <CardTitle id="status-title">{{ t('dashboard.page.systemStatus') }}</CardTitle>
        <Button variant="outline" size="sm" :disabled="system.checking" @click="system.check()">
          {{ system.checking ? t('dashboard.page.checking') : t('common.refresh') }}
        </Button>
      </CardHeader>
      <CardContent>
        <dl class="m-0 divide-y divide-border text-sm">
          <div class="flex items-center justify-between py-2">
            <dt class="font-medium">{{ t('dashboard.page.api') }}</dt>
            <dd class="m-0"><Badge :variant="variant(system.apiStatus)" data-testid="api-status">{{ label(system.apiStatus) }}</Badge></dd>
          </div>
          <div class="flex items-center justify-between py-2">
            <dt class="font-medium">{{ t('dashboard.page.database') }}</dt>
            <dd class="m-0"><Badge :variant="variant(system.databaseStatus)" data-testid="db-status">{{ label(system.databaseStatus) }}</Badge></dd>
          </div>
        </dl>
        <p v-if="system.lastError" class="m-0 mt-3 text-sm text-destructive" role="alert">
          {{ system.lastError.message }}
          <code>{{ system.lastError.code }}</code>
          <span v-if="system.lastError.requestId"> · {{ t('dashboard.page.request', { id: system.lastError.requestId }) }}</span>
        </p>
        <p v-if="system.checkedAt" class="m-0 mt-3 text-sm text-muted-foreground">{{ t('dashboard.page.lastChecked', { time: system.checkedAt.toLocaleTimeString() }) }}</p>
      </CardContent>
    </Card>
  </div>
</template>
