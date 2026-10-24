<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetRoom } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/**
 * The meeting rooms (docs/architecture/36-banquet.md 3.2, 6.8): the set-up and tear-down time that is held around an event, the capacity in each set-up, and the base rooms that a combined room
 * (a ballroom made of two halls) is made of. A combined room is only made of base rooms. Whoever sets up sees and changes; the others read.
 */
const STYLES = ['THEATER', 'CLASSROOM', 'U_SHAPE', 'ROUND_TABLE', 'COCKTAIL', 'BOARDROOM'] as const
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canSetup = computed(() => auth.can('banquet.setup', pid.value))
const rooms = ref<BanquetRoom[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/rooms', { params: { path: { propertyId } } })
    rooms.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

const dialog = ref(false)
const formError = ref<ApiError | null>(null)
const busy = ref(false)
const form = reactive({
  id: null as number | null, version: 0, code: '', name: '', area: '', setup: 60, teardown: 30, combo: false, active: true, sort: 0,
  parts: [] as number[], caps: {} as Record<string, number | null>,
})
const baseRooms = computed(() => rooms.value.filter((r) => !r.is_combo && r.id !== form.id))

function edit(r?: BanquetRoom): void {
  formError.value = null
  const caps: Record<string, number | null> = {}
  for (const s of STYLES) caps[s] = r?.setups.find((x) => x.setup_style === s)?.capacity ?? null
  Object.assign(form, r
    ? { id: r.id, version: r.version, code: r.code, name: r.name, area: r.area_m2 ?? '', setup: r.setup_minutes, teardown: r.teardown_minutes, combo: r.is_combo, active: r.is_active, sort: r.sort_order, parts: r.parts.map((p) => p.room_id) }
    : { id: null, version: 0, code: '', name: '', area: '', setup: 60, teardown: 30, combo: false, active: true, sort: rooms.value.length + 1, parts: [] })
  form.caps = caps
  dialog.value = true
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  formError.value = null
  const base = `/api/v1/properties/{propertyId}/banquet/rooms`
  const body = { code: form.code.trim().toUpperCase(), name: form.name.trim(), area_m2: form.area.trim() || null, setup_minutes: form.setup, teardown_minutes: form.teardown, is_combo: form.combo, is_active: form.active, sort_order: form.sort, version: form.version }
  try {
    const res = form.id === null
      ? await api.POST(base as '/api/v1/properties/{propertyId}/banquet/rooms', { params: { path: { propertyId } }, body })
      : await api.PATCH(`${base}/{id}` as '/api/v1/properties/{propertyId}/banquet/rooms/{id}', { params: { path: { propertyId, id: form.id } }, body })
    const id = res.data?.id
    if (id !== undefined) {
      if (form.combo) await api.PUT('/api/v1/properties/{propertyId}/banquet/rooms/{id}/parts', { params: { path: { propertyId, id } }, body: { room_ids: form.parts } })
      const setups = STYLES.filter((s) => (form.caps[s] ?? 0) > 0).map((s) => ({ setup_style: s, capacity: form.caps[s] as number }))
      await api.PUT('/api/v1/properties/{propertyId}/banquet/rooms/{id}/setups', { params: { path: { propertyId, id } }, body: { setups } })
    }
    dialog.value = false
    toast.success(t('banquet.rooms.saved'))
    await load()
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PageHeader :title="t('banquet.rooms.title')" :description="t('banquet.rooms.intro')">
    <template #actions>
      <Button v-if="canSetup" type="button" data-testid="room-new" @click="edit()">{{ t('banquet.rooms.new') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <EmptyState v-else-if="loaded && !rooms.length" :title="t('banquet.rooms.empty')" :description="t('emptyState.banquetRooms')" />
  <ul v-else class="m-0 grid list-none gap-3 p-0 md:grid-cols-2" data-testid="room-list">
    <li v-for="r in rooms" :key="r.id" class="flex flex-col gap-2 rounded-md border border-border p-4" :data-testid="`room-${r.code}`">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <p class="m-0 font-medium">{{ r.name }} <span class="text-muted-foreground">{{ r.code }}</span></p>
        <div class="flex items-center gap-2">
          <Badge v-if="r.is_combo" variant="booked">{{ t('banquet.rooms.combined') }}</Badge>
          <Badge :variant="r.is_active ? 'success' : 'outline'">{{ r.is_active ? t('common.active') : t('common.inactive') }}</Badge>
          <Button v-if="canSetup" type="button" variant="outline" size="sm" :data-testid="`room-edit-${r.code}`" @click="edit(r)">{{ t('common.edit') }}</Button>
        </div>
      </div>
      <p class="m-0 text-sm text-muted-foreground">{{ t('banquet.rooms.buffers', { setup: r.setup_minutes, teardown: r.teardown_minutes }) }}<template v-if="r.area_m2"> · {{ r.area_m2 }} m²</template></p>
      <p v-if="r.parts.length" class="m-0 text-sm" data-testid="room-parts">{{ t('banquet.rooms.madeOf') }}: {{ r.parts.map((p) => p.name).join(' + ') }}</p>
      <p v-if="r.setups.length" class="m-0 text-sm" data-testid="room-setups">{{ r.setups.map((s) => `${t(`banquet.style.${s.setup_style}`)} ${s.capacity}`).join(' · ') }}</p>
    </li>
  </ul>

  <Dialog :open="dialog" @update:open="(v) => (dialog = v)">
    <DialogContent class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="room-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="save">
        <DialogTitle>{{ form.id === null ? t('banquet.rooms.new') : t('banquet.rooms.edit') }}</DialogTitle>
        <DialogDescription>{{ t('banquet.rooms.hint') }}</DialogDescription>
        <ErrorNotice v-if="formError" :error="formError" inline />
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('banquet.rooms.code')" required :error="formError?.fieldMessage('code')">
            <template #default="{ id }"><Input :id="id" v-model="form.code" name="code" maxlength="20" :disabled="form.id !== null" class="uppercase" /></template>
          </FormField>
          <FormField :label="t('banquet.rooms.name')" required :error="formError?.fieldMessage('name')">
            <template #default="{ id }"><Input :id="id" v-model="form.name" name="name" maxlength="100" /></template>
          </FormField>
        </div>
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('banquet.rooms.area')" :error="formError?.fieldMessage('area_m2')"><template #default="{ id }"><Input :id="id" v-model="form.area" name="area_m2" inputmode="decimal" /></template></FormField>
          <FormField :label="t('banquet.rooms.setup')" :error="formError?.fieldMessage('setup_minutes')"><template #default="{ id }"><Input :id="id" v-model.number="form.setup" name="setup_minutes" type="number" min="0" /></template></FormField>
          <FormField :label="t('banquet.rooms.teardown')" :error="formError?.fieldMessage('teardown_minutes')"><template #default="{ id }"><Input :id="id" v-model.number="form.teardown" name="teardown_minutes" type="number" min="0" /></template></FormField>
        </div>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="form.combo" type="checkbox" name="is_combo" class="size-4 accent-primary" data-testid="room-combo" />{{ t('banquet.rooms.isCombo') }}</label>
        <fieldset v-if="form.combo" class="m-0 flex flex-col gap-1 border-0 p-0" data-testid="room-parts-field">
          <legend class="mb-1 text-sm font-medium">{{ t('banquet.rooms.madeOf') }}</legend>
          <p v-if="!baseRooms.length" class="m-0 text-sm text-muted-foreground">{{ t('banquet.rooms.noBase') }}</p>
          <label v-for="b in baseRooms" :key="b.id" class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="form.parts" type="checkbox" :value="b.id" class="size-4 accent-primary" :data-testid="`part-${b.code}`" />{{ b.name }}</label>
          <p v-if="formError?.fieldMessage('room_ids')" class="m-0 text-sm text-destructive">{{ formError.fieldMessage('room_ids') }}</p>
        </fieldset>
        <fieldset class="m-0 border-0 p-0">
          <legend class="mb-1 text-sm font-medium">{{ t('banquet.rooms.capacity') }}</legend>
          <p class="m-0 mb-2 text-xs text-muted-foreground">{{ t('banquet.rooms.capacityHint') }}</p>
          <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
            <label v-for="s in STYLES" :key="s" class="flex flex-col gap-1 text-sm">{{ t(`banquet.style.${s}`) }}<Input v-model.number="form.caps[s]" :name="`cap_${s}`" type="number" min="0" :data-testid="`cap-${s}`" /></label>
          </div>
        </fieldset>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="form.active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="dialog = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="room-save">{{ t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
