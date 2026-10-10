<script setup lang="ts">
import { SlidersHorizontal } from 'lucide-vue-next'
import { computed, ref, useAttrs, useSlots } from 'vue'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { useLayoutMode } from '@/composables/useLayoutMode'
import { t } from '@/i18n'

/**
 * The filters of a list. From a tablet up they are the row of fields the page always had, in a form: the search field, the other filters, and the buttons (`#search`, the
 * default slot, `#actions`). On a phone the bar is short: the search field and a "Filter (n)" button, `n` being how many filters are on; the button opens a sheet from the bottom
 * edge that has all the filters and the buttons. A list that has only a search (no default slot) shows no button. Submitting, from the bar or from the sheet, emits `submit`.
 * The page's own classes and attributes (`class`, `role`, `data-testid`) go on the form of a tablet or a desktop; a phone has the layout of this bar.
 */
defineOptions({ inheritAttrs: false })
defineProps<{ /** How many filters are on (the search is not counted: it is in the bar). */ active?: number; /** A bar that is not a form on a wide screen (chips that are not submitted). */ plain?: boolean }>()
const emit = defineEmits<{ submit: [] }>()

const attrs = useAttrs()
const slots = useSlots()
const { mode } = useLayoutMode()
const phone = computed(() => mode.value === 'drawer')
const hasFilters = computed(() => !!slots.default)
const open = ref(false)

/** The attributes of the page without its class: on a phone the layout is the bar's. */
const phoneAttrs = computed(() => {
  const { class: _class, ...rest } = attrs
  return rest
})

function submit(): void {
  open.value = false
  emit('submit')
}
</script>

<template>
  <component :is="plain ? 'div' : 'form'" v-if="!phone" v-bind="attrs" data-slot="filter-bar" @submit.prevent="emit('submit')">
    <slot name="search" />
    <slot />
    <slot name="actions" />
  </component>

  <template v-else>
    <component :is="plain ? 'div' : 'form'" v-bind="phoneAttrs" class="mb-4 flex items-end gap-2" data-slot="filter-bar" @submit.prevent="emit('submit')">
      <div v-if="$slots.search" class="min-w-0 flex-1"><slot name="search" /></div>
      <Button v-if="hasFilters" type="button" variant="outline" :class="$slots.search ? 'shrink-0' : 'w-full'" data-testid="open-filters" @click="open = true">
        <SlidersHorizontal />{{ t('filters.button') }}<span v-if="active" data-testid="filter-count">({{ active }})</span>
      </Button>
    </component>
    <Sheet v-if="hasFilters" v-model:open="open">
      <SheetContent side="bottom" class="p-4" data-testid="filter-sheet">
        <SheetTitle class="text-lg">{{ t('filters.title') }}</SheetTitle>
        <SheetDescription class="sr-only">{{ t('filters.title') }}</SheetDescription>
        <form
          class="mt-3 grid gap-4 [&_[data-slot=form-field]]:w-full [&_[data-slot=form-field]]:max-w-none"
          novalidate
          data-testid="filter-sheet-form"
          @submit.prevent="submit"
        >
          <slot />
          <div class="flex flex-wrap justify-end gap-2" data-slot="filter-actions">
            <slot name="actions"><Button type="submit" data-testid="filters-done">{{ t('filters.done') }}</Button></slot>
          </div>
        </form>
      </SheetContent>
    </Sheet>
  </template>
</template>
