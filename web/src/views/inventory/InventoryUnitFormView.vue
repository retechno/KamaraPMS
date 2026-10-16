<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/** A unit of measure: a code that never changes, a name, and whether it is on. */
const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const unitId = computed(() => (route.params.id ? Number(route.params.id) : null))
const isNew = computed(() => unitId.value === null)
const form = reactive({ code: '', name: '', is_active: true })
const loaded = ref(false)
const saving = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const fieldError = (f: string) => formError.value?.fieldMessage(f)

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    if (isNew.value) {
      Object.assign(form, { code: '', name: '', is_active: true })
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/units', { params: { path: { propertyId } } })
      const u = (data?.data ?? []).find((x) => x.id === unitId.value)
      if (u) Object.assign(form, { code: u.code, name: u.name, is_active: u.is_active })
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  saving.value = true
  formError.value = null
  try {
    if (isNew.value) {
      const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/units', { params: { path: { propertyId } }, body: { code: form.code.trim(), name: form.name.trim(), is_active: form.is_active } })
      if (data) await router.replace(`/inventory/units/${data.id}`)
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/inventory/units/{id}', { params: { path: { propertyId, id: unitId.value! } }, body: { name: form.name.trim(), is_active: form.is_active } })
    }
    notice.value = t('inventory.units.saved')
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}
watch([pid, unitId], load, { immediate: true })
</script>

<template>
  <PageHeader :title="isNew ? t('inventory.units.newTitle') : t('inventory.units.editTitle', { name: form.name })">
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/units" data-testid="back">{{ t('inventory.units.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <form v-else-if="loaded" novalidate class="flex max-w-xl flex-col gap-4" data-testid="unit-form" @submit.prevent="save">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="unit-error" />
    <FormField :label="t('inventory.units.code')" :hint="t('inventory.units.codeHint')" :error="fieldError('code')" required>
      <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" maxlength="10" :disabled="!isNew" :aria-invalid="invalid" /></template>
    </FormField>
    <FormField :label="t('inventory.units.name')" :error="fieldError('name')" required>
      <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" maxlength="60" :aria-invalid="invalid" /></template>
    </FormField>
    <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="form.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('inventory.units.active') }}</label>
    <div class="flex justify-end gap-2">
      <Button as-child variant="outline"><RouterLink to="/inventory/units">{{ t('common.cancel') }}</RouterLink></Button>
      <Button type="submit" :disabled="saving || !form.name.trim() || !form.code.trim()" data-testid="unit-save">{{ t('common.save') }}</Button>
    </div>
  </form>
</template>
