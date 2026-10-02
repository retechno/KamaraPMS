<script setup lang="ts">
import type { Component } from 'vue'
import { Card } from '@/components/ui/card'
import { cn } from '@/lib/utils'

/** One figure with its label: occupancy today, arrivals left. `tone` colours the figure when it needs attention. */
withDefaults(
  defineProps<{ label: string; value: string | number; hint?: string; tone?: 'default' | 'success' | 'warning' | 'danger'; icon?: Component }>(),
  { hint: '', tone: 'default', icon: undefined },
)

const toneClass = { default: '', success: 'text-success', warning: 'text-warning', danger: 'text-destructive' } as const
</script>

<template>
  <Card class="flex flex-col gap-1 p-4" data-slot="kpi-card">
    <div class="flex items-center justify-between gap-2 text-sm text-muted-foreground">
      <span>{{ label }}</span>
      <component :is="icon" v-if="icon" class="size-4" aria-hidden="true" />
    </div>
    <p :class="cn('m-0 text-2xl font-semibold tracking-tight', toneClass[tone])" data-slot="kpi-value">{{ value }}</p>
    <p v-if="hint || $slots.default" class="m-0 text-xs text-muted-foreground">
      <slot>{{ hint }}</slot>
    </p>
  </Card>
</template>
