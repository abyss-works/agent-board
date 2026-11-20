import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { ApiClient } from '@/api/client'
import { QUERY_ROOT, agendaQueryKey } from '@/api/queries'
import { decisionItem, issueSummary, sessionFixture } from '@/api/test-fixtures'
import type { Agenda, DecisionItem, DoingItem, IssueSummary, Overview, Section, Session } from '@/api/types'
import { ApiContext, AppStateContext, type AppState } from '@/app/state'
import { OverviewPage } from './overview-page'

const state: AppState = {
  token: 'token',
  actor: 'owner',
  project: 'AGB',
  sessionId: 'session',
  authNotice: null,
  login: () => {},
  logout: () => {},
  selectActor: () => {},
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
    decisions: section<DecisionItem>([]),
    next: section([]),
    unacknowledged: section<IssueSummary>([]),
    recent_activity: section([]),
    ...partial,
  }
}

function agendaOf(o: Overview, waitingSessions: Section<Session>): Agenda {
  return {
    generated_at: o.generated_at,
    projects: [
      {
        project: o.project,
        dormant: 0,
        user_turn: {
          review: o.review,
          decisions: o.decisions,
          unacknowledged: o.unacknowledged,
          tasks: section([]),
          waiting_sessions: waitingSessions,
        },
        agent: {
          doing: o.doing,
          next: o.next,
        },
        waiting: {
          blocked: o.blocked,
          external: section([]),
        },
      },
    ],
  }
}

