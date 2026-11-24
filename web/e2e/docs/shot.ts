import { mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { expect, type Locator, type Page } from 'playwright/test'

/**
 * Screenshots of the user guide (docs/user-guide/screenshots). A name is `[modul]-[tugas]-[langkah]` and a run writes the same names again,
 * so a new run replaces the pictures instead of adding files. Pictures are cut to the part of the screen a step is about (an element or a clip):
 * only the pages that introduce the screen show all of it.
 */
export const SHOTS_DIR = fileURLToPath(new URL('../../../docs/user-guide/screenshots/', import.meta.url))
mkdirSync(SHOTS_DIR, { recursive: true })

/** What is never shown sharp: e-mail addresses in the account menu, guest phone numbers and identity numbers (the data is made up, but the habit is right). */
const BLUR = ['[data-testid=user-email]', 'input[name=phone]', 'input[name=id_number]']

/** Animations and the blinking caret off, the sensitive fields blurred, toasts kept (a step may be about its message). */
const STYLE = `*, *::before, *::after { transition: none !important; animation: none !important; caret-color: transparent !important; }
${BLUR.join(', ')} { filter: blur(4px) !important; }`

export interface ShotOptions {
  /** Room kept around an element, in CSS pixels (default 12). */
  pad?: number
  /** Extra elements to blur in this picture. */
  blur?: string[]
  /** The whole viewport instead of an element (the introduction of a screen). */
  full?: boolean
}

function name(file: string): string {
  if (!/^[a-z0-9]+(-[a-z0-9]+)+$/.test(file)) throw new Error(`screenshot name "${file}" is not [modul]-[tugas]-[langkah] in lower case`)
  return `${SHOTS_DIR}${file}.png`
}

async function quiet(page: Page): Promise<void> {
  await page.waitForLoadState('networkidle', { timeout: 3_000 }).catch(() => { /* the shell polls: never idle for long */ })
  await page.mouse.move(0, 0) // no hover state left on a button
}

/** The height of the sticky top bar of the shell: an element above it is hidden behind the bar. */
const TOP_BAR = 64

/** A picture of the whole window (1440×900). */
export async function shotPage(page: Page, file: string, o: ShotOptions = {}): Promise<void> {
  await quiet(page)
  await page.screenshot({ path: name(file), animations: 'disabled', caret: 'hide', style: STYLE + (o.blur?.length ? `\n${o.blur.join(', ')} { filter: blur(4px) !important; }` : '') })
}

/** A picture of one element and a little room around it (cut to the window). */
export async function shot(target: Locator, file: string, o: ShotOptions = {}): Promise<void> {
  const page = target.page()
  if (o.full) return shotPage(page, file, o)
  await expect(target).toBeVisible()
  await target.scrollIntoViewIfNeeded()
  await quiet(page)
  let box = await target.boundingBox()
  if (!box) throw new Error(`no box for the screenshot ${file}`)
  if (box.y < TOP_BAR) { // under the sticky top bar: bring it below the bar
    const by = box.y - TOP_BAR - 16
    await page.evaluate((d) => window.scrollBy(0, d), by)
    await quiet(page)
    box = await target.boundingBox()
    if (!box) throw new Error(`no box for the screenshot ${file}`)
  }
  const pad = o.pad ?? 12
  const vp = page.viewportSize() ?? { width: 1440, height: 900 }
  const x = Math.max(0, Math.floor(box.x - pad))
  const y = Math.max(0, Math.floor(box.y - pad))
  const clip = { x, y, width: Math.min(vp.width - x, Math.ceil(box.width + 2 * pad)), height: Math.min(vp.height - y, Math.ceil(box.height + 2 * pad)) }
  await page.screenshot({ path: name(file), clip, animations: 'disabled', caret: 'hide', style: STYLE + (o.blur?.length ? `\n${o.blur.join(', ')} { filter: blur(4px) !important; }` : '') })
}

/** A picture of the smallest area holding every element (a bar of the tape chart and the menu it opened), with room around it. */
export async function shotAround(targets: Locator[], file: string, o: ShotOptions = {}): Promise<void> {
  const page = targets[0]!.page()
  await quiet(page)
  const boxes = []
  for (const t of targets) {
    await expect(t).toBeVisible()
    const b = await t.boundingBox()
    if (b) boxes.push(b)
  }
  const pad = o.pad ?? 12
  const vp = page.viewportSize() ?? { width: 1440, height: 900 }
  const x = Math.max(0, Math.floor(Math.min(...boxes.map((b) => b.x)) - pad))
  const y = Math.max(0, Math.floor(Math.min(...boxes.map((b) => b.y)) - pad))
  const right = Math.min(vp.width, Math.ceil(Math.max(...boxes.map((b) => b.x + b.width)) + pad))
  const bottom = Math.min(vp.height, Math.ceil(Math.max(...boxes.map((b) => b.y + b.height)) + pad))
  await page.screenshot({ path: name(file), clip: { x, y, width: right - x, height: bottom - y }, animations: 'disabled', caret: 'hide', style: STYLE })
}
