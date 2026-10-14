<script setup lang="ts">
import { computed, type HTMLAttributes } from 'vue'
import { Input } from '@/components/ui/input'
import { currentLocale } from '@/i18n'
import { formatMoney } from '@/utils/format'

/**
 * An amount typed with the separators of the language of the page ("2.442.000,5" or "2,442,000.5"): the digits are grouped as they are typed, and the value the page keeps is the plain decimal text
 * the API takes ("2442000.5"). Never a float. `decimals` is how many digits may follow the separator (the currency of the property: 0 for rupiah).
 */
defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ decimals?: number; class?: HTMLAttributes['class'] }>(), { decimals: 0 })
const model = defineModel<string>({ default: '' })

const DECIMAL = { en: '.', id: ',' } as const
const shown = computed(() => {
  const [whole = '', frac] = model.value.split('.')
  return formatMoney(whole) + (frac === undefined ? '' : DECIMAL[currentLocale()] + frac)
})

function type(text: string): void {
  const dec = DECIMAL[currentLocale()]
  const at = props.decimals > 0 ? text.indexOf(dec) : -1
  const whole = (at < 0 ? text : text.slice(0, at)).replace(/\D/g, '').replace(/^0+(?=\d)/, '')
  const frac = at < 0 ? null : text.slice(at + 1).replace(/\D/g, '').slice(0, props.decimals)
  model.value = (whole || (frac === null ? '' : '0')) + (frac === null ? '' : `.${frac}`)
}
</script>

<template>
  <Input v-bind="$attrs" :class="props.class" :model-value="shown" inputmode="decimal" autocomplete="off" @update:model-value="type(String($event ?? ''))" />
</template>
