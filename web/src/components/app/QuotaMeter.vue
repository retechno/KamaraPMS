<script setup lang="ts">
import { computed } from 'vue'
import { t } from '@/i18n'
import { formatMoney } from '@/utils/format'

const formatQty = (n: number): string => formatMoney(n)

/**
 * How much of a plan limit is in use: `used / limit` (or "Unlimited" when the limit is null), a bar, and a sentence, because a bar alone says nothing to a person who cannot tell its colour:
 * under 85 % it is quiet, from 85 % "Almost full: N left", at 100 % "Full: a new <thing> cannot be added". `what` is the resource (properties, rooms, users).
 */
const props = defineProps<{ what: 'properties' | 'rooms' | 'users'; label: string; used: number; limit: number | null }>()

const ratio = computed(() => (props.limit === null ? 0 : props.limit === 0 ? 1 : props.used / props.limit))
const level = computed<'ok' | 'near' | 'full'>(() => (props.limit === null ? 'ok' : ratio.value >= 1 ? 'full' : ratio.value >= 0.85 ? 'near' : 'ok'))
const left = computed(() => (props.limit === null ? 0 : Math.max(props.limit - props.used, 0)))
const barClass = { ok: 'bg-primary', near: 'bg-warning', full: 'bg-destructive' } as const
const noteClass = { ok: '', near: 'text-foreground', full: 'font-medium text-destructive' } as const
</script>

<template>
  <div :data-testid="`quota-${what}`" :data-level="level">
    <div class="flex items-baseline justify-between gap-2 text-sm">
      <span :id="`quota-${what}-label`" class="font-medium">{{ label }}</span>
      <span class="tabular-nums text-muted-foreground">
        <template v-if="limit === null">{{ formatQty(used) }} · {{ t('subscription.unlimited') }}</template>
        <template v-else>{{ t('subscription.usedOf', { used: formatQty(used), limit: formatQty(limit) }) }}</template>
      </span>
    </div>
    <div
      v-if="limit !== null"
      class="mt-1.5 h-2 overflow-hidden rounded-full bg-muted"
      role="meter"
      :aria-labelledby="`quota-${what}-label`"
      aria-valuemin="0"
      :aria-valuemax="limit"
      :aria-valuenow="Math.min(used, limit)"
      :aria-valuetext="t('subscription.usedOf', { used, limit })"
    >
      <div :class="['h-full rounded-full', barClass[level]]" :style="{ width: `${Math.min(ratio, 1) * 100}%` }" />
    </div>
    <p v-if="level === 'full'" :class="['m-0 mt-1 text-xs', noteClass.full]" data-testid="quota-note">{{ t(`subscription.full.${what}` as never) }}</p>
    <p v-else-if="level === 'near'" :class="['m-0 mt-1 text-xs', noteClass.near]" data-testid="quota-note">{{ t('subscription.almostFull', { n: formatQty(left) }) }}</p>
  </div>
</template>
