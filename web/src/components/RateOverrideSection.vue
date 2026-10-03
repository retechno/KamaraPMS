<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'

/** A night of the stay as the editor needs it: the standard price, and what it is now. */
export interface OverrideNight {
  date: string
  standard: string
  current: string
}
export interface NightChange {
  date: string
  amount: string
}
export interface RateChange {
  overrides: NightChange[]
  reason: string
}

/**
 * The editor of the nightly prices of a stay: a button opens a table with the standard price of each night and a field
 * for the agreed price. Only the nights whose price differs from what it is now are sent. A change needs a reason, and
 * the approval of someone who may approve it (the parent asks for it, see `needsApproval`). Shown only to who holds
 * reservation.override_rate.
 */
const props = defineProps<{ nights: OverrideNight[]; modelValue: RateChange }>()
const emit = defineEmits<{ 'update:modelValue': [value: RateChange] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const canApprove = computed(() => auth.can('reservation.override_rate_approve', property.currentId))

const open = ref(false)
const amounts = reactive<Record<string, string>>({})
const all = ref('')

function changes(): NightChange[] {
  return props.nights
    .filter((n) => {
      const v = (amounts[n.date] ?? '').trim()
      return v !== '' && Number(v) !== Number(n.current)
    })
    .map((n) => ({ date: n.date, amount: (amounts[n.date] ?? '').trim() }))
}

function publish(reason = props.modelValue.reason): void {
  emit('update:modelValue', { overrides: open.value ? changes() : [], reason })
}

function reset(): void {
  for (const k of Object.keys(amounts)) delete amounts[k]
  for (const n of props.nights) amounts[n.date] = n.current
  all.value = ''
}

function applyAll(): void {
  if (!all.value.trim()) return
  for (const n of props.nights) amounts[n.date] = all.value.trim()
  publish()
}

function toggle(): void {
  open.value = !open.value
  if (!open.value) reset()
  publish()
}

// A new set of nights (another room type, plan or dates) starts over.
watch(() => props.nights.map((n) => `${n.date}:${n.current}`).join('|'), () => {
  reset()
  publish()
}, { immediate: true })

const isChanged = (n: OverrideNight): boolean => changes().some((c) => c.date === n.date)
const money = (v: string): string => formatMoney(v)
</script>

<template>
  <div data-testid="rate-override" class="rounded-lg border border-border p-3">
    <div class="flex items-center justify-between gap-3">
      <Button type="button" variant="outline" size="sm" data-testid="override-toggle" @click="toggle">{{ open ? t('rateOverride.cancel') : t('rateOverride.change') }}</Button>
      <small v-if="open" class="text-xs text-muted-foreground" data-testid="override-approval-hint">{{ canApprove ? t('rateOverride.selfApprove') : t('rateOverride.needsApproval') }}</small>
    </div>

    <template v-if="open">
      <div class="mt-3 flex flex-wrap items-end gap-3">
        <FormField class="w-44" :label="t('rateOverride.allNights')">
          <template #default="{ id }"><Input :id="id" v-model="all" name="override_all" inputmode="decimal" @keydown.enter.prevent="applyAll" /></template>
        </FormField>
        <Button type="button" variant="outline" size="sm" data-testid="override-apply-all" @click="applyAll">{{ t('rateOverride.apply') }}</Button>
      </div>
      <div class="mt-3 overflow-x-auto">
        <table class="w-full border-collapse text-sm" data-testid="override-table">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="px-2 py-1 font-medium">{{ t('rateOverride.night') }}</th>
              <th class="px-2 py-1 text-right font-medium">{{ t('rateOverride.standard') }}</th>
              <th class="px-2 py-1 font-medium">{{ t('rateOverride.agreed') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="n in nights" :key="n.date" :data-testid="`override-row-${n.date}`" :class="isChanged(n) && 'bg-warning/10'">
              <td class="px-2 py-1">{{ $date(n.date) }}</td>
              <td class="px-2 py-1 text-right tabular-nums text-muted-foreground">{{ money(n.standard) }}</td>
              <td class="px-2 py-1">
                <Input
                  v-model="amounts[n.date]"
                  :name="`override_amount_${n.date}`"
                  inputmode="decimal"
                  class="h-8 w-40"
                  @update:model-value="publish()"
                />
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <FormField class="mt-3 max-w-md" :label="t('rateOverride.reason')">
        <template #default="{ id }">
          <Input :id="id" :model-value="modelValue.reason" name="rate_override_reason" maxlength="500" :placeholder="t('rateOverride.reasonHint')" @update:model-value="(v) => publish(String(v))" />
        </template>
      </FormField>
    </template>
  </div>
</template>
