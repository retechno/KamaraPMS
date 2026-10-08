<script setup lang="ts">
import { computed } from 'vue'
import type { Arrival } from '@/api/types'
import ReadinessBadge from '@/components/ReadinessBadge.vue'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { type ArrivalAction, arrivalActions } from '@/utils/arrivals'

/** The detail of an arrival beside the list: guest, reservation, room, dates, rate, company, deposit and readiness. It only reads and offers the actions of the row. */
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ row: Arrival | null }>()
const emit = defineEmits<{ act: [action: ArrivalAction, row: Arrival] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const actions = computed(() => (props.row ? arrivalActions(props.row, (p) => auth.can(p, property.currentId)) : []))
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent v-if="row" class="p-6" data-testid="arrival-drawer">
      <SheetTitle class="text-lg" data-testid="drawer-guest">{{ row.guest_name || '—' }}</SheetTitle>
      <SheetDescription class="mt-1">{{ row.confirmation_number }} · {{ row.status }}</SheetDescription>

      <dl class="mt-5 grid gap-4 text-sm">
        <div>
          <dt class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('frontDesk.inHouse.sectionStay') }}</dt>
          <dd class="m-0 mt-1">
            <div>{{ $date(row.arrival_date) }} → {{ $date(row.departure_date) }}</div>
            <div class="text-muted-foreground">{{ t('frontDesk.inHouse.party', { a: row.adult_count, c: row.child_count }) }}</div>
            <div class="text-muted-foreground">{{ t('frontDesk.page.room') }} {{ row.room_number || t('frontDesk.arrivals.noRoom') }} · {{ row.room_type_name || row.room_type_code }}</div>
          </dd>
        </div>
        <div>
          <dt class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('frontDesk.arrivals.readinessTitle') }}</dt>
          <dd class="m-0 mt-1"><ReadinessBadge :readiness="row.readiness" /></dd>
        </div>
        <div>
          <dt class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('frontDesk.inHouse.sectionRate') }}</dt>
          <dd class="m-0 mt-1" data-testid="drawer-rate">
            <div>{{ row.rate.rate_plan_code }} · {{ row.rate.rate_plan_name }}</div>
            <div class="tabular-nums">{{ row.rate.amount ? $money(row.rate.amount) : '—' }} {{ t('frontDesk.inHouse.perNight') }}</div>
          </dd>
        </div>
        <div>
          <dt class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('frontDesk.inHouse.sectionCompany') }}</dt>
          <dd class="m-0 mt-1" data-testid="drawer-company">{{ row.company ? row.company.name : t('frontDesk.inHouse.noCompany') }}</dd>
        </div>
        <div>
          <dt class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('frontDesk.arrivals.deposit') }}</dt>
          <dd class="m-0 mt-1 tabular-nums" data-testid="drawer-deposit">{{ row.deposit ? $money(row.deposit.paid) : t('frontDesk.arrivals.noDeposit') }}</dd>
        </div>
      </dl>

      <div class="mt-6 flex flex-wrap gap-2" data-testid="drawer-actions">
        <Button v-for="a in actions" :key="a" type="button" size="sm" :variant="a === 'checkIn' ? 'default' : 'outline'" :data-testid="`drawer-${a}`" @click="emit('act', a, row)">
          {{ t(`frontDesk.arrivals.action.${a}`) }}
        </Button>
      </div>
    </SheetContent>
  </Sheet>
</template>
