<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { computed } from 'vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import RateItemsSection from './RateItemsSection.vue'

const property = usePropertyStore()
// The sentence has a link in it: the text before it and the text after it.
const intro = computed(() => t('taxes.intro', { link: '\u0000' }).split('\u0000'))
</script>

<template>
  <PageHeader :title="t('taxes.title')" />
  <p v-if="property.currentId === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <p class="mb-4 text-sm text-muted-foreground">
      {{ intro[0] }}<RouterLink to="/setup/charge-codes" class="text-primary underline-offset-2 hover:underline">{{ t('taxes.chargeCodes') }}</RouterLink>{{ intro[1] }}
    </p>
    <RateItemsSection :key="`tax-${property.currentId}`" kind="tax" />
    <RateItemsSection :key="`service-${property.currentId}`" kind="service" />
  </template>
</template>
