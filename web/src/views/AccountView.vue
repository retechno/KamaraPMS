<script setup lang="ts">
import { reactive, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { THEME_PREFERENCES, useTheme, type ThemePreference } from '@/composables/useTheme'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const theme = useTheme()
const themeLabel: Record<ThemePreference, string> = {
  light: 'account.themeLight',
  dark: 'account.themeDark',
  system: 'account.themeSystem',
}
const form = reactive({ current_password: '', new_password: '', confirm: '' })
const busy = ref(false)
const error = ref<ApiError | null>(null)
const mismatch = ref(false)
const saved = ref(false)

async function submit(): Promise<void> {
  saved.value = false
  error.value = null
  mismatch.value = form.new_password !== form.confirm
  if (mismatch.value) return
  busy.value = true
  try {
    await api.POST('/api/v1/auth/password', { body: { current_password: form.current_password, new_password: form.new_password } })
    Object.assign(form, { current_password: '', new_password: '', confirm: '' })
    saved.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PageHeader :title="t('account.title')" />
  <Card v-if="auth.me" class="mb-4">
    <CardHeader>
      <CardTitle>{{ auth.me.user.full_name }}</CardTitle>
      <p class="m-0 text-sm text-muted-foreground">{{ auth.me.user.email }} · {{ auth.me.tenant.name }} ({{ auth.me.tenant.code }})</p>
    </CardHeader>
  </Card>

  <Card class="mb-4">
    <CardHeader>
      <CardTitle>{{ t('account.theme') }}</CardTitle>
      <p class="m-0 text-sm text-muted-foreground">{{ t('account.themeHint') }}</p>
    </CardHeader>
    <CardContent>
      <div role="radiogroup" :aria-label="t('account.theme')" class="flex flex-wrap gap-2" data-testid="theme-switcher">
        <Button
          v-for="p in THEME_PREFERENCES"
          :key="p"
          type="button"
          role="radio"
          size="sm"
          :variant="theme.preference.value === p ? 'default' : 'outline'"
          :aria-checked="theme.preference.value === p"
          :data-testid="`theme-${p}`"
          @click="theme.setPreference(p)"
        >
          {{ t(themeLabel[p] as never) }}
        </Button>
      </div>
    </CardContent>
  </Card>

  <Card>
    <form novalidate @submit.prevent="submit">
      <CardHeader><CardTitle>{{ t('account.changePassword') }}</CardTitle></CardHeader>
      <CardContent>
        <p v-if="saved" class="alert warning" role="status">{{ t('account.changed') }}</p>
        <p v-if="error && !error.fieldErrors.length" class="alert" role="alert">{{ error.message }}</p>
        <div class="grid gap-4 sm:grid-cols-3">
          <FormField :label="t('account.current')" :error="error?.fieldMessage('current_password') ? t('account.currentWrong') : undefined">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.current_password" type="password" autocomplete="current-password" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('account.new')" :hint="t('account.newHint')" :error="error?.fieldMessage('new_password')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.new_password" type="password" autocomplete="new-password" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('account.repeat')" :error="mismatch ? t('account.mismatch') : undefined">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.confirm" type="password" autocomplete="new-password" :aria-invalid="invalid" /></template>
          </FormField>
        </div>
        <div class="mt-4 flex justify-end">
          <Button type="submit" :disabled="busy">{{ busy ? t('account.saving') : t('account.changePassword') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>
</template>
