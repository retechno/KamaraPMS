<script setup lang="ts">
import { computed } from 'vue'
import FormField from '@/components/app/FormField.vue'
import PeriodPicks from '@/components/app/PeriodPicks.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import type { Period } from '@/utils/periods'

/**
 * The period of a report (from, to, this month, last month) and the button of the CSV file, the same on every report of the inventory. `single` is for a report as of one day.
 */
const from = defineModel<string>('from', { default: '' })
const to = defineModel<string>('to', { default: '' })
const props = withDefaults(defineProps<{ busy?: boolean; single?: boolean; csv?: boolean }>(), { busy: false, single: false, csv: true })
const emit = defineEmits<{ show: []; csv: [] }>()
const property = usePropertyStore()
const businessDate = computed(() => property.clock?.business_date ?? '')

function print(): void {
  window.print()
}

function pick(p: Period): void {
  from.value = p.from
  to.value = p.to
  emit('show')
}
</script>

<template>
  <form class="mb-4 flex flex-wrap items-end gap-3 print:hidden" novalidate data-testid="report-period" @submit.prevent="emit('show')">
    <FormField v-if="!props.single" :label="t('inventory.reports.from')"><template #default="{ id }"><Input :id="id" v-model="from" type="date" name="from" class="w-40" /></template></FormField>
    <FormField :label="props.single ? t('inventory.reports.asOf') : t('inventory.reports.to')"><template #default="{ id }"><Input :id="id" v-model="to" type="date" name="to" class="w-40" /></template></FormField>
    <slot />
    <Button type="submit" variant="outline" :disabled="props.busy" data-testid="report-show">{{ t('inventory.reports.show') }}</Button>
    <PeriodPicks v-if="!props.single" :business-date="businessDate" :years="[]" @pick="pick" />
    <div class="ml-auto flex flex-wrap gap-2">
      <slot name="actions" />
      <Button type="button" variant="outline" data-testid="report-print" @click="print">{{ t('inventory.reports.print') }}</Button>
      <Button v-if="props.csv" type="button" variant="outline" :disabled="props.busy" data-testid="report-csv" @click="emit('csv')">{{ t('inventory.reports.csv') }}</Button>
    </div>
  </form>
</template>
