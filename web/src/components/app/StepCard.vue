<script setup lang="ts">
import { AlertTriangle, CheckCircle2, Circle } from 'lucide-vue-next'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'

/**
 * One step of a checklist (the night audit): a number, a title, a state and what is needed. `ok` is done, `blocked`
 * needs attention, `pending` is not reached yet. The body is the slot.
 */
withDefaults(defineProps<{ step: number; title: string; state: 'ok' | 'blocked' | 'pending'; summary?: string }>(), { summary: '' })
</script>

<template>
  <Card :data-state="state" class="mb-4">
    <CardHeader class="flex-row items-center gap-3 pb-2">
      <span
        :class="cn('grid size-8 shrink-0 place-items-center rounded-full text-sm font-semibold', state === 'ok' ? 'bg-success/15 text-success' : state === 'blocked' ? 'bg-warning/20 text-warning' : 'bg-muted text-muted-foreground')"
        aria-hidden="true"
      >
        <CheckCircle2 v-if="state === 'ok'" class="size-5" />
        <AlertTriangle v-else-if="state === 'blocked'" class="size-5" />
        <Circle v-else class="size-5" />
      </span>
      <div class="min-w-0 flex-1">
        <CardTitle>{{ step }}. {{ title }}</CardTitle>
        <p v-if="summary" class="m-0 mt-0.5 text-sm text-muted-foreground">{{ summary }}</p>
      </div>
      <slot name="aside" />
    </CardHeader>
    <CardContent v-if="$slots.default"><slot /></CardContent>
  </Card>
</template>
