<script setup lang="ts">
import { computed, ref } from 'vue'
import { t } from '@/i18n'

/**
 * A line of one percentage a day (the food cost %) with the target as a dashed line (docs/architecture/35-inventory.md 12.2, mockup 31). One axis, in percent, from zero; the line is 2 px; a legend names what
 * is drawn; a day above the target is a square with an outline and the others a dot, so the colour is not the only difference; the exact figures are in a tooltip on hover or focus of each day.
 * The drawing is for the eye only: the figures stay the strings the server sent and the table beside the chart reads them.
 */
export interface CostPoint {
  key: string
  /** The date under the point ("12 Okt"). */
  label: string
  /** The percent, or null for a day with no revenue (the line breaks there). */
  value: number | null
  /** The text of the tooltip. */
  title: string
  above: boolean
}
const props = withDefaults(defineProps<{ points: CostPoint[]; target?: number | null; seriesLabel: string; testPrefix?: string }>(), { target: null, testPrefix: 'cost' })

const W = 640
const H = 220
const PAD = { left: 40, right: 12, top: 12, bottom: 28 }
const innerW = W - PAD.left - PAD.right
const innerH = H - PAD.top - PAD.bottom

const top = computed(() => {
  const max = Math.max(props.target ?? 0, ...props.points.map((p) => p.value ?? 0), 1)
  return Math.ceil(max / 5) * 5 || 5
})
const x = (i: number): number => PAD.left + (props.points.length <= 1 ? innerW / 2 : (i / (props.points.length - 1)) * innerW)
const y = (v: number): number => PAD.top + innerH - (v / top.value) * innerH
const ticks = computed(() => [0, top.value / 2, top.value])

/** The line is drawn in runs: a day with no value ends one run and the next starts another. */
const runs = computed(() => {
  const out: string[] = []
  let cur: string[] = []
  props.points.forEach((p, i) => {
    if (p.value === null) {
      if (cur.length) out.push(cur.join(' '))
      cur = []
    } else {
      cur.push(`${x(i).toFixed(1)},${y(p.value).toFixed(1)}`)
    }
  })
  if (cur.length) out.push(cur.join(' '))
  return out
})

const active = ref<number | null>(null)
const tip = computed(() => {
  const i = active.value
  if (i === null) return null
  const p = props.points[i]
  return p ? { text: p.title, left: `${(x(i) / W) * 100}%`, top: `${((p.value === null ? PAD.top : y(p.value)) / H) * 100}%` } : null
})
const labelEvery = computed(() => Math.max(1, Math.ceil(props.points.length / 10)))
const summary = computed(() => t('inventory.reports.chartSummary', { series: props.seriesLabel, n: props.points.length }))
</script>

<template>
  <figure class="relative m-0" :data-testid="`${testPrefix}-chart`">
    <div class="relative">
      <svg :viewBox="`0 0 ${W} ${H}`" class="block h-auto w-full" role="img" :aria-label="summary" @mouseleave="active = null">
        <g class="text-muted-foreground" font-size="10" fill="currentColor">
          <template v-for="v in ticks" :key="v">
            <line :x1="PAD.left" :x2="W - PAD.right" :y1="y(v)" :y2="y(v)" stroke="currentColor" stroke-opacity="0.2" stroke-width="1" />
            <text :x="PAD.left - 6" :y="y(v) + 3" text-anchor="end">{{ v }}%</text>
          </template>
          <template v-for="(p, i) in points" :key="p.key">
            <text v-if="i % labelEvery === 0" :x="x(i)" :y="H - 8" text-anchor="middle">{{ p.label }}</text>
          </template>
        </g>
        <line v-if="target !== null" :x1="PAD.left" :x2="W - PAD.right" :y1="y(target)" :y2="y(target)" class="text-warning-text" stroke="currentColor" stroke-width="2" stroke-dasharray="6 4" :data-testid="`${testPrefix}-target`" />
        <polyline v-for="(r, n) in runs" :key="n" :points="r" fill="none" class="text-primary" stroke="currentColor" stroke-width="2" vector-effect="non-scaling-stroke" stroke-linecap="round" stroke-linejoin="round" :data-testid="`${testPrefix}-line`" />
        <template v-for="(p, i) in points" :key="p.key">
          <template v-if="p.value !== null">
            <rect v-if="p.above" :x="x(i) - 4.5" :y="y(p.value) - 4.5" width="9" height="9" class="text-destructive" fill="currentColor" stroke="white" stroke-width="1.5" :data-testid="`${testPrefix}-above`" />
            <circle v-else :cx="x(i)" :cy="y(p.value)" r="3.5" class="text-primary" fill="currentColor" />
          </template>
          <!-- a wide invisible strip catches the pointer and the keyboard for each day -->
          <rect :x="x(i) - innerW / Math.max(points.length, 1) / 2" :y="PAD.top" :width="innerW / Math.max(points.length, 1)" :height="innerH" fill="transparent" tabindex="0" :aria-label="p.title"
            :data-testid="`${testPrefix}-hit`" @mouseenter="active = i" @focus="active = i" @blur="active = null" />
        </template>
      </svg>
      <div v-if="tip" class="pointer-events-none absolute z-10 -translate-x-1/2 -translate-y-full whitespace-nowrap rounded-md border border-border bg-popover px-2 py-1 text-xs text-popover-foreground shadow" :style="{ left: tip.left, top: tip.top }" role="status" :data-testid="`${testPrefix}-tip`">{{ tip.text }}</div>
    </div>
    <figcaption class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground" :data-testid="`${testPrefix}-legend`">
      <span class="inline-flex items-center gap-1.5"><svg width="22" height="8" aria-hidden="true"><line x1="0" y1="4" x2="22" y2="4" class="text-primary" stroke="currentColor" stroke-width="2" /></svg>{{ seriesLabel }}</span>
      <span v-if="target !== null" class="inline-flex items-center gap-1.5"><svg width="22" height="8" aria-hidden="true"><line x1="0" y1="4" x2="22" y2="4" class="text-warning-text" stroke="currentColor" stroke-width="2" stroke-dasharray="6 4" /></svg>{{ t('inventory.reports.targetLine', { target }) }}</span>
      <span class="inline-flex items-center gap-1.5"><svg width="10" height="10" aria-hidden="true"><rect x="0.5" y="0.5" width="9" height="9" class="text-destructive" fill="currentColor" stroke="white" /></svg>{{ t('inventory.reports.aboveTarget') }}</span>
    </figcaption>
  </figure>
</template>
