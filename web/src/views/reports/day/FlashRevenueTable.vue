<script setup lang="ts">
import { computed } from 'vue'
import type { DailyFlash, FlashBlock, FlashLine } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'
import FlashCols from './FlashCols.vue'
import { flashValue, flashVariance, rowLabel } from './flashLabels'

/** The revenue of the daily flash as ONE table (docs/design/63): one heading, the groups A, B and C with the outlet as a heading row and one total row per group, then the totals and the sources. */
const props = defineProps<{ revenue: DailyFlash['revenue']; hasBudget: boolean }>()
const th = 'whitespace-nowrap px-2 py-1.5 text-right text-[11px] font-medium uppercase text-muted-foreground'
const td = 'px-2 py-1.5 text-right'
const blockTitle = (b: FlashBlock): string => (b.kind === 'OUTLET' ? b.title : t(`dayReports.flash.block.${b.kind}` as 'dayReports.flash.block.ROOMS'))
const groupTitle = (code: string): string => t(`dayReports.flash.group.${code}` as 'dayReports.flash.group.A')
const totalOf = (title: string): string => t('dayReports.flash.totalOf', { title })
const totals = computed<FlashLine[]>(() => [props.revenue.total_net, props.revenue.service, props.revenue.tax, props.revenue.gtr, ...(props.revenue.journal ? [props.revenue.journal] : [])])
const span = computed(() => (props.hasBudget ? 8 : 6))
const variance = (l: FlashLine) => [td, l.favourable === true && 'text-success', l.favourable === false && 'text-destructive']
</script>

<template>
  <table class="w-full table-fixed border-collapse text-sm tabular-nums" data-testid="flash-revenue-table">
    <FlashCols :has-budget="hasBudget" />
    <thead>
      <tr class="border-b border-border">
        <th class="px-2 py-1.5 text-left text-[11px] font-medium uppercase text-muted-foreground" />
        <th :class="th">{{ t('dayReports.flash.today') }}</th>
        <th :class="th">{{ t('dayReports.flash.mtd') }}</th>
        <th :class="th">{{ t('dayReports.flash.lastMonth') }}</th>
        <th :class="th">{{ t('dayReports.flash.lastYear') }}</th>
        <th v-if="hasBudget" :class="th">{{ t('dayReports.flash.budget') }}</th>
        <th v-if="hasBudget" :class="th">{{ t('dayReports.flash.variance') }}</th>
        <th :class="th">{{ t('dayReports.flash.ytd') }}</th>
      </tr>
    </thead>
    <tbody v-for="g in revenue.groups" :key="g.code" :data-testid="`flash-group-${g.code}`">
      <tr class="bg-muted">
        <th :colspan="span" class="px-2 py-1.5 text-left text-sm font-semibold">{{ g.code }} · {{ groupTitle(g.code) }}</th>
      </tr>
      <template v-for="(b, i) in g.blocks" :key="i">
        <tr :data-testid="`flash-block-${g.code}-${b.kind}`">
          <th :colspan="span" class="px-2 pb-0.5 pt-2 text-left text-xs font-semibold uppercase text-muted-foreground">
            {{ blockTitle(b) }} <Badge v-if="b.kind === 'UNMAPPED'" variant="outline" class="ml-1">{{ t('dayReports.flash.unmapped') }}</Badge>
          </th>
        </tr>
        <tr v-for="l in b.rows" :key="l.key" class="border-b border-border" :data-testid="`flash-row-${l.key}`">
          <td class="truncate px-2 py-1.5 pl-4" :title="rowLabel(l)">{{ rowLabel(l) }}</td>
          <td :class="td">{{ flashValue(l.kind, l.today) }}</td>
          <td :class="td">{{ flashValue(l.kind, l.mtd) }}</td>
          <td :class="[td, 'text-muted-foreground']">{{ flashValue(l.kind, l.last_month) }}</td>
          <td :class="[td, 'text-muted-foreground']">{{ flashValue(l.kind, l.last_year) }}</td>
          <td v-if="hasBudget" :class="td">{{ flashValue(l.kind, l.budget) }}</td>
          <td v-if="hasBudget" :class="variance(l)">{{ flashVariance(l) }}</td>
          <td :class="td">{{ flashValue(l.kind, l.ytd) }}</td>
        </tr>
      </template>
      <tr class="border-b border-border font-semibold" :data-testid="`flash-group-total-${g.code}`">
        <td class="truncate px-2 py-1.5">{{ totalOf(`${g.code} · ${groupTitle(g.code)}`) }}</td>
        <td :class="td">{{ flashValue(g.total.kind, g.total.today) }}</td>
        <td :class="td">{{ flashValue(g.total.kind, g.total.mtd) }}</td>
        <td :class="td">{{ flashValue(g.total.kind, g.total.last_month) }}</td>
        <td :class="td">{{ flashValue(g.total.kind, g.total.last_year) }}</td>
        <td v-if="hasBudget" :class="td">{{ flashValue(g.total.kind, g.total.budget) }}</td>
        <td v-if="hasBudget" :class="variance(g.total)">{{ flashVariance(g.total) }}</td>
        <td :class="td">{{ flashValue(g.total.kind, g.total.ytd) }}</td>
      </tr>
    </tbody>
    <tbody data-testid="flash-totals">
      <tr v-for="l in totals" :key="l.key" class="border-b border-border font-semibold" :data-testid="`flash-row-${l.key}`">
        <td class="px-2 py-1.5">{{ rowLabel(l) }}</td>
        <td :class="td">{{ flashValue(l.kind, l.today) }}</td>
        <td :class="td">{{ flashValue(l.kind, l.mtd) }}</td>
        <td :class="td">{{ flashValue(l.kind, l.last_month) }}</td>
        <td :class="td">{{ flashValue(l.kind, l.last_year) }}</td>
        <td v-if="hasBudget" :class="td">{{ flashValue(l.kind, l.budget) }}</td>
        <td v-if="hasBudget" :class="variance(l)">{{ flashVariance(l) }}</td>
        <td :class="td">{{ flashValue(l.kind, l.ytd) }}</td>
      </tr>
    </tbody>
    <tbody data-testid="flash-sources">
      <tr>
        <th :colspan="span" class="px-2 pb-0.5 pt-3 text-left text-xs font-semibold uppercase text-muted-foreground">{{ t('dayReports.flash.bySource') }}</th>
      </tr>
      <tr v-for="l in revenue.by_source" :key="l.key" class="border-b border-border" :data-testid="`flash-row-${l.key}`">
        <td class="px-2 py-1.5 pl-4">{{ rowLabel(l) }}</td>
        <td :class="td">{{ flashValue(l.kind, l.today) }}</td>
        <td :class="td">{{ flashValue(l.kind, l.mtd) }}</td>
        <td :class="[td, 'text-muted-foreground']">{{ flashValue(l.kind, l.last_month) }}</td>
        <td :class="[td, 'text-muted-foreground']">{{ flashValue(l.kind, l.last_year) }}</td>
        <td v-if="hasBudget" :class="td">{{ flashValue(l.kind, l.budget) }}</td>
        <td v-if="hasBudget" :class="variance(l)">{{ flashVariance(l) }}</td>
        <td :class="td">{{ flashValue(l.kind, l.ytd) }}</td>
      </tr>
    </tbody>
  </table>
</template>
