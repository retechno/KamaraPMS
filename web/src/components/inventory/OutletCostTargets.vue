<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryCostTarget } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/**
 * The target cost % of food and of beverage of an outlet (docs/architecture/35-inventory.md 12.2). An outlet without its own target takes the property's, which is shown beside the field. With `banquet` it is the target of the banquet, which has no outlet (docs/architecture/36-banquet.md 4.9). Saved on its own, by who
 * manages the inventory: the daily cost and the reports mark the days above it. Nothing is shown to a person who may not read the inventory.
 */
const props = withDefaults(defineProps<{ outletId?: number; banquet?: boolean }>(), { outletId: undefined, banquet: false })
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canEdit = computed(() => auth.can('inventory.manage', pid.value))
const classes = ['FOOD', 'BEVERAGE'] as const
type Class = (typeof classes)[number]

const targets = ref<InventoryCostTarget[]>([])
const available = ref(false)
const own = reactive<Record<Class, string>>({ FOOD: '', BEVERAGE: '' })
const saved = reactive<Record<Class, string>>({ FOOD: '', BEVERAGE: '' })
const saving = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')

const source = computed(() => (props.banquet ? 'BANQUET' : 'POS'))
const ownOf = (c: Class): InventoryCostTarget | undefined => targets.value.find((x) => x.source === source.value && x.class === c && (props.banquet ? x.outlet_id === null : x.outlet_id === props.outletId))
const inherited = (c: Class): string | null => targets.value.find((x) => x.source === 'POS' && x.outlet_id === null && x.class === c)?.target_pct ?? null
const dirty = computed(() => classes.some((c) => own[c].trim() !== saved[c]))
const valid = computed(() => classes.every((c) => own[c].trim() === '' || (/^\d{1,3}(\.\d{1,2})?$/.test(own[c].trim()) && Number(own[c]) > 0 && Number(own[c]) <= 100)))

async function load(): Promise<void> {
  const propertyId = pid.value
  available.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/cost-targets', { params: { path: { propertyId } } })
    targets.value = data?.data ?? []
    available.value = true
    for (const c of classes) {
      own[c] = saved[c] = ownOf(c)?.target_pct ?? ''
    }
  } catch {
    available.value = false // the person may not read the inventory: nothing to show
  }
}
watch([pid, () => props.outletId], load, { immediate: true })

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !valid.value) return
  saving.value = true
  error.value = null
  notice.value = ''
  try {
    for (const c of classes) {
      const value = own[c].trim()
      if (value === saved[c]) continue
      if (value === '') await api.DELETE('/api/v1/properties/{propertyId}/inventory/cost-targets', { params: { path: { propertyId }, query: props.banquet ? { source: 'BANQUET', class: c } : { outlet_id: props.outletId, class: c } } })
      else await api.PUT('/api/v1/properties/{propertyId}/inventory/cost-targets', { params: { path: { propertyId } }, body: props.banquet ? { source: 'BANQUET', outlet_id: null, class: c, target_pct: value } : { outlet_id: props.outletId ?? null, class: c, target_pct: value } })
    }
    notice.value = t('inventory.targets.saved')
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <fieldset v-if="available" class="m-0 rounded-lg border border-border p-3" data-testid="outlet-targets">
    <legend class="px-1 text-sm font-semibold">{{ t('inventory.targets.title') }}</legend>
    <p class="m-0 mb-3 text-xs text-muted-foreground">{{ t('inventory.targets.hint') }}</p>
    <ErrorNotice v-if="error" :error="error" inline />
    <div class="grid gap-3 sm:grid-cols-2">
      <FormField v-for="c in classes" :key="c" :label="t(`inventory.targets.${c === 'FOOD' ? 'food' : 'beverage'}`)" :hint="inherited(c) ? t('inventory.targets.inherits', { pct: inherited(c) }) : t('inventory.targets.none')">
        <template #default="{ id }"><Input :id="id" v-model="own[c]" :name="`target_${c.toLowerCase()}`" inputmode="decimal" class="w-28" :disabled="!canEdit" :placeholder="inherited(c) ?? ''" :data-testid="`target-${c}`" /></template>
      </FormField>
    </div>
    <p v-if="!valid" class="m-0 mt-2 text-xs text-destructive" data-testid="target-invalid">{{ t('inventory.targets.invalid') }}</p>
    <div v-if="canEdit" class="mt-3 flex items-center gap-3">
      <Button type="button" size="sm" variant="outline" :disabled="saving || !dirty || !valid" data-testid="targets-save" @click="save">{{ t('inventory.targets.save') }}</Button>
      <span v-if="notice" class="text-xs text-muted-foreground" role="status" data-testid="targets-notice">{{ notice }}</span>
    </div>
  </fieldset>
</template>
