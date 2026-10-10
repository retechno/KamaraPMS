<script setup lang="ts">
import { focusProgrammatically } from '@/lib/focus'
import type { DialogContentEmits, DialogContentProps } from 'reka-ui'
import type { HTMLAttributes } from 'vue'
import { DialogContent, DialogOverlay, DialogPortal, useForwardPropsEmits } from 'reka-ui'
import { computed } from 'vue'
import { cn } from '@/lib/utils'

/**
 * A dialog panel with its overlay, rendered in a portal. `variant="left"` and `"right"` are drawers from that side
 * (the menu on a phone; a side sheet for a detail or a form); `"bottom"` is a sheet that rises from the bottom edge (the filters on a phone). Put a DialogTitle inside: it names the dialog for screen readers.
 */
interface Props extends DialogContentProps {
  class?: HTMLAttributes['class']
  variant?: 'center' | 'left' | 'right' | 'bottom'
}

const props = withDefaults(defineProps<Props>(), { variant: 'center' })
const emits = defineEmits<DialogContentEmits>()

const delegated = computed(() => {
  const { class: _class, variant: _variant, ...rest } = props
  return rest
})
const forwarded = useForwardPropsEmits(delegated, emits)

/**
 * A click on a toast is not a click outside the dialog: the toast of a failure sits over the dialog it came from, and closing that dialog (and losing what was typed) to dismiss it would be wrong.
 */
function onInteractOutside(event: CustomEvent<{ originalEvent: Event }> | Event): void {
  const original = (event as CustomEvent<{ originalEvent?: Event }>).detail?.originalEvent ?? event
  const target = original.target
  if (target instanceof Element && target.closest('[data-slot=toast-host]')) event.preventDefault()
}

/**
 * The field a dialog wants focused when it opens: the first element inside it that carries `data-autofocus` (otherwise the first focusable one, as before). It is an attribute and not a ref because the
 * content is rendered in a portal only while the dialog is open.
 */
function onOpenAutoFocus(event: Event): void {
  const content = event.target instanceof Element ? event.target : null
  const wanted = content?.querySelector<HTMLElement>('[data-autofocus]:not([disabled])')
  if (wanted) {
    event.preventDefault()
    focusProgrammatically(wanted)
  }
}

const placement = {
  center: 'left-1/2 top-[12vh] w-[calc(100%-2rem)] max-w-xl -translate-x-1/2 rounded-xl border border-border',
  left: 'inset-y-0 left-0 w-72 max-w-[85vw] border-r border-border',
  right: 'inset-y-0 right-0 w-[32rem] max-w-[95vw] overflow-y-auto border-l border-border',
  bottom: 'inset-x-0 bottom-0 max-h-[85vh] overflow-y-auto rounded-t-xl border-t border-border',
} as const
</script>

<template>
  <DialogPortal>
    <DialogOverlay data-slot="dialog-overlay" class="fixed inset-0 z-50 bg-black/50" />
    <DialogContent
      data-slot="dialog-content"
      v-bind="{ ...forwarded, ...$attrs }"
      @interact-outside="onInteractOutside"
      @open-auto-focus="onOpenAutoFocus"
      :class="cn('fixed z-50 bg-card text-card-foreground shadow-lg outline-none', placement[variant], props.class)"
    >
      <slot />
    </DialogContent>
  </DialogPortal>
</template>
