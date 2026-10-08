<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GuestView, PatchGuestRequest } from '@/api/types'
import GuestFields from '@/components/GuestFields.vue'
import { blankGuestForm } from '@/components/guestForm'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { t } from '@/i18n'

/**
 * Edits a guest profile in place, with the same endpoint and fields as the guest page (GET and PATCH /guests/{id}). The server decides who may edit: a profile the person
 * cannot edit is shown read-only. `saved` carries the updated guest so the page that opened the dialog can update itself without reloading.
 */
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ guestId: number | null }>()
const emit = defineEmits<{ saved: [guest: GuestView] }>()

const guest = ref<GuestView | null>(null)
const form = reactive(blankGuestForm())
const error = ref<ApiError | null>(null)
const loading = ref(false)
const saving = ref(false)

function fill(g: GuestView): void {
  guest.value = g
  for (const k of Object.keys(form) as (keyof typeof form)[]) form[k] = (g[k] as string | undefined) ?? ''
}

async function load(): Promise<void> {
  if (props.guestId === null) return
  guest.value = null
  error.value = null
  loading.value = true
  try {
    const { data } = await api.GET('/api/v1/guests/{id}', { params: { path: { id: props.guestId } } })
    if (data) fill(data)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

/** Every field is sent: an empty string clears it on the server. */
async function save(): Promise<void> {
  if (props.guestId === null) return
  saving.value = true
  error.value = null
  try {
    const { data } = await api.PATCH('/api/v1/guests/{id}', { params: { path: { id: props.guestId } }, body: { ...form } as PatchGuestRequest })
    if (data) {
      fill(data)
      emit('saved', data)
      open.value = false
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => [open.value, props.guestId], () => { if (open.value) void load() }, { immediate: true })
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="top-[6vh] max-h-[88vh] max-w-3xl overflow-y-auto p-5" data-testid="guest-edit-dialog">
      <DialogTitle>{{ t('frontDesk.inHouse.action.editGuest') }}</DialogTitle>
      <DialogDescription class="mt-1">{{ guest?.code ?? '' }}</DialogDescription>
      <p v-if="error" class="alert mt-3" role="alert" data-testid="guest-edit-error">{{ error.message }} <code>{{ error.code }}</code></p>
      <form v-if="guest" class="mt-4" novalidate @submit.prevent="save">
        <GuestFields v-model="form" :error="error" :disabled="!guest.can_edit" />
        <p v-if="!guest.can_edit" class="mb-0 mt-3 text-sm text-muted-foreground" data-testid="guest-edit-readonly">{{ t('guest.readOnly') }}</p>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="open = false">{{ t('common.cancel') }}</Button>
          <Button v-if="guest.can_edit" type="submit" :disabled="saving" data-testid="guest-edit-save">{{ t('common.save') }}</Button>
        </div>
      </form>
      <p v-else-if="loading" class="muted mt-4">…</p>
    </DialogContent>
  </Dialog>
</template>
