<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { t } from '@/i18n'

/**
 * A question with two answers. It closes with "cancel" on Escape or a click outside. Used through `confirm()` and the
 * ConfirmHost in the shell, or directly with v-model:open when a page wants to keep the dialog in its own template.
 */
const open = defineModel<boolean>('open', { required: true })

withDefaults(
  defineProps<{
    title: string
    description?: string
    confirmLabel?: string
    cancelLabel?: string
    destructive?: boolean
  }>(),
  { description: '', confirmLabel: '', cancelLabel: '', destructive: false },
)
const emit = defineEmits<{ confirm: []; cancel: [] }>()

// The answer goes out before the dialog closes: a host that treats "closed" as "no" must already have its "yes".
function onConfirm(): void {
  emit('confirm')
  open.value = false
}

function onOpenChange(value: boolean): void {
  if (!value && open.value) emit('cancel')
  open.value = value
}
</script>

<template>
  <Dialog :open="open" @update:open="onOpenChange">
    <DialogContent class="max-w-md p-5" data-testid="confirm-dialog">
      <DialogTitle>{{ title }}</DialogTitle>
      <DialogDescription v-if="description" class="mt-2">{{ description }}</DialogDescription>
      <div class="mt-5 flex justify-end gap-2">
        <Button variant="outline" data-testid="confirm-cancel" @click="onOpenChange(false)">{{ cancelLabel || t('common.cancel') }}</Button>
        <Button :variant="destructive ? 'destructive' : 'default'" data-testid="confirm-ok" @click="onConfirm">
          {{ confirmLabel || t('common.confirm') }}
        </Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
