<script setup lang="ts">
import { cn } from '@/lib/utils'
import type { HTMLAttributes } from 'vue'

/**
 * The top of a page: its title (the page's only h1), an optional line under it, extra marks beside the title (a
 * status badge) and the page's main actions on the right.
 */
const props = defineProps<{ title: string; description?: string; class?: HTMLAttributes['class'] }>()
</script>

<template>
  <header :class="cn('mb-5 flex flex-wrap items-start justify-between gap-x-4 gap-y-2', props.class)" data-slot="page-header">
    <div class="min-w-0">
      <div class="flex flex-wrap items-center gap-2">
        <h1 class="m-0 text-2xl font-semibold tracking-tight">{{ title }}</h1>
        <slot name="marks" />
      </div>
      <p v-if="description" class="m-0 mt-1 text-sm text-muted-foreground">{{ description }}</p>
    </div>
    <div v-if="$slots.actions" class="flex flex-wrap items-center gap-2" data-slot="page-actions">
      <slot name="actions" />
    </div>
  </header>
</template>
