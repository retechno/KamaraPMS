<script setup lang="ts">
import { CalendarDays } from 'lucide-vue-next'
import { computed, onMounted, ref, useAttrs, useId, watch, type HTMLAttributes } from 'vue'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { formatDisplayDate, looksLikeWholeDate, maskDateInput, parseDisplayDate } from '@/utils/dates'
import { formatDateWeekdayLong } from '@/utils/format'

/**
 * A date field that is written the same way whatever the language of the browser: dd/mm/yyyy, with the slashes put by a mask, a button for the calendar of
 * the system, and under the field the weekday of the date that was understood ("Senin, 2 Nov 2026"). The value of the model is the ISO date ("2026-11-02") or ''.
 *
 * Two fields are drawn. The one that is seen holds the text, the `id` of the label and the `aria-*`; it has no `name`. The other is a native `<input type="date">`
 * that is only hidden (it is still drawn, out of the tab order and for assistive technology): it holds the `name`, `min`, `max` and the ISO value, it opens the
 * calendar, and a value written into it (a click in the calendar, a test) is the value of the field.
 *
 * The model changes only when the text is a whole, real date inside `min` and `max`, or when it is emptied (''). What is half typed changes nothing and
 * emits nothing; when the field loses the focus with a text that is not a date, the text stays, the field is marked invalid with a message, and the model
 * keeps the last valid date. `change` is emitted when the date was chosen (typed whole or picked), not on every key.
 *
 * What is seen must be what is sent: the text field also carries the reason in its validity (`setCustomValidity`: a text that is not complete, a date that is
 * not on the calendar, a date outside `min` and `max`; '' when it is valid or empty). A form that is about to be sent asks `guardForm` (utils/forms.ts), which
 * stops it and puts the cursor on the first field that is not valid; the field then shows its message (the `invalid` event).
 */
defineOptions({ inheritAttrs: false })
const props = defineProps<{ name?: string; min?: string; max?: string; disabled?: boolean; class?: HTMLAttributes['class'] }>()
const model = defineModel<string | null>()
const emit = defineEmits<{ change: [value: string] }>()
const attrs = useAttrs()

const problemId = useId()
const nativeEl = ref<HTMLInputElement | null>(null)
const textEl = ref<HTMLInputElement | null>(null)
const text = ref(formatDisplayDate(model.value))
type Problem = '' | 'incomplete' | 'notOnCalendar' | 'tooEarly' | 'tooLate'
const problem = ref<Problem>('')
let lastEmitted = model.value ?? ''

// A change that did not come from this field (the page resets its filters, a draft is loaded) rewrites the text.
watch(model, (now) => {
  if ((now ?? '') === lastEmitted) return
  lastEmitted = now ?? ''
  text.value = formatDisplayDate(now)
  problem.value = ''
  applyValidity()
})

function outOfRange(iso: string): Problem {
  if (props.min && iso < props.min) return 'tooEarly'
  if (props.max && iso > props.max) return 'tooLate'
  return ''
}

/** What is wrong with a text: nothing (a real date inside min and max, or empty), a whole date that does not exist, a text that is not a date yet, a date outside the range. */
function problemOf(value: string): Problem {
  if (value === '') return ''
  const iso = parseDisplayDate(value)
  if (iso === null) return looksLikeWholeDate(value) ? 'notOnCalendar' : 'incomplete'
  return outOfRange(iso)
}

function messageOf(p: Problem): string {
  if (p === 'incomplete') return t('dateInput.invalid')
  if (p === 'notOnCalendar') return t('dateInput.notOnCalendar', { text: text.value.trim() })
  if (p === 'tooEarly') return t('dateInput.tooEarly', { date: formatDisplayDate(props.min) })
  if (p === 'tooLate') return t('dateInput.tooLate', { date: formatDisplayDate(props.max) })
  return ''
}

// The state of the text as it is now (it follows min and max too), and what the person has been told about it.
const current = computed(() => problemOf(text.value))
const typedDate = computed(() => parseDisplayDate(text.value))
const invalid = computed(() => problem.value !== '' || attrs['aria-invalid'] === true || attrs['aria-invalid'] === 'true')
const weekday = computed(() => (invalid.value || current.value !== '' || !typedDate.value ? '' : formatDateWeekdayLong(typedDate.value)))
const describedBy = computed(() => [attrs['aria-describedby'], problem.value ? problemId : ''].filter(Boolean).join(' ') || undefined)
const message = computed(() => messageOf(problem.value))

