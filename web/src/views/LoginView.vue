<script setup lang="ts">
import { Eye, EyeOff } from 'lucide-vue-next'
import { computed, reactive, ref, useId } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { vAutofocus } from '@/directives/autofocus'
import { NativeSelect } from '@/components/ui/native-select'
import { i18n, LOCALES, setLocale, t, type Locale } from '@/i18n'
import { rememberedTenantCode, useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const form = reactive({ tenant_code: rememberedTenantCode(), email: '', password: '' })
const showPassword = ref(false) // the eye of the password field; not kept anywhere, hidden on every visit
const forgotOpen = ref(false)
const forgotId = useId()
const busy = ref(false)
const message = ref('')

const known = ['INVALID_CREDENTIALS', 'TOO_MANY_ATTEMPTS', 'VALIDATION_FAILED'] as const
const messageFor = (code: string, fallback: string): string => ((known as readonly string[]).includes(code) ? t(`login.${code}` as 'login.INVALID_CREDENTIALS') : fallback)
const locale = computed(() => i18n.global.locale.value)

/**
 * Only same-site paths are allowed after sign-in. "//host" and "/\host" are
 * protocol-relative URLs to another site (open redirect), so they are refused.
 */
function safeRedirect(target: unknown): string {
  if (typeof target !== 'string' || !target.startsWith('/') || target.startsWith('//') || target.startsWith('/\\')) return '/'
  return target
}

async function submit(): Promise<void> {
  busy.value = true
  message.value = ''
  try {
    await auth.login(form.tenant_code, form.email, form.password)
    await router.replace(safeRedirect(route.query.redirect))
  } catch (e) {
    message.value = e instanceof ApiError ? messageFor(e.code, e.message) : t('login.unreachable')
    form.password = ''
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="grid min-h-screen place-items-center p-4">
    <Card class="w-full max-w-sm">
      <form v-autofocus class="grid gap-4" novalidate @submit.prevent="submit">
        <CardContent class="grid gap-4 pt-6">
          <div class="flex items-center gap-2.5">
            <span class="grid size-8 place-items-center rounded-lg bg-primary font-bold text-primary-foreground" aria-hidden="true">K</span>
            <h1 class="m-0 text-lg font-semibold">{{ t('login.title') }}</h1>
          </div>
          <p v-if="message" class="alert m-0" role="alert" data-testid="login-error">{{ message }}</p>
          <FormField :label="t('login.tenantCode')">
            <template #default="{ id }"><Input :id="id" v-model="form.tenant_code" :data-autofocus="form.tenant_code ? undefined : ''" name="tenant_code" autocomplete="organization" autocapitalize="characters" required /></template>
          </FormField>
          <FormField :label="t('login.email')">
            <template #default="{ id }"><Input :id="id" v-model="form.email" :data-autofocus="form.tenant_code ? '' : undefined" name="email" type="email" autocomplete="username" required /></template>
          </FormField>
          <FormField :label="t('login.password')">
            <template #default="{ id }">
              <div class="relative">
                <Input :id="id" v-model="form.password" name="password" :type="showPassword ? 'text' : 'password'" autocomplete="current-password" class="pr-10 pointer-coarse:pr-[52px]" required />
                <button
                  type="button"
                  class="absolute right-0 top-1/2 flex size-9 -translate-y-1/2 cursor-pointer items-center justify-center rounded-md border-0 bg-transparent text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:size-[44px]"
                  :aria-pressed="showPassword"
                  :aria-label="t('login.showPassword')"
                  data-testid="toggle-password"
                  @click="showPassword = !showPassword"
                >
                  <EyeOff v-if="showPassword" class="size-4" aria-hidden="true" /><Eye v-else class="size-4" aria-hidden="true" />
                </button>
              </div>
            </template>
          </FormField>
          <div class="-mt-2">
            <button
              type="button"
              class="cursor-pointer border-0 bg-transparent p-0 text-sm text-primary underline-offset-2 hover:underline focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:min-h-[44px]"
              :aria-expanded="forgotOpen"
              :aria-controls="forgotId"
              data-testid="forgot-password"
              @click="forgotOpen = !forgotOpen"
            >{{ t('login.forgot') }}</button>
            <p v-if="forgotOpen" :id="forgotId" class="m-0 mt-1 text-sm text-muted-foreground" data-testid="forgot-help">{{ t('login.forgotHelp') }}</p>
          </div>
          <Button type="submit" :disabled="busy">{{ busy ? t('login.signingIn') : t('login.signIn') }}</Button>
          <NativeSelect :model-value="locale" class="w-auto justify-self-end" :aria-label="t('language.label')" @change="void setLocale(($event.target as HTMLSelectElement).value as Locale)">
            <option v-for="l in LOCALES" :key="l" :value="l">{{ t(`language.${l}` as never) }}</option>
          </NativeSelect>
        </CardContent>
      </form>
    </Card>
  </main>
</template>
