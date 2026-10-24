<script setup lang="ts">
import { computed } from 'vue'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDate } from '@/utils/format'

/**
 * The block that opens a printed report (it shows on paper only; the screen has the page header): the property, the period (or the day) the figures are of, and the moment of printing.
 * The title of the report is the page's own h1. Saved as PDF from the browser, this is what tells a page of a file apart.
 */
const props = defineProps<{ from?: string; to?: string }>()
const property = usePropertyStore()
const name = computed(() => property.current?.name ?? '')
const period = computed(() => {
  if (props.from && props.to) return `${formatDate(props.from)} – ${formatDate(props.to)}`
  const one = props.to ?? props.from
  return one ? formatDate(one) : ''
})
const printed = computed(() => new Date().toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }))
</script>

<template>
  <dl class="m-0 mb-4 hidden grid-cols-3 gap-x-6 border-b border-border pb-2 text-xs print:grid" data-testid="print-header">
    <div><dt class="text-muted-foreground">{{ t('inventory.reports.printProperty') }}</dt><dd class="m-0 font-medium">{{ name }}</dd></div>
    <div><dt class="text-muted-foreground">{{ t('inventory.reports.printPeriod') }}</dt><dd class="m-0 font-medium">{{ period }}</dd></div>
    <div><dt class="text-muted-foreground">{{ t('inventory.reports.printedAt') }}</dt><dd class="m-0 font-medium">{{ printed }}</dd></div>
  </dl>
</template>
