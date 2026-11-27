import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { isValidElement, type ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import { ApiClient, type SessionListParams } from '@/api/client'
import { activityQueryKey, sessionQueryKey, sessionsQueryKey } from '@/api/queries'
import { activityFixture, sessionFixture } from '@/api/test-fixtures'
import type { Activity, Page, Session, SessionRef } from '@/api/types'
import { ApiContext, AppStateContext, type AppState } from '@/app/state'
import { sessionKey } from '@/lib/session-tree'
import { SessionDetailBody, SessionLocationDetails, SessionsPage } from './sessions-page'

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

function listResponse(items: Session[], total = items.length): Page<Session> {
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

function findReactElements(
  node: unknown,
  predicate: (el: ReactElement<Record<string, unknown>>) => boolean,
): Array<ReactElement<Record<string, unknown>>> {
  const out: Array<ReactElement<Record<string, unknown>>> = []
  const visit = (n: unknown) => {
    if (Array.isArray(n)) {
      for (const item of n) visit(item)
      return
    }
    if (!isValidElement(n)) return
    const el = n as ReactElement<Record<string, unknown>>
    if (predicate(el)) out.push(el)
    if (el.props.children !== undefined) visit(el.props.children)
  }
  visit(node)
  return out
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
    expect(html).toContain('border-rose-500/40 bg-rose-500/15 text-rose-300')
    expect(html).toContain('운영 배포 승인을 기다립니다')
    expect(html).toContain('입력 대기')
    expect(html).toContain('border-amber-500/40 bg-amber-500/15 text-amber-300')
    expect(html).toContain('캐시 백엔드 선택이 필요합니다')
    expect(html).toContain('외부 대기')
    expect(html).toContain('외부 빌드 완료를 기다립니다')
    expect(html).toContain('기본 ID')
    expect(html).toContain('대기 3')
    expect(html).not.toMatch(/<button\b[^>]*>(?:(?!<\/button>)[\s\S])*<(?:div|p)\b/)
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

  it('충돌 세션이 있을 때 트리 노드 배지와 호스트 헤더 배지, 경고 행 강조를 표시한다', () => {
    const conflicting = sessionFixture({
      actor: 'agent-a',
      session_id: 'sess-conflict',
      last_host: 'host-a',
      location: {
        repo_remote: 'https://example.com/org/app.git',
        branch: 'feat/cache',
        worktree: '/work/a',
        head: 'abc1234',
        dirty_count: 1,
        reported_at: '2026-10-17T07:00:00.000Z',
      },
      location_conflicts: [{ actor: 'agent-b', session_id: 'sess-other' }],
    })
    const waitingConflict = sessionFixture({
      actor: 'agent-c',
      session_id: 'sess-waiting-conflict',
      last_host: 'host-b',
      state: 'waiting_approval',
      state_reason: '승인 대기',
      location_conflicts: [
        { actor: 'agent-d', session_id: 'sess-d' },
        { actor: 'agent-e', session_id: 'sess-e' },
      ],
    })

    const html = renderSessions({ sessions: [conflicting, waitingConflict] })
    expect(html).toContain('위치 충돌 1')
    expect(html).toContain('title="agent-b/sess-other"')
    expect(html).toContain('충돌 1')
    expect(html).toContain('border-rose-500/35 bg-rose-500/[0.04] hover:bg-rose-500/[0.08]')
    expect(html).toContain('위치 충돌 2')
    expect(html).toContain('title="agent-d/sess-d, agent-e/sess-e"')
    expect(html).toContain('대기 1')
    expect(html).toContain('border-amber-500/30')

    const selectedHtml = renderSessions({
      sessions: [conflicting],
      initialEntry: '/sessions?actor=agent-a&session=sess-conflict',
    })
    expect(selectedHtml).toContain('border-ring/60')
    expect(selectedHtml).not.toContain('border-rose-500/35')
    expect(selectedHtml).toContain('위치 충돌 1')
  })

  it('상세 패널에서 충돌 세션 버튼 목록이 보이고 클릭 시 해당 세션 선택으로 이동한다', () => {
    const conflictTarget = sessionFixture({
      actor: 'agent-b',
      session_id: 'sess-conflict-1',
      last_host: 'host-a',
      location: {
        repo_remote: 'https://example.com/org/app.git',
        branch: 'feat/conflict',
        worktree: '/work/b',
        head: '1112223',
        dirty_count: 0,
        reported_at: '2026-10-17T07:05:00.000Z',
      },
      location_conflicts: [{ actor: 'agent-a', session_id: 'sess-main' }],
    })
    const selected = sessionFixture({
      actor: 'agent-a',
      session_id: 'sess-main',
      last_host: 'host-a',
      location: {
        repo_remote: 'https://example.com/org/app.git',
        branch: 'feat/conflict',
        worktree: '/work/a',
        head: 'deadbeef',
        dirty_count: 2,
        reported_at: '2026-10-17T07:10:00.000Z',
      },
      location_conflicts: [
        { actor: 'agent-b', session_id: 'sess-conflict-1' },
        { actor: 'agent-c', session_id: 'sess-conflict-2' },
      ],
    })

    const html = renderSessions({
      sessions: [selected, conflictTarget],
      detailSession: selected,
      activities: [],
      initialEntry: '/sessions?actor=agent-a&session=sess-main',
    })
    const detailHtml = html.slice(html.indexOf('aria-label="세션 상세"'))
    expect(detailHtml).toContain('role="alert"')
    expect(detailHtml).toContain('agent-b')
    expect(detailHtml).toContain('sess-conflict-1')
    expect(detailHtml).toContain('agent-c')
    expect(detailHtml).toContain('sess-conflict-2')

    const onSelect = vi.fn<(ref: SessionRef) => void>()
    const detailTree = SessionDetailBody({ session: selected, onSelect })
    const locEls = findReactElements(detailTree, (el) => el.type === SessionLocationDetails)
    expect(locEls).toHaveLength(1)
    const locTree = SessionLocationDetails(locEls[0].props as Parameters<typeof SessionLocationDetails>[0])
    const alerts = findReactElements(locTree, (el) => el.props.role === 'alert')
    expect(alerts).toHaveLength(1)
    const buttons = findReactElements(alerts[0], (el) => el.type === 'button')
    expect(buttons).toHaveLength(2)
    for (const btn of buttons) {
      expect(btn.props.type).toBe('button')
    }
    ;(buttons[0].props.onClick as () => void)()
    ;(buttons[1].props.onClick as () => void)()
    expect(onSelect).toHaveBeenNthCalledWith(1, { actor: 'agent-b', session_id: 'sess-conflict-1' })
    expect(onSelect).toHaveBeenNthCalledWith(2, { actor: 'agent-c', session_id: 'sess-conflict-2' })

    const switchedHtml = renderSessions({
      sessions: [selected, conflictTarget],
      detailSession: conflictTarget,
      activities: [],
      initialEntry: '/sessions?actor=agent-b&session=sess-conflict-1',
    })
    const switchedDetail = switchedHtml.slice(switchedHtml.indexOf('aria-label="세션 상세"'))
    expect(switchedDetail).toContain('sess-conflict-1')
    expect(switchedDetail).toContain('/work/b')
  })

  it('충돌이 없으면 경고 배지와 경고 박스를 렌더링하지 않는다', () => {
    const clean = sessionFixture({
      actor: 'agent-a',
      session_id: 'sess-clean',
      last_host: 'host-a',
      location: {
        repo_remote: 'https://example.com/org/app.git',
        branch: 'feat/clean',
        worktree: '/work/clean',
        head: 'abc1234',
        dirty_count: 0,
        reported_at: '2026-10-17T07:00:00.000Z',
      },
      location_conflicts: [],
    })

    const html = renderSessions({
      sessions: [clean],
      detailSession: clean,
      activities: [],
      initialEntry: '/sessions?actor=agent-a&session=sess-clean',
    })
    expect(html).not.toContain('위치 충돌')
    expect(html).not.toContain('충돌 ')
    expect(html).not.toContain('border-rose-500/35')
    expect(html).not.toContain('role="alert"')
  })

  it('SessionDetailBody는 활동 타임라인에 sessionKey를 key로 부여해 세션 전환 시 이전 타임라인 잔상을 막는다', () => {
    const selected = sessionFixture({ actor: 'agent-a', session_id: 'sess-alpha' })
    const tree = SessionDetailBody({ session: selected, onSelect: () => {} })
    const timelines = findReactElements(tree, (el) => typeof el.type === 'function' && el.type.name === 'SessionActivityTimeline')
    expect(timelines).toHaveLength(1)
    expect(timelines[0].key).toBe(sessionKey(selected))
  })

  it('전체 목록을 모두 불러온 경우 캐시된 요약 쿼리가 남아 있어도 목록 집계 기준으로 요약을 표시한다', () => {
    const s1 = sessionFixture({ session_id: 'all-1', stale: false, state: 'waiting_input' })
    const s2 = sessionFixture({ session_id: 'all-2', stale: true, state: null })
    const html = renderSessions({
      sessions: [s1, s2],
      total: 2,
      summaryTotals: { active: 99, waiting: 99 },
      initialEntry: '/sessions',
    })
    expect(summaryValueOf(html, '전체')).toBe('2')
    expect(summaryValueOf(html, '활성')).toBe('1')
    expect(summaryValueOf(html, '대기 중')).toBe('1')
    expect(summaryValueOf(html, '멈춤')).toBe('1')
  })

  it('location_conflicts가 누락되거나 null이어도 예외 없이 그리고, 자식 세션 충돌 배지를 상세 목록에 표시한다', () => {
    const missingConflicts = { ...sessionFixture({ actor: 'agent-a', session_id: 'sess-missing' }) } as Record<string, unknown>
    delete missingConflicts.location_conflicts

    const childWithConflict = sessionFixture({
      actor: 'agent-b',
      session_id: 'sess-child-conflict',
      role: 'subagent',
      parent: { actor: 'agent-a', session_id: 'sess-parent' },
      location_conflicts: [{ actor: 'agent-c', session_id: 'sess-other' }],
    })
    const parentNullConflicts = sessionFixture({
      actor: 'agent-a',
      session_id: 'sess-parent',
      last_host: 'host-a',
      location: {
        repo_remote: 'https://example.com/org/app.git',
        branch: 'feat/main',
        worktree: '/work/main',
        head: 'abc1234',
        dirty_count: 0,
        reported_at: '2026-10-17T07:00:00.000Z',
      },
      location_conflicts: null as unknown as SessionRef[],
      children: [childWithConflict],
    })

    const html = renderSessions({
      sessions: [missingConflicts as unknown as Session, parentNullConflicts, childWithConflict],
      detailSession: parentNullConflicts,
      activities: [],
      initialEntry: '/sessions?actor=agent-a&session=sess-parent',
    })
    const detailHtml = html.slice(html.indexOf('aria-label="세션 상세"'))
    expect(detailHtml).toContain('sess-child-conflict')
    expect(detailHtml).toContain('위치 충돌 1')
    expect(detailHtml).toContain('title="agent-c/sess-other"')
    expect(detailHtml).not.toContain('role="alert"')
  })
})
