<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryAmenityRun } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatDateTime, formatMoney } from '@/utils/format'
import { formatQty } from '@/utils/inventory'
import { postedVariant, ruleText } from '@/utils/movements'

/**
 * The amenities of one night (docs/architecture/35-inventory.md 5.9, 8.11, mockup 33): the document the close made for the rooms that were slept in: how many rooms and guests, the cost, the item that was short, the
 * rule, the quantity, what the shelf held and the cost of every line, the rooms and the journal. A close that fell in a count holds its lines (they say so); the document is corrected as a whole with a reversing document.
 */
const route = useRoute()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const date = computed(() => String(route.params.date))

const run = ref<InventoryAmenityRun | null>(null)
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const showRooms = ref(false)
const reverseOpen = ref(false)
const reason = ref('')

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/amenities/runs/{date}', { params: { path: { propertyId, date: date.value } } })
    run.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, date], load, { immediate: true })

async function reverse(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !run.value) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/amenities/runs/{date}/reverse', { params: { path: { propertyId, date: date.value } }, body: { reason: reason.value.trim() } })
    if (data) {
      run.value = data
      notice.value = t('inventory.amenityRun.reversed')
      reverseOpen.value = false
      reason.value = ''
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

const rule = (l: InventoryAmenityRun['lines'][number]): string => ruleText(l.qty_per, l.multiplier, l.basis, l.room_type_name, t('inventory.amenityRun.unitRoom'), t('inventory.amenityRun.unitGuest'))
const shortLines = computed(() => run.value?.short ?? [])
const shortQty = (l: InventoryAmenityRun['lines'][number]): string => String(Math.max(Number(l.qty_stock) - Math.max(Number(l.stock_before ?? 0), 0), 0))
</script>

<template>
  <PageHeader :title="t('inventory.amenityRun.title', { date: formatDate(date) })" :description="run ? t('inventory.amenityRun.madeBy', { when: formatDateTime(run.created_at) }) : ''">
    <template v-if="run" #marks><Badge variant="outline" data-testid="run-number">{{ run.iss_number }}</Badge> <Badge :variant="postedVariant(run.status)" data-testid="run-status">{{ t(`inventory.postedStatus.${run.status}`) }}</Badge></template>
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/issues" data-testid="back">{{ t('inventory.issue.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <div v-else-if="loaded && run" class="flex flex-col gap-4" data-testid="run-view">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="run-error" />
    <p v-if="run.held_lines > 0" class="alert warning m-0" data-testid="held-note">{{ t('inventory.amenityRun.held', { n: run.held_lines, count: run.held_by_count ?? '' }) }}</p>
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" data-testid="run-cards">
      <div class="rounded-xl border border-border bg-card p-3"><div class="text-xs text-muted-foreground">{{ t('inventory.amenityRun.rooms') }}</div><div class="text-lg font-semibold" data-testid="run-rooms">{{ run.rooms }}</div><div class="text-xs text-muted-foreground">{{ t('inventory.amenityRun.roomsDetail', { comp: run.comp_rooms, house: run.house_use_rooms }) }}</div></div>
      <div class="rounded-xl border border-border bg-card p-3"><div class="text-xs text-muted-foreground">{{ t('inventory.amenityRun.guests') }}</div><div class="text-lg font-semibold" data-testid="run-guests">{{ run.guests }}</div><div class="text-xs text-muted-foreground">{{ t('inventory.amenityRun.guestsDetail') }}</div></div>
      <div class="rounded-xl border border-border bg-card p-3"><div class="text-xs text-muted-foreground">{{ t('inventory.amenityRun.cost') }}</div><div class="text-lg font-semibold" data-testid="run-total">{{ run.held_lines > 0 ? '—' : formatMoney(run.total_value) }}</div><div class="text-xs text-muted-foreground">{{ t('inventory.amenityRun.perRoom', { cost: formatMoney(run.cost_per_room) }) }}</div></div>
      <div class="rounded-xl border border-border bg-card p-3"><div class="text-xs text-muted-foreground">{{ t('inventory.amenityRun.short') }}</div><div class="text-lg font-semibold" data-testid="run-short-count">{{ shortLines.length }}</div><div v-if="shortLines.length" class="text-xs text-muted-foreground">{{ shortLines[0]!.item_name }}: {{ t('inventory.amenityRun.minus', { qty: formatQty(shortQty(shortLines[0]!)), unit: shortLines[0]!.stock_unit_code }) }}</div></div>
    </div>

    <div class="overflow-x-auto">
      <table class="w-full min-w-[44rem] text-sm" data-testid="run-lines">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.amenityRun.ruleCol') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.amenityRun.qty') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.amenityRun.before') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.issue.cost') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="l in run.lines" :key="l.line_no" class="border-t border-border" :data-testid="`run-line-${l.item_code}`">
            <td class="py-2 pr-2">{{ l.item_name }} <span class="text-xs text-muted-foreground">{{ l.item_code }}</span></td>
            <td class="py-2 pr-2 text-muted-foreground">{{ rule(l) }}</td>
            <td class="py-2 pr-2 text-right">{{ formatQty(l.qty_stock) }}</td>
            <td class="py-2 pr-2 text-right" :class="l.stock_before !== null && Number(l.stock_before) < Number(l.qty_stock) ? 'text-destructive' : ''">{{ l.stock_before !== null ? formatQty(l.stock_before) : '' }}</td>
            <td class="py-2 text-right">{{ l.status === 'HELD' ? t('inventory.issue.held1') : l.value !== null ? formatMoney(l.value) : '' }}</td>
          </tr>
        </tbody>
        <tfoot><tr class="border-t border-border"><td colspan="4" class="py-2 pr-2 text-right font-medium">{{ t('inventory.issue.total') }}</td><td class="py-2 text-right font-semibold">{{ formatMoney(run.total_value) }}</td></tr></tfoot>
      </table>
    </div>
    <p v-if="shortLines.length" class="muted m-0 text-sm" data-testid="short-note">{{ t('inventory.amenityRun.shortNote') }}</p>

    <section class="grid gap-4 md:grid-cols-2">
      <div class="rounded-xl border border-border bg-card p-4" data-testid="run-types">
        <h2 class="m-0 text-sm font-semibold">{{ t('inventory.amenityRun.perType') }}</h2>
        <ul class="m-0 mt-2 list-none p-0 text-sm"><li v-for="x in run.types" :key="x.room_type_id" class="flex justify-between gap-2 border-t border-border py-1 first:border-0"><span>{{ x.room_type_name }}</span><span class="text-muted-foreground">{{ t('inventory.amenityRun.typeLine', { rooms: x.rooms, guests: x.guests }) }}</span></li></ul>
        <button type="button" class="mt-2 cursor-pointer border-0 bg-transparent p-0 text-sm text-primary hover:underline" data-testid="run-rooms-toggle" @click="showRooms = !showRooms">{{ t('inventory.amenityRun.roomList') }}</button>
        <ul v-if="showRooms" class="m-0 mt-2 max-h-48 list-none overflow-y-auto p-0 text-sm" data-testid="run-room-list">
          <li v-for="r in run.room_list" :key="r.room_id" class="flex justify-between gap-2 border-t border-border py-0.5 first:border-0"><span>{{ r.room_number }} · {{ r.room_type_name }}</span><span class="text-muted-foreground">{{ r.guests }} · {{ t(`inventory.amenityRun.kind.${r.occupancy_kind}`) }}</span></li>
        </ul>
      </div>
      <div class="rounded-xl border border-border bg-card p-4" data-testid="run-journal">
        <h2 class="m-0 text-sm font-semibold">{{ t('inventory.amenityRun.journal') }}</h2>
        <ul v-if="run.journal.length" class="m-0 mt-2 list-none p-0 text-sm">
          <li v-for="(j, n) in run.journal" :key="n" class="border-t border-border py-1 first:border-0">
            <div class="flex justify-between gap-2"><span>{{ t('inventory.amenityRun.debit') }} {{ j.debit_code }} {{ j.debit_name }}<template v-if="j.department_name"> · {{ j.department_name }}</template></span><span>{{ formatMoney(j.amount) }}</span></div>
            <div class="flex justify-between gap-2 text-muted-foreground"><span>{{ t('inventory.amenityRun.credit') }} {{ j.credit_code }} {{ j.credit_name }}</span><span>{{ formatMoney(j.amount) }}</span></div>
          </li>
        </ul>
        <p v-else class="muted mt-2 text-sm">{{ t('inventory.amenityRun.noJournal') }}</p>
      </div>
    </section>
    <p v-if="run.reversal_number" class="muted m-0 text-sm" data-testid="reversal-note">{{ t('inventory.amenityRun.reversedBy', { number: run.reversal_number }) }}</p>
    <div v-if="run.reversible && can('inventory.manage')" class="flex justify-end">
      <Button type="button" variant="outline" data-testid="run-reverse" @click="reverseOpen = true">{{ t('inventory.amenityRun.reverse') }}</Button>
    </div>
  </div>

  <Dialog :open="reverseOpen" @update:open="(v: boolean) => (reverseOpen = v)">
    <DialogContent class="max-w-md p-5" data-testid="reverse-dialog">
      <DialogTitle>{{ t('inventory.amenityRun.reverseTitle', { number: run?.iss_number ?? '' }) }}</DialogTitle>
      <DialogDescription class="mt-2">{{ t('inventory.amenityRun.reverseHint') }}</DialogDescription>
      <ErrorNotice v-if="formError" :error="formError" inline class="mt-2" />
      <FormField class="mt-3" :label="t('inventory.receipt.reverseReasonLabel')" required><template #default="{ id }"><Input :id="id" v-model="reason" name="reason" maxlength="300" data-testid="reverse-reason" /></template></FormField>
      <div class="mt-5 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="reverseOpen = false">{{ t('common.cancel') }}</Button>
        <Button type="button" variant="destructive" :disabled="busy || !reason.trim()" data-testid="reverse-confirm" @click="reverse">{{ t('inventory.amenityRun.reverse') }}</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
