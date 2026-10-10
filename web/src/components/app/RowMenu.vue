<script setup lang="ts">
import { MoreVertical } from 'lucide-vue-next'
import { ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * The "..." menu of a row: the actions of the row folded into one button, for a phone, where a row of buttons or links is wider than the screen. The items are
 * drawn only while the menu is open, so a row that also shows its actions as buttons (from a tablet up) has each test id once until the menu is opened.
 * Choosing an item closes the menu and runs it.
 */
export interface RowMenuItem {
  key: string
  label: string
  destructive?: boolean
  disabled?: boolean
}

defineProps<{ items: RowMenuItem[]; label?: string }>()
const emit = defineEmits<{ select: [key: string] }>()
const open = ref(false)

function choose(key: string): void {
  open.value = false
  emit('select', key)
}
</script>

<template>
  <Popover v-if="items.length" v-model:open="open">
    <PopoverTrigger as-child>
      <Button type="button" size="sm" variant="ghost" :aria-label="label ?? t('common.moreActions')" data-slot="row-menu-trigger"><MoreVertical /></Button>
    </PopoverTrigger>
    <PopoverContent class="w-48 p-1">
      <div data-slot="row-menu">
        <Button
          v-for="i in items"
          :key="i.key"
          type="button"
          variant="ghost"
          size="sm"
          :disabled="i.disabled"
          :class="cn('w-full justify-start', i.destructive && 'text-destructive')"
          :data-testid="`menu-${i.key}`"
          @click="choose(i.key)"
        >{{ i.label }}</Button>
      </div>
    </PopoverContent>
  </Popover>
</template>
