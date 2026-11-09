import { readFileSync } from 'node:fs'
import { test, expect } from '../support/fixtures'

/**
 * SMOKE-01..: every page without an id in its address opens for the administrator: the page is not sent home (no missing permission or module),
 * it shows no "no access" block, and nothing in it throws or gets a 5xx from the API (pageGuard).
 * The list is read from src/router/index.ts, so a new page is covered without touching this file.
 */
const source = readFileSync(new URL('../../src/router/index.ts', import.meta.url), 'utf8')
const routes = [...new Set([...source.matchAll(/path: '(\/[^']*)'/g)].map((m) => m[1]!))]
  .filter((p) => !p.includes('/:') && p !== '/login')

// Grouped by the first part of the address: one sign-in per group, one step per page.
const groups = new Map<string, string[]>()
for (const r of routes) {
  const key = r === '/' ? 'home' : r.split('/')[1]!
  groups.set(key, [...(groups.get(key) ?? []), r])
}

test.describe('Smoke: every page opens', () => {
  for (const [group, paths] of groups) {
    test(`SMOKE ${group} (${paths.length} pages)`, async ({ page, loginAs }) => {
      test.setTimeout(30_000 + paths.length * 15_000)
      await loginAs('admin')
      for (const path of paths) {
        await test.step(path, async () => {
          await page.goto(path)
          await expect(page.locator('main#main')).toBeVisible()
          await page.waitForLoadState('networkidle', { timeout: 5_000 }).catch(() => { /* a page that polls (KDS, night audit) never goes idle */ })
          expect.soft(new URL(page.url()).pathname, `${path} was sent elsewhere (permission or module)`).toBe(path)
          await expect.soft(page.getByTestId('no-access'), `${path} says no access`).toHaveCount(0)
        })
      }
    })
  }
})
