<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import { usePropertyStore } from '@/stores/property'

type Role = components['schemas']['Role']

const props = defineProps<{ id?: string }>()
const router = useRouter()
const properties = usePropertyStore()
const isNew = computed(() => !props.id)

const form = reactive({ email: '', full_name: '', password: '', is_tenant_admin: false, is_active: true })
const grants = ref<{ property_id: number | null; role_id: number | null }[]>([])
const roles = ref<Role[]>([])
const newPassword = ref('')
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)

onMounted(async () => {
  if (!properties.loaded) await properties.loadProperties()
  try {
    roles.value = (await api.GET('/api/v1/roles')).data?.data ?? []
    if (!isNew.value) {
      const u = (await api.GET('/api/v1/users/{userId}', { params: { path: { userId: Number(props.id) } } })).data
      if (u) {
        Object.assign(form, { email: u.email, full_name: u.full_name, is_tenant_admin: u.is_tenant_admin, is_active: u.is_active })
        grants.value = u.grants.map((g) => ({ property_id: g.property_id, role_id: g.role_id }))
      }
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
})

const completeGrants = computed(() =>
  grants.value.filter((g): g is { property_id: number; role_id: number } => g.property_id !== null && g.role_id !== null),
)

async function save(): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    if (isNew.value) {
      await api.POST('/api/v1/users', {
        body: { email: form.email, full_name: form.full_name, password: form.password, is_tenant_admin: form.is_tenant_admin, grants: completeGrants.value },
      })
    } else {
      const userId = Number(props.id)
      await api.PATCH('/api/v1/users/{userId}', {
        params: { path: { userId } },
        body: { full_name: form.full_name, is_active: form.is_active, is_tenant_admin: form.is_tenant_admin },
      })
      await api.PUT('/api/v1/users/{userId}/properties', { params: { path: { userId } }, body: { grants: completeGrants.value } })
    }
    await router.push('/setup/users')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function resetPassword(): Promise<void> {
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/users/{userId}/password', { params: { path: { userId: Number(props.id) } }, body: { password: newPassword.value } })
    newPassword.value = ''
    notice.value = 'Password set. The user was signed out everywhere.'
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
</script>

<template>
  <h1 class="page-title">{{ isNew ? 'New user' : form.full_name || 'User' }}</h1>
  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status">{{ notice }}</p>

  <form class="card" novalidate @submit.prevent="save">
    <h2>Profile</h2>
    <div class="form-grid">
      <label class="field">
        <span>Email</span>
        <input v-model="form.email" type="email" name="email" :disabled="!isNew" :aria-invalid="!!error?.fieldMessage('email')" />
        <small v-if="error?.fieldMessage('email')" class="error-text">{{ error.fieldMessage('email') }}</small>
      </label>
      <label class="field">
        <span>Full name</span>
        <input v-model="form.full_name" name="full_name" :aria-invalid="!!error?.fieldMessage('full_name')" />
        <small v-if="error?.fieldMessage('full_name')" class="error-text">{{ error.fieldMessage('full_name') }}</small>
      </label>
      <label v-if="isNew" class="field">
        <span>Initial password</span>
        <input v-model="form.password" type="password" name="password" autocomplete="new-password" :aria-invalid="!!error?.fieldMessage('password')" />
        <small class="hint">At least 12 characters. Share it privately; the user can change it after signing in.</small>
        <small v-if="error?.fieldMessage('password')" class="error-text">{{ error.fieldMessage('password') }}</small>
      </label>
    </div>
    <div style="margin-top: 14px; display: grid; gap: 10px">
      <label class="check">
        <input v-model="form.is_tenant_admin" type="checkbox" name="is_tenant_admin" />
        <span>Tenant administrator (all properties, manages users, roles and properties)</span>
      </label>
      <label v-if="!isNew" class="check">
        <input v-model="form.is_active" type="checkbox" name="is_active" />
        <span>Active (deactivating signs the user out immediately)</span>
      </label>
    </div>

    <h2 style="margin-top: 24px">Property access</h2>
    <p v-if="form.is_tenant_admin" class="muted">Administrators can access every property.</p>
    <template v-else>
      <p v-if="!grants.length" class="muted">No property access yet.</p>
      <div v-for="(g, i) in grants" :key="i" class="grant-row">
        <select v-model="g.property_id" :aria-label="`Property ${i + 1}`">
          <option :value="null" disabled>Property…</option>
          <option v-for="p in properties.properties" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
        </select>
        <select v-model="g.role_id" :aria-label="`Role ${i + 1}`">
          <option :value="null" disabled>Role…</option>
          <option v-for="r in roles" :key="r.id" :value="r.id">{{ r.name }}</option>
        </select>
        <button type="button" @click="grants.splice(i, 1)">Remove</button>
      </div>
      <button type="button" @click="grants.push({ property_id: null, role_id: null })">Add property</button>
      <small v-if="error?.fieldMessage('grants')" class="error-text">{{ error.fieldMessage('grants') }}</small>
    </template>

    <div class="form-actions">
      <button type="button" @click="router.push('/setup/users')">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="busy">{{ busy ? 'Saving…' : isNew ? 'Create user' : 'Save changes' }}</button>
    </div>
  </form>

  <form v-if="!isNew" class="card" novalidate @submit.prevent="resetPassword">
    <h2>Set a new password</h2>
    <div class="form-grid">
      <label class="field">
        <span>New password</span>
        <input v-model="newPassword" type="password" autocomplete="new-password" />
      </label>
    </div>
    <div class="form-actions">
      <button type="submit" :disabled="newPassword.length < 12">Set password</button>
    </div>
  </form>
</template>

<style scoped>
.grant-row {
  display: flex;
  gap: 8px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}
.grant-row select {
  font: inherit;
  font-size: 14px;
  padding: 6px 8px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
  min-width: 200px;
}
</style>
