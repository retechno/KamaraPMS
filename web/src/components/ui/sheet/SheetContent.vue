<script setup lang="ts">
import type { DialogContentEmits, DialogContentProps } from 'reka-ui'
import type { HTMLAttributes } from 'vue'
import { useForwardPropsEmits } from 'reka-ui'
import { computed } from 'vue'
import { DialogContent } from '@/components/ui/dialog'

interface Props extends DialogContentProps {
  class?: HTMLAttributes['class']
  side?: 'left' | 'right' | 'bottom'
}

const props = withDefaults(defineProps<Props>(), { side: 'right' })
const emits = defineEmits<DialogContentEmits>()
const delegated = computed(() => {
  const { side: _side, ...rest } = props
  return rest
})
const forwarded = useForwardPropsEmits(delegated, emits)
</script>

<template>
  <DialogContent v-bind="forwarded" :variant="side" data-slot="sheet-content">
    <slot />
  </DialogContent>
</template>
