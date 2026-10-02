import { shallowRef } from 'vue'

export interface ConfirmOptions {
  title: string
  description?: string
  confirmLabel?: string
  cancelLabel?: string
  /** Styles the confirm button as a destructive action (delete, close a period). */
  destructive?: boolean
}

interface Pending {
  options: ConfirmOptions
  resolve: (answer: boolean) => void
}

const pending = shallowRef<Pending | null>(null)
let hosts = 0

/**
 * Asks the person to confirm: `if (!(await confirm({ title: 'Delete the rule?' }))) return`. The answer comes from the
 * dialog the shell shows (ConfirmHost). Without a shell (a page mounted on its own, a test) it falls back to the
 * browser's confirm, so the call always answers.
 */
export function confirm(options: ConfirmOptions): Promise<boolean> {
  if (hosts === 0) return Promise.resolve(window.confirm([options.title, options.description].filter(Boolean).join('\n')))
  pending.value?.resolve(false) // a second question answers the first with "no"
  return new Promise<boolean>((resolve) => {
    pending.value = { options, resolve }
  })
}

/** For the host: the question on screen and how to answer it. */
export function useConfirmState() {
  function answer(value: boolean): void {
    const p = pending.value
    pending.value = null
    p?.resolve(value)
  }
  return { pending, answer }
}

/** The host registers while it is mounted, so `confirm` knows a dialog will answer. */
export function registerConfirmHost(): () => void {
  hosts++
  return () => {
    hosts--
    if (hosts === 0) pending.value?.resolve(false)
    if (hosts === 0) pending.value = null
  }
}
