<script setup lang="ts">
import { Inbox } from 'lucide-vue-next'
import type { Component } from 'vue'

/** What a list or a page shows when there is nothing to show: a short title, why, and what to do next (the action slot). */
withDefaults(defineProps<{ title: string; description?: string; icon?: Component }>(), { description: '', icon: undefined })
</script>

<template>
  <div class="flex flex-col items-center gap-2 px-4 py-10 text-center" data-slot="empty-state">
    <component :is="icon ?? Inbox" class="size-8 text-muted-foreground" aria-hidden="true" />
    <p class="m-0 text-sm font-medium">{{ title }}</p>
    <p v-if="description" class="m-0 max-w-md text-sm text-muted-foreground">{{ description }}</p>
    <div v-if="$slots.action" class="mt-2"><slot name="action" /></div>
  </div>
</template>
