<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'

// A styled native select: it opens the system picker, which is what a touch screen wants. Options go in the slot.
defineOptions({ inheritAttrs: false })
const props = defineProps<{ class?: HTMLAttributes['class'] }>()
const model = defineModel<string | number>()
</script>

<template>
  <select v-bind="$attrs" v-model="model" data-slot="select" :class="cn('h-9 w-full px-2 min-w-0 rounded-md border border-border bg-card text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 aria-[invalid=true]:border-destructive', props.class)">
    <slot />
  </select>
</template>
