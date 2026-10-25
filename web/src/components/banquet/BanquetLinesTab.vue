<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetEvent, BanquetItem, BanquetLine, BanquetLines, BanquetMoney, BanquetPackage } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { confirm } from '@/composables/useConfirm'
import { t } from '@/i18n'
import { formatDateShort, formatMoney } from '@/utils/format'
import BanquetLineDialog from './BanquetLineDialog.vue'

/**
 * The tab "Items & packages" (docs/architecture/36-banquet.md 6.5, mockup 40): what is sold in each session with the price and the mode frozen on the line, the estimate of the event by the charge engine
 * and the revenue by charge code. A package is shown with its components; a session that is posted is closed to changes (the correction is an adjustment on the folio).
 */
const props = defineProps<{ propertyId: number; event: BanquetEvent; lines: BanquetLines; money: BanquetMoney; items: BanquetItem[]; packages: BanquetPackage[]; defaultMode: 'EXCLUSIVE' | 'INCLUSIVE'; canManage: boolean }>()
const emit = defineEmits<{ changed: [] }>()

const error = ref<ApiError | null>(null)
const dialog = ref(false)
const editing = ref<BanquetLine | null>(null)
const forSession = ref<number | null>(null)

const open = computed(() => ['INQUIRY', 'TENTATIVE', 'DEFINITE'].includes(props.event.status))
const posted = computed(() => new Set(props.money.postings.map((p) => p.session_id)))
const sessions = computed(() => props.event.sessions.filter((s) => s.status === 'ACTIVE'))
const linesOf = (sessionId: number): BanquetLine[] => props.lines.lines.filter((l) => l.session_id === sessionId)
const estimateOf = (lineId: number) => props.lines.estimate.lines.find((e) => e.line_id === lineId)
const sessionTotal = (sessionId: number): string => formatMoney(String(linesOf(sessionId).reduce((sum, l) => sum + Number(estimateOf(l.id)?.total ?? 0), 0)))
const modeLabel = (m: string): string => (m === 'INCLUSIVE' ? t('banquet.line.nett') : t('banquet.line.plusPlus'))
const quantityOf = (l: BanquetLine): string => {
  const pax = estimateOf(l.id)?.pax ?? 0
  return l.package_id || l.basis === 'PER_PAX' ? t('banquet.line.paxCount', { n: pax }) : formatMoney(l.quantity)
}
const discountOf = (l: BanquetLine): string => (l.discount_pct ? `${l.discount_pct}%` : l.discount_amount ? formatMoney(l.discount_amount) : '–')

