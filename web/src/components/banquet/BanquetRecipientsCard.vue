<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetBeoRecipient, Department } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'

/**
 * "Who reads the BEO" (docs/architecture/36-banquet.md 4.6, mockup 42): the department of each section of the BEO. The head and the deputy of the department, as the Departments settings name them (a
 * sub-department with none takes the ones of its parent), are told when the section of a Definite event changes. A section left with no department tells nobody.
 */
const props = defineProps<{ propertyId: number }>()

const SECTIONS = ['KITCHEN', 'FB_SERVICE', 'HOUSEKEEPING', 'ENGINEERING', 'SECURITY', 'FRONT_OFFICE'] as const
const recipients = ref<BanquetBeoRecipient[]>([])
const departments = ref<Department[]>([])
const choice = ref<Record<string, number>>({})
const error = ref<ApiError | null>(null)
const busy = ref(false)

const byDepartment = computed(() => departments.value.filter((d) => d.is_active))

function fill(list: BanquetBeoRecipient[]): void {
  recipients.value = list
  choice.value = Object.fromEntries(SECTIONS.map((s) => [s, list.find((r) => r.section === s)?.department_id ?? 0]))
}

async function load(): Promise<void> {
  error.value = null
  try {
    const path = { params: { path: { propertyId: props.propertyId } } }
    const [r, d] = await Promise.all([api.GET('/api/v1/properties/{propertyId}/banquet/beo-recipients', path), api.GET('/api/v1/properties/{propertyId}/departments', path)])
    departments.value = d.data?.data ?? []
    fill(r.data?.data ?? [])
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(() => props.propertyId, load, { immediate: true })

async function save(): Promise<void> {
  busy.value = true
  error.value = null
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/banquet/beo-recipients', {
      params: { path: { propertyId: props.propertyId } },
      body: { items: SECTIONS.map((s) => ({ section: s, department_id: choice.value[s] ? choice.value[s]! : null })) },
    })
    fill(data?.data ?? [])
    toast.success(t('banquet.recipients.saved'))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const people = (s: string): string => {
  const r = recipients.value.find((x) => x.section === s)
  return r?.department_id ? t('banquet.recipients.people', { head: r.head ?? '–', deputy: r.deputy ?? '–' }) : t('banquet.recipients.nobody')
}
// a department with neither a head nor a deputy tells nobody, as a section with no department
const noHead = (s: string): boolean => {
  const r = recipients.value.find((x) => x.section === s)
  return !!r?.department_id && !r.head && !r.deputy
}
</script>

<template>
  <Card class="p-4" data-testid="recipients-section">
    <form novalidate class="flex flex-col gap-3" @submit.prevent="save">
      <h2 class="m-0 text-base font-semibold">{{ t('banquet.recipients.title') }}</h2>
      <p class="m-0 text-sm text-muted-foreground">{{ t('banquet.recipients.hint') }}</p>
      <ErrorNotice v-if="error" :error="error" inline />
      <div v-for="s in SECTIONS" :key="s" class="grid items-center gap-2 sm:grid-cols-[12rem_1fr]" :data-testid="`recipient-${s}`">
        <label :for="`rcp-${s}`" class="text-sm font-medium">{{ t(`banquet.beo.section.${s}`) }}</label>
        <div>
          <NativeSelect :id="`rcp-${s}`" v-model.number="choice[s]" :name="`department_${s}`">
            <option :value="0">{{ t('banquet.recipients.none') }}</option>
            <option v-for="d in byDepartment" :key="d.id" :value="d.id">{{ d.level === 2 ? '– ' : '' }}{{ d.code }} · {{ d.name }}</option>
          </NativeSelect>
          <p class="m-0 mt-1 text-xs" :class="noHead(s) ? 'text-destructive' : 'text-muted-foreground'" :data-testid="`recipient-people-${s}`">{{ noHead(s) ? t('banquet.recipients.noHead') : people(s) }}</p>
        </div>
      </div>
      <div class="flex justify-end"><Button type="submit" :disabled="busy" data-testid="recipients-save">{{ t('common.save') }}</Button></div>
    </form>
  </Card>
</template>
