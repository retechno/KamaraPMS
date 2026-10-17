<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryApprovalChain, InventoryApprovalLevel } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * The approval chains (docs/architecture/35-inventory.md 5.8, mockup 34): the levels a purchase request (PR) and a store requisition (SR) pass, for the property. Each level is named, says who approves (the head
 * of the requesting department, or the holders of one permission) and when it applies (always, or when the total is above an amount). A change applies only to documents submitted afterwards.
 */
type Doc = 'PR' | 'SR'
interface Editable {
  doc: Doc
  skip: boolean
  edit: boolean
  version: number
  levels: { name: string; approver: string; condition: 'ALWAYS' | 'TOTAL_ABOVE'; threshold: string }[]
  error: ApiError | null
  busy: boolean
  saved: boolean
}

const property = usePropertyStore()
const pid = computed(() => property.currentId)
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const chains = reactive<Record<Doc, Editable | null>>({ PR: null, SR: null })
const docs: Doc[] = ['PR', 'SR']

// An approver is "DEPT_HEAD" or the code of a permission.
const approvers = (doc: Doc): { value: string; label: string }[] => [
  { value: 'DEPT_HEAD', label: t('inventory.chain.whoHead') },
  { value: 'inventory.cost_control', label: t('inventory.chain.whoPermission', { permission: t('inventory.chain.permission.inventory.cost_control') }) },
  { value: 'inventory.final_approve', label: t('inventory.chain.whoPermission', { permission: t('inventory.chain.permission.inventory.final_approve') }) },
  ...(doc === 'SR' ? [{ value: 'inventory.sr_final_approve', label: t('inventory.chain.whoPermission', { permission: t('inventory.chain.permission.inventory.sr_final_approve') }) }] : []),
]

function toEditable(c: InventoryApprovalChain): Editable {
  return {
    doc: c.doc_type, skip: c.skip_level1_if_head, edit: c.approver_may_edit, version: c.version, error: null, busy: false, saved: false,
    levels: c.levels.map((l) => ({ name: l.name, approver: l.approver_kind === 'DEPT_HEAD' ? 'DEPT_HEAD' : (l.permission ?? ''), condition: l.condition_kind, threshold: l.threshold ?? '' })),
  }
}

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  loaded.value = false
  if (propertyId === null) return
  try {
    for (const doc of docs) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/approval-chains/{doc}', { params: { path: { propertyId, doc } } })
      chains[doc] = data ? toEditable(data) : null
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(pid, load, { immediate: true })

function addLevel(c: Editable): void {
  c.levels.push({ name: '', approver: 'DEPT_HEAD', condition: 'ALWAYS', threshold: '' })
  c.saved = false
}
function removeLevel(c: Editable, i: number): void {
  c.levels.splice(i, 1)
  c.saved = false
}
const levelError = (c: Editable, i: number, f: string): string | undefined => c.error?.fieldMessage(`levels[${i}].${f}`)

async function save(c: Editable): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  c.busy = true
  c.error = null
  c.saved = false
  try {
    const levels: InventoryApprovalLevel[] = c.levels.map((l) => ({
      name: l.name.trim(),
      approver_kind: l.approver === 'DEPT_HEAD' ? 'DEPT_HEAD' : 'PERMISSION',
      permission: l.approver === 'DEPT_HEAD' ? null : l.approver,
      condition_kind: l.condition,
      threshold: l.condition === 'TOTAL_ABOVE' ? l.threshold.trim() : null,
    }))
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/inventory/approval-chains/{doc}', {
      params: { path: { propertyId, doc: c.doc } },
      body: { skip_level1_if_head: c.skip, approver_may_edit: c.edit, version: c.version, levels },
    })
    if (data) {
      chains[c.doc] = { ...toEditable(data), saved: true }
    }
  } catch (e) {
    c.error = e instanceof ApiError ? e : null
  } finally {
    c.busy = false
  }
}
</script>