function render(o: Overview, waitingSessions?: Section<Session>): string {
  const client = new QueryClient()
  client.setQueryData([QUERY_ROOT, 'overview', 'AGB'], o)
  if (waitingSessions !== undefined) {
    client.setQueryData(agendaQueryKey('AGB'), agendaOf(o, waitingSessions))
  }
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

const asked = decisionItem({ key: 'AGB-7', number: 7, title: '캐시 도입', decision_needed: 'Redis를 쓸지 정해 주세요' })
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
  it('대기 세션이 없는 결정 항목은 세션 링크나 충돌 배지 없이 깔끔히 그린다', () => {
    const decisions = sectionOf(html, '결정 필요')
    expect(decisions).not.toContain('/sessions?')
    expect(decisions).not.toContain('위치 충돌')
  })
  it('연결된 대기 세션이 1건이면 이슈 링크 밖에 세션 링크, 행위자, 대기 상태, 사유를 그린다', () => {
    const waiting = sessionFixture({
      actor: 'agent-a',
      session_id: 'sess-decide-1',
      issue: 'AGB-7',
      state: 'waiting_approval',
      state_reason: 'Redis 클러스터 구성 승인을 기다립니다',
    })
    const item = decisionItem({
      key: 'AGB-7',
      number: 7,
      title: '캐시 도입',
      decision_needed: 'Redis를 쓸지 정해 주세요',
      waiting_sessions: [waiting],
    })
    const decisions = sectionOf(render(overview({ decisions: section([item]) })), '결정 필요')
    expect(decisions).toContain('Redis를 쓸지 정해 주세요')
    expect(decisions).toContain('agent-a')
    expect(decisions).toContain('sess-decide-1')
    expect(decisions).toContain('승인 대기')
    expect(decisions).toContain('Redis 클러스터 구성 승인을 기다립니다')
    expect(decisions).toContain('href="/sessions?actor=agent-a&amp;session=sess-decide-1"')

    const issueLinkStart = decisions.indexOf('href="/issues/AGB-7"')
    const issueLinkEnd = decisions.indexOf('</a>', issueLinkStart)
    const sessionLinkStart = decisions.indexOf('href="/sessions?actor=agent-a&amp;session=sess-decide-1"')
    expect(issueLinkStart).toBeGreaterThanOrEqual(0)
    expect(issueLinkEnd).toBeGreaterThan(issueLinkStart)
    expect(decisions.slice(issueLinkStart, issueLinkEnd)).not.toContain('/sessions?')
    expect(sessionLinkStart).toBeGreaterThan(issueLinkEnd)
    expect(decisions).not.toMatch(/<a\b[^>]*>(?:(?!<\/a>)[\s\S])*<a\b/)
  })
  it('대기 세션이 여럿이고 위치 충돌이 있으면 모두 형제 링크와 배지로 표시한다', () => {
    const first = sessionFixture({
      actor: 'agent-a',
      session_id: 'sess-first',
      issue: 'AGB-7',
      state: 'waiting_input',
      state_reason: '직렬화 형식 선택 필요',
      location_conflicts: [{ actor: 'agent-c', session_id: 'sess-other' }],
    })
    const second = sessionFixture({
      actor: 'agent-b',
      session_id: 'sess-second',
      issue: 'AGB-7',
      state: 'waiting_external',
      state_reason: null,
      location_conflicts: [],
    })
    const third = sessionFixture({
      actor: 'agent-c',
      session_id: 'sess-third',
      issue: 'AGB-7',
      state: null,
      state_reason: null,
    })
    const item = decisionItem({
      key: 'AGB-7',
      number: 7,
      title: '캐시 도입',
      decision_needed: 'Redis를 쓸지 정해 주세요',
      waiting_sessions: [first, second, third],
    })
    const decisions = sectionOf(render(overview({ decisions: section([item]) })), '결정 필요')
    expect(decisions).toContain('href="/sessions?actor=agent-a&amp;session=sess-first"')
    expect(decisions).toContain('href="/sessions?actor=agent-b&amp;session=sess-second"')
    expect(decisions).toContain('href="/sessions?actor=agent-c&amp;session=sess-third"')
    expect(decisions).toContain('입력 대기')
    expect(decisions).toContain('직렬화 형식 선택 필요')
    expect(decisions).toContain('외부 대기')
    expect(decisions).toContain('위치 충돌 1')
    expect(decisions).toContain('agent-c/sess-other')
    expect(decisions.indexOf('sess-first')).toBeLessThan(decisions.indexOf('sess-second'))
    expect(decisions.indexOf('sess-second')).toBeLessThan(decisions.indexOf('sess-third'))
    expect(decisions).not.toMatch(/<a\b[^>]*>(?:(?!<\/a>)[\s\S])*<a\b/)
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

describe('현황의 대기 세션', () => {
  it('아젠다 waiting_sessions를 state_at 순서와 total 기준으로 표시하고 issue 귀속 세션도 누락하지 않는다', () => {
    const earlierByIssue = sessionFixture({
      actor: 'agent-a',
      session_id: 'sess-wait-earlier',
      last_project: null,
      issue: 'APP-12',
      state: 'waiting_input',
      state_reason: '옵션 확인이 필요합니다',
      state_at: '2026-10-17T07:01:00.000Z',
    })
    const laterOtherLastProject = sessionFixture({
      actor: 'agent-b',
      session_id: 'sess-wait-later',
      last_project: 'OTHER',
      issue: 'APP-15',
      state: 'waiting_approval',
      state_reason: '배포 승인이 필요합니다',
      state_at: '2026-10-17T07:05:00.000Z',
    })
    const html = render(overview(), { total: 4, items: [earlierByIssue, laterOtherLastProject] })
    const card = sectionOf(html, '대기 세션')
    expect(card).toContain('>4</span>')
    expect(card).toContain('agent-a')
    expect(card).toContain('sess-wait-earlier')
    expect(card).toContain('입력 대기')
    expect(card).toContain('옵션 확인이 필요합니다')
    expect(card).toContain('href="/issues/APP-12"')
    expect(card).toContain('agent-b')
    expect(card).toContain('sess-wait-later')
    expect(card).toContain('승인 대기')
    expect(card).toContain('배포 승인이 필요합니다')
    expect(card).toContain('href="/issues/APP-15"')
    expect(card.indexOf('sess-wait-earlier')).toBeLessThan(card.indexOf('sess-wait-later'))
    expect(card).toContain('href="/sessions"')
    expect(card).toContain('href="/sessions?actor=agent-a&amp;session=sess-wait-earlier"')
  })

  it('대기 세션이 0건이면 대기 세션 카드를 보이지 않는다', () => {
    const html = render(overview(), section([]))
    expect(html).not.toContain('>대기 세션</h3>')
  })
})
