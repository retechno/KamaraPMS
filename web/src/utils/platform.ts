/** Whether the keyboard has ⌘ (an Apple computer) rather than Ctrl. */
export function isApplePlatform(nav: Pick<Navigator, 'platform' | 'userAgent'> | undefined = typeof navigator === 'undefined' ? undefined : navigator): boolean {
  if (!nav) return false
  return /Mac|iPhone|iPad|iPod/i.test(nav.platform || nav.userAgent || '')
}

/** The hint for the shortcut of the quick finder: "⌘K" on a Mac, "Ctrl K" on the others. */
export function finderShortcut(nav?: Pick<Navigator, 'platform' | 'userAgent'>): string {
  return isApplePlatform(nav) ? '⌘K' : 'Ctrl K'
}
