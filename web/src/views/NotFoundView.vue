<script setup lang="ts">
import { RouterLink } from 'vue-router'
import EmptyState from '@/components/app/EmptyState.vue'
import { t } from '@/i18n'
</script>

<template>
  <EmptyState :title="t('notFound.title')" :description="t('notFound.text')">
    <RouterLink to="/" class="text-sm text-primary hover:underline">{{ t('notFound.back') }}</RouterLink>
  </EmptyState>
</template>
