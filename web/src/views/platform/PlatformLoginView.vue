<script setup lang="ts">
import { Eye, EyeOff } from 'lucide-vue-next'
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ApiError } from '@/api/problem'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { vAutofocus } from '@/directives/autofocus'
import { i18n, LOCALES, setLocale, t, type Locale } from '@/i18n'
import { usePlatformAuthStore } from '@/stores/platformAuth'

/**
 * The sign-in of the platform console in three steps on one page: the password, then the code (or a recovery code), or, for an account that has no
 * authenticator yet, the enrolment. The challenge, the secret and the recovery codes live only in this component's state: no store, no storage, no URL, no log.
 */
const auth = usePlatformAuthStore()
const router = useRouter()
const route = useRoute()

type Step = 'password' | 'code' | 'enroll'
const step = ref<Step>('password')
const form = reactive({ email: '', password: '', totp: '', recovery: '', enrollCode: '' })
const showPassword = ref(false)
const recoveryOpen = ref(false)
const busy = ref(false)
const message = ref('')
const attemptsLeft = ref<number | null>(null)
const challenge = ref('')
const locale = computed(() => i18n.global.locale.value)

// the enrolment
const qr = ref('')
const secret = ref('')
const secretCopied = ref(false)
const codes = ref<string[]>([])
const codesCopied = ref(false)
const saved = ref(false)
let openConsole: (() => void) | null = null

/** Only a page of the console is a place to return to: never another site, never a page of the hotel. */
function safeRedirect(target: unknown): string {
  const fallback = '/platform/tenants'
  if (typeof target !== 'string' || !target.startsWith('/platform/') || target.includes('\\') || target.startsWith('//')) return fallback
  return target
}

async function enter(): Promise<void> {
  await router.replace(safeRedirect(route.query.redirect))
}

function backToPassword(text = ''): void {
  step.value = 'password'
  challenge.value = ''
  message.value = text
  attemptsLeft.value = null
  recoveryOpen.value = false
  form.password = ''
  form.totp = ''
  form.recovery = ''
  form.enrollCode = ''
  qr.value = ''
  secret.value = ''
}

/** What a failed step says. A challenge that is gone sends the person back to the password; the rest is told here. */
function fail(e: unknown, own: Record<string, string> = {}): void {
  if (!(e instanceof ApiError)) {
    message.value = t('platform.login.unreachable')
    return
  }
  if (e.code === 'CHALLENGE_INVALID') {
    backToPassword(t('platform.login.expired'))
    return
  }
  const left = e.context.attempts_left
  attemptsLeft.value = typeof left === 'number' ? left : null
  message.value = own[e.code] ?? e.message
}

async function submitPassword(): Promise<void> {
  busy.value = true
  message.value = ''
  try {
    const c = await auth.login(form.email.trim(), form.password)
    challenge.value = c.challenge
    form.password = ''
    attemptsLeft.value = null
    if (c.next === 'enroll') {
      step.value = 'enroll'
      await startEnrolment()
    } else step.value = 'code'
  } catch (e) {
    form.password = ''
    fail(e, { INVALID_CREDENTIALS: t('platform.login.wrongPassword') })
  } finally {
    busy.value = false
  }
}

async function submitCode(): Promise<void> {
  const digits = form.totp.replace(/\D/g, '')
  if (!digits) return
  await verify({ code: digits })
}

async function submitRecovery(): Promise<void> {
  const code = form.recovery.trim()
  if (!code) return
  await verify({ recoveryCode: code })
}

async function verify(input: { code?: string; recoveryCode?: string }): Promise<void> {
  busy.value = true
  message.value = ''
  try {
    await auth.verify(challenge.value, input)
    await enter()
  } catch (e) {
    form.totp = ''
    form.recovery = ''
    fail(e, { TOTP_INVALID: t('platform.login.totp.wrongCode') })
  } finally {
    busy.value = false
  }
}

async function startEnrolment(): Promise<void> {
  try {
    const started = await auth.enrollStart(challenge.value)
    qr.value = started.qr_png
    secret.value = started.secret
  } catch (e) {
    fail(e)
  }
}

