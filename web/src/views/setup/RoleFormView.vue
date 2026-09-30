<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'

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
  <h1 class="page-title">{{ isNew ? 'New role' : `Role: ${form.name}` }}</h1>
  <p v-if="error" class="alert" role="alert">{{ error.message }} <code>{{ error.code }}</code></p>

  <form class="card" novalidate @submit.prevent="save">
    <div class="form-grid">
      <label class="field">
        <span>Name</span>
        <input v-model="form.name" name="name" :aria-invalid="!!error?.fieldMessage('name')" />
        <small v-if="error?.fieldMessage('name')" class="error-text">{{ error.fieldMessage('name') }}</small>
      </label>
      <label class="field">
        <span>Description</span>
        <input v-model="form.description" name="description" />
      </label>
    </div>

    <h2 style="margin-top: 24px">Permissions</h2>
    <p class="muted">Changes apply to everyone holding this role on their next request.</p>
    <fieldset v-for="[group, items] in groups" :key="group" class="group">
      <legend>
        <label class="check">
          <input type="checkbox" :checked="items.every((p) => selected.has(p.code))" @change="toggleGroup(items, ($event.target as HTMLInputElement).checked)" />
          <strong>{{ group }}</strong>
        </label>
      </legend>
      <label v-for="p in items" :key="p.code" class="check">
        <input type="checkbox" :checked="selected.has(p.code)" :data-permission="p.code" @change="toggle(p.code, ($event.target as HTMLInputElement).checked)" />
        <span>{{ p.description }} <code class="code">{{ p.code }}</code> <span class="badge">{{ p.milestone }}</span></span>
      </label>
    </fieldset>

    <div class="form-actions">
      <button type="button" @click="router.push('/setup/roles')">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="busy">{{ busy ? 'Saving…' : isNew ? 'Create role' : 'Save changes' }}</button>
    </div>
  </form>
</template>

<style scoped>
.group {
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 10px 14px 12px;
  margin: 0 0 12px;
  display: grid;
  gap: 8px;
}
.code {
  font-size: 11px;
  color: var(--text-muted);
}
.badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 999px;
  border: 1px solid var(--border);
  color: var(--text-muted);
}
</style>
