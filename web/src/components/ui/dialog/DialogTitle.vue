<script setup lang="ts">
import type { DialogTitleProps } from 'reka-ui'
import type { HTMLAttributes } from 'vue'
import { DialogTitle } from 'reka-ui'
import { cn } from '@/lib/utils'

const props = defineProps<DialogTitleProps & { class?: HTMLAttributes['class'] }>()
</script>

<template>
  <DialogTitle data-slot="dialog-title" :as="as" :as-child="asChild" :class="cn('text-base font-semibold leading-none', props.class)">
    <slot />
  </DialogTitle>
</template>
