<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import { rememberedTenantCode, useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const form = reactive({ tenant_code: rememberedTenantCode(), email: '', password: '' })
const busy = ref(false)
const message = ref('')

const messages: Record<string, string> = {
  INVALID_CREDENTIALS: 'The tenant code, email or password is incorrect.',
  TOO_MANY_ATTEMPTS: 'Too many failed attempts. Please wait a few minutes and try again.',
  VALIDATION_FAILED: 'Enter your tenant code, email and password.',
}

/**
 * Only same-site paths are allowed after sign-in. "//host" and "/\host" are
 * protocol-relative URLs to another site (open redirect), so they are refused.
 */
function safeRedirect(target: unknown): string {
  if (typeof target !== 'string' || !target.startsWith('/') || target.startsWith('//') || target.startsWith('/\\')) return '/'
  return target
}

async function submit(): Promise<void> {
  busy.value = true
  message.value = ''
  try {
    await auth.login(form.tenant_code, form.email, form.password)
    await router.replace(safeRedirect(route.query.redirect))
  } catch (e) {
    message.value = e instanceof ApiError ? (messages[e.code] ?? e.message) : 'The server could not be reached.'
    form.password = ''
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="login">
    <form class="card" novalidate @submit.prevent="submit">
      <div class="brand">
        <span class="brand-mark" aria-hidden="true">K</span>
        <h1>Sign in to KamaraPMS</h1>
      </div>
      <p v-if="message" class="alert" role="alert" data-testid="login-error">{{ message }}</p>
      <label class="field">
        <span>Tenant code</span>
        <input v-model="form.tenant_code" name="tenant_code" autocomplete="organization" autocapitalize="characters" required />
      </label>
      <label class="field">
        <span>Email</span>
        <input v-model="form.email" name="email" type="email" autocomplete="username" required />
      </label>
      <label class="field">
        <span>Password</span>
        <input v-model="form.password" name="password" type="password" autocomplete="current-password" required />
      </label>
      <button type="submit" class="btn-primary" :disabled="busy">{{ busy ? 'Signing in…' : 'Sign in' }}</button>
    </form>
  </main>
</template>

<style scoped>
.login {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 16px;
}
form {
  width: min(380px, 100%);
  display: grid;
  gap: 14px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
}
.brand h1 {
  font-size: 18px;
  margin: 0;
}
.brand-mark {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 8px;
  background: var(--accent);
  color: var(--accent-contrast);
  font-weight: 700;
}
.alert {
  margin: 0;
}
button {
  padding: 9px 14px;
}
</style>
