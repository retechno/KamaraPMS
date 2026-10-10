<script setup lang="ts">
import { computed } from 'vue'
import type { FiscalYear } from '@/api/types'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { fiscalYearToDate, lastMonth, thisMonth, type Period } from '@/utils/periods'

/**
 * Quick periods beside the dates of a report: this month, last month, and the fiscal year to date. All are counted from the business date of the property. The fiscal year
 * is offered only when one is set up for the business date.
 */
const props = defineProps<{ businessDate: string; years: readonly FiscalYear[] }>()
const emit = defineEmits<{ pick: [period: Period] }>()

const options = computed(() => {
  const bd = props.businessDate
  if (!bd) return []
  const fiscal = fiscalYearToDate(props.years, bd)
  return [
    { key: 'month', label: t('periods.thisMonth'), period: thisMonth(bd) },
    { key: 'last-month', label: t('periods.lastMonth'), period: lastMonth(bd) },
    ...(fiscal ? [{ key: 'fiscal-year', label: t('periods.fiscalYear'), period: fiscal }] : []),
  ]
})
</script>

<template>
  <div v-if="options.length" role="group" :aria-label="t('periods.quick')" class="flex flex-wrap items-center gap-1.5" data-testid="period-picks">
    <Button v-for="o in options" :key="o.key" type="button" variant="outline" size="sm" :data-testid="`pick-${o.key}`" @click="emit('pick', o.period)">{{ o.label }}</Button>
  </div>
</template>
