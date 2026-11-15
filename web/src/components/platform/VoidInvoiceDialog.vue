<script setup lang="ts">
import { ref, watch } from 'vue'
import { platformApi } from '@/api/platformClient'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'

/** Void an invoice (docs/architecture/39-platform-console.md 11, P6): a reason, nothing else. Voiding cannot be undone, so the reason is required. */
const props = defineProps<{ open: boolean; invoice: components['schemas']['PlatformInvoiceRow'] }>()
const emit = defineEmits<{ 'update:open': [boolean]; voided: [] }>()

const reason = ref('')
const error = ref<ApiError | null>(null)
const busy = ref(false)

watch(
  () => props.open,
  (now) => {
    if (!now) return
    reason.value = ''
    error.value = null
    busy.value = false
  },
  { immediate: true },
)

async function submit(): Promise<void> {
  if (busy.value || reason.value.trim() === '') return
  busy.value = true
  error.value = null
  try {
    await platformApi.POST('/api/v1/platform/invoices/{number}/void', {
      params: { path: { number: props.invoice.number } },
      body: { reason: reason.value.trim() },
    })
    emit('voided')
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="max-w-md p-5" data-testid="void-invoice-dialog">
      <DialogTitle>{{ t('platform.invoices.voidDialog.title', { number: invoice.number }) }}</DialogTitle>
      <DialogDescription class="mt-1">{{ t('platform.invoices.voidDialog.description') }}</DialogDescription>

      <form class="mt-4 grid gap-4" novalidate data-testid="void-form" @submit.prevent="submit">
        <ErrorNotice :error="error" inline data-testid="void-error" />
        <FormField :label="t('platform.invoices.voidDialog.reason')" :hint="t('platform.invoices.voidDialog.reasonHint')" required :error="error?.fieldMessage('reason')">
          <template #default="{ id, describedBy, invalid }">
            <Input :id="id" v-model="reason" v-autofocus name="reason" maxlength="500" :aria-describedby="describedBy" :aria-invalid="invalid" />
          </template>
        </FormField>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('platform.invoices.voidDialog.cancel') }}</Button>
          <Button type="submit" variant="destructive" :disabled="busy || reason.trim() === ''" data-testid="void-submit">{{ t('platform.invoices.voidDialog.submit') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
