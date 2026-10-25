<script setup lang="ts">
import { computed, ref } from 'vue'
import type { BanquetEvent, BanquetMoney } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { t } from '@/i18n'
import { rowVariant } from '@/utils/banquetMoney'
import { formatDate, formatMoney } from '@/utils/format'
import BanquetPolicyDialog from './BanquetPolicyDialog.vue'
import BanquetReceiveDialog from './BanquetReceiveDialog.vue'
import BanquetScheduleDialog from './BanquetScheduleDialog.vue'

/**
 * The tab "Deposit & policy" (docs/architecture/36-banquet.md 6.6, mockup 41): the deposit schedule against the deposits received on the EVENT folio, the button "Receive a down payment" and the cancellation
 * policy of the event with the tier in force today. A row is paid when the deposits received reach its cumulative due: no payment is linked to a row.
 */
const props = defineProps<{ propertyId: number; event: BanquetEvent; money: BanquetMoney; canManage: boolean; canConfirm: boolean }>()
const emit = defineEmits<{ changed: [] }>()

const open = computed(() => ['INQUIRY', 'TENTATIVE', 'DEFINITE'].includes(props.event.status))
const receiveOpen = ref(false)
const scheduleOpen = ref(false)
const policyOpen = ref(false)
const m = computed(() => props.money)
const remaining = computed(() => String(Math.max(0, Number(m.value.estimate.total) - Number(m.value.deposit_received))))
const posted = computed(() => m.value.folio?.debit ?? '0')
const tierRange = (from: number, to: number | null): string => (to === null ? t('banquet.policy.orMore', { n: from }) : from === to ? t('banquet.policy.exactly', { n: from }) : t('banquet.policy.range', { from, to }))
const baseAmount = computed(() => (m.value.policy.base === 'GROSS' ? m.value.estimate.total : m.value.estimate.net))
const feeOf = (percent: string): string => String(Math.round((Number(baseAmount.value) * Number(percent)) / 100))
</script>

