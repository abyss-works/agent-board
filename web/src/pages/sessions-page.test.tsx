import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { ApiClient, type SessionListParams } from '@/api/client'
import { activityQueryKey, sessionQueryKey, sessionsQueryKey } from '@/api/queries'
import { activityFixture, sessionFixture } from '@/api/test-fixtures'
import type { Activity, Page, Session, SessionListResponse } from '@/api/types'
import { ApiContext, AppStateContext, type AppState } from '@/app/state'
import { SessionsPage } from './sessions-page'

const defaultState: AppState = {
  token: 'token',
  actor: 'owner',
  project: 'APP',
  sessionId: 'web-session',
  authNotice: null,
  login: () => {},
  logout: () => {},
  selectActor: () => {},
  selectProject: () => {},
}

// 캐시에 채워 둔 데이터만 그리므로 실제 HTTP 요청이 나가면 시험이 실패한다.
const api = new ApiClient({ baseUrl: '', token: 'token', actor: 'owner', sessionId: 'web-session', clientHost: 'web' }, () => {
  throw new Error('세션 화면이 요청을 보냈습니다.')
})

function listResponse(items: Session[], total = items.length): SessionListResponse {
  return { items, total, limit: 200, offset: 0 }
}

interface RenderOptions {
  sessions?: Session[]
  total?: number
  listParams?: SessionListParams
  summaryTotals?: {
    all?: number
    active?: number
    waiting?: number
  }
  initialEntry?: string
  project?: string | null
  detailSession?: Session
  activities?: Activity[]
}

function renderSessions({
  sessions = [],
  total,
  listParams,
  summaryTotals,
  initialEntry = '/sessions',
  project = 'APP',
  detailSession,
  activities,
}: RenderOptions = {}): string {
  const client = new QueryClient()
  const effectiveParams: SessionListParams = listParams ?? {
    project: project ?? undefined,
    named: undefined,
    limit: 200,
  }
  client.setQueryData(sessionsQueryKey(effectiveParams), listResponse(sessions, total ?? sessions.length))
  if (summaryTotals) {
    const base = { project: effectiveParams.project, named: effectiveParams.named, limit: 1 } as const
    if (summaryTotals.all !== undefined) {
      client.setQueryData(sessionsQueryKey(base), { items: [], total: summaryTotals.all, limit: 1, offset: 0 })
    }
    if (summaryTotals.active !== undefined) {
      client.setQueryData(sessionsQueryKey({ ...base, active: true }), {
        items: [],
        total: summaryTotals.active,
        limit: 1,
        offset: 0,
      })
    }
    if (summaryTotals.waiting !== undefined) {
      client.setQueryData(sessionsQueryKey({ ...base, waiting: true }), {
        items: [],
        total: summaryTotals.waiting,
        limit: 1,
        offset: 0,
      })
    }
  }
  if (detailSession) {
    client.setQueryData(sessionQueryKey(detailSession.actor, detailSession.session_id), detailSession)
  }
  if (detailSession && activities) {
    const page: Page<Activity> = { items: activities, total: activities.length, limit: 30, offset: 0 }
    client.setQueryData(
      activityQueryKey({ project: null, actor: detailSession.actor, kind: null, session: detailSession.session_id, limit: 30 }),
      page,
    )
  }

  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <AppStateContext.Provider value={{ ...defaultState, project }}>
        <ApiContext.Provider value={api}>
          <MemoryRouter initialEntries={[initialEntry]}>
            <SessionsPage />
          </MemoryRouter>
        </ApiContext.Provider>
      </AppStateContext.Provider>
    </QueryClientProvider>,
  )
}

function summaryValueOf(html: string, label: string): string {
  const match = html.match(new RegExp(`>${label}</div><div[^>]*>([^<]+)</div>`))
  if (!match) throw new Error(`${label} 요약 카드가 없습니다.`)
  return match[1]
}

