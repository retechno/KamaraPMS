import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { en } from '@/i18n/locales/en'
import { id } from '@/i18n/locales/id'

/**
 * The shape every page shares. These tests read the source of the router and of the views: a page that is added later with its own kind of header, or a list that has nothing
 * to say when it is empty, fails here and not in a review of screenshots.
 */
const SRC = path.resolve(__dirname, '..')
const read = (file: string): string => fs.readFileSync(path.join(SRC, file), 'utf8').replace(/\r\n/g, '\n')

function walk(dir: string): string[] {
  return fs.readdirSync(path.join(SRC, dir), { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? walk(`${dir}/${e.name}`) : e.name.endsWith('.vue') ? [`${dir}/${e.name}`] : []))
}

/** The view of every route that is behind the sign-in: what the router imports, less the sign-in page. */
function routedViews(): string[] {
  const router = read('router/index.ts')
  const files = new Set<string>()
  for (const m of router.matchAll(/(?:import\(|from )'@\/(views\/[^']+\.vue)'/g)) files.add(m[1]!)
  files.delete('views/LoginView.vue') // public: it is not inside the app's layout, so it has no page header
  return [...files].sort()
}

describe('the header of a page', () => {
  const views = routedViews()

  it('finds the views of the routes', () => {
    expect(views.length).toBeGreaterThan(70)
    expect(views).toContain('views/HomeView.vue')
    expect(views).toContain('views/NotFoundView.vue')
  })

  it('is one PageHeader in every page of a route, no more and no fewer', () => {
    const wrong = views.map((v) => [v, (read(v).match(/<PageHeader\b/g) ?? []).length] as const).filter(([, n]) => n !== 1)
    expect(wrong).toEqual([])
  })

  it('has no <h1> of its own: the title is the PageHeader\'s', () => {
    expect(views.filter((v) => /<h1\b/.test(read(v)))).toEqual([])
  })

  it('has actions that are buttons, not underlined links', () => {
    const bad: string[] = []
    for (const v of walk('views')) {
      const s = read(v)
      for (const m of s.matchAll(/<PageHeader\b[\s\S]*?<\/PageHeader>/g)) {
        for (const link of m[0].matchAll(/<RouterLink\b[^>]*class="[^"]*(?:hover:underline|underline)[^"]*"/g)) bad.push(`${v}: ${link[0].slice(0, 80)}`)
      }
    }
    expect(bad).toEqual([])
  })

  it('has a link in its actions only as a button, and not a ghost one (a ghost button over a link is read as a green text link)', () => {
    const bare: string[] = []
    for (const v of walk('views')) {
      const s = read(v)
      for (const m of s.matchAll(/<PageHeader\b[\s\S]*?<\/PageHeader>/g)) {
        const header = m[0]
        for (const link of header.matchAll(/<RouterLink\b/g)) {
          const before = header.slice(Math.max(0, link.index! - 160), link.index)
          const wrapped = /<Button\b[^>]*\bas-child\b[^>]*>\s*$/.test(before) || /custom/.test(header.slice(link.index!, link.index! + 80))
          if (!wrapped) bare.push(`${v}: a RouterLink that is not a Button`)
        }
        for (const ghost of header.matchAll(/<Button\b[^>]*as-child[^>]*variant="ghost"|<Button\b[^>]*variant="ghost"[^>]*as-child/g)) bare.push(`${v}: ${ghost[0].slice(0, 70)}`)
      }
    }
    expect(bare).toEqual([])
  })
})

describe('an empty state', () => {
  const files = [...walk('views'), ...walk('components')]
  const tags = files.flatMap((f) => [...read(f).matchAll(/<EmptyState\b[^>]*?\/>|<EmptyState\b[^>]*?>/g)].map((m) => ({ file: f, tag: m[0] })))

  it('is found in the lists', () => {
    expect(tags.length).toBeGreaterThan(50)
  })

  it('always says why, in a line of its own', () => {
    const bare = tags.filter(({ tag }) => !/\bdescription=/.test(tag)).map(({ file, tag }) => `${file}: ${tag.slice(0, 100)}`)
    expect(bare).toEqual([])
  })

  it('has a text in English and in Indonesian for every hint of emptyState', () => {
    const used = new Set(files.flatMap((f) => [...read(f).matchAll(/emptyState\.(\w+)/g)].map((m) => m[1]!)))
    expect(used.size).toBeGreaterThan(40)
    const missing = [...used].filter((k) => !(k in en.emptyState) || !(k in id.emptyState))
    expect(missing).toEqual([])
  })

  it('offers an action only through the action props, with the permission of the add button it repeats', () => {
    // the action label of an add button is the one of the button of the header, so the two cannot drift apart: a label that is conditional carries its condition
    const loose = tags.filter(({ tag }) => /action-label="t\(/.test(tag) && !/action-to=/.test(tag) && !/@action=/.test(tag)).map(({ file }) => file)
    expect(loose).toEqual([])
  })
})
