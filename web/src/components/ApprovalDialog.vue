<script setup lang="ts">
import { ref } from 'vue'
import type { ApiError } from '@/api/problem'
import type { Approval } from '@/api/types'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'

/**
 * The shared "Approval" dialog: every correction (adjustment, reversal, void, refund) needs the approver's own
 * credentials. The approver may be the person who is signed in (their email is prefilled) or someone else.
 * It is a Dialog (focus stays inside, Escape cancels); a click outside does not close it, so a typed password is not lost by a slip.
 * The password lives only in this component's state: it is handed to `approve` once and cleared, never stored.
 */
defineProps<{
  title: string
  message?: string
  busy?: boolean
  error?: ApiError | null
}>()
const emit = defineEmits<{ approve: [approval: Approval]; cancel: [] }>()

const auth = useAuthStore()
const email = ref(auth.me?.user.email ?? '')
const password = ref('')
const passwordBox = ref<{ $el: HTMLInputElement } | null>(null)

// The password is where the approver starts typing (the e-mail is usually prefilled).
function focusPassword(event: Event): void {
  event.preventDefault()
  passwordBox.value?.$el.focus()
}

function submit(): void {
  const approval = { email: email.value.trim(), password: password.value }
  password.value = ''
  emit('approve', approval)
}

function cancel(): void {
  password.value = ''
  emit('cancel')
}
</script>

<template>
  <Dialog :open="true" @update:open="(value) => { if (!value) cancel() }">
    <DialogContent class="max-w-md p-5" @open-auto-focus="focusPassword" @interact-outside.prevent>
      <form novalidate data-testid="approval-dialog" @submit.prevent="submit">
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription class="mb-3 mt-1">{{ message ?? t('approval.message') }}</DialogDescription>
        <p v-if="error" class="alert" role="alert" data-testid="approval-error">{{ error.message }} <code>{{ error.code }}</code></p>
        <div class="flex flex-col gap-3">
          <FormField :label="t('approval.email')">
            <template #default="{ id }"><Input :id="id" v-model="email" name="approval_email" type="email" autocomplete="off" /></template>
          </FormField>
          <FormField :label="t('approval.password')">
            <template #default="{ id }"><Input :id="id" ref="passwordBox" v-model="password" name="approval_password" type="password" autocomplete="off" /></template>
          </FormField>
        </div>
        <div class="mt-5 flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="approval-cancel" @click="cancel">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !email || !password" data-testid="approval-submit">{{ t('approval.approve') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
