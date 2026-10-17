<script setup lang="ts">
import MoneyInput from '@/components/app/MoneyInput.vue'

/**
 * A quantity typed with the separators of the language of the page ("12,5" in Indonesian, "12.5" in English): the digits are grouped as they are typed and the value the page keeps is the plain decimal text the
 * API takes ("12.5"). Never a float. `decimals` is the precision of the quantity (six, as the stock keeps it; docs/architecture/35-inventory.md 4.1). It behaves like MoneyInput and takes its attributes.
 */
defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ decimals?: number }>(), { decimals: 6 })
const model = defineModel<string>({ default: '' })
</script>

<template>
  <MoneyInput v-bind="$attrs" v-model="model" :decimals="props.decimals" />
</template>
