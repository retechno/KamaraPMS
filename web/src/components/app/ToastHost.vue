<script setup lang="ts">
import { AlertTriangle, CheckCircle2, Info, X } from 'lucide-vue-next'
import { toast, useToasts, type ToastKind } from '@/composables/useToast'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * Shows the toasts raised with `toast.success(...)`, stacked at the bottom right. The shell mounts one. It sits above the dialogs and the sheets (z-60 against z-50) and a click on it does not close
 * them (DialogContent leaves it out of "outside").
 */
const { items, dismiss } = useToasts()

const icons = { success: CheckCircle2, error: AlertTriangle, info: Info } as const
const tone: Record<ToastKind, string> = {
  success: 'border-success/40 [&_svg:first-child]:text-success-text',
  error: 'border-destructive/40 [&_svg:first-child]:text-destructive',
  info: 'border-border [&_svg:first-child]:text-muted-foreground',
}
</script>

<template>
  <div data-slot="toast-host" class="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2" data-testid="toasts">
    <div
      v-for="item in items"
      :key="item.id"
      :role="item.kind === 'error' ? 'alert' : 'status'"
      :data-testid="`toast-${item.kind}`"
      :class="cn('pointer-events-auto flex items-start gap-2 rounded-lg border bg-card p-3 text-sm shadow-lg', tone[item.kind])"
      @mouseenter="toast.hold(item.id)"
      @mouseleave="toast.release(item.id)"
      @focusin="toast.hold(item.id)"
      @focusout="toast.release(item.id)"
    >
      <component :is="icons[item.kind]" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <p class="m-0 flex-1 break-words">{{ item.message }}</p>
      <button
        type="button"
        class="cursor-pointer rounded border-0 bg-transparent p-0.5 text-muted-foreground hover:text-foreground"
        :aria-label="t('common.close')"
        data-testid="toast-close"
        @click="dismiss(item.id)"
      >
        <X class="size-4" aria-hidden="true" />
      </button>
    </div>
  </div>
</template>
