<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import type { TierRow } from '@/utils/banquetMoney'

/** The tiers of a cancellation policy as rows: from how many days before, up to how many (blank: no limit), the percent of the base and whether it is a fee or a forfeit of the deposit. */
const rows = defineModel<TierRow[]>({ required: true })
function add(): void {
  rows.value = [...rows.value, { days_from: 0, days_to: null, percent: '100', action: 'FEE' }]
}
function remove(i: number): void {
  rows.value = rows.value.filter((_, at) => at !== i)
}
</script>

<template>
  <div class="flex flex-col gap-2" data-testid="tiers-editor">
    <div v-for="(r, i) in rows" :key="i" class="grid grid-cols-[1fr_1fr_1fr_1.4fr_auto] items-end gap-2" :data-testid="`tier-row-${i}`">
      <label class="text-xs text-muted-foreground">{{ t('banquet.policy.daysFrom') }}<Input v-model.number="r.days_from" type="number" min="0" :name="`tier_from_${i}`" /></label>
      <label class="text-xs text-muted-foreground">{{ t('banquet.policy.daysTo') }}<Input v-model.number="r.days_to" type="number" min="0" :name="`tier_to_${i}`" :placeholder="t('banquet.policy.open')" /></label>
      <label class="text-xs text-muted-foreground">{{ t('banquet.policy.percent') }}<Input v-model="r.percent" inputmode="decimal" :name="`tier_percent_${i}`" /></label>
      <label class="text-xs text-muted-foreground">{{ t('banquet.policy.action') }}
        <NativeSelect v-model="r.action" :name="`tier_action_${i}`"><option value="FEE">{{ t('banquet.policy.FEE') }}</option><option value="FORFEIT">{{ t('banquet.policy.FORFEIT') }}</option></NativeSelect>
      </label>
      <Button type="button" variant="outline" size="sm" :data-testid="`tier-remove-${i}`" @click="remove(i)">{{ t('common.remove') }}</Button>
    </div>
    <div><Button type="button" variant="outline" size="sm" data-testid="tier-add" @click="add">{{ t('banquet.policy.addTier') }}</Button></div>
  </div>
</template>
