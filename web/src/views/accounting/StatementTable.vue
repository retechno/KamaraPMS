<script setup lang="ts">
import type { StatementLine } from '@/api/types'
import { t } from '@/i18n'
import { money } from './reportApi'

defineProps<{ lines: StatementLine[] }>()
</script>

<template>
  <table class="w-full border-collapse text-sm" data-testid="statement">
    <tbody>
      <template v-for="l in lines" :key="l.key">
        <tr v-if="l.kind === 'HEADING'" :data-testid="`line-${l.key}`"><th colspan="3" class="pb-1 pt-4 text-left text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ l.title }}</th></tr>
        <template v-else-if="l.kind === 'GROUP'">
          <tr :data-testid="`line-${l.key}`" class="border-b border-border">
            <td colspan="2" class="py-1.5 font-medium">{{ l.title }}</td>
            <td class="py-1.5 text-right tabular-nums">{{ l.accounts.length > 1 ? '' : money(l.amount) }}</td>
          </tr>
          <tr v-for="a in l.accounts.length > 1 ? l.accounts : []" :key="a.account_id" class="text-muted-foreground">
            <td class="w-24 py-1 pl-4">{{ a.code }}</td>
            <td class="py-1">{{ a.name }}</td>
            <td class="py-1 text-right tabular-nums">{{ money(a.amount) }}</td>
          </tr>
          <tr v-if="l.accounts.length > 1" class="border-b border-border">
            <td colspan="2" class="py-1.5">{{ t('statements.totalOf', { title: l.title.toLowerCase() }) }}</td>
            <td class="py-1.5 text-right tabular-nums">{{ money(l.amount) }}</td>
          </tr>
        </template>
        <tr v-else :class="l.kind === 'TOTAL' ? 'border-t-2 border-foreground' : 'border-t border-foreground'" :data-testid="`line-${l.key}`">
          <td colspan="2" class="py-1.5"><b>{{ l.title }}</b></td>
          <td class="py-1.5 text-right tabular-nums"><b>{{ money(l.amount) }}</b></td>
        </tr>
      </template>
    </tbody>
  </table>
</template>
