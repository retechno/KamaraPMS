<script setup lang="ts">
import { Check, CircleDot, CornerUpLeft, Minus, X } from 'lucide-vue-next'
import { computed } from 'vue'
import type { InventoryApprovalEdit, InventoryApprovalStep, InventoryRequest } from '@/api/types'
import { t } from '@/i18n'
import { formatDate, formatMoney } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/**
 * The approval chain of a purchase request (docs/architecture/35-inventory.md 5.8, mockup 19): the submission, then each level of the round with its state, who decided it and when, the note, who is waited for at
 * the active level, and why a conditional level applies or not. Earlier rounds (after a return) are listed under it; the edits of the approvers are recorded under the levels.
 */
const props = defineProps<{ request: InventoryRequest }>()

const cycles = computed(() => [...new Set(props.request.steps.map((s) => s.cycle))].sort((a, b) => b - a))
const stepsOf = (cycle: number): InventoryApprovalStep[] => props.request.steps.filter((s) => s.cycle === cycle).sort((a, b) => a.level_no - b.level_no)
const current = computed(() => stepsOf(props.request.cycle))
const earlier = computed(() => cycles.value.filter((c) => c !== props.request.cycle))
const editsOf = (cycle: number): InventoryApprovalEdit[] => props.request.edits.filter((e) => e.cycle === cycle)
const day = (iso: string | null | undefined): string => (iso ? formatDate(iso.slice(0, 10)) : '')

/** What the level says about itself: the approver it asks, and when it applies. */
function who(s: InventoryApprovalStep): string {
  return s.approver_kind === 'DEPT_HEAD' ? t('inventory.chain.whoHead') : t('inventory.chain.whoPermission', { permission: t(`inventory.chain.permission.${s.permission ?? 'none'}`) })
}
function condition(s: InventoryApprovalStep): string {
  if (s.condition_kind !== 'TOTAL_ABOVE' || !s.threshold) return ''
  if (s.status === 'SKIPPED' && s.skip_reason === 'CONDITION') return t('inventory.chain.notNeeded', { amount: formatMoney(s.threshold) })
  if (s.status === 'WAITING') return s.applies ? t('inventory.chain.requiredAbove', { amount: formatMoney(s.threshold) }) : t('inventory.chain.notNeeded', { amount: formatMoney(s.threshold) })
  return t('inventory.chain.above', { amount: formatMoney(s.threshold) })
}
const icon = (s: InventoryApprovalStep) => ({ APPROVED: Check, ACTIVE: CircleDot, RETURNED: CornerUpLeft, SKIPPED: Minus, NOT_REACHED: X, WAITING: CircleDot })[s.status] ?? CircleDot
const tone = (s: InventoryApprovalStep): string =>
  ({ APPROVED: 'bg-success/15 text-success-text', ACTIVE: 'bg-warning/25 text-foreground', RETURNED: 'bg-destructive/15 text-destructive' } as Record<string, string>)[s.status] ?? 'bg-muted text-muted-foreground'
</script>

<template>
  <section class="rounded-xl border border-border bg-card p-4" data-testid="approval-chain">
    <h2 class="m-0 text-base font-semibold">{{ t('inventory.chain.title', { n: current.length }) }}</h2>
    <ol class="m-0 mt-3 list-none p-0">
      <li class="flex gap-3 pb-3">
        <span class="grid size-6 shrink-0 place-items-center rounded-full bg-success/15 text-success-text"><Check class="size-3.5" aria-hidden="true" /></span>
        <div class="text-sm"><strong>{{ t('inventory.chain.submitted') }}</strong><br /><span class="text-muted-foreground">{{ request.requester_name }}<template v-if="request.submitted_at"> · {{ day(request.submitted_at) }}</template></span></div>
      </li>
      <li v-for="s in current" :key="s.id" class="flex gap-3 pb-3" :data-testid="`step-${s.level_no}`" :data-status="s.status">
        <span :class="`grid size-6 shrink-0 place-items-center rounded-full ${tone(s)}`"><component :is="icon(s)" class="size-3.5" aria-hidden="true" /></span>
        <div class="min-w-0 text-sm">
          <strong>{{ s.level_no }} · {{ s.name }}</strong> <span class="text-xs text-muted-foreground">{{ t(`inventory.chain.state.${s.status}`) }}</span>
          <div v-if="s.status === 'APPROVED' || s.status === 'RETURNED'" class="text-muted-foreground">{{ s.decided_by_name }} · {{ day(s.decided_at) }}</div>
          <div v-if="s.note" class="text-muted-foreground" data-testid="step-note">“{{ s.note }}”</div>
          <div v-if="s.status === 'ACTIVE'" class="text-muted-foreground" data-testid="step-waiting">
            {{ who(s) }}<template v-if="s.approvers && s.approvers.length"> · {{ s.approvers.join(', ') }}</template>
          </div>
          <div v-else-if="s.status === 'WAITING' || s.status === 'SKIPPED'" class="text-muted-foreground">
            <template v-if="s.status === 'SKIPPED' && s.skip_reason === 'REQUESTER_IS_HEAD'">{{ t('inventory.chain.requesterIsHead') }}</template>
            <template v-else>{{ who(s) }}</template>
          </div>
          <div v-if="condition(s)" class="text-xs text-muted-foreground" data-testid="step-condition">{{ condition(s) }}</div>
          <div v-if="s.total_seen && (s.status === 'APPROVED' || s.status === 'RETURNED')" class="text-xs text-muted-foreground">{{ t('inventory.chain.totalSeen', { total: formatMoney(s.total_seen) }) }}</div>
        </div>
      </li>
      <li v-if="request.status === 'APPROVED' || request.status === 'CONVERTED'" class="flex gap-3">
        <span class="grid size-6 shrink-0 place-items-center rounded-full bg-success/15 text-success-text"><Check class="size-3.5" aria-hidden="true" /></span>
        <div class="text-sm"><strong>{{ t('inventory.chain.done') }}</strong></div>
      </li>
    </ol>
    <div v-if="editsOf(request.cycle).length" class="mt-2 border-t border-border pt-2" data-testid="chain-edits">
      <h3 class="m-0 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('inventory.chain.edits') }}</h3>
      <ul class="m-0 mt-1 list-none p-0 text-xs">
        <li v-for="e in editsOf(request.cycle)" :key="e.id">
          {{ t('inventory.chain.edit', { line: e.line_no, field: t(`inventory.chain.field.${e.field}`), from: e.field === 'QTY' ? formatQty(e.old_value) : formatMoney(e.old_value), to: e.field === 'QTY' ? formatQty(e.new_value) : formatMoney(e.new_value), by: e.edited_by_name, level: e.level_no }) }}
        </li>
      </ul>
    </div>
    <details v-if="earlier.length" class="mt-3 border-t border-border pt-2 text-sm" data-testid="chain-history">
      <summary class="cursor-pointer text-muted-foreground">{{ t('inventory.chain.earlier', { n: earlier.length }) }}</summary>
      <div v-for="c in earlier" :key="c" class="mt-2">
        <strong class="text-xs">{{ t('inventory.chain.round', { n: c }) }}</strong>
        <ul class="m-0 list-none p-0 text-xs text-muted-foreground">
          <li v-for="s in stepsOf(c)" :key="s.id">{{ s.level_no }} · {{ s.name }}: {{ t(`inventory.chain.state.${s.status}`) }}<template v-if="s.decided_by_name"> · {{ s.decided_by_name }}</template><template v-if="s.note"> · “{{ s.note }}”</template></li>
        </ul>
      </div>
    </details>
  </section>
</template>
