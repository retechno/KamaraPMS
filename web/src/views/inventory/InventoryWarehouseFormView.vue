<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Department, InventoryItem, PosOutlet } from '@/api/types'
import { departmentLabel, loadDepartments } from '@/composables/useDepartments'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * A warehouse (docs/architecture/35-inventory.md 3.2): its kind (the main store, the warehouse of an outlet, a department's), its outlet and department, the cost class its sales
 * default to, and its minimum levels. The page fills the screen; the list it comes from has no column beside it.
 */
interface LevelRow {
  item_id: number
  min_qty: string
  max_qty: string
  reorder_qty: string
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const whId = computed(() => (route.params.id ? Number(route.params.id) : null))
const isNew = computed(() => whId.value === null)

const outlets = ref<PosOutlet[]>([])
const departments = ref<Department[]>([])
const items = ref<InventoryItem[]>([])
const levels = ref<LevelRow[]>([])
const loaded = ref(false)
const saving = ref(false)
const savingLevels = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const levelError = ref<ApiError | null>(null)
const notice = ref('')
const fieldError = (f: string) => formError.value?.fieldMessage(f)
const locked = ref(false)

const KINDS = ['MAIN', 'OUTLET', 'DEPARTMENT'] as const
const CLASSES = ['FOOD', 'BEVERAGE', 'OTHER'] as const
const form = reactive({ code: '', name: '', kind: 'MAIN' as (typeof KINDS)[number], outlet_id: 0, department_id: 0, cost_class: 'OTHER' as (typeof CLASSES)[number], is_active: true, version: 0 })

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    // the outlets belong to the POS and the departments to the accounting: a person who may not read them gets none, and the choice is not offered
    outlets.value = await api.GET('/api/v1/properties/{propertyId}/pos/outlets', path).then((r) => r.data?.data ?? []).catch(() => [])
    departments.value = await loadDepartments(propertyId)
    if (isNew.value) {
      Object.assign(form, { code: '', name: '', kind: 'MAIN', outlet_id: 0, department_id: 0, cost_class: 'OTHER', is_active: true, version: 0 })
      levels.value = []
    } else {
      const [list, lv, its] = await Promise.all([
        api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path),
        api.GET('/api/v1/properties/{propertyId}/inventory/warehouses/{id}/levels', { params: { path: { propertyId, id: whId.value! } } }),
        api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200, active: true } } }),
      ])
      const w = (list.data?.data ?? []).find((x) => x.id === whId.value)
      if (w) {
        Object.assign(form, { code: w.code, name: w.name, kind: w.kind, outlet_id: w.outlet_id ?? 0, department_id: w.department_id ?? 0, cost_class: w.cost_class, is_active: w.is_active, version: w.version })
        locked.value = Number(w.item_count) > 0 || w.stock_value !== '0'
      }
      items.value = its.data?.items ?? []
      levels.value = (lv.data?.data ?? []).map((l) => ({ item_id: l.item_id, min_qty: l.min_qty, max_qty: l.max_qty ?? '', reorder_qty: l.reorder_qty ?? '' }))
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  saving.value = true
  formError.value = null
  const body = {
    code: form.code.trim().toUpperCase(), name: form.name.trim(), kind: form.kind, outlet_id: form.kind === 'OUTLET' ? form.outlet_id || null : null,
    department_id: form.department_id || null, cost_class: form.cost_class, is_active: form.is_active, version: isNew.value ? undefined : form.version,
  }
  try {
    if (isNew.value) {
      const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/warehouses', { params: { path: { propertyId } }, body })
      if (data) await router.replace(`/inventory/warehouses/${data.id}`)
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/inventory/warehouses/{id}', { params: { path: { propertyId, id: whId.value! } }, body })
      await load()
    }
    notice.value = t('inventory.warehouses.saved')
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

const freeItems = (own: number) => items.value.filter((i) => i.id === own || !levels.value.some((l) => l.item_id === i.id))
function addLevel(): void {
  const free = freeItems(0)[0]
  if (free) levels.value.push({ item_id: free.id, min_qty: '0', max_qty: '', reorder_qty: '' })
}
async function saveLevels(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || whId.value === null) return
  savingLevels.value = true
  levelError.value = null
  try {
    await api.PUT('/api/v1/properties/{propertyId}/inventory/warehouses/{id}/levels', {
      params: { path: { propertyId, id: whId.value } },
      body: { levels: levels.value.map((l) => ({ item_id: l.item_id, min_qty: l.min_qty.trim() || '0', max_qty: l.max_qty.trim() || null, reorder_qty: l.reorder_qty.trim() || null })) },
    })
    notice.value = t('inventory.warehouses.levelsSaved')
  } catch (e) {
    levelError.value = e instanceof ApiError ? e : null
  } finally {
    savingLevels.value = false
  }
}
watch([pid, whId], load, { immediate: true })
const unitOf = (id: number): string => items.value.find((i) => i.id === id)?.stock_unit_code ?? ''
</script>

