import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * Text is 12 px at the least. `text-[10px]` and `text-[11px]` were used for chips, table heads and sub-labels; they are 12 px now (`text-[12px]`: the root font is 15 px, so `text-xs` is 11.25 px).
 * The only small text left is the axis of a chart that is `aria-hidden` (read by nobody, it only draws the scale). A new place needs a reason in this list.
 */
const AXES = new Set(['components/app/NightsChart.vue', 'views/dashboard/PerformanceView.vue'])
const root = join(__dirname) // web/src

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return name === 'node_modules' ? [] : sources(path)
    return /\.(vue|ts)$/.test(name) && !name.endsWith('.test.ts') ? [path] : []
  })
}

describe('the smallest text', () => {
  it('is 12 px, but on the aria-hidden axes of a chart', () => {
    const found = new Set<string>()
    for (const file of sources(root)) {
      if (/text-\[1[01]px\]/.test(readFileSync(file, 'utf8'))) found.add(relative(root, file).split(sep).join('/'))
    }
    expect([...found].sort()).toEqual([...AXES].sort())
  })

  it('keeps the small text of the axes aria-hidden', () => {
    const nights = readFileSync(join(root, 'components/app/NightsChart.vue'), 'utf8')
    expect(nights).toMatch(/text-\[10px\][^"]*"\s+aria-hidden="true"/)
    const performance = readFileSync(join(root, 'views/dashboard/PerformanceView.vue'), 'utf8')
    expect(performance).toMatch(/aria-hidden="true">\s*<span class="block h-0.5[^\n]*\n\s*<small class="mt-1 text-\[10px\]/)
  })

  it('makes the badge 12 px', () => {
    expect(readFileSync(join(root, 'components/ui/badge/index.ts'), 'utf8')).toContain('text-[12px] font-medium')
  })
})
