<script setup lang="ts">
import { permissionGroupText, permissionText } from '@/utils/permissions'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { ChevronDown, ChevronRight } from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { onBeforeRouteLeave, RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { components } from '@/api/schema'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { confirm } from '@/composables/useConfirm'
import { vAutofocus } from '@/directives/autofocus'
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

// What the page was loaded with: a change is a difference from it. The sticky save bar, and the question before leaving, follow it.
const baseline = ref({ name: '', description: '', permissions: [] as string[] })
const dirty = computed(() => {
  const b = baseline.value
  if (form.name !== b.name || form.description !== b.description || selected.value.size !== b.permissions.length) return true
  return !b.permissions.every((c) => selected.value.has(c))
})
function rebase(): void {
  baseline.value = { name: form.name, description: form.description, permissions: [...selected.value] }
}

const groups = computed(() => {
  const out = new Map<string, PermissionInfo[]>()
  for (const p of catalogue.value) out.set(p.group, [...(out.get(p.group) ?? []), p])
  return [...out.entries()]
})

// A group is open at first when the role has a permission in it; the person's own opening and closing wins from then on.
const toggled = reactive<Record<string, boolean>>({})
const initiallyOpen = computed(() => new Set(catalogue.value.filter((p) => baseline.value.permissions.includes(p.code)).map((p) => p.group)))
const isOpen = (group: string): boolean => toggled[group] ?? initiallyOpen.value.has(group)
const toggleOpen = (group: string): void => {
  toggled[group] = !isOpen(group)
}
const chosen = (items: PermissionInfo[]): number => items.filter((p) => selected.value.has(p.code)).length
const allChosen = (items: PermissionInfo[]): boolean => items.length > 0 && chosen(items) === items.length
const someChosen = (items: PermissionInfo[]): boolean => chosen(items) > 0 && !allChosen(items)

// Closing or reloading the tab with unsaved changes: the browser asks.
function warnBeforeUnload(e: BeforeUnloadEvent): void {
  if (!dirty.value) return
  e.preventDefault()
  e.returnValue = ''
}
window.addEventListener('beforeunload', warnBeforeUnload)
onBeforeUnmount(() => window.removeEventListener('beforeunload', warnBeforeUnload))

// Leaving for another page of the app with unsaved changes: ask first.
onBeforeRouteLeave(async () => {
  if (!dirty.value) return true
  return confirm({ title: t('roles.leaveTitle'), description: t('roles.leaveText'), confirmLabel: t('roles.leave'), cancelLabel: t('roles.keepEditing'), destructive: true })
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
    rebase()
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
    rebase() // saved: nothing is unsaved, so leaving is not questioned
    await router.push('/setup/roles')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PageHeader :title="isNew ? t('roles.new') : t('roles.role', { name: form.name })">
    <template #actions>
      <Button as-child variant="outline" size="sm"><RouterLink to="/setup/roles" data-testid="back">{{ t('roles.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />

  <Card>
    <form v-autofocus novalidate @submit.prevent="save">
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
        <section v-for="[group, items] in groups" :key="group" class="mb-3 rounded-lg border border-border" :data-testid="`group-${group}`">
          <div class="flex items-center gap-2 px-3.5 py-2">
            <input
              type="checkbox"
              class="size-4 accent-primary"
              :checked="allChosen(items)"
              :indeterminate.prop="someChosen(items)"
              :aria-checked="someChosen(items) ? 'mixed' : allChosen(items)"
              :aria-label="permissionGroupText(group)"
              :data-testid="`group-check-${group}`"
              @change="toggleGroup(items, ($event.target as HTMLInputElement).checked)"
            />
            <button
              type="button"
              class="flex flex-1 cursor-pointer items-center gap-2 border-0 bg-transparent p-0 text-left text-sm text-foreground"
              :aria-expanded="isOpen(group)"
              :data-testid="`group-toggle-${group}`"
              @click="toggleOpen(group)"
            >
              <component :is="isOpen(group) ? ChevronDown : ChevronRight" class="size-4 text-muted-foreground" aria-hidden="true" />
              <strong>{{ permissionGroupText(group) }}</strong>
              <span class="ml-auto text-xs text-muted-foreground" :data-testid="`group-count-${group}`">{{ t('roles.selectedOf', { n: chosen(items), total: items.length }) }}</span>
            </button>
          </div>
          <div v-show="isOpen(group)" class="grid gap-2 border-t border-border px-3.5 pb-3 pt-2.5" :data-testid="`group-items-${group}`">
            <label v-for="p in items" :key="p.code" class="flex items-start gap-2 text-sm">
              <input type="checkbox" class="mt-0.5 size-4 accent-primary" :checked="selected.has(p.code)" :data-permission="p.code" @change="toggle(p.code, ($event.target as HTMLInputElement).checked)" />
              <span>{{ permissionText(p) }}</span>
            </label>
          </div>
        </section>
      </CardContent>

      <!-- Sticks to the bottom of the screen while there is something to save, so the button is in reach however far the list is scrolled. -->
      <div v-if="dirty" class="sticky bottom-0 z-10 flex flex-wrap items-center justify-end gap-2 rounded-b-xl border-t border-border bg-card px-4 py-3" data-testid="save-bar">
        <span class="mr-auto text-sm text-muted-foreground">{{ t('roles.unsaved') }}</span>
        <Button type="button" variant="outline" :disabled="busy" data-testid="discard" @click="router.push('/setup/roles')">{{ t('common.cancel') }}</Button>
        <Button type="submit" :disabled="busy" data-testid="save">{{ busy ? t('roles.saving') : isNew ? t('roles.create') : t('roles.saveChanges') }}</Button>
      </div>
    </form>
  </Card>
</template>
