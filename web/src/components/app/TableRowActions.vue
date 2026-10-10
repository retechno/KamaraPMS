<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { Button } from '@/components/ui/button'
import RowMenu, { type RowMenuItem } from './RowMenu.vue'
import type { RowAction } from './rowActions'

/**
 * The actions of one row: the main ones (`primary`, at most `maxPrimary`) as buttons, every other in the "..." menu. A row of a table shows what fits;
 * a card on a phone shows one main action and the menu.
 */
const props = withDefaults(defineProps<{ actions: RowAction[]; maxPrimary?: number }>(), { maxPrimary: 2 })

const buttons = computed(() => props.actions.filter((a) => a.primary).slice(0, props.maxPrimary))
const menu = computed<RowMenuItem[]>(() =>
  props.actions.filter((a) => !buttons.value.includes(a)).map((a) => ({ key: a.key, label: a.label, destructive: a.destructive, disabled: a.disabled, to: a.to, testId: a.testId })),
)
const run = (key: string): void => props.actions.find((a) => a.key === key)?.onSelect?.()
</script>

<template>
  <div class="flex items-center justify-end gap-1.5" data-slot="row-actions">
    <template v-for="a in buttons" :key="a.key">
      <Button v-if="a.to" as-child size="sm" variant="outline" :data-testid="a.testId ?? a.key">
        <RouterLink :to="a.to">{{ a.label }}</RouterLink>
      </Button>
      <Button v-else type="button" size="sm" variant="outline" :disabled="a.disabled" :data-testid="a.testId ?? a.key" @click="a.onSelect?.()">{{ a.label }}</Button>
    </template>
    <RowMenu :items="menu" @select="run" />
  </div>
</template>
