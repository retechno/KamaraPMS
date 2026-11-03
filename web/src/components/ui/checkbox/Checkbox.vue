<script setup lang="ts">
import { computed, nextTick, useAttrs, useSlots, type HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'

/**
 * A checkbox that is a target for a finger. It always draws a `<label>` (inline-flex) that holds the native `<input type="checkbox">` and the text (the default slot): on a coarse pointer
 * (a phone, a tablet) the box is 24 px and the label is at least 44 px high, so a tap on the text counts. With no slot it is the box alone, in a 44 x 44 area on a coarse pointer, and it
 * needs an `aria-label` (or `aria-labelledby`): a box without text has no name. The type system cannot see a slot, so this is checked when it is drawn (a warning in development) and by
 * `Checkbox.usage.test.ts`, which reads every template.
 *
 * The attributes (`name`, `data-testid`, `disabled`, `id`, `class`, `aria-*`, `@change`) go on the native input, where a form and a test read them; `labelClass` styles the label.
 *
 * `v-model` is a boolean, or an array when there is a `:value` (as on a native checkbox: the value is in the array when the box is on). `indeterminate` is for a "select all" that
 * holds some of its rows. A caller that keeps its own state can pass `:model-value` and listen to `@change`, as it did with `:checked`: the box shows what it is given. A box with no
 * `modelValue` at all is not controlled and keeps what the person did.
 */
defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{
    modelValue?: boolean | readonly unknown[] | null
    /** The value that is in the array of `v-model` when the box is on. */
    value?: unknown
    indeterminate?: boolean
    labelClass?: HTMLAttributes['class']
  }>(),
  { modelValue: undefined }, // not given is not false (a Boolean prop that is absent would be): the box is then not controlled
)
const emit = defineEmits<{ 'update:modelValue': [value: boolean | unknown[]] }>()

const attrs = useAttrs()
const slots = useSlots()
const hasText = computed(() => !!slots.default)
if (import.meta.env.DEV && !hasText.value && !attrs['aria-label'] && !attrs['aria-labelledby']) {
  console.warn('[Checkbox] a checkbox with no text needs an aria-label')
}

/** What goes on the native input: all the attributes of the caller but its class (that is merged below); a box that holds some of its rows says so (`aria-checked="mixed"`). */
function inputAttrs(): Record<string, unknown> {
  const { class: _class, ...rest } = attrs
  return props.indeterminate ? { ...rest, 'aria-checked': 'mixed' } : rest
}

const same = (a: unknown, b: unknown): boolean => a === b || (typeof a !== 'object' && typeof b !== 'object' && String(a) === String(b))
const isChecked = computed(() => (Array.isArray(props.modelValue) ? props.modelValue.some((v) => same(v, props.value)) : !!props.modelValue))

function onChange(event: Event): void {
  const el = event.target as HTMLInputElement
  const now = props.modelValue
  if (Array.isArray(now)) {
    const rest = now.filter((v) => !same(v, props.value))
    emit('update:modelValue', el.checked ? [...rest, props.value] : rest)
  } else emit('update:modelValue', el.checked)
  // what is shown is what the page holds: a page that did not take the change puts the box back (a box that is not controlled keeps what the person did)
  void nextTick(() => {
    if (now !== undefined) el.checked = isChecked.value
    el.indeterminate = !!props.indeterminate
  })
}
</script>

<template>
  <label
    :class="
      cn(
        'inline-flex cursor-pointer items-center gap-2 align-middle pointer-coarse:min-h-[44px] has-disabled:cursor-not-allowed',
        !hasText && 'pointer-coarse:min-w-[44px] pointer-coarse:justify-center',
        props.labelClass,
      )
    "
    data-slot="checkbox"
  >
    <input
      v-bind="inputAttrs()"
      type="checkbox"
      :class="cn('size-4 shrink-0 cursor-pointer accent-primary disabled:cursor-not-allowed pointer-coarse:size-[24px]', attrs.class as HTMLAttributes['class'])"
      :checked="isChecked"
      :value="props.value"
      :indeterminate.prop="props.indeterminate"
      @change="onChange"
    />
    <slot />
  </label>
</template>
