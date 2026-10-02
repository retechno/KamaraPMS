<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: HTMLAttributes['class'] }>()
</script>

<template>
  <h2 data-slot="card-title" :class="cn('m-0 text-base font-semibold leading-tight', props.class)">
    <slot />
  </h2>
</template>
