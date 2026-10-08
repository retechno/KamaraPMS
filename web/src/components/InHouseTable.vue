<script setup lang="ts">
import { MoreVertical } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import type { GuestView, InHouseRow } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import CheckoutStatus from '@/components/CheckoutStatus.vue'
import EditRateDialog from '@/components/EditRateDialog.vue'
import GuestEditDialog from '@/components/GuestEditDialog.vue'
import InHouseBalance from '@/components/InHouseBalance.vue'
import InHouseDrawer from '@/components/InHouseDrawer.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { type InHouseAction, MORE_ACTIONS, PRIMARY_ACTIONS, actionTarget, actionsFor, fullName } from '@/utils/inHouse'

/**
 * The table of stays of the front desk that the In-house and Departures tabs share: columns, row actions, the detail drawer, the guest editor and the rate editor. The tab owns the loading,
 * the filters and the paging; this only shows the rows it is given and tells what was saved (`guestSaved` with the new name, `rateSaved` so the tab reloads).
 */
const props = withDefaults(defineProps<{ rows: InHouseRow[]; loaded: boolean; mode?: 'in-house' | 'departures'; businessDate?: string }>(), { mode: 'in-house', businessDate: '' })
const emit = defineEmits<{ guestSaved: [guestId: number, name: string]; rateSaved: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()

// The drawer and the dialogs work on a row by its id, so an edit that changes the rows shows in them at once.
const drawerOpen = ref(false)
const selectedId = ref<number | null>(null)
const selected = computed(() => props.rows.find((r) => r.id === selectedId.value) ?? null)
const editGuestId = ref<number | null>(null)
const guestOpen = ref(false)
const rateOpen = ref(false)
const rateRowId = ref<number | null>(null)
const rateRow = computed(() => props.rows.find((r) => r.id === rateRowId.value) ?? null)

const can = (permission: string) => auth.can(permission, property.currentId)
const actionsOf = (row: InHouseRow) => actionsFor(row, can)
const primary = (row: InHouseRow) => actionsOf(row).filter((a) => PRIMARY_ACTIONS.includes(a))
const more = (row: InHouseRow) => actionsOf(row).filter((a) => MORE_ACTIONS.includes(a))
const overdue = (row: InHouseRow) => props.mode === 'departures' && !!props.businessDate && row.stay.departure_date < props.businessDate
const scopeLabel = (b: InHouseRow['billing'][number]) => `${t(`frontDesk.inHouse.scope.${b.scope}`)}${b.charge_code ? ` ${b.charge_code}` : ''} → ${b.company_name}`

const columns = computed<Column<InHouseRow>[]>(() => [
  { key: 'room_number', label: t('frontDesk.page.room'), sortable: true, filter: 'text' as const, sortValue: (r) => r.room.number, filterValue: (r) => r.room.number },
  { key: 'guest_name', label: t('frontDesk.page.guest'), sortable: true, filter: 'text' as const, sortValue: (r) => r.guest.name, filterValue: (r) => `${r.guest.name} ${r.stay_number}` },
  { key: 'company', label: t('frontDesk.inHouse.company'), sortable: true, sortValue: (r) => r.company?.name },
  { key: 'rate', label: t('frontDesk.inHouse.rate'), align: 'right', sortable: true, sortValue: (r) => (r.rate.amount ? Number(r.rate.amount) : null), class: 'tabular-nums' },
  { key: 'stay', label: t('frontDesk.page.stay'), sortable: true, sortValue: (r) => (props.mode === 'departures' ? r.stay.departure_date : r.stay.arrival_date) },
  { key: 'balance', label: t('frontDesk.inHouse.balance'), align: 'right', sortable: true, sortValue: (r) => (r.balance.amount ? Number(r.balance.amount) : null) },
  { key: 'actions', label: t('frontDesk.inHouse.actions'), align: 'right' },
])

function openDetail(row: InHouseRow): void {
  selectedId.value = row.id
  drawerOpen.value = true
}

/** One handler for the row, its menu, the drawer and the cards: an action done in place opens its dialog, the others go to the page that does them. */
function act(action: InHouseAction, row: InHouseRow): void {
  if (action === 'editGuest') {
    editGuestId.value = row.guest.id
    guestOpen.value = true
    return
  }
  if (action === 'editRate') {
    rateRowId.value = row.id
    rateOpen.value = true
    return
  }
  const target = actionTarget(row, action)
  if (target) void router.push(target)
}

/** The guest was saved: the tab shows the new name on every row of that guest, without reloading the list. */
function guestSaved(g: GuestView): void {
  const name = fullName(g)
  emit('guestSaved', g.id, name)
  toast.success(t('frontDesk.inHouse.guestSaved', { name }))
}

function rateSaved(): void {
  toast.success(t('frontDesk.rateEdit.saved'))
  emit('rateSaved')
}
</script>

<template>
  <div>
    <DataTable class="hidden md:block" :columns="columns" :rows="rows" row-key="id" :loading="!loaded" :row-test-id="(s) => `stay-${s.stay_number}`" :caption="mode === 'departures' ? t('frontDesk.page.departures') : t('frontDesk.page.inHouse')">
      <template #cell-room_number="{ row }">
        <div class="font-semibold">{{ row.room.number }}</div>
        <div class="text-xs text-muted-foreground" :title="row.room.room_type_name">{{ row.room.room_type_code }}</div>
      </template>
      <template #cell-guest_name="{ row }">
        <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-left font-medium text-primary hover:underline" :data-testid="`open-${row.stay_number}`" :aria-label="t('frontDesk.inHouse.openDetail', { guest: row.guest.name })" @click="openDetail(row)">{{ row.guest.name }}</button>
        <div class="text-xs text-muted-foreground">
          <RouterLink :to="`/stays/${row.id}`">{{ row.stay_number }}</RouterLink> · {{ row.confirmation_number }}
        </div>
      </template>
      <template #cell-company="{ row }">
        <template v-if="row.company">
          <div :data-testid="`company-${row.stay_number}`">{{ row.company.name }}</div>
          <div v-if="row.billing.length" class="text-xs text-muted-foreground" :title="row.billing.map(scopeLabel).join(' / ')">
            {{ [...new Set(row.billing.map((b) => t(`frontDesk.inHouse.scope.${b.scope}`)))].join(' · ') }}
          </div>
        </template>
        <span v-else class="text-muted-foreground" :data-testid="`company-${row.stay_number}`">{{ t('frontDesk.inHouse.noCompany') }}</span>
      </template>
      <template #cell-rate="{ row }">
        <div :data-testid="`rate-${row.stay_number}`">{{ row.rate.amount ? $money(row.rate.amount) : '—' }}</div>
        <div class="text-xs text-muted-foreground" :title="row.rate.rate_plan_name">{{ row.rate.rate_plan_code || '—' }}</div>
      </template>
      <template #cell-stay="{ row }">
        <div class="whitespace-nowrap">
          {{ $date(row.stay.arrival_date) }} → {{ $date(row.stay.departure_date) }}
          <Badge v-if="overdue(row)" variant="warning" class="ml-1" data-testid="overdue">{{ t('frontDesk.page.overdue') }}</Badge>
        </div>
        <div class="text-xs text-muted-foreground">{{ t('frontDesk.inHouse.nights', { n: row.stay.nights }) }} · {{ row.stay.adults }}+{{ row.stay.children }}</div>
      </template>
      <template #cell-balance="{ row }">
        <div :data-testid="`balance-${row.stay_number}`"><InHouseBalance :balance="row.balance" /></div>
        <div v-if="row.balance.folios.length > 1" class="mt-0.5 text-xs text-muted-foreground tabular-nums">
          <div v-for="f in row.balance.folios" :key="f.id">{{ f.folio_type === 'COMPANY' ? t('frontDesk.inHouse.companyFolio') : t('frontDesk.inHouse.guestFolio') }} {{ $money(f.balance) }}</div>
        </div>
        <div v-if="mode === 'departures'" class="mt-0.5"><CheckoutStatus :checkout="row.checkout" :data-testid="`checkout-status-${row.stay_number}`" /></div>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex flex-wrap items-center justify-end gap-1.5">
          <Button v-for="a in primary(row)" :key="a" type="button" size="sm" :variant="a === 'checkOut' ? 'default' : 'outline'" :data-testid="`${a}-${row.stay_number}`" @click="act(a, row)">{{ t(`frontDesk.inHouse.action.${a}`) }}</Button>
          <Popover v-if="more(row).length">
            <PopoverTrigger as-child>
              <Button type="button" size="sm" variant="ghost" :aria-label="t('frontDesk.inHouse.moreActions')" :data-testid="`more-${row.stay_number}`"><MoreVertical /></Button>
            </PopoverTrigger>
            <PopoverContent class="w-44 p-1">
              <div :data-testid="`menu-${row.stay_number}`">
                <Button v-for="a in more(row)" :key="a" type="button" variant="ghost" size="sm" class="w-full justify-start" :data-testid="`${a}-${row.stay_number}`" @click="act(a, row)">{{ t(`frontDesk.inHouse.action.${a}`) }}</Button>
              </div>
            </PopoverContent>
          </Popover>
        </div>
      </template>
      <template #empty><EmptyState :title="mode === 'departures' ? t('frontDesk.page.noDepartures') : t('frontDesk.page.nobodyInHouse')" data-testid="empty" /></template>
    </DataTable>

    <!-- A narrow screen gets a card for each stay instead of the table. -->
    <ul class="m-0 grid list-none gap-3 p-0 md:hidden" data-testid="in-house-cards">
      <li v-for="row in rows" :key="row.id" class="rounded-lg border border-border p-3" :data-testid="`card-${row.stay_number}`">
        <div class="flex items-start justify-between gap-2">
          <div>
            <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-left font-medium text-primary" @click="openDetail(row)">{{ row.guest.name }}</button>
            <div class="text-sm text-muted-foreground">{{ t('frontDesk.page.room') }} {{ row.room.number }} · {{ row.room.room_type_code }}</div>
          </div>
          <InHouseBalance :balance="row.balance" />
        </div>
        <div class="mt-1 text-sm">{{ $date(row.stay.arrival_date) }} → {{ $date(row.stay.departure_date) }}</div>
        <div v-if="row.company" class="text-sm text-muted-foreground">{{ row.company.name }}</div>
        <div v-if="mode === 'departures'" class="mt-1"><CheckoutStatus :checkout="row.checkout" /></div>
        <div class="mt-2 flex flex-wrap gap-1.5">
          <Button v-for="a in actionsOf(row)" :key="a" type="button" size="sm" variant="outline" @click="act(a, row)">{{ t(`frontDesk.inHouse.action.${a}`) }}</Button>
        </div>
      </li>
    </ul>

    <InHouseDrawer v-model:open="drawerOpen" :row="selected" :mode="mode" @act="act" />
    <GuestEditDialog v-model:open="guestOpen" :guest-id="editGuestId" @saved="guestSaved" />
    <EditRateDialog v-model:open="rateOpen" :row="rateRow" @saved="rateSaved" />
  </div>
</template>
