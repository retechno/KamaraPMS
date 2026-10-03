<script setup lang="ts">
import type { PopoverContentProps } from 'reka-ui'
import type { HTMLAttributes } from 'vue'
import { PopoverContent, PopoverPortal, useForwardProps } from 'reka-ui'
import { cn } from '@/lib/utils'

const props = withDefaults(defineProps<PopoverContentProps & { class?: HTMLAttributes['class'] }>(), { align: 'end', sideOffset: 6 })
const forwarded = useForwardProps(() => ({ align: props.align, sideOffset: props.sideOffset, side: props.side }))
</script>

<template>
  <PopoverPortal>
    <PopoverContent
      data-slot="popover-content"
      v-bind="forwarded"
      :class="cn('z-50 w-64 rounded-md border border-border bg-popover p-3 text-sm text-popover-foreground shadow-md outline-none', props.class)"
    >
      <slot />
    </PopoverContent>
  </PopoverPortal>
</template>
