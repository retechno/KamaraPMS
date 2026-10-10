<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
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
    notice.value = t('users.passwordSet')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
</script>

<template>
  <PageHeader :title="isNew ? t('users.new') : form.full_name || t('users.userFallback')" />
  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status">{{ notice }}</p>

  <Card class="mb-4">
    <form v-autofocus novalidate @submit.prevent="save">
      <CardHeader><CardTitle>{{ t('users.profile') }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <FormField :label="t('users.email')" :error="error?.fieldMessage('email')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.email" type="email" name="email" :disabled="!isNew" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('users.fullName')" :error="error?.fieldMessage('full_name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.full_name" name="full_name" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField v-if="isNew" :label="t('users.initialPassword')" :hint="t('users.passwordHint')" :error="error?.fieldMessage('password')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.password" type="password" name="password" autocomplete="new-password" :aria-invalid="invalid" /></template>
          </FormField>
        </div>
        <div class="mt-4 grid gap-2.5">
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.is_tenant_admin" type="checkbox" name="is_tenant_admin" class="size-4 accent-primary" />
            <span>{{ t('users.tenantAdmin') }}</span>
          </label>
          <label v-if="!isNew" class="flex items-center gap-2 text-sm">
            <input v-model="form.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" />
            <span>{{ t('users.activeCheck') }}</span>
          </label>
        </div>

        <h2 class="mb-2 mt-6 text-base font-semibold">{{ t('users.propertyAccess') }}</h2>
        <p v-if="form.is_tenant_admin" class="m-0 text-sm text-muted-foreground">{{ t('users.adminAll') }}</p>
        <template v-else>
          <p v-if="!grants.length" class="mb-2 mt-0 text-sm text-muted-foreground">{{ t('users.noGrants') }}</p>
          <div v-for="(g, i) in grants" :key="i" class="mb-2 flex flex-wrap gap-2">
            <NativeSelect v-model="g.property_id" class="w-56" :aria-label="t('users.propertyN', { n: i + 1 })">
              <option :value="null" disabled>{{ t('users.propertyPlaceholder') }}</option>
              <option v-for="p in properties.properties" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
            </NativeSelect>
            <NativeSelect v-model="g.role_id" class="w-56" :aria-label="t('users.roleN', { n: i + 1 })">
              <option :value="null" disabled>{{ t('users.rolePlaceholder') }}</option>
              <option v-for="r in roles" :key="r.id" :value="r.id">{{ r.name }}</option>
            </NativeSelect>
            <Button type="button" variant="outline" @click="grants.splice(i, 1)">{{ t('users.remove') }}</Button>
          </div>
          <Button type="button" variant="outline" size="sm" @click="grants.push({ property_id: null, role_id: null })">{{ t('users.addProperty') }}</Button>
          <small v-if="error?.fieldMessage('grants')" role="alert" class="block text-xs text-destructive">{{ error.fieldMessage('grants') }}</small>
        </template>

        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="router.push('/setup/users')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy">{{ busy ? t('users.saving') : isNew ? t('users.create') : t('users.saveChanges') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>

  <Card v-if="!isNew">
    <form novalidate @submit.prevent="resetPassword">
      <CardHeader><CardTitle>{{ t('users.newPasswordTitle') }}</CardTitle></CardHeader>
      <CardContent>
        <FormField class="max-w-sm" :label="t('users.newPassword')">
          <template #default="{ id }"><Input :id="id" v-model="newPassword" type="password" autocomplete="new-password" /></template>
        </FormField>
        <div class="mt-4 flex justify-end">
          <Button type="submit" variant="outline" :disabled="newPassword.length < 12">{{ t('users.setPassword') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>
</template>
