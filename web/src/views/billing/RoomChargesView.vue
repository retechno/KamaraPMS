<script setup lang="ts">
import { formatDate } from '@/utils/format'
import { labelOf } from '@/i18n/labels'
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { RoomChargeItem, RoomChargePostResponse, RoomChargePreview } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'

const auth = useAuthStore()
const property = usePropertyStore()

const preview = ref<RoomChargePreview | null>(null)
const outcome = ref<RoomChargePostResponse | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
// One key per posting attempt (the run is idempotent anyway; the header is part of the contract).
let key = newIdempotencyKey()

const columns = computed<Column<RoomChargeItem>[]>(() => [
  { key: 'stay_number', label: t('roomCharges.stay') },
  { key: 'guest', label: t('roomCharges.guest') },
  { key: 'room_number', label: t('roomCharges.room') },
  { key: 'service_date', label: t('roomCharges.night'), format: 'date' as const },
  { key: 'room_rate', label: t('roomCharges.rate'), align: 'right', format: 'money' as const },
  { key: 'service_charge', label: t('roomCharges.service'), align: 'right', format: 'money' as const },
  { key: 'tax', label: t('roomCharges.tax'), align: 'right', format: 'money' as const },
  { key: 'total', label: 'Total', align: 'right', format: 'money' as const },
  { key: 'status', label: t('roomCharges.status') },
])

const pid = computed(() => property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const allowed = computed(() => auth.can('nightaudit.run', pid.value) || auth.can('folio.post_charge', pid.value))
const ready = computed(() => preview.value?.items.filter((i) => i.status === 'READY') ?? [])
const problems = computed(() => preview.value?.items.filter((i) => i.status === 'ERROR') ?? [])
const isMissing = (i: RoomChargeItem) => i.status === 'READY' && i.service_date < businessDate.value

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !allowed.value || !businessDate.value) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/night-audit/room-charges/preview', {
      params: { path: { propertyId } }, body: { business_date: businessDate.value },
    })
    preview.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/night-audit/room-charges', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': key } }, body: { business_date: businessDate.value },
    })
    outcome.value = data ?? null
    key = newIdempotencyKey()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) key = newIdempotencyKey()
  } finally {
    busy.value = false
  }
  await load()
}

watch([pid, businessDate], () => {
  preview.value = null
  outcome.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('roomCharges.title')" :description="formatDate(businessDate)">
    <template #actions>
      <Button type="button" variant="outline" :disabled="busy" data-testid="refresh" @click="load">{{ t('roomCharges.refresh') }}</Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!allowed" class="muted" data-testid="no-access">{{ t('roomCharges.noAccess') }}</p>

  <template v-else-if="preview">
    <Card v-if="outcome" class="mb-4" data-testid="outcome">
      <CardHeader><CardTitle>{{ t('roomCharges.posted') }}</CardTitle></CardHeader>
      <CardContent>
        <p class="m-0 text-sm">
          {{ t('roomCharges.outcome', {
            posted: outcome.results.filter((r) => r.status === 'POSTED').length,
            already: outcome.results.filter((r) => r.status === 'ALREADY_POSTED').length,
            errors: outcome.revalidation.errors.length,
            ready: outcome.revalidation.ready,
          }) }}
        </p>
        <p v-if="outcome.revalidation.invalid.length" class="alert mb-0 mt-3" data-testid="invalid">{{ t('roomCharges.invalid', { n: outcome.revalidation.invalid.length }) }}</p>
      </CardContent>
    </Card>

    <Card>
      <CardContent class="pt-4">
        <div class="mb-3 flex flex-wrap items-center gap-6">
          <span data-testid="ready-count">{{ t('roomCharges.readyCount', { n: preview.totals.ready_count }) }}</span>
          <span>{{ t('roomCharges.total') }} <strong data-testid="ready-total">{{ $money(preview.totals.ready_total) }}</strong></span>
          <Button v-if="preview.totals.ready_count" type="button" :disabled="busy" data-testid="post" @click="post">{{ t('roomCharges.post') }}</Button>
        </div>
        <EmptyState v-if="!preview.items.length" :title="t('roomCharges.empty')" data-testid="empty" />
        <DataTable
          v-else
          :columns="columns"
          :rows="preview.items"
          :row-key="(i) => `${i.stay_id}-${i.service_date}`"
          :row-test-id="(i) => `item-${i.stay_id}-${i.service_date}`"
          :row-class="(i) => (i.status === 'ERROR' ? 'text-destructive' : i.status === 'ALREADY_POSTED' || i.status === 'NOT_APPLICABLE' ? 'text-muted-foreground' : undefined)"
          :caption="t('roomCharges.title')"
          data-testid="items"
        >
          <template #cell-room_number="{ row }">{{ row.room_number || '—' }}</template>
          <template #cell-service_date="{ row }">{{ $date(row.service_date) }} <small v-if="isMissing(row)" class="text-muted-foreground">{{ t('roomCharges.missing') }}</small></template>
          <template #cell-service_charge="{ row }">{{ row.status === 'READY' ? $money(row.service_charge) : '' }}</template>
          <template #cell-tax="{ row }">{{ row.status === 'READY' ? $money(row.tax) : '' }}</template>
          <template #cell-total="{ row }">{{ row.status === 'READY' ? $money(row.total) : '' }}</template>
          <template #cell-status="{ row }"><Badge :variant="row.status === 'READY' ? 'success' : row.status === 'ERROR' ? 'destructive' : 'outline'">{{ labelOf('roomChargeStatus', row.status) }}</Badge><small v-if="row.reason" class="text-muted-foreground"> {{ row.reason }}</small></template>
        </DataTable>
        <p v-if="problems.length" class="alert mb-0 mt-3" data-testid="problems">{{ t('roomCharges.problems', { n: problems.length }) }}</p>
        <p v-if="ready.length === 0 && preview.items.length" class="mb-0 mt-3 text-sm text-muted-foreground">{{ t('roomCharges.nothingDue') }}</p>
      </CardContent>
    </Card>
  </template>
</template>
