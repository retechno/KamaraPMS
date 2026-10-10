<script setup lang="ts">
import { Check, ChevronsUpDown } from 'lucide-vue-next'
import {
  ComboboxAnchor, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxItemIndicator, ComboboxPortal, ComboboxRoot, ComboboxTrigger, ComboboxViewport,
} from 'reka-ui'
import { computed, nextTick, ref, type HTMLAttributes } from 'vue'
import { t } from '@/i18n'
import { isProgrammaticFocus } from '@/lib/focus'
import { cn } from '@/lib/utils'

/**
 * A select with a search box, for lists that are too long to scan: rooms, accounts, suppliers. Type to narrow the list,
 * arrows and Enter to choose, Escape to close. The options are an array (`{ value, label }`); put the "choose one" or
 * "all" entry in it as an option of its own, as a native select would have it.
 *
 * A native `<select>` that mirrors the value is kept out of sight (the way Reka's own Select does it): a form still has
 * its field `name`, and nothing that reads the field by name has to know the control is not a plain select.
 */
export interface ComboboxOption {
  value: string | number
  label: string
  disabled?: boolean
}

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{
  options: ComboboxOption[]
  id?: string
  name?: string
  disabled?: boolean
  placeholder?: string
  /** The list opens when the field gets the focus (the default). A field that is focused by the page when it appears turns it off, so the list does not cover the form at that moment; a click, a key or the arrow still open it. */
  openOnFocus?: boolean
  class?: HTMLAttributes['class']
}>(), { openOnFocus: true })
const model = defineModel<string | number | null>({ default: null })

const term = ref('')
const open = ref(false)

const selected = computed(() => props.options.find((o) => o.value === model.value))
const shown = computed(() => {
  const q = term.value.trim().toLowerCase()
  return q ? props.options.filter((o) => o.label.toLowerCase().includes(q)) : props.options
})
const labelOf = (value: unknown): string => props.options.find((o) => o.value === value)?.label ?? ''

// The page puts the focus on this field by itself (a form that appears): the list stays closed, it was not asked for.
function onFocusIn(): void {
  if (isProgrammaticFocus()) void nextTick(() => { open.value = false })
}

function onOpen(value: boolean): void {
  open.value = value
  if (!value) term.value = ''
}
</script>

<template>
  <div :class="cn('relative w-full min-w-0', props.class)" data-slot="combobox" @focusin="onFocusIn">
    <ComboboxRoot v-model="model" v-model:open="open" ignore-filter :open-on-focus="openOnFocus" open-on-click :disabled="disabled" @update:open="onOpen">
      <ComboboxAnchor class="relative block">
        <ComboboxInput
          :id="id"
          v-model="term"
          v-bind="$attrs"
          :display-value="labelOf"
          :placeholder="placeholder ?? selected?.label"
          autocomplete="off"
          class="h-9 w-full min-w-0 rounded-md border border-border bg-card pl-3 pr-8 text-sm text-foreground outline-none placeholder:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 aria-[invalid=true]:border-destructive"
        />
        <ComboboxTrigger class="absolute inset-y-0 right-0 flex w-8 cursor-pointer items-center justify-center border-0 bg-transparent text-muted-foreground" :aria-label="t('common.search')" tabindex="-1">
          <ChevronsUpDown class="size-4" aria-hidden="true" />
        </ComboboxTrigger>
      </ComboboxAnchor>
      <ComboboxPortal>
        <ComboboxContent
          position="popper"
          :side-offset="4"
          class="z-[60] w-[var(--reka-combobox-trigger-width)] overflow-hidden rounded-md border border-border bg-popover text-popover-foreground shadow-md"
          data-slot="combobox-content"
        >
          <ComboboxViewport class="max-h-64 overflow-y-auto p-1">
            <ComboboxEmpty class="px-2 py-1.5 text-sm text-muted-foreground">{{ t('common.noResults') }}</ComboboxEmpty>
            <ComboboxItem
              v-for="o in shown"
              :key="String(o.value)"
              :value="o.value"
              :disabled="o.disabled"
              class="relative flex cursor-pointer select-none items-center rounded-sm py-1.5 pl-7 pr-2 text-sm outline-none data-[highlighted]:bg-accent data-[disabled]:pointer-events-none data-[disabled]:opacity-50"
            >
              <ComboboxItemIndicator class="absolute left-2 inline-flex items-center"><Check class="size-4" aria-hidden="true" /></ComboboxItemIndicator>
              <span class="truncate">{{ o.label }}</span>
            </ComboboxItem>
          </ComboboxViewport>
        </ComboboxContent>
      </ComboboxPortal>
    </ComboboxRoot>
    <select v-if="name" v-model="model" :name="name" :disabled="disabled" class="sr-only" tabindex="-1" aria-hidden="true">
      <option v-for="o in options" :key="String(o.value)" :value="o.value" :disabled="o.disabled">{{ o.label }}</option>
    </select>
  </div>
</template>
