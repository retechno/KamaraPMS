<script setup lang="ts">
import type { StatementLine } from '@/api/types'
import { money } from './reportApi'

defineProps<{ lines: StatementLine[] }>()
</script>

<template>
  <table class="list statement" data-testid="statement">
    <tbody>
      <template v-for="l in lines" :key="l.key">
        <tr v-if="l.kind === 'HEADING'" class="heading" :data-testid="`line-${l.key}`"><th colspan="3">{{ l.title }}</th></tr>
        <template v-else-if="l.kind === 'GROUP'">
          <tr class="group" :data-testid="`line-${l.key}`">
            <td colspan="2">{{ l.title }}</td>
            <td class="num">{{ l.accounts.length > 1 ? '' : money(l.amount) }}</td>
          </tr>
          <tr v-for="a in l.accounts.length > 1 ? l.accounts : []" :key="a.account_id" class="account">
            <td class="code">{{ a.code }}</td>
            <td>{{ a.name }}</td>
            <td class="num">{{ money(a.amount) }}</td>
          </tr>
          <tr v-if="l.accounts.length > 1" class="subtotal-row">
            <td colspan="2">Total {{ l.title.toLowerCase() }}</td>
            <td class="num">{{ money(l.amount) }}</td>
          </tr>
        </template>
        <tr v-else :class="l.kind === 'TOTAL' ? 'total' : 'subtotal'" :data-testid="`line-${l.key}`">
          <td colspan="2"><b>{{ l.title }}</b></td>
          <td class="num"><b>{{ money(l.amount) }}</b></td>
        </tr>
      </template>
    </tbody>
  </table>
</template>

<style scoped>
.heading th {
  text-align: left;
  padding-top: 14px;
}
.group td {
  font-weight: 500;
}
.account td {
  color: var(--muted, #6b7280);
}
.account .code {
  padding-left: 18px;
  width: 90px;
}
.num {
  text-align: right;
  white-space: nowrap;
}
.total td {
  border-top: 2px solid currentcolor;
}
.subtotal td {
  border-top: 1px solid currentcolor;
}
</style>
