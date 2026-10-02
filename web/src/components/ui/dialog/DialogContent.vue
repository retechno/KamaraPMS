<script setup lang="ts">
import type { DialogContentEmits, DialogContentProps } from 'reka-ui'
import type { HTMLAttributes } from 'vue'
import { DialogContent, DialogOverlay, DialogPortal, useForwardPropsEmits } from 'reka-ui'
import { computed } from 'vue'
import { cn } from '@/lib/utils'

/**
 * A dialog panel with its overlay, rendered in a portal. `variant="left"` is a drawer that slides in from the left
 * (the menu on a phone). Put a DialogTitle inside: it names the dialog for screen readers.
 */
interface Props extends DialogContentProps {
  class?: HTMLAttributes['class']
  variant?: 'center' | 'left'
}

const props = withDefaults(defineProps<Props>(), { variant: 'center' })
const emits = defineEmits<DialogContentEmits>()

const delegated = computed(() => {
  const { class: _class, variant: _variant, ...rest } = props
  return rest
})
const forwarded = useForwardPropsEmits(delegated, emits)

const placement = {
  center: 'left-1/2 top-[12vh] w-[calc(100%-2rem)] max-w-xl -translate-x-1/2 rounded-xl border border-border',
  left: 'inset-y-0 left-0 w-72 max-w-[85vw] border-r border-border',
} as const
</script>

<template>
  <DialogPortal>
    <DialogOverlay data-slot="dialog-overlay" class="fixed inset-0 z-50 bg-black/50" />
    <DialogContent
      data-slot="dialog-content"
      v-bind="{ ...forwarded, ...$attrs }"
      :class="cn('fixed z-50 bg-card text-card-foreground shadow-lg outline-none', placement[variant], props.class)"
    >
      <slot />
    </DialogContent>
  </DialogPortal>
</template>
