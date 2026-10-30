import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { parse } from '@vue/compiler-sfc'
import { NodeTypes, type ElementNode, type TemplateChildNode } from '@vue/compiler-dom'
import { describe, expect, it } from 'vitest'

// PageHeader renders only its named slots (marks, actions): whatever a page puts in the default slot is dropped.
// This reads every view and lists the pages that put a child anywhere else.

const root = join(__dirname, '..', '..', 'views')

function vueFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return vueFiles(path)
    return name.endsWith('.vue') ? [path] : []
  })
}

function slotOf(node: ElementNode): string | null {
  for (const p of node.props) {
    if (p.type === NodeTypes.DIRECTIVE && p.name === 'slot') {
      return p.arg && p.arg.type === NodeTypes.SIMPLE_EXPRESSION ? p.arg.content : 'default'
    }
  }
  return null
}

function headers(nodes: TemplateChildNode[], out: ElementNode[]): void {
  for (const n of nodes) {
    if (n.type !== NodeTypes.ELEMENT) {
      if (n.type === NodeTypes.IF) for (const b of n.branches) headers(b.children, out)
      if (n.type === NodeTypes.FOR) headers(n.children, out)
      continue
    }
    if (n.tag === 'PageHeader') out.push(n)
    headers(n.children, out)
  }
}

function strays(file: string): string[] {
  const { descriptor } = parse(readFileSync(file, 'utf8'), { filename: file })
  const ast = descriptor.template?.ast
  if (!ast) return []
  const found: ElementNode[] = []
  headers(ast.children, found)
  const bad: string[] = []
  for (const header of found) {
    for (const child of header.children) {
      if (child.type === NodeTypes.COMMENT) continue
      if (child.type === NodeTypes.TEXT && child.content.trim() === '') continue
      if (child.type === NodeTypes.ELEMENT && child.tag === 'template') {
        const slot = slotOf(child)
        if (slot === 'actions' || slot === 'marks') continue
      }
      bad.push(`${relative(root, file).replaceAll('\\', '/')}:${child.loc.start.line}`)
    }
  }
  return bad
}

describe('PageHeader slots', () => {
  it('has only #actions and #marks templates as children in every view', () => {
    const files = vueFiles(root)
    expect(files.length).toBeGreaterThan(100)
    expect(files.flatMap(strays)).toEqual([])
  })
})
