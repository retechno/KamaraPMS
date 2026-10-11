import { onBeforeUnmount } from 'vue'

/**
 * Runs `reload` by itself while a page is open: every `everyMs`, and when the tab comes back to the front. It does nothing while the tab is hidden, while `paused()` says a sheet or a dialog
 * is open (what a person is filling in must not move under their hands), or while `busy()` says a read is still on its way, so a slow server is never asked twice at once.
 * The timer and the listener go when the page is closed. The first read is the page's own (a watch that runs at once); this only repeats it.
 */
export function useAutoRefresh(reload: () => void, options: { everyMs: number; paused?: () => boolean; busy?: () => boolean }): { tick: () => void } {
  const { everyMs, paused = () => false, busy = () => false } = options

  function tick(): void {
    if (paused() || busy() || (typeof document !== 'undefined' && document.hidden)) return
    reload()
  }
  function visibility(): void {
    if (typeof document !== 'undefined' && document.visibilityState === 'visible') tick()
  }
  const timer = setInterval(tick, everyMs)
  document.addEventListener('visibilitychange', visibility)
  onBeforeUnmount(() => {
    clearInterval(timer)
    document.removeEventListener('visibilitychange', visibility)
  })
  return { tick }
}
