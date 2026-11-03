import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { parse } from '@vue/compiler-sfc'
import { NodeTypes, type ElementNode, type TemplateChildNode } from '@vue/compiler-dom'
import { describe, expect, it } from 'vitest'

/**
 * A `Checkbox` with no text is a box with no name. The type of a component cannot see its slot, so this reads every template that uses one: a Checkbox with nothing inside it
 * needs an `aria-label` or `aria-labelledby` (static or bound).
 */
const root = join(__dirname, '..', '..', '..') // web/src

function vueFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return name === 'node_modules' ? [] : vueFiles(path)
    return name.endsWith('.vue') ? [path] : []
  })
}

const named = (el: ElementNode): boolean =>
  el.props.some((p) => (p.type === NodeTypes.ATTRIBUTE && (p.name === 'aria-label' || p.name === 'aria-labelledby')) || (p.type === NodeTypes.DIRECTIVE && p.name === 'bind' && p.arg?.type === NodeTypes.SIMPLE_EXPRESSION && (p.arg.content === 'aria-label' || p.arg.content === 'aria-labelledby')))

const hasText = (el: ElementNode): boolean => el.children.some((c) => !(c.type === NodeTypes.COMMENT || (c.type === NodeTypes.TEXT && c.content.trim() === '')))

function checkboxes(nodes: TemplateChildNode[], out: ElementNode[]): void {
  for (const n of nodes) {
    if (n.type === NodeTypes.IF) for (const b of n.branches) checkboxes(b.children, out)
    else if (n.type === NodeTypes.FOR) checkboxes(n.children, out)
    else if (n.type === NodeTypes.ELEMENT) {
      if (n.tag === 'Checkbox') out.push(n)
      checkboxes(n.children, out)
    }
  }
}

/** The places in a template where a Checkbox with no text has no name ("file:line"). */
function unnamedBoxes(source: string, file = 'x.vue'): string[] {
  const ast = parse(source, { filename: file }).descriptor.template?.ast
  if (!ast) return []
  const found: ElementNode[] = []
  checkboxes(ast.children, found)
  return found.filter((el) => !hasText(el) && !named(el)).map((el) => `${file}:${el.loc.start.line}`)
}

describe('Checkbox usage', () => {
  it('sees a box with no text and no name, and not one that has either', () => {
    expect(unnamedBoxes('<template><Checkbox v-model="a" /></template>')).toEqual(['x.vue:1'])
    expect(unnamedBoxes('<template><div>\n<Checkbox v-model="a"></Checkbox></div></template>')).toEqual(['x.vue:2'])
    expect(unnamedBoxes('<template><Checkbox v-model="a" aria-label="Pick" /></template>')).toEqual([])
    expect(unnamedBoxes('<template><Checkbox v-model="a" :aria-label="t(\'x\')" /></template>')).toEqual([])
    expect(unnamedBoxes('<template><Checkbox v-model="a" aria-labelledby="h" /></template>')).toEqual([])
    expect(unnamedBoxes('<template><Checkbox v-model="a">Agree</Checkbox></template>')).toEqual([])
    expect(unnamedBoxes('<template><tr v-for="r in rows"><td><Checkbox v-if="x" v-model="a" /></td></tr></template>')).toEqual(['x.vue:1'])
  })

  it('has a name for every Checkbox of the pages that has no text', () => {
    const bad = vueFiles(root).flatMap((file) => {
      const text = readFileSync(file, 'utf8')
      return text.includes('<Checkbox') ? unnamedBoxes(text, relative(root, file).replaceAll('\\', '/')) : []
    })
    expect(bad, 'A Checkbox with no text needs an aria-label (a box has no name of its own).').toEqual([])
  }, 60000)
})
