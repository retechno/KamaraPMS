<script setup lang="ts">
import { computed, onUnmounted } from 'vue'
import ConfirmDialog from '@/components/app/ConfirmDialog.vue'
import { registerConfirmHost, useConfirmState } from '@/composables/useConfirm'

/** Shows the question raised by `confirm()`. The shell mounts one. */
const unregister = registerConfirmHost()
onUnmounted(unregister)

const { pending, answer } = useConfirmState()
const open = computed({
  get: () => pending.value !== null,
  set: (v: boolean) => {
    if (!v && pending.value) answer(false)
  },
})
</script>

<template>
  <ConfirmDialog
    v-model:open="open"
    :title="pending?.options.title ?? ''"
    :description="pending?.options.description"
    :confirm-label="pending?.options.confirmLabel"
    :cancel-label="pending?.options.cancelLabel"
    :destructive="pending?.options.destructive"
    @confirm="answer(true)"
  />
</template>
