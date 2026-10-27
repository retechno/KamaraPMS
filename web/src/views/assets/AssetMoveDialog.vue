<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetDetail, AssetLocation } from '@/api/types'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'

/** Move an asset to another place, another department or both (docs/architecture/37-fixed-assets.md 4.6): dated the open business day, never backdated; the depreciation follows the department at the end of the month. */
const props = defineProps<{ asset: AssetDetail | null; locations: AssetLocation[]; propertyId: number }>()
const emit = defineEmits<{ close: []; done: [asset: AssetDetail] }>()

const busy = ref(false)
const error = ref<ApiError | null>(null)
const form = ref({ change_location: false, location_id: 0, change_department: false, department_id: null as number | null, reason: '' })
let key = crypto.randomUUID()

watch(() => props.asset, (a) => {
  if (!a) return
  form.value = { change_location: false, location_id: a.location_id ?? 0, change_department: false, department_id: a.department_id ?? null, reason: '' }
  error.value = null
  key = crypto.randomUUID()
}, { immediate: true })
watch(form, () => { key = crypto.randomUUID() }, { deep: true })

const valid = computed(() => (form.value.change_location || form.value.change_department) && form.value.reason.trim() !== '')

async function submit(): Promise<void> {
  const a = props.asset
  if (!a) return
  busy.value = true
  error.value = null
  const f = form.value
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/{id}/moves', {
      params: { path: { propertyId: props.propertyId, id: a.id }, header: { 'Idempotency-Key': key } },
      body: {
        change_location: f.change_location, location_id: f.change_location ? f.location_id || null : null,
        change_department: f.change_department, department_id: f.change_department ? f.department_id : null, reason: f.reason.trim(),
      },
    })
    key = crypto.randomUUID()
    toast.success(t('assets.move.done'))
    if (data) emit('done', data)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog :open="!!asset" @update:open="(v) => { if (!v) emit('close') }">
    <DialogContent v-if="asset" class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="move-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="submit">
        <DialogTitle>{{ t('assets.move.title') }}</DialogTitle>
        <DialogDescription>{{ t('assets.move.hint') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <label class="flex cursor-pointer items-center gap-2 text-sm font-medium"><input v-model="form.change_location" type="checkbox" name="change_location" class="size-4 accent-primary" data-testid="move-location-on" /> {{ t('assets.move.place') }}</label>
        <FormField v-if="form.change_location" :label="t('assets.move.toPlace', { from: asset.location_name ?? '–' })">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.location_id" name="location_id" data-testid="move-location">
              <option :value="0">–</option>
              <option v-for="l in locations.filter((x) => x.is_active)" :key="l.id" :value="l.id">{{ l.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <label class="flex cursor-pointer items-center gap-2 text-sm font-medium"><input v-model="form.change_department" type="checkbox" name="change_department" class="size-4 accent-primary" data-testid="move-department-on" /> {{ t('assets.move.department') }}</label>
        <FormField v-if="form.change_department" :label="t('assets.move.toDepartment', { from: asset.department_name ?? '–' })" :error="error?.fieldMessage('department_id')">
          <template #default="{ id }"><DepartmentSelect :id="id" v-model="form.department_id" /></template>
        </FormField>
        <FormField :label="t('assets.asset.reason')" required :error="error?.fieldMessage('reason')"><template #default="{ id }"><Input :id="id" v-model="form.reason" name="reason" maxlength="300" /></template></FormField>
        <p class="m-0 text-xs text-muted-foreground">{{ t('assets.move.dateNote') }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('close')">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !valid" data-testid="move-confirm">{{ t('assets.move.confirm') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
