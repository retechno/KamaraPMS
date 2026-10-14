import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { ApprovalKind, ApprovalRule } from '@/api/types'

/**
 * The rule of approval of each kind of correction at the property (docs/architecture/32-distinct-approver.md), read once per property and kept: the dialog of approval says the rule in force
 * for the correction it asks for, and the screen of the rules reads and changes the same list. A kind without a rule is FREE.
 */
export const useApprovalRulesStore = defineStore('approvalRules', () => {
  const rules = ref<ApprovalRule[]>([])
  const forProperty = ref<number | null>(null)
  const error = ref<ApiError | null>(null)
  let pending: Promise<void> | null = null

  /** Reads the rules of the property, once: `force` reads them again (after a change). */
  function load(propertyId: number | null, force = false): Promise<void> {
    if (propertyId === null) {
      rules.value = []
      forProperty.value = null
      return Promise.resolve()
    }
    if (!force && forProperty.value === propertyId) return pending ?? Promise.resolve()
    forProperty.value = propertyId
    rules.value = []
    pending = (async () => {
      try {
        const { data } = await api.GET('/api/v1/properties/{propertyId}/approval-rules', { params: { path: { propertyId } } })
        if (forProperty.value === propertyId) rules.value = data?.data ?? []
        error.value = null
      } catch (e) {
        error.value = e instanceof ApiError ? e : null
        if (forProperty.value === propertyId) forProperty.value = null // read again next time
      }
    })()
    return pending
  }

  /** Puts the list that came back from a change. */
  function set(propertyId: number, list: ApprovalRule[]): void {
    forProperty.value = propertyId
    rules.value = list
  }

  const ruleOf = (kind: ApprovalKind | undefined): ApprovalRule | undefined => (kind ? rules.value.find((r) => r.kind === kind) : undefined)

  function reset(): void {
    rules.value = []
    forProperty.value = null
    pending = null
  }

  return { rules, error, load, set, ruleOf, reset }
})
