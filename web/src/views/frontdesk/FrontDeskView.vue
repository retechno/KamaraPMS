<script setup lang="ts">
import { CalendarPlus, DoorOpen } from 'lucide-vue-next'
import { computed, reactive } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatBusinessDate } from '@/utils/dates'
import ArrivalsView from './ArrivalsView.vue'
import DeparturesView from './DeparturesView.vue'
import InHouseView from './InHouseView.vue'

/**
 * The front desk in one place: arrivals, in-house and departures as tabs. Each tab keeps its own address
 * (/arrivals, /in-house, /departures), so the menu, bookmarks and links keep working; choosing a tab goes to that
 * address. All three lists load together, so the counts on the tabs are there at once.
 */
type Tab = 'arrivals' | 'in-house' | 'departures'

const props = defineProps<{ tab: Tab }>()

const PATHS: Record<Tab, string> = { arrivals: '/arrivals', 'in-house': '/in-house', departures: '/departures' }

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()

const counts = reactive<Record<Tab, string>>({ arrivals: '', 'in-house': '', departures: '' })

const pid = computed(() => property.currentId)
const can = (permission: string) => pid.value !== null && auth.can(permission, pid.value)
const businessDate = computed(() => (property.clock ? formatBusinessDate(property.clock.business_date) : ''))

const tabs = computed(() => [
  { value: 'arrivals' as const, label: t('frontDesk.page.arrivals') },
  { value: 'in-house' as const, label: t('frontDesk.page.inHouse') },
  { value: 'departures' as const, label: t('frontDesk.page.departures') },
])

function select(value: string | number): void {
  const path = PATHS[value as Tab]
  if (path && value !== props.tab) void router.push(path)
}

function setCount(tab: Tab, count: number, more = false): void {
  counts[tab] = `${count}${more ? '+' : ''}`
}
</script>

<template>
  <PageHeader :title="t('frontDesk.page.title')" :description="t('frontDesk.page.description')">
    <template v-if="businessDate" #marks>
      <Badge variant="secondary" data-testid="front-desk-date">{{ businessDate }}</Badge>
    </template>
    <template #actions>
      <Button v-if="can('frontdesk.checkin')" as-child size="sm" data-testid="walk-in">
        <RouterLink to="/walk-in"><DoorOpen />{{ t('frontDesk.page.walkIn') }}</RouterLink>
      </Button>
      <Button v-if="can('reservation.create')" as-child size="sm" variant="outline" data-testid="new-reservation">
        <RouterLink to="/reservations/new"><CalendarPlus />{{ t('frontDesk.page.newReservation') }}</RouterLink>
      </Button>
    </template>
  </PageHeader>

  <Tabs :model-value="tab" @update:model-value="select">
    <TabsList data-testid="front-desk-tabs">
      <TabsTrigger v-for="tb in tabs" :key="tb.value" :value="tb.value" :data-testid="`tab-${tb.value}`">
        {{ tb.label }}
        <Badge v-if="counts[tb.value]" variant="outline" :data-testid="`count-${tb.value}`">{{ counts[tb.value] }}</Badge>
      </TabsTrigger>
    </TabsList>
    <TabsContent value="arrivals" force-mount :class="tab !== 'arrivals' && 'hidden'" data-testid="panel-arrivals">
      <ArrivalsView @loaded="(n: number) => setCount('arrivals', n)" />
    </TabsContent>
    <TabsContent value="in-house" force-mount :class="tab !== 'in-house' && 'hidden'" data-testid="panel-in-house">
      <InHouseView @loaded="(n: number, more: boolean) => setCount('in-house', n, more)" />
    </TabsContent>
    <TabsContent value="departures" force-mount :class="tab !== 'departures' && 'hidden'" data-testid="panel-departures">
      <DeparturesView @loaded="(n: number, more: boolean) => setCount('departures', n, more)" />
    </TabsContent>
  </Tabs>
</template>
