<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { GlAccount } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { listAccounts } from '@/views/accounting/accountApi'

/**
 * The account code of a charge code, tax or service charge: a text input that suggests the accounts of the chart that
 * can take the amounts (revenue for what is sold, liabilities for taxes, liabilities or revenue for service charges) and
 * says what the typed code is, or what will happen to the amounts if the journals cannot use it. Without
 * accounting.view it is a plain input.
 */
const props = defineProps<{ kind: 'CHARGE_CODE' | 'TAX' | 'SERVICE_CHARGE'; invalid?: boolean }>()
const model = defineModel<string>({ required: true })

const auth = useAuthStore()
const property = usePropertyStore()
const accounts = ref<GlAccount[]>([])
const listId = `gl-accounts-${props.kind.toLowerCase()}-${Math.random().toString(36).slice(2, 8)}`

const FALLBACK: Record<string, string> = { CHARGE_CODE: 'the suspense account', TAX: 'the tax payable account', SERVICE_CHARGE: 'the service payable account' }
const fits = (a: GlAccount): boolean =>
  props.kind === 'CHARGE_CODE' ? a.account_type === 'REVENUE' : props.kind === 'TAX' ? a.account_type === 'LIABILITY' : a.account_type === 'LIABILITY' || a.account_type === 'REVENUE'

const suggestions = computed(() => accounts.value.filter((a) => a.is_postable && a.is_active && fits(a)))
const enabled = computed(() => property.currentId !== null && auth.can('accounting.view', property.currentId))

const hint = computed(() => {
  const code = model.value.trim()
  if (!enabled.value || !accounts.value.length) return null
  if (code === '') return { ok: false, text: `No account: amounts go to ${FALLBACK[props.kind]} until you choose one.` }
  const a = accounts.value.find((x) => x.code === code)
  if (!a) return { ok: false, text: `${code} is not in the chart of accounts: amounts go to ${FALLBACK[props.kind]}.` }
  if (!a.is_active) return { ok: false, text: `${a.code} ${a.name} is inactive: amounts go to ${FALLBACK[props.kind]}.` }
  if (!a.is_postable) return { ok: false, text: `${a.code} ${a.name} is a header account and takes no postings: amounts go to ${FALLBACK[props.kind]}.` }
  if (!fits(a)) return { ok: false, text: `${a.code} ${a.name} is a ${a.account_type.toLowerCase()} account, which does not fit here: amounts go to ${FALLBACK[props.kind]}.` }
  return { ok: true, text: `${a.code} - ${a.name}` }
})

watch(() => property.currentId, async (id) => {
  accounts.value = []
  if (id === null || !auth.can('accounting.view', id)) return
  try {
    accounts.value = await listAccounts(id)
  } catch {
    accounts.value = [] // the hint is a convenience: the form works without it
  }
}, { immediate: true })
</script>

<template>
  <input v-model="model" name="gl_account_code" :list="enabled ? listId : undefined" placeholder="e.g. 4110" maxlength="30" autocomplete="off" :aria-invalid="invalid" />
  <datalist v-if="enabled" :id="listId">
    <option v-for="a in suggestions" :key="a.id" :value="a.code">{{ a.name }}</option>
  </datalist>
  <small v-if="hint" :class="hint.ok ? 'muted' : 'error-text'" data-testid="account-hint">{{ hint.text }}</small>
</template>