<template>
  <PageHeader :title="t('inventory.chains.title')" :description="t('inventory.chains.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <div v-else-if="loaded" class="flex flex-col gap-6">
    <template v-for="doc in docs" :key="doc">
      <section v-if="chains[doc]" class="rounded-xl border border-border bg-card p-4" :data-testid="`chain-${doc}`">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="m-0 text-base font-semibold">{{ t(`inventory.chains.doc.${doc}`) }}</h2>
          <Button type="button" variant="outline" size="sm" :disabled="chains[doc]!.levels.length >= 5" :data-testid="`add-level-${doc}`" @click="addLevel(chains[doc]!)">{{ t('inventory.chains.addLevel') }}</Button>
        </div>
        <ErrorNotice v-if="chains[doc]!.error" :error="chains[doc]!.error!" inline :data-testid="`chain-error-${doc}`" />
        <p v-if="chains[doc]!.levels.length === 0" class="alert warning mt-3 text-sm">{{ t('inventory.chains.noLevel') }}</p>
        <div v-else class="mt-3 overflow-x-auto">
          <table class="w-full min-w-[42rem] text-sm">
            <thead><tr class="text-left text-xs text-muted-foreground"><th class="py-1 pr-2 font-medium">#</th><th class="py-1 pr-2 font-medium">{{ t('inventory.chains.levelName') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.chains.who') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.chains.when') }}</th><th /></tr></thead>
            <tbody>
              <tr v-for="(l, i) in chains[doc]!.levels" :key="i" class="border-t border-border align-top" :data-testid="`level-${doc}-${i + 1}`">
                <td class="py-2 pr-2">{{ i + 1 }}</td>
                <td class="py-2 pr-2"><Input v-model="l.name" :name="`name_${doc}_${i}`" class="max-w-56" maxlength="60" :aria-label="t('inventory.chains.levelName')" :aria-invalid="!!levelError(chains[doc]!, i, 'name')" /><p v-if="levelError(chains[doc]!, i, 'name')" class="m-0 mt-1 text-xs text-destructive">{{ levelError(chains[doc]!, i, 'name') }}</p></td>
                <td class="py-2 pr-2"><NativeSelect v-model="l.approver" :name="`approver_${doc}_${i}`" :aria-label="t('inventory.chains.who')"><option v-for="a in approvers(doc)" :key="a.value" :value="a.value">{{ a.label }}</option></NativeSelect></td>
                <td class="py-2 pr-2">
                  <div class="flex flex-wrap items-center gap-2">
                    <NativeSelect v-model="l.condition" :name="`condition_${doc}_${i}`" class="w-56" :aria-label="t('inventory.chains.when')"><option value="ALWAYS">{{ t('inventory.chains.always') }}</option><option value="TOTAL_ABOVE">{{ t('inventory.chains.totalAbove') }}</option></NativeSelect>
                    <MoneyInput v-if="l.condition === 'TOTAL_ABOVE'" v-model="l.threshold" :name="`threshold_${doc}_${i}`" :decimals="decimals" class="w-36" :aria-label="t('inventory.chains.threshold')" :aria-invalid="!!levelError(chains[doc]!, i, 'threshold')" />
                  </div>
                  <p v-if="levelError(chains[doc]!, i, 'threshold')" class="m-0 mt-1 text-xs text-destructive">{{ levelError(chains[doc]!, i, 'threshold') }}</p>
                </td>
                <td class="py-2 text-right"><Button type="button" variant="ghost" size="sm" :data-testid="`remove-level-${doc}-${i + 1}`" @click="removeLevel(chains[doc]!, i)">{{ t('common.remove') }}</Button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <fieldset class="mt-4 border-0 p-0">
          <legend class="mb-1 text-sm font-semibold">{{ t('inventory.chains.rules') }}</legend>
          <label class="flex items-center gap-2 py-0.5 text-sm text-muted-foreground"><input type="checkbox" checked disabled /> {{ t('inventory.chains.ruleOnePerson') }}</label>
          <label class="flex items-center gap-2 py-0.5 text-sm text-muted-foreground"><input type="checkbox" checked disabled /> {{ t('inventory.chains.ruleNotOwn') }}</label>
          <label class="flex items-center gap-2 py-0.5 text-sm"><input v-model="chains[doc]!.skip" type="checkbox" :name="`skip_${doc}`" :data-testid="`skip-${doc}`" /> {{ t('inventory.chains.optionSkip') }}</label>
          <label class="flex items-center gap-2 py-0.5 text-sm"><input v-model="chains[doc]!.edit" type="checkbox" :name="`edit_${doc}`" :data-testid="`edit-${doc}`" /> {{ t('inventory.chains.optionEdit') }}</label>
        </fieldset>
        <p class="muted mt-3 text-sm">{{ t('inventory.chains.howItWorks') }}</p>
        <div class="mt-3 flex items-center justify-end gap-3">
          <span v-if="chains[doc]!.saved" class="text-sm text-muted-foreground" role="status" :data-testid="`saved-${doc}`">{{ t('inventory.chains.saved') }}</span>
          <Button type="button" :disabled="chains[doc]!.busy" :data-testid="`save-${doc}`" @click="save(chains[doc]!)">{{ t('inventory.chains.save') }}</Button>
        </div>
      </section>
    </template>
  </div>
</template>