describe('세션 관제 화면', () => {
  it('호스트별로 세션을 묶고 서브에이전트 트리와 교차 호스트 배지를 표시한다', () => {
    const rootA = sessionFixture({
      actor: 'agent-a',
      session_id: 'main-1',
      last_host: 'host-a',
      role: 'main',
      issue: 'APP-12',
      location: {
        repo_remote: 'https://example.com/org/app.git',
        branch: 'feat/cache',
        worktree: '/work/app',
        head: 'abc1234',
        dirty_count: 3,
        reported_at: '2026-10-17T07:00:00.000Z',
      },
    })
    const subA = sessionFixture({
      actor: 'agent-b',
      session_id: 'main-1.sub',
      last_host: 'host-b',
      role: 'subagent',
      parent: { actor: 'agent-a', session_id: 'main-1' },
    })
    const otherHost = sessionFixture({
      actor: 'agent-c',
      session_id: 'runner-1',
      last_host: 'host-c',
      role: 'runner',
      stale: true,
    })

    const html = renderSessions({ sessions: [rootA, subA, otherHost] })
    expect(html).toContain('>host-a</h3>')
    expect(html).toContain('>host-c</h3>')
    // 교차 호스트 자식은 별도 host-b 섹션이 아니라 부모 아래에 붙고 host-b 배지를 표시한다.
    expect(html).not.toContain('>host-b</h3>')
    expect(html).toContain('main-1.sub')
    expect(html).toContain('부모와 다른 호스트: host-b')
    expect(html).toContain('href="/issues/APP-12"')
    expect(html).toContain('feat/cache')
    expect(html).toContain('변경 3')
    expect(html).toContain('>main</span>')
    expect(html).toContain('>subagent</span>')
    expect(html).toContain('>runner</span>')
    expect(html).toContain('>활성</span>')
    expect(html).toContain('>멈춤</span>')
  })

  it('대기 상태 3종과 대기 사유, 기본 ID 배지를 표시한다', () => {
    const approval = sessionFixture({
      actor: 'agent-a',
      session_id: 'sess-approval',
      last_host: 'host-a',
      state: 'waiting_approval',
      state_reason: '운영 배포 승인을 기다립니다',
      state_at: '2026-10-17T07:10:00.000Z',
    })
    const input = sessionFixture({
      actor: 'agent-b',
      session_id: 'sess-input',
      last_host: 'host-a',
      state: 'waiting_input',
      state_reason: '캐시 백엔드 선택이 필요합니다',
      state_at: '2026-10-17T07:11:00.000Z',
    })
    const external = sessionFixture({
      actor: 'agent-c',
      session_id: 'cli-20261017',
      named: false,
      last_host: 'host-a',
      state: 'waiting_external',
      state_reason: '외부 빌드 완료를 기다립니다',
      state_at: '2026-10-17T07:12:00.000Z',
    })

    const html = renderSessions({ sessions: [approval, input, external] })
    expect(html).toContain('승인 대기')
    expect(html).toContain('운영 배포 승인을 기다립니다')
    expect(html).toContain('입력 대기')
    expect(html).toContain('캐시 백엔드 선택이 필요합니다')
    expect(html).toContain('외부 대기')
    expect(html).toContain('외부 빌드 완료를 기다립니다')
    expect(html).toContain('기본 ID')
    expect(html).toContain('대기 3')
  })

  it('선택된 세션의 상세 정보, 부모·자식 세션, 작업 위치, 활동 타임라인을 표시한다', () => {
    const child = sessionFixture({
      actor: 'agent-a',
      session_id: 'sess-main.child',
      role: 'subagent',
      parent: { actor: 'agent-a', session_id: 'sess-main' },
    })
    const selected = sessionFixture({
      actor: 'agent-a',
      session_id: 'sess-main',
      last_host: 'host-a',
      last_project: 'APP',
      role: 'main',
      issue: 'APP-15',
      parent: { actor: 'agent-root', session_id: 'sess-root' },
      state: 'waiting_approval',
      state_reason: '마이그레이션 실행 확인 요청',
      state_at: '2026-10-17T07:20:00.000Z',
      location: {
        repo_remote: 'https://example.com/org/app.git',
        branch: 'feat/sessions',
        worktree: '/srv/worktrees/sessions',
        head: 'deadbeef',
        dirty_count: 2,
        reported_at: '2026-10-17T07:19:00.000Z',
      },
      children: [child],
    })
    const acts = [
      activityFixture({
        id: 101,
        issue: 'APP-15',
        actor: 'agent-a',
        session_id: 'sess-main',
        kind: 'commented',
        summary: 'API 연동 완료',
        body: '세부 내용을 기록했습니다.',
      }),
    ]

    const html = renderSessions({
      sessions: [selected, child],
      detailSession: selected,
      activities: acts,
      initialEntry: '/sessions?actor=agent-a&session=sess-main',
    })
    expect(html).toContain('aria-label="세션 상세"')
    expect(html).toContain('마이그레이션 실행 확인 요청')
    expect(html).toContain('agent-root/sess-root')
    expect(html).toContain('sess-main.child')
    expect(html).toContain('https://example.com/org/app.git')
    expect(html).toContain('feat/sessions')
    expect(html).toContain('/srv/worktrees/sessions')
    expect(html).toContain('deadbeef')
    expect(html).toContain('2개')
    expect(html).toContain('활동 타임라인')
    expect(html).toContain('API 연동 완료')
    expect(html).toContain('세부 내용을 기록했습니다.')
    expect(html).toContain('href="/issues/APP-15"')
  })

  it('세션이 없으면 빈 상태를 표시한다', () => {
    const html = renderSessions({ sessions: [] })
    expect(html).toContain('기록된 세션이 없습니다.')
  })

  it('필터 조건에 맞는 세션이 없으면 조건 빈 상태를 표시한다', () => {
    const html = renderSessions({
      sessions: [],
      listParams: { project: 'APP', waiting: true, limit: 200 },
      initialEntry: '/sessions?status=waiting',
    })
    expect(html).toContain('조건에 맞는 세션이 없습니다.')
  })

  it('상태 필터별로 서버 질의 매개변수(active·waiting)를 직접 전달한다', () => {
    const activeItem = sessionFixture({ session_id: 'srv-active', stale: false })
    const activeHtml = renderSessions({
      sessions: [activeItem],
      listParams: { project: 'APP', named: true, active: true, limit: 200 },
      initialEntry: '/sessions?status=active&named=true',
    })
    expect(activeHtml).toContain('srv-active')

    const staleItem = sessionFixture({ session_id: 'srv-stale', stale: true })
    const staleHtml = renderSessions({
      sessions: [staleItem],
      listParams: { project: 'APP', active: false, limit: 200 },
      initialEntry: '/sessions?status=stale',
    })
    expect(staleHtml).toContain('srv-stale')

    const waitingItem = sessionFixture({ session_id: 'srv-waiting', state: 'waiting_approval', state_reason: '승인 대기' })
    const waitingHtml = renderSessions({
      sessions: [waitingItem],
      listParams: { project: 'APP', waiting: true, limit: 200 },
      initialEntry: '/sessions?status=waiting',
    })
    expect(waitingHtml).toContain('srv-waiting')
  })

  it('전체가 200건을 초과해도 헤더·요약 카드·절단 안내가 서버 total 기준으로 일치한다', () => {
    const s1 = sessionFixture({ session_id: 'sess-1', stale: false })
    const s2 = sessionFixture({ session_id: 'sess-2', stale: true })
    const html = renderSessions({
      sessions: [s1, s2],
      total: 250,
      summaryTotals: { active: 180, waiting: 12 },
      initialEntry: '/sessions',
    })
    expect(html).toContain('>250개</span>')
    expect(summaryValueOf(html, '전체')).toBe('250')
    expect(summaryValueOf(html, '활성')).toBe('180')
    expect(summaryValueOf(html, '대기 중')).toBe('12')
    expect(summaryValueOf(html, '멈춤')).toBe('70')
    expect(html).toContain('총 250개 중 최근 2개 표시')
  })

  it('대기 필터 결과가 200건을 초과할 때도 헤더·요약·절단 안내가 서버 total 기준으로 일치한다', () => {
    const w1 = sessionFixture({ session_id: 'wait-1', state: 'waiting_input', state_reason: '입력 요청' })
    const html = renderSessions({
      sessions: [w1],
      total: 215,
      listParams: { project: 'APP', waiting: true, limit: 200 },
      summaryTotals: { all: 320, active: 210 },
      initialEntry: '/sessions?status=waiting',
    })
    expect(html).toContain('>215개</span>')
    expect(summaryValueOf(html, '전체')).toBe('320')
    expect(summaryValueOf(html, '활성')).toBe('210')
    expect(summaryValueOf(html, '대기 중')).toBe('215')
    expect(summaryValueOf(html, '멈춤')).toBe('110')
    expect(html).toContain('총 215개 중 최근 1개 표시')
  })
})
