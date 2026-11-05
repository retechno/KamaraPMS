<script setup lang="ts">
/** Said before the payment is posted: an amount above the balance leaves the folio in credit. It warns and never blocks (the server accepts such a payment; the credit is refunded later). */
import { computed } from 'vue'
import { t } from '@/i18n'
import { creditAfterPayment } from '@/utils/folioCredit'

const props = defineProps<{ balance: string; amount: string }>()
const credit = computed(() => creditAfterPayment(props.balance, props.amount))
</script>

<template>
  <p v-if="credit > 0" class="alert warning" role="status" data-testid="overpay-warning">{{ t('folio.overpay', { credit: $money(String(credit)) }) }}</p>
</template>
