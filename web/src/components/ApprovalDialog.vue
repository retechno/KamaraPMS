<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { ApiError } from '@/api/problem'
import type { Approval } from '@/api/types'
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
const passwordInput = ref<HTMLInputElement | null>(null)

onMounted(() => passwordInput.value?.focus())

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
  <div class="overlay" @keydown.esc="cancel">
    <form class="dialog card" role="dialog" aria-modal="true" aria-labelledby="approval-title" novalidate data-testid="approval-dialog" @submit.prevent="submit">
      <h2 id="approval-title">{{ title }}</h2>
      <p class="muted">{{ message ?? 'A correction needs an approval. Enter the approver\'s own email and password.' }}</p>
      <p v-if="error" class="alert" role="alert" data-testid="approval-error">{{ error.message }} <code>{{ error.code }}</code></p>
      <label class="field">
        <span>Approver email</span>
        <input v-model="email" name="approval_email" type="email" autocomplete="off" />
      </label>
      <label class="field">
        <span>Approver password</span>
        <input ref="passwordInput" v-model="password" name="approval_password" type="password" autocomplete="off" />
      </label>
      <div class="form-actions">
        <button type="button" data-testid="approval-cancel" @click="cancel">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy || !email || !password" data-testid="approval-submit">Approve</button>
      </div>
    </form>
  </div>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  background: rgb(0 0 0 / 0.4);
  display: grid;
  place-items: center;
  z-index: 50;
  padding: 16px;
}
.dialog {
  width: min(420px, 100%);
  margin: 0;
}
</style>
