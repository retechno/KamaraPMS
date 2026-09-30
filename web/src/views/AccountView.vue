<script setup lang="ts">
import { reactive, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const form = reactive({ current_password: '', new_password: '', confirm: '' })
const busy = ref(false)
const error = ref<ApiError | null>(null)
const mismatch = ref(false)
const saved = ref(false)

async function submit(): Promise<void> {
  saved.value = false
  error.value = null
  mismatch.value = form.new_password !== form.confirm
  if (mismatch.value) return
  busy.value = true
  try {
    await api.POST('/api/v1/auth/password', { body: { current_password: form.current_password, new_password: form.new_password } })
    Object.assign(form, { current_password: '', new_password: '', confirm: '' })
    saved.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <h1 class="page-title">Account</h1>
  <section v-if="auth.me" class="card">
    <h2>{{ auth.me.user.full_name }}</h2>
    <p class="muted">{{ auth.me.user.email }} · {{ auth.me.tenant.name }} ({{ auth.me.tenant.code }})</p>
  </section>

  <form class="card" novalidate @submit.prevent="submit">
    <h2>Change password</h2>
    <p v-if="saved" class="alert warning" role="status">Password changed. Your other sessions were signed out.</p>
    <p v-if="error && !error.fieldErrors.length" class="alert" role="alert">{{ error.message }}</p>
    <div class="form-grid">
      <label class="field">
        <span>Current password</span>
        <input v-model="form.current_password" type="password" autocomplete="current-password" :aria-invalid="!!error?.fieldMessage('current_password')" />
        <small v-if="error?.fieldMessage('current_password')" class="error-text">The current password is incorrect.</small>
      </label>
      <label class="field">
        <span>New password</span>
        <input v-model="form.new_password" type="password" autocomplete="new-password" :aria-invalid="!!error?.fieldMessage('new_password')" />
        <small class="hint">At least 12 characters. A few random words make a strong passphrase.</small>
        <small v-if="error?.fieldMessage('new_password')" class="error-text">{{ error.fieldMessage('new_password') }}</small>
      </label>
      <label class="field">
        <span>Repeat new password</span>
        <input v-model="form.confirm" type="password" autocomplete="new-password" :aria-invalid="mismatch" />
        <small v-if="mismatch" class="error-text">The passwords do not match.</small>
      </label>
    </div>
    <div class="form-actions">
      <button type="submit" class="btn-primary" :disabled="busy">{{ busy ? 'Saving…' : 'Change password' }}</button>
    </div>
  </form>
</template>