async function activate(): Promise<void> {
  const digits = form.enrollCode.replace(/\D/g, '')
  if (!digits) return
  busy.value = true
  message.value = ''
  try {
    const done = await auth.enrollConfirm(challenge.value, digits)
    codes.value = done.codes
    openConsole = done.open
    form.enrollCode = ''
  } catch (e) {
    form.enrollCode = ''
    fail(e, { TOTP_INVALID: t('platform.login.totp.wrongCode') })
  } finally {
    busy.value = false
  }
}

async function copyText(text: string, which: 'secret' | 'codes'): Promise<void> {
  const flag = which === 'secret' ? secretCopied : codesCopied
  try {
    await navigator.clipboard.writeText(text)
    flag.value = true
  } catch {
    flag.value = false
  }
}

function download(): void {
  try {
    const text = `${t('platform.login.enroll.fileHeader')}\n\n${codes.value.join('\n')}\n`
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }))
    const a = document.createElement('a')
    a.href = url
    a.download = 'kamarapms-platform-recovery-codes.txt'
    a.click()
    URL.revokeObjectURL(url)
  } catch {
    // no download in this browser: the codes are on the screen, and "Copy all" is there
  }
}

async function openTheConsole(): Promise<void> {
  if (!saved.value || !openConsole) return
  const open = openConsole
  openConsole = null
  open()
  await enter()
}

onBeforeUnmount(() => {
  codes.value = []
  secret.value = ''
  openConsole = null
})
</script>