<template>
  <div class="flex flex-col gap-4">
    <Card class="p-4" data-testid="schedule-card">
      <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 class="m-0 text-base font-semibold">{{ t('banquet.deposit.title') }}</h3>
          <p class="m-0 text-sm text-muted-foreground">{{ t('banquet.deposit.base', { total: formatMoney(m.estimate.total) }) }}</p>
        </div>
        <div class="flex gap-2">
          <Button v-if="canConfirm && open" type="button" variant="outline" data-testid="schedule-edit" @click="scheduleOpen = true">{{ t('banquet.deposit.edit') }}</Button>
          <Button v-if="canManage && open" type="button" data-testid="receive-open" @click="receiveOpen = true">{{ t('banquet.deposit.receive') }}</Button>
        </div>
      </div>
      <p v-if="!m.schedule.length" class="m-0 text-sm text-muted-foreground" data-testid="schedule-empty">{{ t('banquet.deposit.empty') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm" data-testid="schedule-table">
          <thead><tr class="text-left text-muted-foreground"><th class="py-1 pr-3 font-normal">{{ t('banquet.deposit.stage') }}</th><th class="py-1 pr-3 font-normal">{{ t('banquet.deposit.due') }}</th><th class="py-1 pr-3 text-right font-normal">{{ t('banquet.deposit.percent') }}</th><th class="py-1 pr-3 text-right font-normal">{{ t('banquet.deposit.amount') }}</th><th class="py-1 pr-3 text-right font-normal">{{ t('banquet.deposit.cumulative') }}</th><th class="py-1 font-normal">{{ t('banquet.deposit.status') }}</th></tr></thead>
          <tbody>
            <tr v-for="r in m.schedule" :key="r.seq" class="border-t border-border" :data-testid="`schedule-${r.seq}`">
              <td class="py-1.5 pr-3">{{ r.label }}</td>
              <td class="py-1.5 pr-3">{{ formatDate(r.due_date) }}</td>
              <td class="py-1.5 pr-3 text-right">{{ r.percent ? `${r.percent}%` : '–' }}</td>
              <td class="py-1.5 pr-3 text-right">{{ formatMoney(r.amount) }}</td>
              <td class="py-1.5 pr-3 text-right">{{ formatMoney(r.cumulative) }}</td>
              <td class="py-1.5"><Badge :variant="rowVariant(r.status)" :data-testid="`schedule-status-${r.seq}`">{{ t(`banquet.deposit.status_${r.status}`) }}</Badge></td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="m-0 mt-2 text-xs text-muted-foreground">{{ t('banquet.deposit.hint') }}</p>
    </Card>

    <Card class="p-4" data-testid="policy-card">
      <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 class="m-0 text-base font-semibold">{{ t('banquet.policy.title') }}</h3>
          <p v-if="m.policy.base" class="m-0 text-sm text-muted-foreground">{{ t('banquet.policy.basis', { base: t(`banquet.policy.base${m.policy.base}` as never), amount: formatMoney(baseAmount) }) }}</p>
        </div>
        <Button v-if="canConfirm && open" type="button" variant="outline" data-testid="policy-edit" @click="policyOpen = true">{{ t('banquet.policy.edit') }}</Button>
      </div>
      <p v-if="!m.policy.tiers.length" class="m-0 text-sm text-muted-foreground" data-testid="policy-empty">{{ t('banquet.policy.empty') }}</p>
      <table v-else class="w-full text-sm" data-testid="policy-table">
        <thead><tr class="text-left text-muted-foreground"><th class="py-1 pr-3 font-normal">{{ t('banquet.policy.cancelled') }}</th><th class="py-1 pr-3 text-right font-normal">{{ t('banquet.policy.fee') }}</th><th class="py-1 text-right font-normal">{{ t('banquet.deposit.amount') }}</th></tr></thead>
        <tbody>
          <tr v-for="x in m.policy.tiers" :key="x.id" class="border-t border-border" :class="m.policy.current?.id === x.id ? 'bg-primary/10 font-medium' : ''" :data-testid="`tier-${x.id}`">
            <td class="py-1.5 pr-3">{{ tierRange(x.days_from, x.days_to) }}<Badge v-if="x.action === 'FORFEIT'" variant="outline" class="ml-2">{{ t('banquet.policy.FORFEIT') }}</Badge><Badge v-if="m.policy.current?.id === x.id" variant="booked" class="ml-2">{{ t('banquet.policy.today') }}</Badge></td>
            <td class="py-1.5 pr-3 text-right">{{ x.percent }}%</td>
            <td class="py-1.5 text-right">{{ formatMoney(feeOf(x.percent)) }}</td>
          </tr>
        </tbody>
      </table>
      <p class="m-0 mt-2 text-xs text-muted-foreground">{{ t('banquet.policy.hint') }}</p>
    </Card>

    <Card class="p-4" data-testid="deposit-summary">
      <dl class="m-0 grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 text-sm sm:max-w-sm">
        <dt class="text-muted-foreground">{{ t('banquet.deposit.held') }}</dt><dd class="m-0 text-right font-medium" data-testid="deposit-held">{{ formatMoney(m.deposit_received) }}</dd>
        <dt class="text-muted-foreground">{{ t('banquet.deposit.remaining') }}</dt><dd class="m-0 text-right" data-testid="deposit-remaining">{{ formatMoney(remaining) }}</dd>
        <dt class="text-muted-foreground">{{ t('banquet.deposit.posted') }}</dt><dd class="m-0 text-right">{{ formatMoney(posted) }}</dd>
      </dl>
    </Card>
  </div>

  <BanquetReceiveDialog v-model:open="receiveOpen" :property-id="propertyId" :event="event" :folio="m.folio" @saved="emit('changed')" />
  <BanquetScheduleDialog v-model:open="scheduleOpen" :property-id="propertyId" :event-id="event.id" :schedule="m.schedule" @saved="emit('changed')" />
  <BanquetPolicyDialog v-model:open="policyOpen" :property-id="propertyId" :event-id="event.id" :policy="m.policy" @saved="emit('changed')" />
</template>
