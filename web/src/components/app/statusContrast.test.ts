import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { STATUS_VARIANTS } from '@/components/ui/badge/statusFill'

/**
 * The colours of the statuses are tokens in tailwind.css; this reads them and keeps every text on its background at 4.5:1 or more (WCAG AA), in the light theme and in the
 * dark one. A colour that is changed later and drops under is a failing test, not a screen that is hard to read.
 */
const css = fs.readFileSync(path.resolve(__dirname, '../../assets/tailwind.css'), 'utf8')

function tokens(selector: string): Record<string, string> {
  const start = css.indexOf(selector)
  expect(start, `${selector} in tailwind.css`).toBeGreaterThan(-1)
  const body = css.slice(start, css.indexOf('\n  }', start))
  return Object.fromEntries([...body.matchAll(/(--[\w-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)].map((m) => [m[1]!, m[2]!.toLowerCase()]))
}

const THEMES: Record<string, Record<string, string>> = { light: tokens(':root {'), dark: tokens(":root[data-theme='dark'] {") }

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)) as [number, number, number]
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}
function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number]
  return (hi + 0.05) / (lo + 0.05)
}

// Which tokens make a status: a light fill has a text, a background and a line; a solid fill has a colour and the text on it.
const SOLID: readonly string[] = ['inspected', 'inhouse', 'ooo']
const PAIRS: { name: string; text: string; on: string }[] = [
  ...STATUS_VARIANTS.filter((v) => !SOLID.includes(v)).map((v) => ({ name: `status ${v}`, text: `--status-${v}`, on: `--status-${v}-bg` })),
  ...SOLID.map((v) => ({ name: `status ${v} (solid)`, text: `--status-${v}-fg`, on: `--status-${v}` })),
  { name: 'out of order, on the lighter stripe', text: '--status-ooo-fg', on: '--status-ooo-stripe' },
  // what the status tokens sit among: text on a card, on the page, the muted text, the text colours of ok / warning / danger, links
  { name: 'text on a card', text: '--text', on: '--surface' },
  { name: 'text on the page', text: '--text', on: '--surface-2' },
  { name: 'muted text on a card', text: '--text-muted', on: '--surface' },
  { name: 'muted text on the page', text: '--text-muted', on: '--surface-2' },
  { name: 'ok text on a card', text: '--ok-text', on: '--surface' },
  { name: 'warning text on a card', text: '--warn-text', on: '--surface' },
  { name: 'danger on a card', text: '--danger', on: '--surface' },
  { name: 'link on a card', text: '--accent-strong', on: '--surface' },
  { name: 'button text on the action colour', text: '--accent-contrast', on: '--accent' },
]

describe('the colours of the statuses', () => {
  for (const [theme, t] of Object.entries(THEMES)) {
    it(`keep every text on its background at 4.5:1 or more in the ${theme} theme`, () => {
      const low: string[] = []
      for (const p of PAIRS) {
        const text = t[p.text]
        const on = t[p.on]
        expect(text, `${p.text} (${p.name}) is defined in the ${theme} theme`).toBeDefined()
        expect(on, `${p.on} (${p.name}) is defined in the ${theme} theme`).toBeDefined()
        const ratio = contrast(text!, on!)
        if (ratio < 4.5) low.push(`${p.name}: ${text} on ${on} is ${ratio.toFixed(2)}:1`)
      }
      expect(low).toEqual([])
    })

    it(`define every status in the ${theme} theme`, () => {
      for (const v of STATUS_VARIANTS) {
        expect(t[`--status-${v}`], `--status-${v}`).toBeDefined()
        expect(t[SOLID.includes(v) ? `--status-${v}-fg` : `--status-${v}-bg`], `the second colour of ${v}`).toBeDefined()
      }
    })

    it(`tell a stay in the house from a confirmed booking by weight and not only by hue (${theme})`, () => {
      // the confirmed bar is close to the card in lightness, the in-house bar is far from it
      const booked = contrast(t['--status-booked-bg']!, t['--surface']!)
      const inhouse = contrast(t['--status-inhouse']!, t['--surface']!)
      expect(Math.abs(inhouse - booked), `booked ${booked.toFixed(2)} vs in house ${inhouse.toFixed(2)}`).toBeGreaterThan(2.5)
    })
  }
})
