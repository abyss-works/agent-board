import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { issueSummary } from '@/api/test-fixtures'
import { IssueRow } from './issue-row'

function render(decisionNeeded: string | null, compact?: boolean, labels: string[] = []): string {
  return renderToStaticMarkup(
    <MemoryRouter>
      <IssueRow issue={issueSummary({ decision_needed: decisionNeeded, labels })} compact={compact} />
    </MemoryRouter>,
  )
}

/** 제목이 든 칸. 좁아지면 줄어드는 칸이라 넘치는 표시를 담으면 옆 항목과 겹친다. */
function titleCell(html: string): string {
  const m = html.match(/<div class="min-w-0 flex-1">([\s\S]*?)<\/div><\/div>/)
  return m ? m[1] : ''
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
  it('결정 필요 표시는 줄어드는 제목 칸 밖에 둔다', () => {
    const html = render('질문', true, ['api'])
    expect(titleCell(html)).not.toContain('결정 필요')
    expect(html).toContain('>결정 필요</span>')
  })
  it('좁은 행은 라벨을 보이지 않아 제목 자리를 남긴다', () => {
    expect(render(null, true, ['api', 'web'])).not.toContain('>api<')
    expect(render(null, false, ['api', 'web'])).toContain('>api<')
  })
})
