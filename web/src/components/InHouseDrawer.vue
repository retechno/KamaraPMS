<script setup lang="ts">
import { computed } from 'vue'
import type { InHouseRow } from '@/api/types'
import InHouseBalance from '@/components/InHouseBalance.vue'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { t } from '@/i18n'
import { type InHouseAction, actionsFor } from '@/utils/inHouse'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/** The detail of an in-house stay beside the list, so the list keeps its place. It only reads and offers the actions of the row; nothing is posted from here. */
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ row: InHouseRow | null }>()
const emit = defineEmits<{ act: [action: InHouseAction, row: InHouseRow] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const actions = computed(() => (props.row ? actionsFor(props.row, (p) => auth.can(p, property.currentId)) : []))
const folioLabel = (type: string) => (type === 'COMPANY' ? t('frontDesk.inHouse.companyFolio') : t('frontDesk.inHouse.guestFolio'))
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent v-if="row" class="p-6" data-testid="in-house-drawer">
      <SheetTitle class="text-lg" data-testid="drawer-guest">{{ row.guest.name }}</SheetTitle>
      <SheetDescription class="mt-1" data-testid="drawer-room">
        {{ t('frontDesk.page.room') }} {{ row.room.number }} · {{ row.room.room_type_name }} · {{ row.stay_number }}
      </SheetDescription>

      <dl class="mt-5 grid gap-4 text-sm">
        <div>
          <dt class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('frontDesk.inHouse.sectionStay') }}</dt>
          <dd class="m-0 mt-1">
            <div>{{ $date(row.stay.arrival_date) }} → {{ $date(row.stay.departure_date) }} · {{ t('frontDesk.inHouse.nights', { n: row.stay.nights }) }}</div>
            <div class="text-muted-foreground">{{ t('frontDesk.inHouse.party', { a: row.stay.adults, c: row.stay.children }) }}</div>
            <div class="text-muted-foreground">{{ t('frontDesk.inHouse.confirmation') }} {{ row.confirmation_number }}</div>
          </dd>
        </div>
        <div>
          <dt class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('frontDesk.inHouse.sectionRate') }}</dt>
          <dd class="m-0 mt-1" data-testid="drawer-rate">
            <div>{{ row.rate.rate_plan_code }} · {{ row.rate.rate_plan_name }}</div>
            <div class="tabular-nums">{{ $money(row.rate.amount) }} {{ t('frontDesk.inHouse.perNight') }}<span v-if="row.rate.is_override" class="text-muted-foreground"> · {{ t('frontDesk.inHouse.override') }}</span></div>
          </dd>
        </div>
        <div>
          <dt class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('frontDesk.inHouse.sectionCompany') }}</dt>
          <dd class="m-0 mt-1" data-testid="drawer-company">
            <template v-if="row.company">
              <div>{{ row.company.name }}</div>
              <ul class="m-0 list-none p-0 text-muted-foreground">
                <li v-for="b in row.billing" :key="`${b.scope}-${b.charge_code ?? ''}-${b.company_id}`">{{ t(`frontDesk.inHouse.scope.${b.scope}`) }}<template v-if="b.charge_code"> {{ b.charge_code }}</template> → {{ b.company_name }}</li>
              </ul>
            </template>
            <template v-else>{{ t('frontDesk.inHouse.noCompany') }}</template>
          </dd>
        </div>
        <div>
          <dt class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('frontDesk.inHouse.sectionBalance') }}</dt>
          <dd class="m-0 mt-1">
            <InHouseBalance :balance="row.balance" />
            <ul class="m-0 mt-2 list-none p-0" data-testid="drawer-folios">
              <li v-for="f in row.balance.folios" :key="f.id" class="flex justify-between gap-3">
                <span>{{ folioLabel(f.folio_type) }} <span class="text-muted-foreground">{{ f.folio_number }}</span></span>
                <span class="tabular-nums">{{ $money(f.balance) }}</span>
              </li>
            </ul>
          </dd>
        </div>
      </dl>

      <div class="mt-6 flex flex-wrap gap-2" data-testid="drawer-actions">
        <Button v-for="a in actions" :key="a" type="button" size="sm" :variant="a === 'checkOut' ? 'default' : 'outline'" :data-testid="`drawer-${a}`" @click="emit('act', a, row)">
          {{ t(`frontDesk.inHouse.action.${a}`) }}
        </Button>
      </div>
    </SheetContent>
  </Sheet>
</template>
