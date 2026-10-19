import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { issueSummary } from '@/api/test-fixtures'
import { IssueRow } from './issue-row'

function render(decisionNeeded: string | null, compact?: boolean): string {
  return renderToStaticMarkup(
    <MemoryRouter>
      <IssueRow issue={issueSummary({ decision_needed: decisionNeeded })} compact={compact} />
    </MemoryRouter>,
  )
}

describe('IssueRow의 결정 필요 표시', () => {
  it('결정 필요가 있으면 표시를 달고 질문을 설명으로 보인다', () => {
    const html = render('캐시를 Redis로 할지')
    expect(html).toContain('>결정 필요</span>')
    expect(html).toContain('캐시를 Redis로 할지')
  })
  it('결정 필요가 없으면 표시가 없다', () => {
    expect(render(null)).not.toContain('결정 필요')
  })
  it('좁은 행에도 같은 표시를 단다', () => {
    expect(render('질문', true)).toContain('>결정 필요</span>')
    expect(render(null, true)).not.toContain('결정 필요')
  })
})
