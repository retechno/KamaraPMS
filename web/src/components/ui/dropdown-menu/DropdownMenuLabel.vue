<script setup lang="ts">
import type { DropdownMenuLabelProps } from 'reka-ui'
import type { HTMLAttributes } from 'vue'
import { DropdownMenuLabel } from 'reka-ui'
import { cn } from '@/lib/utils'

const props = defineProps<DropdownMenuLabelProps & { class?: HTMLAttributes['class'] }>()
</script>

<template>
  <DropdownMenuLabel data-slot="dropdown-menu-label" :as="as" :as-child="asChild" :class="cn('px-2 py-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground', props.class)">
    <slot />
  </DropdownMenuLabel>
</template>
