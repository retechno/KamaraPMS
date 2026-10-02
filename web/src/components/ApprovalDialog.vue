<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { ApiError } from '@/api/problem'
import type { Approval } from '@/api/types'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'

/**
 * The shared "Approval" dialog: every correction (adjustment, reversal, void, refund) needs the approver's own
 * credentials. The approver may be the person who is signed in (their email is prefilled) or someone else.
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

onMounted(() => passwordBox.value?.$el.focus())

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
  <div class="fixed inset-0 z-50 grid place-items-center bg-black/40 p-4" @keydown.esc="cancel">
    <form
      class="w-full max-w-md rounded-xl border border-border bg-card p-5 text-card-foreground shadow-lg"
      role="dialog"
      aria-modal="true"
      aria-labelledby="approval-title"
      novalidate
      data-testid="approval-dialog"
      @submit.prevent="submit"
    >
      <h2 id="approval-title" class="m-0 text-base font-semibold">{{ title }}</h2>
      <p class="mb-3 mt-1 text-sm text-muted-foreground">{{ message ?? t('approval.message') }}</p>
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
  </div>
</template>
