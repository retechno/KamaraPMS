<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { GlAccount } from '@/api/types'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { listAccounts } from '@/views/accounting/accountApi'

/**
 * The account code of a charge code, tax or service charge: a text input that suggests the accounts of the chart that
 * can take the amounts (revenue for what is sold, liabilities for taxes, liabilities or revenue for service charges) and
 * says what the typed code is, or what will happen to the amounts if the journals cannot use it. Without
 * accounting.view it is a plain input.
 */
const props = defineProps<{ kind: 'CHARGE_CODE' | 'TAX' | 'SERVICE_CHARGE'; invalid?: boolean; id?: string }>()
const model = defineModel<string>({ required: true })

const auth = useAuthStore()
const property = usePropertyStore()
const accounts = ref<GlAccount[]>([])
const listId = `gl-accounts-${props.kind.toLowerCase()}-${Math.random().toString(36).slice(2, 8)}`

const fallback = (): string => t(props.kind === 'CHARGE_CODE' ? 'glAccount.fallbackChargeCode' : props.kind === 'TAX' ? 'glAccount.fallbackTax' : 'glAccount.fallbackService')
const fits = (a: GlAccount): boolean =>
  props.kind === 'CHARGE_CODE' ? a.account_type === 'REVENUE' : props.kind === 'TAX' ? a.account_type === 'LIABILITY' : a.account_type === 'LIABILITY' || a.account_type === 'REVENUE'

const suggestions = computed(() => accounts.value.filter((a) => a.is_postable && a.is_active && fits(a)))
const enabled = computed(() => property.currentId !== null && auth.can('accounting.view', property.currentId))

const hint = computed(() => {
  const code = model.value.trim()
  if (!enabled.value || !accounts.value.length) return null
  const f = { fallback: fallback() }
  if (code === '') return { ok: false, text: t('glAccount.none', f) }
  const a = accounts.value.find((x) => x.code === code)
  if (!a) return { ok: false, text: t('glAccount.unknown', { code, ...f }) }
  const named = { code: a.code, name: a.name, ...f }
  if (!a.is_active) return { ok: false, text: t('glAccount.inactive', named) }
  if (!a.is_postable) return { ok: false, text: t('glAccount.header', named) }
  if (!fits(a)) return { ok: false, text: t('glAccount.misfit', { ...named, type: a.account_type.toLowerCase() }) }
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
  <Input :id="id" v-model="model" name="gl_account_code" :list="enabled ? listId : undefined" :placeholder="t('glAccount.placeholder')" maxlength="30" autocomplete="off" :aria-invalid="invalid" />
  <datalist v-if="enabled" :id="listId">
    <option v-for="a in suggestions" :key="a.id" :value="a.code">{{ a.name }}</option>
  </datalist>
  <small v-if="hint" :class="hint.ok ? 'text-xs text-muted-foreground' : 'text-xs text-destructive'" data-testid="account-hint">{{ hint.text }}</small>
</template>
