import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { ApiClient } from '@/api/client'
import { issueSummary } from '@/api/test-fixtures'
import type { DoingItem, IssueSummary, Overview, Section } from '@/api/types'
import { ApiContext, AppStateContext, type AppState } from '@/app/state'
import { OverviewPage } from './overview-page'

const INSTANCE = { id: 'test', label: 'test', baseUrl: '', description: 'test' }

const state: AppState = {
  instances: [INSTANCE],
  instance: INSTANCE,
  token: 'token',
  project: 'AGB',
  sessionId: 'session',
  authNotice: null,
  selectInstance: () => {},
  login: () => {},
  logout: () => {},
  selectProject: () => {},
}

// 캐시에 채워 둔 현황만 그리므로 요청이 나가면 시험이 실패한다.
const api = new ApiClient({ baseUrl: '', token: 'token', actor: 'owner', sessionId: 'session', clientHost: 'web' }, () => {
  throw new Error('현황 화면이 요청을 보냈습니다.')
})

function section<T>(items: T[]): Section<T> {
  return { total: items.length, items }
}

function overview(partial: Partial<Overview> = {}): Overview {
  return {
    project: { key: 'AGB', name: 'agent-board', status: 'active', created_at: '2026-10-17T00:00:00.000Z', updated_at: '2026-10-17T00:00:00.000Z', created_by: 'owner', updated_by: 'owner' },
    generated_at: '2026-10-17T07:10:21.000Z',
    counts: {
      total: 0,
      open: 0,
      by_status: { backlog: 0, doing: 0, review: 0, done: 0, cancelled: 0, deleted: 0 },
      by_type: { feature: 0, bug: 0, design: 0, idea: 0, observation: 0 },
      by_priority: { P0: 0, P1: 0, P2: 0, P3: 0 },
    },
    doing: section<DoingItem>([]),
    review: section<IssueSummary>([]),
    blocked: section([]),
    decisions: section<IssueSummary>([]),
    next: section([]),
    unacknowledged: section<IssueSummary>([]),
    recent_activity: section([]),
    ...partial,
  }
}

function render(o: Overview): string {
  const client = new QueryClient()
  client.setQueryData([INSTANCE.id, 'overview', 'AGB'], o)
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <AppStateContext.Provider value={state}>
        <ApiContext.Provider value={api}>
          <MemoryRouter>
            <OverviewPage />
          </MemoryRouter>
        </ApiContext.Provider>
      </AppStateContext.Provider>
    </QueryClientProvider>,
  )
}

/** 제목이 title인 칸의 마크업. 칸은 서로 겹치지 않는다. */
function sectionOf(html: string, title: string): string {
  const found = html.split('<section').find((s) => s.includes(`>${title}</h3>`))
  if (!found) throw new Error(`${title} 칸이 없습니다.`)
  return found.split('</section>')[0]
}

const asked = issueSummary({ key: 'AGB-7', number: 7, title: '캐시 도입', decision_needed: 'Redis를 쓸지 정해 주세요' })
function held(summary: IssueSummary, actor: string, lastActiveAt: string, stale: boolean): DoingItem {
  return { ...summary, holder: { actor, session_id: `${actor}-s`, last_active_at: lastActiveAt, stale } }
}

const working = held(issueSummary({ key: 'AGB-8', number: 8, title: '검색 개선', status: 'doing' }), 'claude', '2026-10-17T07:00:00.000Z', false)

describe('현황의 결정 필요', () => {
  const html = render(overview({ decisions: section([asked]), doing: section([working]) }))

  it('칸과 요약 카드의 이름이 결정 필요다', () => {
    expect(sectionOf(html, '결정 필요')).toContain('AGB-7')
    expect(html).toContain('>결정 필요</div>')
    expect(html).not.toContain('결정 대기')
  })
  it('칸에는 decisions의 이슈만 담기고 질문이 표시로 달린다', () => {
    const decisions = sectionOf(html, '결정 필요')
    expect(decisions).toContain('캐시 도입')
    expect(decisions).toContain('Redis를 쓸지 정해 주세요')
    expect(decisions).not.toContain('검색 개선')
    expect(sectionOf(html, '진행 중')).toContain('검색 개선')
  })
  it('없는 종류 필터로 잇는 전체 보기 링크를 만들지 않는다', () => {
    expect(sectionOf(html, '결정 필요')).not.toContain('전체 보기')
    expect(html).not.toContain('type=decision')
  })
  it('결정을 기다리는 이슈가 없으면 비어 있다고 보인다', () => {
    const empty = sectionOf(render(overview()), '결정 필요')
    expect(empty).toContain('없음')
    expect(empty).not.toContain('AGB-')
  })
})

describe('현황의 다음 할 일', () => {
  it('전체 보기는 backlog 목록으로 잇는다', () => {
    const html = render(overview())
    expect(sectionOf(html, '다음 할 일')).toContain('href="/issues?status=backlog"')
    expect(html).not.toContain('status=todo')
  })
})

describe('현황의 진행 중', () => {
  const quiet = held(issueSummary({ key: 'AGB-9', number: 9, title: '오래된 작업', status: 'doing' }), 'codex', '2026-10-17T03:00:00.000Z', true)
  const second = held(issueSummary({ key: 'AGB-10', number: 10, title: '두 번째 작업', status: 'doing' }), 'claude', '2026-10-17T07:00:00.000Z', false)
  const doing = sectionOf(render(overview({ doing: section([working, quiet, second]) })), '진행 중')

  it('점유한 행위자별로 묶고 마지막 활동 시각을 보인다', () => {
    expect(doing.match(/>claude</g)).toHaveLength(1)
    expect(doing).toContain('>codex<')
    expect(doing.indexOf('검색 개선')).toBeLessThan(doing.indexOf('두 번째 작업'))
    expect(doing.indexOf('두 번째 작업')).toBeLessThan(doing.indexOf('codex'))
    expect(doing).toContain('16:00')
  })
  it('멈춘 행위자에만 멈춤 표시를 붙인다', () => {
    expect(doing.match(/멈춤/g)).toHaveLength(1)
    expect(doing.indexOf('멈춤')).toBeGreaterThan(doing.indexOf('codex'))
  })
})
