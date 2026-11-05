<script setup lang="ts">
/**
 * Said before the cash is posted, not after: a property that asks for a shift takes cash only on the open shift of the cashier. When the method is cash and the caller has no open shift, this
 * notice stands in the form with the way out, a shift opened in place (drawer and float). A drawer that somebody else holds is answered with the name of the holder and a drawer that is free.
 * It is not a form itself (it sits inside the payment forms), so its buttons are type=button. It says nothing for any other method, when no shift is asked for, or when the caller has one.
 */
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const props = defineProps<{ method: string }>()
const emit = defineEmits<{ opened: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)

const required = ref(false)
const hasShift = ref(false)
const checked = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const form = reactive({ drawer: '', float: '' })
const holder = ref<{ drawer: string; name: string; number: string } | null>(null)

const canOpen = computed(() => pid.value !== null && auth.can('cashier.shift', pid.value))
const needed = computed(() => props.method === 'CASH' && checked.value && required.value && !hasShift.value)

async function check(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || props.method !== 'CASH') return
  try {
    const s = await api.GET('/api/v1/properties/{propertyId}/cashier/settings', { params: { path: { propertyId } } })
    required.value = !!s.data?.require_shift_for_cash
    if (required.value && canOpen.value) {
      const c = await api.GET('/api/v1/properties/{propertyId}/cashier/shifts/current', { params: { path: { propertyId } } })
      hasShift.value = !!c.data?.shift
      if (!hasShift.value) await suggestFloat()
    }
  } catch {
    // the server still answers NO_OPEN_SHIFT when the payment is posted
    required.value = false
  } finally {
    checked.value = true
  }
}

async function suggestFloat(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const f = await api.GET('/api/v1/properties/{propertyId}/cashier/shifts/suggested-float', { params: { path: { propertyId }, query: { drawer: form.drawer.trim() || undefined } } })
    form.float = f.data?.opening_float ?? ''
  } catch {
    form.float = ''
  }
}

async function open(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  holder.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/cashier/shifts', {
      params: { path: { propertyId } },
      body: { drawer: form.drawer.trim() || undefined, opening_float: form.float.trim() || undefined },
    })
    hasShift.value = true
    emit('opened')
  } catch (e) {
    if (e instanceof ApiError && e.code === 'DRAWER_IN_USE' && typeof e.context.holder_name === 'string') {
      holder.value = { drawer: String(e.context.drawer ?? ''), name: e.context.holder_name, number: String(e.context.holder_shift_number ?? '') }
      form.drawer = String(e.context.suggested_drawer ?? '')
      await suggestFloat()
    } else {
      error.value = e instanceof ApiError ? e : null
    }
  } finally {
    busy.value = false
  }
}

watch(() => [props.method, pid.value], () => void check(), { immediate: true })
</script>

<template>
  <div v-if="needed" class="alert warning" role="status" data-testid="shift-required">
    <p class="m-0 font-medium">{{ t('shiftNotice.title') }}</p>
    <p class="m-0 mt-1 text-sm">{{ canOpen ? t('shiftNotice.body') : t('shiftNotice.noPermission') }}</p>
    <p v-if="holder" class="m-0 mt-2 text-sm font-medium" data-testid="shift-drawer-busy">
      {{ t('shiftNotice.drawerBusy', { drawer: holder.drawer, name: holder.name, number: holder.number, free: form.drawer }) }}
    </p>
    <div v-if="canOpen" class="mt-2 flex flex-wrap items-end gap-3">
      <FormField class="w-36" :label="t('shifts.drawer')">
        <template #default="{ id }"><Input :id="id" v-model="form.drawer" name="shift_drawer" placeholder="MAIN" maxlength="20" @keydown.enter.prevent="open" /></template>
      </FormField>
      <FormField class="w-44" :label="t('shifts.float')">
        <template #default="{ id }"><Input :id="id" v-model="form.float" name="shift_float" inputmode="decimal" @keydown.enter.prevent="open" /></template>
      </FormField>
      <Button type="button" :disabled="busy" data-testid="shift-open" @click="open">{{ t('shifts.open') }}</Button>
    </div>
    <ErrorNotice :error="error" inline />
  </div>
</template>
