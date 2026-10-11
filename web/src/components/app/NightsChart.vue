<script setup lang="ts">
import { cn } from '@/lib/utils'

/**
 * A bar for each night: how much of the sellable rooms is held, with a line at 50% and one at 100% so the height reads without a scale. A night at or above `strongFrom`
 * (percent) has the strong colour. `testPrefix` names the parts for tests (`<prefix>-day`, `<prefix>-bar`).
 */
export interface NightBar {
  key: string
  /** The percent held, 0 to 100 (more is drawn as 100). */
  percent: number
  /** The short date under the bar ("12 Okt"). */
  label: string
  /** What is under the bar on a phone, where there is no room for the month ("12"). */
  short?: string
  title?: string
}
const props = withDefaults(defineProps<{ bars: NightBar[]; strongFrom?: number; testPrefix?: string; height?: string }>(), { strongFrom: 90, testPrefix: 'night', height: 'h-40' })
const clamp = (p: number): number => Math.min(100, Math.max(0, p))
</script>

<template>
  <div :class="cn('flex gap-2', props.height)" data-slot="nights-chart">
    <div class="flex shrink-0 flex-col justify-between pb-4 text-right text-[10px] leading-none text-muted-foreground" aria-hidden="true">
      <span>100%</span>
      <span>50%</span>
      <span>0</span>
    </div>
    <div class="relative min-w-0 flex-1">
      <!-- The lines are drawn over the area of the bars, which is the chart less the row of dates. -->
      <div class="pointer-events-none absolute inset-x-0 bottom-4 top-0" aria-hidden="true">
        <span class="absolute inset-x-0 top-0 border-t border-dashed border-border" data-slot="line-100" />
        <span class="absolute inset-x-0 top-1/2 border-t border-dashed border-border" data-slot="line-50" />
      </div>
      <div class="relative flex h-full items-stretch gap-1.5">
        <div v-for="b in bars" :key="b.key" class="flex min-w-0 flex-1 flex-col" :data-testid="`${testPrefix}-day`" :title="b.title">
          <div class="flex flex-1 items-end">
            <span
              :class="cn('block min-h-0.5 w-full rounded-t-sm', b.percent >= strongFrom ? 'bg-primary' : 'bg-primary/50')"
              :data-testid="`${testPrefix}-bar`"
              :data-strong="b.percent >= strongFrom"
              :style="{ height: `${clamp(b.percent)}%` }"
            />
          </div>
          <small class="h-4 whitespace-nowrap pt-0.5 text-center text-[10px] leading-3 text-muted-foreground">
            <template v-if="b.short"><span class="sm:hidden" data-slot="label-short">{{ b.short }}</span><span class="hidden sm:inline" data-slot="label-long">{{ b.label }}</span></template>
            <template v-else>{{ b.label }}</template>
          </small>
        </div>
      </div>
    </div>
  </div>
</template>
