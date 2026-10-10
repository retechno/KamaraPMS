<script setup lang="ts">
import { computed } from 'vue'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * The choice of the property: a select of the system (a touch screen gives its own picker). It shows "CODE · Name": a name that does not fit is cut with an ellipsis
 * after the code, so the code is always seen, and the title holds the whole name.
 */
defineOptions({ inheritAttrs: false })
const property = usePropertyStore()

const current = computed(() => property.properties.find((p) => p.id === property.currentId))
const full = computed(() => (current.value ? `${current.value.code} · ${current.value.name}` : ''))

function onSelect(event: Event): void {
  const id = Number((event.target as HTMLSelectElement).value)
  if (id) void property.select(id)
}
</script>

<template>
  <label v-if="property.hasProperties" :class="['block min-w-0', $attrs.class]">
    <span class="sr-only">{{ t('shell.property') }}</span>
    <select
      :value="property.currentId ?? ''"
      :title="full"
      class="h-9 w-full min-w-0 overflow-hidden text-ellipsis whitespace-nowrap rounded-md border border-border bg-card px-2 text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
      data-testid="property-switcher"
      @change="onSelect"
    >
      <option v-for="p in property.properties" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
    </select>
  </label>
</template>
