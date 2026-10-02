<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'

defineOptions({ inheritAttrs: false })
const props = defineProps<{ class?: HTMLAttributes['class'] }>()
const model = defineModel<string | number | null>()
</script>

<template>
  <input
    v-bind="$attrs"
    v-model="model"
    data-slot="input"
    :class="cn('h-9 w-full px-3 placeholder:text-muted-foreground min-w-0 rounded-md border border-border bg-card text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 aria-[invalid=true]:border-destructive', props.class)"
  />
</template>
