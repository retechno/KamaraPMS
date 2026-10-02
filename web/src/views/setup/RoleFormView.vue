<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'

type PermissionInfo = components['schemas']['PermissionInfo']

const props = defineProps<{ id?: string }>()
const router = useRouter()
const isNew = computed(() => !props.id)

const form = reactive({ name: '', description: '' })
const selected = ref<Set<string>>(new Set())
const catalogue = ref<PermissionInfo[]>([])
const error = ref<ApiError | null>(null)
const busy = ref(false)

const groups = computed(() => {
  const out = new Map<string, PermissionInfo[]>()
  for (const p of catalogue.value) out.set(p.group, [...(out.get(p.group) ?? []), p])
  return [...out.entries()]
})

onMounted(async () => {
  try {
    catalogue.value = (await api.GET('/api/v1/permissions')).data?.data ?? []
    if (!isNew.value) {
      const r = (await api.GET('/api/v1/roles/{roleId}', { params: { path: { roleId: Number(props.id) } } })).data
      if (r) {
        Object.assign(form, { name: r.name, description: r.description ?? '' })
        selected.value = new Set(r.permissions)
      }
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
})

function toggle(code: string, on: boolean): void {
  const next = new Set(selected.value)
  if (on) next.add(code)
  else next.delete(code)
  selected.value = next
}

function toggleGroup(items: PermissionInfo[], on: boolean): void {
  const next = new Set(selected.value)
  for (const p of items) {
    if (on) next.add(p.code)
    else next.delete(p.code)
  }
  selected.value = next
}

async function save(): Promise<void> {
  busy.value = true
  error.value = null
  const body = { name: form.name, description: form.description, permissions: [...selected.value] }
  try {
    if (isNew.value) await api.POST('/api/v1/roles', { body })
    else await api.PATCH('/api/v1/roles/{roleId}', { params: { path: { roleId: Number(props.id) } }, body })
    await router.push('/setup/roles')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PageHeader :title="isNew ? t('roles.new') : t('roles.role', { name: form.name })" />
  <p v-if="error" class="alert" role="alert">{{ error.message }} <code>{{ error.code }}</code></p>

  <Card>
    <form novalidate @submit.prevent="save">
      <CardContent class="pt-4">
        <div class="grid gap-4 sm:grid-cols-2">
          <FormField :label="t('roles.name')" :error="error?.fieldMessage('name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('roles.description')">
            <template #default="{ id }"><Input :id="id" v-model="form.description" name="description" /></template>
          </FormField>
        </div>

        <h2 class="mb-1 mt-6 text-base font-semibold">{{ t('roles.permissions') }}</h2>
        <p class="mb-3 mt-0 text-sm text-muted-foreground">{{ t('roles.permissionsHint') }}</p>
        <fieldset v-for="[group, items] in groups" :key="group" class="mb-3 grid gap-2 rounded-lg border border-border px-3.5 pb-3 pt-2">
          <legend class="px-1">
            <label class="flex items-center gap-2 text-sm">
              <input type="checkbox" class="size-4 accent-primary" :checked="items.every((p) => selected.has(p.code))" @change="toggleGroup(items, ($event.target as HTMLInputElement).checked)" />
              <strong>{{ group }}</strong>
            </label>
          </legend>
          <label v-for="p in items" :key="p.code" class="flex items-start gap-2 text-sm">
            <input type="checkbox" class="mt-0.5 size-4 accent-primary" :checked="selected.has(p.code)" :data-permission="p.code" @change="toggle(p.code, ($event.target as HTMLInputElement).checked)" />
            <span>{{ p.description }} <code class="text-[11px] text-muted-foreground">{{ p.code }}</code> <Badge variant="outline">{{ p.milestone }}</Badge></span>
          </label>
        </fieldset>

        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="router.push('/setup/roles')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy">{{ busy ? t('roles.saving') : isNew ? t('roles.create') : t('roles.saveChanges') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>
</template>
