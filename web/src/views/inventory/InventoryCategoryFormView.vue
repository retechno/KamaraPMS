<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * A category and its accounts (docs/architecture/35-inventory.md 6.1): the inventory account (an asset of the group inventories), and the expense accounts of a sale of class other,
 * of an issue to a department, of waste and of a count variance. Empty means the default of the system.
 */
interface Account {
  id: number
  code: string
  name: string
  account_type: string
  statement_group?: string | null
  is_postable: boolean
  is_active: boolean
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const categoryId = computed(() => (route.params.id ? Number(route.params.id) : null))
const isNew = computed(() => categoryId.value === null)

const inventoryAccounts = ref<{ id: number; code: string; name: string }[]>([])
const expenseAccounts = ref<Account[]>([])
const loaded = ref(false)
const saving = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const locked = ref(false)
const fieldError = (f: string) => formError.value?.fieldMessage(f)
const form = reactive({
  code: '', name: '', kind: 'FOOD', inventory_account_id: 0, cogs_account_id: 0, issue_account_id: 0, waste_account_id: 0, variance_account_id: 0, sort_order: 0, is_active: true, version: 0,
})
const KINDS = ['FOOD', 'BEVERAGE', 'SUPPLIES', 'OTHER'] as const

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    const path = { params: { path: { propertyId } } }
    const [inv, accounts, cats] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/inventory/accounts', path),
      api.GET('/api/v1/properties/{propertyId}/accounting/accounts', { params: { path: { propertyId }, query: { account_type: 'EXPENSE', postable: true, active: true } } }),
      api.GET('/api/v1/properties/{propertyId}/inventory/categories', path),
    ])
    inventoryAccounts.value = inv.data?.data ?? []
    expenseAccounts.value = (((accounts.data as unknown as { data?: Account[] } | undefined)?.data ?? []) as Account[]).filter((a) => a.account_type === 'EXPENSE' && a.is_postable && a.is_active)
    if (isNew.value) {
      Object.assign(form, { code: '', name: '', kind: 'FOOD', inventory_account_id: inventoryAccounts.value[0]?.id ?? 0, cogs_account_id: 0, issue_account_id: 0, waste_account_id: 0, variance_account_id: 0, sort_order: 0, is_active: true, version: 0 })
    } else {
      const c = (cats.data?.data ?? []).find((x) => x.id === categoryId.value)
      if (c) {
        Object.assign(form, {
          code: c.code, name: c.name, kind: c.kind, inventory_account_id: c.inventory_account_id, cogs_account_id: c.cogs_account_id ?? 0, issue_account_id: c.issue_account_id ?? 0,
          waste_account_id: c.waste_account_id ?? 0, variance_account_id: c.variance_account_id ?? 0, sort_order: c.sort_order, is_active: c.is_active, version: c.version,
        })
        locked.value = c.item_count > 0
      }
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
    code: form.code.trim().toUpperCase(), name: form.name.trim(), kind: form.kind as (typeof KINDS)[number], inventory_account_id: form.inventory_account_id,
    cogs_account_id: form.cogs_account_id || null, issue_account_id: form.issue_account_id || null, waste_account_id: form.waste_account_id || null, variance_account_id: form.variance_account_id || null,
    is_active: form.is_active, sort_order: form.sort_order, version: isNew.value ? undefined : form.version,
  }
  try {
    if (isNew.value) {
      const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/categories', { params: { path: { propertyId } }, body })
      if (data) await router.replace(`/inventory/categories/${data.id}`)
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/inventory/categories/{id}', { params: { path: { propertyId, id: categoryId.value! } }, body })
      await load()
    }
    notice.value = t('inventory.categories.saved')
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}
watch([pid, categoryId], load, { immediate: true })
</script>

<template>
  <PageHeader :title="isNew ? t('inventory.categories.newTitle') : t('inventory.categories.editTitle', { name: form.name })" :description="t('inventory.categories.intro')">
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/categories" data-testid="back">{{ t('inventory.categories.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <form v-else-if="loaded" novalidate class="flex flex-col gap-4" data-testid="category-form" @submit.prevent="save">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="category-error" />
    <p v-if="locked" class="m-0 rounded-lg border border-border bg-muted/40 p-3 text-sm" data-testid="locked">{{ t('inventory.categories.locked') }}</p>
    <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
      <FormField :label="t('inventory.categories.code')" :hint="t('inventory.categories.codeHint')" :error="fieldError('code')" required>
        <template #default="{ id, invalid }"><Input :id="id" v-model="form.code" name="code" maxlength="20" :disabled="!isNew" :aria-invalid="invalid" class="uppercase" /></template>
      </FormField>
      <FormField class="xl:col-span-2" :label="t('inventory.categories.name')" :error="fieldError('name')" required>
        <template #default="{ id, invalid }"><Input :id="id" v-model="form.name" name="name" maxlength="100" :aria-invalid="invalid" /></template>
      </FormField>
      <FormField :label="t('inventory.categories.kind')" :hint="t('inventory.categories.kindHint')" :error="fieldError('kind')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="form.kind" name="kind" :disabled="locked">
            <option v-for="k in KINDS" :key="k" :value="k">{{ t(`inventory.categoryKind.${k}`) }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField class="xl:col-span-2" :label="t('inventory.categories.inventoryAccount')" :hint="t('inventory.categories.inventoryHint')" :error="fieldError('inventory_account_id')" required>
        <template #default="{ id, invalid }">
          <NativeSelect :id="id" v-model.number="form.inventory_account_id" name="inventory_account_id" :disabled="locked" :aria-invalid="invalid">
            <option :value="0" disabled>{{ t('common.choose') }}</option>
            <option v-for="a in inventoryAccounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField v-for="f in ([['cogs_account_id', 'cogsAccount', 'cogsHint'], ['issue_account_id', 'issueAccount', 'optionalHint'], ['waste_account_id', 'wasteAccount', 'optionalHint'], ['variance_account_id', 'varianceAccount', 'optionalHint']] as const)"
        :key="f[0]" :label="t(`inventory.categories.${f[1]}`)" :hint="t(`inventory.categories.${f[2]}`)" :error="fieldError(f[0])">
        <template #default="{ id, invalid }">
          <NativeSelect :id="id" v-model.number="form[f[0]]" :name="f[0]" :aria-invalid="invalid">
            <option :value="0">{{ t('inventory.categories.none') }}</option>
            <option v-for="a in expenseAccounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField :label="t('inventory.categories.sort')" :error="fieldError('sort_order')">
        <template #default="{ id }"><Input :id="id" v-model.number="form.sort_order" name="sort_order" type="number" min="0" class="max-w-28" /></template>
      </FormField>
      <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="form.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('inventory.categories.active') }}</label>
    </div>
    <div class="flex justify-end gap-2">
      <Button as-child variant="outline"><RouterLink to="/inventory/categories">{{ t('common.cancel') }}</RouterLink></Button>
      <Button type="submit" :disabled="saving || !form.name.trim() || !form.code.trim() || !form.inventory_account_id" data-testid="category-save">{{ t('common.save') }}</Button>
    </div>
  </form>
</template>
