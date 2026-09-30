<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { usePropertyStore } from '@/stores/property'
import RateItemsSection from './RateItemsSection.vue'

const property = usePropertyStore()
</script>

<template>
  <h1 class="page-title">Taxes and service charges</h1>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <template v-else>
    <p class="muted">
      Rates apply to future postings only. A tax or service charge is attached to charge codes under
      <RouterLink to="/setup/charge-codes">Charge codes</RouterLink>, in the order they are calculated.
    </p>
    <RateItemsSection :key="`tax-${property.currentId}`" kind="tax" />
    <RateItemsSection :key="`service-${property.currentId}`" kind="service" />
  </template>
</template>
