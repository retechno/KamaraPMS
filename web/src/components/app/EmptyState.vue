<script setup lang="ts">
import { Inbox } from 'lucide-vue-next'
import type { Component } from 'vue'
import { RouterLink } from 'vue-router'
import { Button } from '@/components/ui/button'

/**
 * What a list or a page shows when there is nothing to show: a short title, why, and what to do next. The next step is
 * `actionLabel` (a button that emits `action`, or a link when `actionTo` is set); leave it empty for a person who may not
 * do it. A list that is empty because of its filters offers "Clear filters" (`actionVariant="outline"`); one that is empty
 * because nothing was added offers the add button. The action slot is for anything else.
 */
withDefaults(
  defineProps<{ title: string; description?: string; icon?: Component; actionLabel?: string; actionTo?: string; actionVariant?: 'default' | 'outline' }>(),
  { description: '', icon: undefined, actionLabel: '', actionTo: '', actionVariant: 'default' },
)
const emit = defineEmits<{ action: [] }>()
</script>

<template>
  <div class="flex flex-col items-center gap-2 px-4 py-10 text-center" data-slot="empty-state">
    <component :is="icon ?? Inbox" class="size-8 text-muted-foreground" aria-hidden="true" />
    <p class="m-0 text-sm font-medium">{{ title }}</p>
    <p v-if="description" class="m-0 max-w-md text-sm text-muted-foreground">{{ description }}</p>
    <div v-if="actionLabel || $slots.action" class="mt-2 flex flex-wrap justify-center gap-2" data-slot="empty-action">
      <Button v-if="actionLabel && actionTo" as-child :variant="actionVariant"><RouterLink :to="actionTo" data-testid="empty-action">{{ actionLabel }}</RouterLink></Button>
      <Button v-else-if="actionLabel" type="button" :variant="actionVariant" data-testid="empty-action" @click="emit('action')">{{ actionLabel }}</Button>
      <slot name="action" />
    </div>
  </div>
</template>
