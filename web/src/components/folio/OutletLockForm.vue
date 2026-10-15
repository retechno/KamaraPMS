<script setup lang="ts">
import { computed } from 'vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import type { PosOutlet } from '@/api/types'
import { t } from '@/i18n'

/**
 * The three things the front desk decides when it opens a folio for outlet charges (docs/architecture/34-pos.md section 6): the limit (required, there is no unlimited), how long it lasts
 * and which outlets may charge it. Used by the dialog of a folio and by the check-in. The state is the one of the parent; this only edits it.
 */
export interface OutletLockDraft {
  limit: string
  mode: 'UNTIL_CLOSE' | 'TODAY'
  outletIds: number[]
}

const props = defineProps<{ outlets: PosOutlet[]; decimals: number; untilLabel?: string; disabled?: boolean; idPrefix?: string }>()
const draft = defineModel<OutletLockDraft>({ required: true })

const prefix = computed(() => props.idPrefix ?? 'lock')
const allTicked = computed(() => props.outlets.length > 0 && props.outlets.every((o) => draft.value.outletIds.includes(o.id)))

// The draft is the state of the parent (a reactive object): it is changed in place, so the parent sees it whether it passed a ref or a part of a composable.
function toggle(id: number, on: boolean): void {
  const set = new Set(draft.value.outletIds)
  if (on) set.add(id)
  else set.delete(id)
  draft.value.outletIds = [...set]
}
function toggleAll(on: boolean): void {
  draft.value.outletIds = on ? props.outlets.map((o) => o.id) : []
}
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="outlet-lock-form">
    <div class="grid items-start gap-4 sm:grid-cols-2">
    <div>
      <label class="mb-1.5 block text-xs text-muted-foreground" :for="`${prefix}-limit`">{{ t('outletLock.limit') }}</label>
      <MoneyInput :id="`${prefix}-limit`" v-model="draft.limit" name="outlet_limit" :decimals="decimals" :disabled="disabled" class="h-11" :placeholder="t('outletLock.limitPlaceholder')" data-testid="lock-limit" />
      <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('outletLock.limitHint') }}</p>
    </div>

    <fieldset class="m-0 border-0 p-0" :disabled="disabled">
      <legend class="mb-1.5 text-xs text-muted-foreground">{{ t('outletLock.validUntil') }}</legend>
      <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <label
          class="flex min-h-11 cursor-pointer items-center gap-2 rounded-lg border px-3 text-sm"
          :class="draft.mode === 'UNTIL_CLOSE' ? 'border-primary bg-primary/10 font-semibold' : 'border-border'"
        >
          <input v-model="draft.mode" type="radio" :name="`${prefix}-mode`" value="UNTIL_CLOSE" class="accent-primary" data-testid="lock-mode-until" />
          <span>{{ untilLabel ?? t('outletLock.untilCheckout') }}</span>
        </label>
        <label
          class="flex min-h-11 cursor-pointer items-center gap-2 rounded-lg border px-3 text-sm"
          :class="draft.mode === 'TODAY' ? 'border-primary bg-primary/10 font-semibold' : 'border-border'"
        >
          <input v-model="draft.mode" type="radio" :name="`${prefix}-mode`" value="TODAY" class="accent-primary" data-testid="lock-mode-today" />
          <span>{{ t('outletLock.todayOnly') }}</span>
        </label>
      </div>
    </fieldset>
    </div>

    <fieldset class="m-0 border-0 p-0" :disabled="disabled">
      <legend class="mb-1.5 flex w-full items-center justify-between text-xs text-muted-foreground">
        <span>{{ t('outletLock.outlets') }}</span>
        <label v-if="outlets.length > 1" class="inline-flex cursor-pointer items-center gap-1.5">
          <input type="checkbox" class="accent-primary" :checked="allTicked" data-testid="lock-all" @change="toggleAll(($event.target as HTMLInputElement).checked)" />
          {{ t('outletLock.all') }}
        </label>
      </legend>
      <p v-if="outlets.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="lock-no-outlets">{{ t('outletLock.noOutlets') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-1.5 p-0">
        <li v-for="o in outlets" :key="o.id">
          <label class="flex min-h-11 cursor-pointer items-center gap-2.5 rounded-lg border border-border px-3 text-sm">
            <input type="checkbox" class="size-4 accent-primary" :checked="draft.outletIds.includes(o.id)" :data-testid="`lock-outlet-${o.code}`" @change="toggle(o.id, ($event.target as HTMLInputElement).checked)" />
            <span class="min-w-0 truncate">{{ o.name }}</span>
            <span class="ml-auto shrink-0 text-xs text-muted-foreground">{{ o.code }}</span>
          </label>
        </li>
      </ul>
    </fieldset>
  </div>
</template>
