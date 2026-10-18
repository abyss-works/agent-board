import type { Template } from '@/api/types'

/** 템플릿 모드의 입력 칸 하나. 템플릿 본문의 `## 제목` 하나에 대응한다. */
export interface TemplateField {
  heading: string
  /** 그 제목 아래 안내 문장. 칸의 placeholder로 쓴다. */
  hint: string
}

/** 칸에 쓴 내용. 제목을 키로 한다. 없는 키는 빈 칸이다. */
export type FieldValues = Record<string, string>

export type ParseResult = { ok: true; values: FieldValues } | { ok: false; reason: string }

const HEADING = /^\s*##\s+(.+?)\s*$/
const FENCE = /^\s*(```|~~~)/

function lines(text: string): string[] {
  return text.replace(/\r\n/g, '\n').split('\n')
}

/** 템플릿 본문을 칸으로 나눈다. `## 제목` 줄마다 칸이 하나이고, 다음 제목 전까지의 글이 안내 문장이다. */
export function templateFields(tpl: Pick<Template, 'body'>): TemplateField[] {
  const fields: { heading: string; hint: string[] }[] = []
  for (const line of lines(tpl.body)) {
    const m = HEADING.exec(line)
    if (m) fields.push({ heading: m[1], hint: [] })
    else fields.at(-1)?.hint.push(line)
  }
  return fields.map((f) => ({ heading: f.heading, hint: f.hint.join('\n').trim() }))
}

/** 쓴 내용이 한 칸이라도 있는지. 공백뿐인 칸은 비었다고 본다. */
export function hasContent(values: FieldValues): boolean {
  return Object.values(values).some((v) => v.trim() !== '')
}

/**
 * 칸의 내용을 `## 제목\n내용` 형태로 합쳐 본문을 만든다. 빈 칸도 제목은 남겨 구조를 지키고,
 * 모든 칸이 비었으면 본문을 비운다.
 */
export function composeBody(fields: readonly TemplateField[], values: FieldValues): string {
  if (!hasContent(values)) return ''
  const sections = fields.map((f) => {
    const value = (values[f.heading] ?? '').trim()
    return value ? `## ${f.heading}\n${value}` : `## ${f.heading}`
  })
  return `${sections.join('\n\n')}\n`
}

/** 템플릿 모드에서 마크다운 모드로 갈 때의 본문. 쓴 내용이 없으면 안내가 담긴 템플릿 본문을 그대로 쓴다. */
export function toMarkdown(tpl: Pick<Template, 'body'>, fields: readonly TemplateField[], values: FieldValues): string {
  return composeBody(fields, values) || tpl.body
}

/**
 * 마크다운 본문을 `## 제목`으로 나눠 칸에 넣는다. 템플릿에 없는 제목, 같은 제목의 반복, 첫 제목 앞의 글이 있으면
 * 칸에 옮길 수 없으므로 이유를 돌려준다. 코드 블록 안의 `## ` 줄은 제목이 아니다.
 * 손대지 않은 안내 문장은 빈 칸으로 본다.
 */
export function parseBody(fields: readonly TemplateField[], markdown: string): ParseResult {
  const known = new Map(fields.map((f) => [f.heading, f]))
  const values: FieldValues = {}
  const preamble: string[] = []
  let current: string | null = null
  let buffer: string[] = []
  let inFence = false

  const flush = () => {
    if (current !== null) values[current] = buffer.join('\n').trim()
    buffer = []
  }

  for (const line of lines(markdown)) {
    const fenceLine = FENCE.test(line)
    const m = !inFence && !fenceLine ? HEADING.exec(line) : null
    if (m) {
      const heading = m[1]
      if (!known.has(heading)) return { ok: false, reason: `템플릿에 없는 제목이 있습니다: ## ${heading}` }
      if (heading in values || heading === current) return { ok: false, reason: `같은 제목이 두 번 있습니다: ## ${heading}` }
      flush()
      current = heading
      continue
    }
    if (fenceLine) inFence = !inFence
    if (current === null) preamble.push(line)
    else buffer.push(line)
  }
  flush()

  if (preamble.join('\n').trim() !== '') return { ok: false, reason: '첫 제목 앞에 내용이 있습니다.' }
  const result: FieldValues = {}
  for (const f of fields) {
    const value = values[f.heading] ?? ''
    result[f.heading] = value === f.hint.trim() ? '' : value
  }
  return { ok: true, values: result }
}
