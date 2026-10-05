<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Department } from '@/api/types'
import { NativeSelect } from '@/components/ui/native-select'
import { departmentLabel, loadDepartments } from '@/composables/useDepartments'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * The choice of a department or a sub-department (or none) of the current property. It shows nothing for a person who may not read the departments.
 * A department that is switched off is offered only while it is the one chosen, so an old record can still be shown and saved.
 */
defineOptions({ inheritAttrs: false })
const model = defineModel<number | null>({ default: null })
const props = defineProps<{ noneLabel?: string }>()
const property = usePropertyStore()

const departments = ref<Department[]>([])
const value = computed({
  get: () => (model.value === null || model.value === undefined ? '' : String(model.value)),
  set: (v: string) => {
    model.value = v === '' ? null : Number(v)
  },
})
const options = computed(() => departments.value.filter((d) => d.is_active || d.id === model.value))

watch(() => property.currentId, async (id) => {
  departments.value = id === null ? [] : await loadDepartments(id)
}, { immediate: true })
</script>

<template>
  <NativeSelect v-if="departments.length" v-bind="$attrs" v-model="value" data-testid="department-select">
    <option value="">{{ props.noneLabel ?? t('departments.none') }}</option>
    <option v-for="d in options" :key="d.id" :value="String(d.id)">{{ departmentLabel(d) }}{{ d.is_active ? '' : ` (${t('departments.off')})` }}</option>
  </NativeSelect>
</template>
