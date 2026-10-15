<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { OutletChargeList, OutletChargeRow } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Receipt } from 'lucide-vue-next'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { useAutoRefresh } from '@/composables/useAutoRefresh'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDateTime, formatMoney } from '@/utils/format'

/**
 * The outlet charges that came in on the business day, every room, with the attempts that were refused (docs/architecture/34-pos.md 6.5, mockup 13). It reads again by itself, so a charge
 * that an outlet tried a minute ago is here without a click; a refused one offers the folio, where the lock is opened.
 */
const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const list = ref<OutletChargeList | null>(null)
const error = ref<ApiError | null>(null)
const filter = ref<'ALL' | 'REJECTED' | 'ACCEPTED'>('ALL')
const loading = ref(false)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || loading.value) return
  loading.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/outlet-charges', { params: { path: { propertyId } } })
    list.value = data ?? null
    error.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

const rows = computed<OutletChargeRow[]>(() => (list.value?.data ?? []).filter((r) => filter.value === 'ALL' || r.result === filter.value))
const canOpen = computed(() => auth.can('folio.outlet_charge_open', pid.value))
const who = (r: OutletChargeRow): string => r.room_number ? `${t('outletCharges.room')} ${r.room_number}` : r.group_name || r.folio_number
const why = (r: OutletChargeRow): string => {
  const ctx = (r.context ?? {}) as Record<string, unknown>
  if (r.code === 'OUTLET_FOLIO_LOCKED') return t(`outletLock.reason_${String(ctx.reason ?? 'NEVER_OPENED')}`)
  if (r.code === 'OUTLET_LIMIT_EXCEEDED') return t('outletCharges.exceeded', { remaining: formatMoney(String(ctx.remaining ?? '0')) })
  return r.code ? t(`errors.${r.code}`) : ''
}

useAutoRefresh(() => void load(), { everyMs: 20_000 })
watch(pid, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('outletCharges.title')" />
  <p class="muted">{{ t('outletCharges.intro') }}</p>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <template v-else-if="list">
    <div class="mb-3 flex flex-wrap items-center gap-2" role="group" :aria-label="t('outletCharges.filter')" data-testid="charges-filter">
      <Button v-for="f in (['ALL', 'REJECTED', 'ACCEPTED'] as const)" :key="f" size="sm" :variant="filter === f ? 'default' : 'outline'" :aria-pressed="filter === f" :data-testid="`filter-${f}`" @click="filter = f">
        {{ t(`outletCharges.filter_${f}`) }}
        <span class="ml-1 text-xs opacity-70">{{ f === 'ALL' ? list.data.length : f === 'ACCEPTED' ? list.accepted : list.rejected }}</span>
      </Button>
    </div>
    <section class="overflow-hidden rounded-xl border border-border bg-card" data-testid="charges-table">
      <header class="flex items-baseline justify-between gap-2 px-4 py-3">
        <h2 class="m-0 text-base font-semibold">{{ t('outletCharges.title') }}</h2>
        <span class="text-xs text-muted-foreground">{{ t('outletCharges.allRooms', { n: list.data.length }) }}</span>
      </header>
      <EmptyState
        v-if="rows.length === 0" class="border-t border-border" data-testid="charges-empty" :icon="Receipt"
        :title="list.data.length === 0 ? t('outletCharges.emptyTitle') : t('outletCharges.emptyFiltered')"
        :description="list.data.length === 0 ? t('outletCharges.emptyHint') : ''"
        :action-label="list.data.length === 0 ? '' : t('outletCharges.clearFilter')" action-variant="outline" @action="filter = 'ALL'"
      />
      <ul v-else class="m-0 list-none p-0" data-testid="charges">
        <li
          v-for="r in rows" :key="r.id" class="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-border px-4 py-3 text-sm" :class="r.result === 'REJECTED' ? 'bg-warning/15' : 'bg-primary/5'"
          :data-testid="`charge-${r.id}`" :data-result="r.result"
        >
          <span class="size-2 shrink-0 rounded-full" :class="r.result === 'REJECTED' ? 'bg-warning' : 'bg-primary'" aria-hidden="true" />
          <strong class="w-24 shrink-0">{{ who(r) }}</strong>
          <span class="min-w-0 basis-full flex-1 sm:basis-0">
            <template v-if="r.result === 'REJECTED'">{{ t('outletCharges.tried', { outlet: r.outlet_name }) }}<template v-if="r.bill_number"> · {{ r.bill_number }}</template></template>
            <template v-else>{{ r.outlet_name }}<template v-if="r.bill_number"> · {{ r.bill_number }}</template><template v-if="r.guest_name"> · {{ r.guest_name }}</template></template>
            <span v-if="r.result === 'REJECTED'" class="block text-xs text-destructive" data-testid="charge-why">{{ t('outletCharges.refused') }}: {{ why(r) }}</span>
          </span>
          <span class="text-xs text-muted-foreground">{{ formatDateTime(r.at) }}</span>
          <strong class="text-right tabular-nums sm:w-28" :class="r.result === 'REJECTED' && 'text-muted-foreground line-through'">{{ formatMoney(r.amount) }}</strong>
          <RouterLink v-if="canOpen && r.result === 'REJECTED'" :to="`/folios/${r.folio_id}`" class="inline-flex min-h-9 items-center rounded-md border border-border bg-card px-3 hover:bg-accent" :data-testid="`charge-open-${r.id}`">
            {{ t('outletCharges.openLock') }}
          </RouterLink>
        </li>
      </ul>
    </section>
  </template>
</template>