function add(sessionId: number | null): void {
  editing.value = null
  forSession.value = sessionId
  dialog.value = true
}
function edit(l: BanquetLine): void {
  editing.value = l
  dialog.value = true
}
async function remove(l: BanquetLine): Promise<void> {
  if (!(await confirm({ title: t('banquet.line.removeTitle', { name: l.description }), description: t('banquet.line.removeBody'), confirmLabel: t('common.remove'), destructive: true }))) return
  error.value = null
  try {
    await api.DELETE('/api/v1/properties/{propertyId}/banquet/events/{id}/lines/{lid}', { params: { path: { propertyId: props.propertyId, id: props.event.id, lid: l.id } } })
    emit('changed')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
</script>

<template>
  <ErrorNotice v-if="error" :error="error" inline />
  <div class="mb-3 flex justify-end">
    <Button v-if="canManage && open && sessions.length" type="button" data-testid="line-add" @click="add(null)">{{ t('banquet.line.new') }}</Button>
  </div>
  <EmptyState v-if="!sessions.length" :title="t('banquet.session.empty')" :description="t('emptyState.banquetSessions')" />
  <div v-else class="grid gap-4 lg:grid-cols-[1fr_20rem]">
    <div class="flex min-w-0 flex-col gap-4">
      <Card v-for="s in sessions" :key="s.id" class="p-3" :data-testid="`lines-session-${s.id}`">
        <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
          <h3 class="m-0 text-sm font-semibold">{{ s.name }} · {{ formatDateShort(s.session_date) }}<template v-if="s.room_name"> · {{ s.room_name }}</template></h3>
          <span class="flex items-center gap-2 text-sm">
            <Badge v-if="posted.has(s.id)" variant="closed" :data-testid="`session-posted-${s.id}`">{{ t('banquet.line.posted') }}</Badge>
            <span class="text-muted-foreground" :data-testid="`session-total-${s.id}`">{{ sessionTotal(s.id) }}</span>
            <Button v-if="canManage && open && !posted.has(s.id)" type="button" variant="outline" size="sm" :data-testid="`line-add-${s.id}`" @click="add(s.id)">{{ t('banquet.line.addTo') }}</Button>
          </span>
        </div>
        <p v-if="!linesOf(s.id).length" class="m-0 text-sm text-muted-foreground">{{ t('banquet.line.none') }}</p>
        <ul v-else class="m-0 flex list-none flex-col gap-2 p-0">
          <li v-for="l in linesOf(s.id)" :key="l.id" class="rounded-md border border-border p-2 text-sm" :data-testid="`line-${l.id}`">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <span class="font-medium">{{ l.description }}</span>
              <span class="flex items-center gap-2">
                <Badge variant="outline">{{ modeLabel(l.price_mode) }}</Badge>
                <span class="text-muted-foreground">{{ formatMoney(l.unit_price) }} · {{ quantityOf(l) }}</span>
                <span v-if="l.discount_pct || l.discount_amount" class="text-muted-foreground" :data-testid="`line-discount-${l.id}`">{{ t('banquet.line.discount') }} {{ discountOf(l) }}<Badge v-if="l.discount_approved_by" variant="booked" class="ml-1">{{ t('banquet.line.approved') }}</Badge></span>
                <span class="font-medium" :data-testid="`line-total-${l.id}`">{{ formatMoney(estimateOf(l.id)?.total) }}</span>
              </span>
            </div>
            <ul v-if="l.components.length" class="m-0 mt-1 flex list-none flex-col gap-0.5 p-0 pl-3 text-xs text-muted-foreground">
              <li v-for="c in l.components" :key="c.item_id">{{ c.name }} <span v-if="Number(c.qty_per_pax) !== 1">× {{ c.qty_per_pax }}</span> · {{ c.charge_code }}</li>
            </ul>
            <div v-if="canManage && open && !posted.has(s.id)" class="mt-2 flex gap-2">
              <Button type="button" variant="outline" size="sm" :data-testid="`line-edit-${l.id}`" @click="edit(l)">{{ t('common.edit') }}</Button>
              <Button type="button" variant="outline" size="sm" :data-testid="`line-remove-${l.id}`" @click="remove(l)">{{ t('common.remove') }}</Button>
            </div>
          </li>
        </ul>
      </Card>
    </div>
    <Card class="h-fit p-4" data-testid="estimate-card">
      <h3 class="m-0 mb-2 text-sm font-semibold">{{ t('banquet.money.estimate') }}</h3>
      <dl class="m-0 grid grid-cols-[1fr_auto] gap-x-3 gap-y-1 text-sm">
        <dt class="text-muted-foreground">{{ t('banquet.money.net') }}</dt><dd class="m-0 text-right" data-testid="estimate-net">{{ formatMoney(lines.estimate.net) }}</dd>
        <dt class="text-muted-foreground">{{ t('banquet.money.service') }}</dt><dd class="m-0 text-right">{{ formatMoney(lines.estimate.service) }}</dd>
        <dt class="text-muted-foreground">{{ t('banquet.money.tax') }}</dt><dd class="m-0 text-right">{{ formatMoney(lines.estimate.tax) }}</dd>
        <dt class="font-medium">{{ t('banquet.money.total') }}</dt><dd class="m-0 text-right font-medium" data-testid="estimate-total">{{ formatMoney(lines.estimate.total) }}</dd>
      </dl>
      <template v-if="lines.estimate.by_code.length">
        <h4 class="m-0 mb-1 mt-3 text-xs font-semibold text-muted-foreground">{{ t('banquet.money.byCode') }}</h4>
        <ul class="m-0 flex list-none flex-col gap-0.5 p-0 text-sm" data-testid="estimate-by-code">
          <li v-for="c in lines.estimate.by_code" :key="c.charge_code_id" class="flex justify-between gap-2"><span>{{ c.charge_code }}</span><span>{{ formatMoney(c.net) }}</span></li>
        </ul>
      </template>
      <p class="m-0 mt-3 text-xs text-muted-foreground">{{ t('banquet.money.estimateHint') }}</p>
    </Card>
  </div>

  <BanquetLineDialog v-model:open="dialog" :property-id="propertyId" :event="event" :session-id="forSession" :line="editing" :items="items" :packages="packages" :default-mode="defaultMode" @saved="emit('changed')" />
</template>
