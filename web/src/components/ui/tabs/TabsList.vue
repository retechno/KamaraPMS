<script setup lang="ts">
import type { TabsListProps } from 'reka-ui'
import type { HTMLAttributes } from 'vue'
import { TabsList } from 'reka-ui'
import { cn } from '@/lib/utils'

const props = defineProps<TabsListProps & { class?: HTMLAttributes['class'] }>()
</script>

<template>
  <TabsList data-slot="tabs-list" :loop="loop" :as="as" :as-child="asChild" :class="cn('inline-flex w-fit items-center gap-1 rounded-lg bg-muted p-1', props.class)">
    <slot />
  </TabsList>
</template>