<template>
  <main class="grid min-h-screen place-items-center p-4">
    <div :class="['grid w-full gap-3', step === 'enroll' ? 'max-w-2xl' : 'max-w-[400px]']">
      <Card>
        <CardContent class="grid gap-4 pt-6">
          <!-- Step 1: the password -->
          <form v-if="step === 'password'" v-autofocus class="grid gap-4" novalidate data-testid="step-password" @submit.prevent="submitPassword">
            <div class="flex items-center gap-2.5">
              <span class="grid size-8 shrink-0 place-items-center rounded-lg bg-[#1c2128] font-bold text-[#3fa98d] dark:border dark:border-border" aria-hidden="true">K</span>
              <div class="min-w-0 flex-1">
                <h1 class="m-0 text-lg font-semibold">{{ t('platform.login.title') }}</h1>
                <span class="text-sm text-muted-foreground">{{ t('platform.login.subtitle') }}</span>
              </div>
              <span class="rounded-full bg-[#1c2128] px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-white dark:border dark:border-border" data-testid="platform-pill">{{ t('platform.pill') }}</span>
            </div>
            <p v-if="message" class="alert m-0" role="alert" data-testid="login-error">{{ message }}</p>
            <FormField :label="t('platform.login.email')">
              <template #default="{ id }"><Input :id="id" v-model="form.email" name="email" type="email" autocomplete="username" required /></template>
            </FormField>
            <FormField :label="t('platform.login.password')">
              <template #default="{ id }">
                <div class="relative">
                  <Input :id="id" v-model="form.password" name="password" :type="showPassword ? 'text' : 'password'" autocomplete="current-password" :aria-invalid="message ? true : undefined" class="pr-10 pointer-coarse:pr-[52px]" required />
                  <button
                    type="button"
                    class="absolute right-0 top-1/2 flex size-9 -translate-y-1/2 cursor-pointer items-center justify-center rounded-md border-0 bg-transparent text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:size-[44px]"
                    :aria-pressed="showPassword"
                    :aria-label="t('platform.login.showPassword')"
                    data-testid="toggle-password"
                    @click="showPassword = !showPassword"
                  >
                    <EyeOff v-if="showPassword" class="size-4" aria-hidden="true" /><Eye v-else class="size-4" aria-hidden="true" />
                  </button>
                </div>
              </template>
            </FormField>
            <Button type="submit" :disabled="busy">{{ busy ? t('platform.login.checking') : t('platform.login.continue') }}</Button>
            <p class="m-0 text-sm text-muted-foreground">{{ t('platform.login.note') }}</p>
          </form>

          <!-- Step 2: the code, or a recovery code -->
          <div v-else-if="step === 'code'" class="grid gap-4" data-testid="step-code">
            <div class="flex items-center gap-2.5">
              <span class="grid size-8 shrink-0 place-items-center rounded-lg bg-[#1c2128] font-bold text-[#3fa98d] dark:border dark:border-border" aria-hidden="true">K</span>
              <h1 class="m-0 min-w-0 flex-1 text-lg font-semibold">{{ t('platform.login.totp.title') }}</h1>
              <span class="rounded-full bg-[#1c2128] px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-white dark:border dark:border-border">{{ t('platform.pill') }}</span>
            </div>
            <p class="m-0 text-sm">{{ t('platform.login.totp.intro') }} <strong data-testid="login-email">{{ form.email }}</strong>.</p>
            <p v-if="message" class="alert m-0" role="alert" data-testid="login-error">{{ message }}</p>
            <form v-autofocus class="grid gap-4" novalidate @submit.prevent="submitCode">
              <FormField :label="t('platform.login.totp.label')" :hint="t('platform.login.totp.hint')">
                <template #default="{ id, describedBy }">
                  <Input :id="id" v-model="form.totp" name="totp" inputmode="numeric" autocomplete="one-time-code" maxlength="8" :aria-describedby="describedBy" placeholder="000 000" />
                </template>
              </FormField>
              <Button type="submit" :disabled="busy">{{ busy ? t('platform.login.totp.signingIn') : t('platform.login.totp.signIn') }}</Button>
            </form>
            <details :open="recoveryOpen" class="grid gap-2" @toggle="recoveryOpen = ($event.target as HTMLDetailsElement).open">
              <summary class="cursor-pointer text-sm text-primary pointer-coarse:min-h-[44px]" data-testid="recovery-toggle">{{ t('platform.login.totp.useRecovery') }}</summary>
              <form class="mt-2 grid gap-3" novalidate @submit.prevent="submitRecovery">
                <FormField :label="t('platform.login.totp.recoveryLabel')" :hint="t('platform.login.totp.recoveryHint')">
                  <template #default="{ id, describedBy }">
                    <Input :id="id" v-model="form.recovery" name="recovery_code" autocomplete="off" placeholder="XXXX-XXXX-XX" :aria-describedby="describedBy" />
                  </template>
                </FormField>
                <Button type="submit" variant="outline" :disabled="busy">{{ t('platform.login.totp.signIn') }}</Button>
              </form>
            </details>
            <div class="flex items-center justify-between gap-2">
              <Button type="button" variant="outline" size="sm" data-testid="back" @click="backToPassword()">{{ t('platform.login.totp.back') }}</Button>
              <span v-if="attemptsLeft !== null" class="text-sm text-muted-foreground" data-testid="attempts-left">{{ t('platform.login.totp.attemptsLeft', { n: attemptsLeft }) }}</span>
            </div>
          </div>

          <!-- Step 3: the enrolment of the authenticator -->
          <div v-else class="grid gap-4" data-testid="step-enroll">
            <div class="flex items-center gap-2.5">
              <span class="grid size-8 shrink-0 place-items-center rounded-lg bg-[#1c2128] font-bold text-[#3fa98d] dark:border dark:border-border" aria-hidden="true">K</span>
              <div class="min-w-0 flex-1">
                <h1 class="m-0 text-lg font-semibold">{{ t('platform.login.enroll.title') }}</h1>
                <span class="text-sm text-muted-foreground">{{ t('platform.login.enroll.subtitle') }}</span>
              </div>
              <span class="rounded-full bg-[#1c2128] px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-white dark:border dark:border-border">{{ t('platform.pill') }}</span>
            </div>
            <p v-if="message" class="alert m-0" role="alert" data-testid="login-error">{{ message }}</p>
            <ol class="m-0 grid list-none gap-4 p-0 sm:grid-cols-2">
              <li class="grid content-start gap-2">
                <span class="text-xs font-semibold uppercase text-muted-foreground">{{ t('platform.login.enroll.step1') }}</span>
                <strong>{{ t('platform.login.enroll.scan') }}</strong>
                <img v-if="qr" :src="qr" :alt="t('platform.login.enroll.qrAlt')" class="size-44 rounded-md border bg-white p-2" data-testid="enroll-qr" />
                <span class="text-sm text-muted-foreground">{{ t('platform.login.enroll.apps') }}</span>
                <div v-if="secret" class="grid gap-1">
                  <span class="text-sm">{{ t('platform.login.enroll.manual') }}</span>
                  <div class="flex flex-wrap items-center gap-2">
                    <code class="rounded bg-muted px-2 py-1 text-sm" data-testid="enroll-secret">{{ secret }}</code>
                    <Button type="button" variant="outline" size="sm" data-testid="copy-secret" @click="copyText(secret, 'secret')">{{ t('platform.login.enroll.copy') }}</Button>
                    <span v-if="secretCopied" class="text-sm text-muted-foreground" role="status">{{ t('platform.login.enroll.copied') }}</span>
                  </div>
                </div>
              </li>
              <li class="grid content-start gap-2">
                <span class="text-xs font-semibold uppercase text-muted-foreground">{{ t('platform.login.enroll.step2') }}</span>
                <strong>{{ t('platform.login.enroll.confirm') }}</strong>
                <form class="grid gap-3" novalidate @submit.prevent="activate">
                  <FormField :label="t('platform.login.enroll.codeLabel')">
                    <template #default="{ id }">
                      <Input :id="id" v-model="form.enrollCode" v-autofocus name="totp" inputmode="numeric" autocomplete="one-time-code" maxlength="8" placeholder="000 000" :disabled="codes.length > 0" />
                    </template>
                  </FormField>
                  <Button type="submit" :disabled="busy || codes.length > 0 || !secret">{{ busy ? t('platform.login.enroll.activating') : t('platform.login.enroll.activate') }}</Button>
                </form>
                <span class="text-sm text-muted-foreground">{{ t('platform.login.enroll.after') }}</span>
                <div v-if="!codes.length">
                  <Button type="button" variant="outline" size="sm" data-testid="back" @click="backToPassword()">{{ t('platform.login.totp.back') }}</Button>
                </div>
              </li>
            </ol>
            <section v-if="codes.length" class="grid gap-3 border-t pt-4" aria-labelledby="rc-title" data-testid="recovery-codes">
              <div>
                <h2 id="rc-title" class="m-0 text-base font-semibold">{{ t('platform.login.enroll.codesTitle') }}</h2>
                <span class="text-sm text-muted-foreground">{{ t('platform.login.enroll.codesHint') }}</span>
              </div>
              <ul class="m-0 grid list-none grid-cols-2 gap-2 p-0 font-mono text-sm">
                <li v-for="c in codes" :key="c" class="rounded bg-muted px-2 py-1">{{ c }}</li>
              </ul>
              <div class="flex flex-wrap items-center gap-2">
                <Button type="button" variant="outline" size="sm" data-testid="download-codes" @click="download">{{ t('platform.login.enroll.download') }}</Button>
                <Button type="button" variant="outline" size="sm" data-testid="copy-codes" @click="copyText(codes.join('\n'), 'codes')">{{ t('platform.login.enroll.copyAll') }}</Button>
                <span v-if="codesCopied" class="text-sm text-muted-foreground" role="status">{{ t('platform.login.enroll.copied') }}</span>
                <Checkbox v-model="saved" name="saved" data-testid="codes-saved">{{ t('platform.login.enroll.saved') }}</Checkbox>
                <Button type="button" :disabled="!saved" data-testid="open-console" @click="openTheConsole">{{ t('platform.login.enroll.open') }}</Button>
              </div>
            </section>
          </div>
        </CardContent>
      </Card>
      <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
        <RouterLink to="/login" class="text-primary underline-offset-2 hover:underline pointer-coarse:inline-flex pointer-coarse:min-h-[44px] pointer-coarse:items-center">{{ t('platform.login.hotelStaff') }}</RouterLink>
        <NativeSelect :model-value="locale" class="w-auto" :aria-label="t('language.label')" @change="void setLocale(($event.target as HTMLSelectElement).value as Locale)">
          <option v-for="l in LOCALES" :key="l" :value="l">{{ t(`language.${l}` as never) }}</option>
        </NativeSelect>
      </div>
    </div>
  </main>
</template>
