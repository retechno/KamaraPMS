<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { ApprovalKind, ApprovalRule, ApprovalRuleType } from '@/api/types'
import MoneyInput from '@/components/app/MoneyInput.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useApprovalRulesStore } from '@/stores/approvalRules'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/**
 * The rule of approval of each kind of correction at the property (docs/architecture/32-distinct-approver.md): anyone who may approve (the default), always someone other than the person who makes the
 * change, or someone else above a limit. A rule that asks for someone else warns when fewer than two people besides the administrators can approve that kind: the one who can would have to
 * ask an administrator to approve what they do themselves.
 */
const auth = useAuthStore()
const property = usePropertyStore()
const store = useApprovalRulesStore()

type Draft = { rule: ApprovalRuleType; threshold: string }
const draft = reactive<Record<string, Draft>>({})
const loaded = ref(false)
const saving = ref(false)
const notice = ref('')
const error = ref<ApiError | null>(null)
const limitErrors = reactive<Record<string, boolean>>({})

const canManage = computed(() => auth.can('property.manage', property.currentId))
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const rows = computed<ApprovalRule[]>(() => store.rules)

function adopt(list: ApprovalRule[]): void {
  for (const r of list) draft[r.kind] = { rule: r.rule, threshold: r.threshold ?? '' }
}

const changed = (r: ApprovalRule): boolean => {
  const d = draft[r.kind]
  if (!d) return false
  return d.rule !== r.rule || (d.rule === 'DISTINCT_ABOVE' && d.threshold !== (r.threshold ?? ''))
}
const anyChange = computed(() => rows.value.some(changed))
// Fewer than two approvers besides the administrators, with a rule that asks for someone else (as it is typed, saved or not).
const warning = (r: ApprovalRule): string => {
  if (!draft[r.kind] || draft[r.kind]!.rule === 'FREE' || r.approvers >= 2) return ''
  return r.approvers === 0 ? t('approvalRules.warnNobody') : t('approvalRules.warnOne')
}

async function load(): Promise<void> {
  loaded.value = false
  notice.value = ''
  error.value = null
  const propertyId = property.currentId
  await store.load(propertyId, true)
  error.value = store.error
  adopt(store.rules)
  loaded.value = true
}

function chooseRule(kind: ApprovalKind, rule: string): void {
  const d = draft[kind]
  if (!d) return
  d.rule = rule as ApprovalRuleType
  limitErrors[kind] = false
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  notice.value = ''
  error.value = null
  let blocked = false
  const body = rows.value.filter(changed).map((r) => {
    const d = draft[r.kind]!
    if (d.rule === 'DISTINCT_ABOVE' && d.threshold.trim() === '') {
      limitErrors[r.kind] = true
      blocked = true
    }
    return { kind: r.kind, rule: d.rule, threshold: d.rule === 'DISTINCT_ABOVE' ? d.threshold.trim() : null }
  })
  if (blocked || body.length === 0) return
  saving.value = true
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/approval-rules', { params: { path: { propertyId } }, body: { rules: body } })
    store.set(propertyId, data?.data ?? [])
    adopt(store.rules)
    notice.value = t('approvalRules.saved')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => property.currentId, load, { immediate: true })
</script>

<template>
  <PageHeader :title="t('approvalRules.title')" />
  <p class="muted">{{ t('approvalRules.intro') }}</p>
  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canManage" class="muted" data-testid="read-only">{{ t('approvalRules.readOnly', { permission: 'property.manage' }) }}</p>

  <Card v-if="property.currentId !== null && loaded" class="mt-3">
    <form novalidate @submit.prevent="save">
      <!-- a heading row on a wide screen; on a phone each kind is a block of its own -->
      <div class="hidden gap-3 border-b border-border px-4 py-2 text-xs font-semibold uppercase text-muted-foreground sm:grid sm:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)_minmax(0,1fr)]" aria-hidden="true">
        <span>{{ t('approvalRules.kind') }}</span><span>{{ t('approvalRules.rule') }}</span><span>{{ t('approvalRules.limit') }}</span>
      </div>
      <ul class="m-0 list-none divide-y divide-border p-0" data-testid="rules">
        <li v-for="r in rows" :key="r.kind" class="grid gap-x-3 gap-y-2 px-4 py-3 sm:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)_minmax(0,1fr)]" :data-testid="`rule-${r.kind}`" :data-changed="changed(r) || undefined">
          <div class="min-w-0">
            <strong class="block text-sm">{{ t(`approvalRules.kind_${r.kind}`) }}</strong>
            <small class="block text-xs text-muted-foreground">{{ t('approvalRules.amountOf') }} {{ t(`approvalRules.amount_${r.kind}`).replace(/^./, (c) => c.toLowerCase()) }}</small>
            <small class="block text-xs text-muted-foreground">{{ t('approvalRules.approver') }} <code>{{ r.permission }}</code></small>
          </div>
          <div>
            <label class="sr-only" :for="`rule-select-${r.kind}`">{{ t('approvalRules.rule') }}: {{ t(`approvalRules.kind_${r.kind}`) }}</label>
            <NativeSelect :id="`rule-select-${r.kind}`" :model-value="draft[r.kind]?.rule" :name="`rule_${r.kind}`" :disabled="!canManage || saving" @update:model-value="(v) => chooseRule(r.kind, String(v))">
              <option value="FREE">{{ t('approvalRules.rule_FREE') }}</option>
              <option value="DISTINCT">{{ t('approvalRules.rule_DISTINCT') }}</option>
              <option v-if="r.has_amount" value="DISTINCT_ABOVE">{{ t('approvalRules.rule_DISTINCT_ABOVE') }}</option>
            </NativeSelect>
          </div>
          <div>
            <template v-if="r.has_amount && draft[r.kind]?.rule === 'DISTINCT_ABOVE'">
              <label class="sr-only" :for="`limit-${r.kind}`">{{ t('approvalRules.limit') }}: {{ t(`approvalRules.kind_${r.kind}`) }}</label>
              <MoneyInput :id="`limit-${r.kind}`" v-model="draft[r.kind]!.threshold" :name="`limit_${r.kind}`" :decimals="decimals" :placeholder="t('approvalRules.limitPlaceholder')" :disabled="!canManage || saving" :aria-invalid="limitErrors[r.kind] || undefined" />
              <small v-if="limitErrors[r.kind]" class="block text-xs text-destructive" role="alert">{{ t('approvalRules.limitRequired') }}</small>
            </template>
            <small v-else-if="!r.has_amount" class="text-xs text-muted-foreground">{{ t('approvalRules.noLimit') }}</small>
          </div>
          <p v-if="warning(r)" class="m-0 rounded-md border border-warning/40 bg-warning/15 px-3 py-2 text-xs text-warning-text sm:col-span-3" role="note" :data-testid="`warning-${r.kind}`">{{ warning(r) }}</p>
        </li>
      </ul>
      <div v-if="canManage" class="flex justify-end border-t border-border px-4 py-3">
        <Button type="submit" :disabled="saving || !anyChange" data-testid="save">{{ t('common.save') }}</Button>
      </div>
    </form>
  </Card>
</template>
