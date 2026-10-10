<script setup lang="ts">
import { Pin } from 'lucide-vue-next'
import { computed, inject, type HTMLAttributes } from 'vue'
import { routeLocationKey } from 'vue-router'
import { useNavPins } from '@/composables/useNavPins'
import { useVisibleNavigation } from '@/composables/useVisibleNavigation'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * The top of a page: its title (the page's only h1), an optional line under it, extra marks beside the title (a
 * status badge) and the page's main actions on the right.
 */
const props = defineProps<{ title: string; description?: string; class?: HTMLAttributes['class'] }>()

// A page that is an item of the menu (the address is the item's, not a page below it) can be pinned to the top of the menu, from here.
const route = inject(routeLocationKey, null)
let pins: ReturnType<typeof useNavPins> | null = null
let menu: ReturnType<typeof useVisibleNavigation> | null = null
try {
  if (route) {
    pins = useNavPins()
    menu = useVisibleNavigation()
  }
} catch {
  // a page shown without the stores (a test of a page on its own): no pin
}
const item = computed(() => menu?.items.value.find((x) => x.item.to === route?.path)?.item ?? null)
const pinned = computed(() => !!item.value && !!pins?.isPinned(item.value.id))
</script>

<template>
  <header :class="cn('mb-5 flex flex-wrap items-start justify-between gap-x-4 gap-y-2', props.class)" data-slot="page-header">
    <div class="min-w-0">
      <div class="flex flex-wrap items-center gap-2">
        <h1 class="m-0 text-2xl font-semibold tracking-tight">{{ title }}</h1>
        <button
          v-if="item && pins"
          type="button"
          :class="cn('grid size-7 cursor-pointer place-items-center rounded-md border-0 bg-transparent hover:bg-accent', pinned ? 'text-foreground' : 'text-muted-foreground')"
          :aria-pressed="pinned"
          :aria-label="pinned ? t('nav.unpinPage') : t('nav.pinPage')"
          :title="pinned ? t('nav.unpinPage') : t('nav.pinPage')"
          data-testid="pin-page"
          @click="pins.togglePin(item.id)"
        >
          <!-- a pinned page has the pin filled; the pin of a page that is not is an outline -->
          <Pin class="size-4" :fill="pinned ? 'currentColor' : 'none'" aria-hidden="true" data-testid="pin-icon" :data-filled="pinned" />
        </button>
        <slot name="marks" />
      </div>
      <p v-if="description" class="m-0 mt-1 text-sm text-muted-foreground">{{ description }}</p>
    </div>
    <div v-if="$slots.actions" class="flex flex-wrap items-center gap-2" data-slot="page-actions">
      <slot name="actions" />
    </div>
  </header>
</template>