// The validity of the text field says what is not right, whether or not the person has been told yet.
const validityText = computed(() => messageOf(current.value))
function applyValidity(): void {
  textEl.value?.setCustomValidity(validityText.value)
}
onMounted(applyValidity)
watch(validityText, applyValidity, { flush: 'post' })

/** A form that is about to be sent found this field not valid: it says why. */
function onInvalid(): void {
  problem.value = current.value
}

/** Puts a date (or '') in the model; `fire` also says that it was chosen. */
function commit(iso: string, fire: boolean): void {
  if (iso !== (model.value ?? '')) {
    lastEmitted = iso
    model.value = iso
    text.value = formatDisplayDate(iso)
    problem.value = ''
    applyValidity()
    if (fire) emit('change', iso)
  } else if (fire) {
    emit('change', iso)
  }
}

function onTextInput(event: Event): void {
  const field = event.target as HTMLInputElement
  const masked = maskDateInput(field.value)
  if (masked !== field.value) field.value = masked
  text.value = masked
  applyValidity() // at once: a search that starts with this very change must find the field valid
  if (masked === '') {
    problem.value = ''
    commit('', true)
    return
  }
  const found = problemOf(masked)
  // Half typed: no complaint until the field is left. A whole date that does not exist, or is out of range, is said at once.
  problem.value = found === 'incomplete' ? '' : found
  const iso = parseDisplayDate(masked)
  if (iso !== null && !found && iso !== (model.value ?? '')) commit(iso, true)
}

function onTextBlur(): void {
  problem.value = current.value
}

function onNativeInput(): void {
  commit(nativeEl.value?.value ?? '', false)
}
function onNativeChange(): void {
  commit(nativeEl.value?.value ?? '', true)
}

function openCalendar(): void {
  const el = nativeEl.value
  if (!el || props.disabled) return
  try {
    if (typeof el.showPicker !== 'function') throw new Error('showPicker is not available')
    el.showPicker()
  } catch {
    // An older browser, or a call that is not from a click: the field itself answers to a focus and a click.
    el.focus()
    el.click()
  }
}
</script>

<template>
  <div :class="cn('relative min-w-0', props.class)" data-slot="date-input">
    <div class="relative">
      <input
        ref="textEl"
        :placeholder="t('dateInput.placeholder')"
        v-bind="$attrs"
        :value="text"
        type="text"
        inputmode="numeric"
        autocomplete="off"
        maxlength="10"
        :disabled="disabled"
        :aria-invalid="invalid || undefined"
        :aria-describedby="describedBy"
        data-slot="date-input-text"
        class="h-9 w-full min-w-0 rounded-md border border-border bg-card px-3 pr-10 text-sm text-foreground outline-none placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 aria-[invalid=true]:border-destructive pointer-coarse:pr-[52px]"
        @input="onTextInput"
        @blur="onTextBlur"
        @invalid="onInvalid"
      />
      <button
        type="button"
        :disabled="disabled"
        :aria-label="t('dateInput.openCalendar')"
        class="absolute right-0 top-1/2 flex size-9 -translate-y-1/2 cursor-pointer items-center justify-center rounded-md border-0 bg-transparent text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 pointer-coarse:size-[44px]"
        data-slot="date-input-calendar"
        @click="openCalendar"
      >
        <CalendarDays class="size-4" aria-hidden="true" />
      </button>
      <input
        ref="nativeEl"
        type="date"
        :name="name"
        :min="min || undefined"
        :max="max || undefined"
        :value="model ?? ''"
        :disabled="disabled"
        tabindex="-1"
        aria-hidden="true"
        data-slot="date-input-native"
        class="pointer-events-none absolute bottom-0 right-0 h-9 w-9 opacity-0"
        @input="onNativeInput"
        @change="onNativeChange"
      />
    </div>
    <p v-if="message" :id="problemId" class="absolute left-0 top-full m-0 mt-0.5 whitespace-nowrap text-xs text-destructive" role="alert" data-slot="date-input-problem">{{ message }}</p>
    <p v-else-if="weekday" class="absolute left-0 top-full m-0 mt-0.5 whitespace-nowrap text-xs text-muted-foreground" data-slot="date-input-weekday">{{ weekday }}</p>
  </div>
</template>
