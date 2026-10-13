<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { FolioTransfer } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { formatMoney } from '@/utils/format'
import { usePropertyStore } from '@/stores/property'
import { useAuthStore } from '@/stores/auth'
import ReturnDialog from './ReturnDialog.vue'

/**
 * "Show what was transferred" of a folio (docs/architecture/30-group-folio.md, part B): the transactions that left it, each with the amount, the folio it went to and where it is now (it may have moved on),
 * who moved it and when, and the button Return, for one line or for several that the same folio holds now.
 */
const props = defineProps<{ folioId: number; folioNumber: string; reload?: number }>()
const emit = defineEmits<{ changed: [message: string] }>()

const property = usePropertyStore()
const auth = useAuthStore()
const rows = ref<FolioTransfer[]>([])
const error = ref<ApiError | null>(null)
const picked = ref<number[]>([]) // head transfer ids
const returning = ref<FolioTransfer[] | null>(null)

const pid = computed(() => property.currentId)
const canReturn = computed(() => auth.can('folio.reverse', pid.value))

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/folios/{id}/transfers', { params: { path: { propertyId, id: props.folioId }, query: { direction: 'out' } } })
    rows.value = data?.data ?? []
    picked.value = []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(() => [props.folioId, props.reload, pid.value], () => void load(), { immediate: true })

// Lines that the same folio holds now can be returned together: the first one picked fixes the folio.
const holder = computed(() => rows.value.find((r) => picked.value.includes(r.head_transfer_id))?.current?.id ?? null)
const sameHolder = (r: FolioTransfer): boolean => holder.value === null || r.current?.id === holder.value
const toggle = (r: FolioTransfer): void => {
  picked.value = picked.value.includes(r.head_transfer_id) ? picked.value.filter((x) => x !== r.head_transfer_id) : [...picked.value, r.head_transfer_id]
}
const chosen = computed(() => rows.value.filter((r) => picked.value.includes(r.head_transfer_id)))
const here = (r: FolioTransfer): boolean => r.current?.id === props.folioId
const status = (r: FolioTransfer): string => (r.returned_by_transfer_id ? t('folioTransfer.statusReturned') : here(r) ? t('folioTransfer.statusBack') : '')

function done(message: string): void {
  returning.value = null
  emit('changed', message)
  void load()
}
</script>

<template>
  <Card class="mb-4" data-testid="transferred-list">
    <CardHeader class="flex-row items-start justify-between gap-3">
      <div>
        <CardTitle>{{ t('folioTransfer.listTitle') }}</CardTitle>
        <p class="m-0 mt-1 text-sm text-muted-foreground">{{ t('folioTransfer.listHint') }}</p>
      </div>
      <Button v-if="canReturn && chosen.length" type="button" size="sm" data-testid="return-picked" @click="returning = chosen">{{ t('folioTransfer.returnPicked', { n: chosen.length }) }}</Button>
    </CardHeader>
    <CardContent>
      <ErrorNotice v-if="error" :error="error" inline />
      <p v-if="!rows.length" class="m-0 text-sm text-muted-foreground" data-testid="transferred-empty">{{ t('folioTransfer.listEmpty') }}</p>
      <div v-else class="grid gap-2">
        <div v-for="r in rows" :key="r.id" class="grid gap-1 rounded-lg border border-border p-3 text-sm md:grid-cols-[2rem_1fr_auto] md:items-start" :data-testid="`transferred-${r.id}`">
          <input
            v-if="canReturn && r.returnable" type="checkbox" class="mt-1 size-4 accent-primary" :checked="picked.includes(r.head_transfer_id)" :disabled="!sameHolder(r) && !picked.includes(r.head_transfer_id)"
            :aria-label="t('folioTransfer.pick')" :data-testid="`pick-transfer-${r.id}`" @change="toggle(r)"
          />
          <span v-else />
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium">{{ r.kind === 'BALANCE' ? t('folioTransfer.balanceLine') : r.description }}</span>
              <Badge v-if="r.kind === 'BALANCE'" variant="outline">{{ t('folioTransfer.kindBalance') }}</Badge>
              <Badge v-if="status(r)" variant="outline" :data-testid="`transferred-status-${r.id}`">{{ status(r) }}</Badge>
            </div>
            <div class="text-muted-foreground">
              <span class="tabular-nums" :data-testid="`transferred-amount-${r.id}`">{{ formatMoney(r.amount) }}</span>
              · {{ t('folioTransfer.toFolio') }}
              <RouterLink :to="`/folios/${r.to.id}`" class="text-primary hover:underline" :data-testid="`transferred-to-${r.id}`">{{ r.to.number }}</RouterLink><template v-if="r.to.room"> · {{ t('folioTransfer.room', { room: r.to.room }) }}</template>
              <template v-if="r.current && r.current.id !== r.to.id">
                · {{ t('folioTransfer.nowAt') }} <RouterLink :to="`/folios/${r.current.id}`" class="text-primary hover:underline" :data-testid="`transferred-now-${r.id}`">{{ r.current.number }}</RouterLink>
              </template>
            </div>
            <div class="text-xs text-muted-foreground">{{ t('folioTransfer.byWhen', { by: r.by || '-', when: $dateTime(r.created_at), approver: r.approved_by || '-' }) }} · {{ r.reason }}</div>
          </div>
          <Button v-if="canReturn && r.returnable" type="button" variant="outline" size="sm" :data-testid="`return-${r.id}`" @click="returning = [r]">{{ t('folioTransfer.return') }}</Button>
        </div>
      </div>
    </CardContent>
  </Card>
  <ReturnDialog v-if="returning" :holder-id="returning[0]?.current?.id ?? folioId" :holder-number="returning[0]?.current?.number ?? folioNumber" :rows="returning" @done="done" @close="returning = null" />
</template>