<template>
  <PageHeader :title="isNew ? t('inventory.warehouses.newTitle') : t('inventory.warehouses.editTitle', { name: form.name })" :description="t('inventory.warehouses.intro')">
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/warehouses" data-testid="back">{{ t('inventory.warehouses.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <template v-else-if="loaded">
    <form novalidate class="flex flex-col gap-4" data-testid="warehouse-form" @submit.prevent="save">
      <ErrorNotice v-if="formError" :error="formError" inline data-testid="warehouse-error" />
      <p v-if="locked" class="m-0 rounded-lg border border-border bg-muted/40 p-3 text-sm" data-testid="locked">{{ t('inventory.warehouses.locked') }}</p>
      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <FormField :label="t('inventory.warehouses.code')" :hint="t('inventory.warehouses.codeHint')" :error="fieldError('code')" required>
          <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" maxlength="20" :disabled="!isNew" :aria-invalid="invalid" class="uppercase" /></template>
        </FormField>
        <FormField class="xl:col-span-2" :label="t('inventory.warehouses.name')" :error="fieldError('name')" required>
          <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" maxlength="100" :aria-invalid="invalid" /></template>
        </FormField>
        <FormField :label="t('inventory.warehouses.kind')" :error="fieldError('kind')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="form.kind" name="kind" :disabled="locked">
              <option v-for="k in KINDS" :key="k" :value="k">{{ t(`inventory.warehouseKind.${k}`) }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField v-if="form.kind === 'OUTLET'" :label="t('inventory.warehouses.outlet')" :hint="t('inventory.warehouses.outletHint')" :error="fieldError('outlet_id')" required>
          <template #default="{ id, invalid }">
            <NativeSelect :id="id" v-model.number="form.outlet_id" name="outlet_id" :disabled="locked" :aria-invalid="invalid">
              <option :value="0" disabled>{{ t('common.choose') }}</option>
              <option v-for="o in outlets" :key="o.id" :value="o.id">{{ o.code }} · {{ o.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('inventory.warehouses.department')" :error="fieldError('department_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.department_id" name="department_id">
              <option :value="0">{{ t('inventory.warehouses.noDepartment') }}</option>
              <option v-for="d in departments.filter((x) => x.is_active)" :key="d.id" :value="d.id">{{ departmentLabel(d) }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('inventory.warehouses.costClass')" :hint="t('inventory.warehouses.costClassHint')" :error="fieldError('cost_class')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="form.cost_class" name="cost_class">
              <option v-for="c in CLASSES" :key="c" :value="c">{{ t(`inventory.costClass.${c}`) }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="form.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('inventory.warehouses.active') }}</label>
      </div>
      <div class="flex justify-end gap-2">
        <Button as-child variant="outline"><RouterLink to="/inventory/warehouses">{{ t('common.cancel') }}</RouterLink></Button>
        <Button type="submit" :disabled="saving || !form.name.trim() || !form.code.trim() || (form.kind === 'OUTLET' && !form.outlet_id)" data-testid="warehouse-save">{{ t('common.save') }}</Button>
      </div>
    </form>

    <fieldset v-if="!isNew" class="m-0 mt-6 rounded-xl border border-border p-4" data-testid="levels">
      <legend class="px-1 text-sm font-semibold">{{ t('inventory.warehouses.levelsTitle') }}</legend>
      <p class="m-0 mb-3 text-xs text-muted-foreground">{{ t('inventory.warehouses.levelsHint') }}</p>
      <ErrorNotice v-if="levelError" :error="levelError" inline data-testid="levels-error" />
      <div v-if="levels.length" class="overflow-x-auto">
        <table class="w-full min-w-[34rem] text-sm">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.warehouses.levelItem') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.warehouses.levelMin') }}</th>
              <th class="py-1 pr-2 font-medium">{{ t('inventory.warehouses.levelMax') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.warehouses.levelReorder') }}</th><th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="(l, i) in levels" :key="i" class="border-t border-border" :data-testid="`level-${i}`">
              <td class="py-2 pr-2">
                <NativeSelect v-model.number="l.item_id" :name="`level_item_${i}`" class="max-w-64" :aria-label="t('inventory.warehouses.levelItem')">
                  <option v-for="it in freeItems(l.item_id)" :key="it.id" :value="it.id">{{ it.code }} · {{ it.name }}</option>
                </NativeSelect>
              </td>
              <td class="py-2 pr-2"><Input v-model="l.min_qty" :name="`min_${i}`" inputmode="decimal" class="w-24" :aria-label="t('inventory.warehouses.levelMin')" /> <span class="text-xs text-muted-foreground">{{ unitOf(l.item_id) }}</span></td>
              <td class="py-2 pr-2"><Input v-model="l.max_qty" :name="`max_${i}`" inputmode="decimal" class="w-24" :aria-label="t('inventory.warehouses.levelMax')" /></td>
              <td class="py-2 pr-2"><Input v-model="l.reorder_qty" :name="`reorder_${i}`" inputmode="decimal" class="w-24" :aria-label="t('inventory.warehouses.levelReorder')" /></td>
              <td class="py-2 text-right"><Button type="button" variant="ghost" size="sm" :data-testid="`level-remove-${i}`" @click="levels.splice(i, 1)">{{ t('common.remove') }}</Button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="mt-3 flex flex-wrap gap-2">
        <Button type="button" variant="outline" size="sm" data-testid="level-add" :disabled="freeItems(0).length === 0" @click="addLevel">{{ t('inventory.warehouses.addLevel') }}</Button>
        <Button type="button" size="sm" :disabled="savingLevels" data-testid="levels-save" @click="saveLevels">{{ t('inventory.warehouses.saveLevels') }}</Button>
      </div>
    </fieldset>
  </template>
</template>
