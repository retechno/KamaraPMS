<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { CityLedgerInvoice } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { fromMilli } from '@/views/accounting/accountMeta'
import { checkAllocations, splitFromOldest } from './allocation'

/**
 * The invoices a payment of a company pays (KD-04 in the receipt form, KD-27 in the allocation of a recorded receipt). It fills from the oldest
 * invoice; once the clerk types an amount, the split is kept when the amount changes, until "split from the oldest" is asked again.
 * The parent gives the first split (splitFromOldest) and remounts it (a new key) to start over.
 */
const props = defineProps<{ amount: string; invoices: CityLedgerInvoice[] }>()
const model = defineModel<Record<number, string>>({ required: true })

const touched = ref(false)
const fill = () => { model.value = splitFromOldest(props.amount, props.invoices) }
function again(): void {
  touched.value = false
  fill()
}
function set(id: number, value: string): void {
  model.value = { ...model.value, [id]: value }
  touched.value = true // what the clerk typed is kept when the amount changes
}
// the invoices by what they are, not by the array: a list loaded again with the same invoices does not undo the split
const owed = computed(() => props.invoices.map((i) => `${i.id}:${i.outstanding}`).join(','))
// The first split is the parent's (splitFromOldest): a model sent up while the parent renders the table is not shown by its inputs.
watch(() => [props.amount, owed.value] as const, () => { if (!touched.value) fill() })

const check = computed(() => checkAllocations(model.value, props.invoices, props.amount))
</script>

<template>
  <div v-if="invoices.length" data-testid="allocations">
    <div class="mb-1 flex flex-wrap items-center justify-between gap-2">
      <p class="m-0 text-sm font-medium">{{ t('clAccount.allocTitle') }}</p>
      <Button type="button" variant="outline" size="sm" data-testid="allocate-oldest" @click="again">{{ t('clAccount.allocateOldest') }}</Button>
    </div>
    <p class="m-0 mb-2 text-sm text-muted-foreground">{{ t('clAccount.allocHint') }}</p>
    <div class="overflow-x-auto">
      <table class="w-full border-collapse text-sm">
        <thead>
          <tr class="border-b border-border text-left text-xs text-muted-foreground">
            <th class="py-1 pr-3 font-medium">{{ t('clAccount.number') }}</th>
            <th class="px-3 font-medium">{{ t('clAccount.date') }}</th>
            <th class="px-3 font-medium">{{ t('clAccount.due') }}</th>
            <th class="px-3 text-right font-medium">{{ t('clAccount.outstanding') }}</th>
            <th class="pl-3 text-right font-medium">{{ t('clAccount.allocAmount') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="l in check.lines" :key="l.inv.id" class="border-b border-border" :data-testid="`alloc-row-${l.inv.invoice_number}`">
            <td class="py-1.5 pr-3">{{ l.inv.invoice_number }}</td>
            <td class="px-3">{{ $date(l.inv.invoice_date) }}</td>
            <td class="px-3">{{ $date(l.inv.due_date) }}</td>
            <td class="px-3 text-right tabular-nums">{{ $money(l.inv.outstanding) }}</td>
            <td class="py-1 pl-3">
              <Input
                class="ml-auto w-36 text-right"
                :model-value="model[l.inv.id] ?? ''"
                :name="`alloc_${l.inv.id}`"
                inputmode="decimal"
                :aria-label="t('clAccount.allocFor', { number: l.inv.invoice_number })"
                :aria-invalid="l.bad || l.over"
                @update:model-value="(v) => set(l.inv.id, String(v ?? ''))"
              />
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="m-0 mt-2 text-right text-sm" data-testid="unallocated">{{ t('clAccount.unallocated', { amount: $money(fromMilli(check.unallocated > 0n ? check.unallocated : 0n)) }) }}</p>
    <p v-if="check.problem" role="alert" class="m-0 mt-1 text-right text-sm text-destructive" data-testid="alloc-error">{{ check.problem }}</p>
  </div>
</template>
