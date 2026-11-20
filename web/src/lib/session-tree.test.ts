import { describe, expect, it } from 'vitest'
import { sessionFixture } from '@/api/test-fixtures'
import {
  buildHostSessionTrees,
  filterSessionsByStatus,
  parseSessionStatusFilter,
  sessionDetailPath,
  sessionListParamsFor,
  summarizeSessions,
} from './session-tree'

describe('buildHostSessionTrees', () => {
  it('부모-자식 다단계 중첩 트리를 구성한다', () => {
    const root = sessionFixture({ actor: 'agent-a', session_id: 'root', last_host: 'host-a', role: 'main' })
    const child = sessionFixture({
      actor: 'agent-a',
      session_id: 'root.sub1',
      last_host: 'host-a',
      role: 'subagent',
      parent: { actor: 'agent-a', session_id: 'root' },
    })
    const grand = sessionFixture({
      actor: 'agent-a',
      session_id: 'root.sub1.leaf',
      last_host: 'host-a',
      role: 'runner',
      parent: { actor: 'agent-a', session_id: 'root.sub1' },
    })

    const groups = buildHostSessionTrees([grand, root, child])
    expect(groups).toHaveLength(1)
    expect(groups[0].host).toBe('host-a')
    expect(groups[0].totalCount).toBe(3)
    expect(groups[0].roots).toHaveLength(1)
    expect(groups[0].roots[0].session.session_id).toBe('root')
    expect(groups[0].roots[0].crossHost).toBe(false)
    expect(groups[0].roots[0].children).toHaveLength(1)
    expect(groups[0].roots[0].children[0].session.session_id).toBe('root.sub1')
    expect(groups[0].roots[0].children[0].crossHost).toBe(false)
    expect(groups[0].roots[0].children[0].children).toHaveLength(1)
    expect(groups[0].roots[0].children[0].children[0].session.session_id).toBe('root.sub1.leaf')
  })

  it('부모가 목록에 없으면 루트로 승격한다', () => {
    const orphan = sessionFixture({
      actor: 'agent-a',
      session_id: 'orphan-1',
      last_host: 'host-a',
      parent: { actor: 'agent-a', session_id: 'missing-parent' },
    })

    const groups = buildHostSessionTrees([orphan])
    expect(groups).toHaveLength(1)
    expect(groups[0].roots.map((r) => r.session.session_id)).toEqual(['orphan-1'])
    expect(groups[0].roots[0].crossHost).toBe(false)
  })

  it('자기 참조와 순환 참조를 안전하게 끊어 루트로 둔다', () => {
    const selfRef = sessionFixture({
      actor: 'agent-a',
      session_id: 'self',
      last_host: 'host-a',
      parent: { actor: 'agent-a', session_id: 'self' },
    })
    const cycA = sessionFixture({
      actor: 'agent-a',
      session_id: 'cyc-a',
      last_host: 'host-a',
      parent: { actor: 'agent-a', session_id: 'cyc-b' },
    })
    const cycB = sessionFixture({
      actor: 'agent-a',
      session_id: 'cyc-b',
      last_host: 'host-a',
      parent: { actor: 'agent-a', session_id: 'cyc-a' },
    })
    const tail = sessionFixture({
      actor: 'agent-a',
      session_id: 'tail-under-b',
      last_host: 'host-a',
      parent: { actor: 'agent-a', session_id: 'cyc-b' },
    })

    const groups = buildHostSessionTrees([selfRef, cycA, cycB, tail])
    expect(groups).toHaveLength(1)
    expect(groups[0].totalCount).toBe(4)
    const rootIds = groups[0].roots.map((r) => r.session.session_id).sort()
    expect(rootIds).toEqual(['cyc-a', 'cyc-b', 'self'])
    const nodeB = groups[0].roots.find((r) => r.session.session_id === 'cyc-b')!
    expect(nodeB.children.map((c) => c.session.session_id)).toEqual(['tail-under-b'])
  })

  it('부모와 다른 호스트의 자식은 부모 아래에 유지하고 crossHost를 표시한다', () => {
    const parent = sessionFixture({ actor: 'agent-a', session_id: 'main-1', last_host: 'host-a' })
    const remoteChild = sessionFixture({
      actor: 'agent-b',
      session_id: 'worker-1',
      last_host: 'host-b',
      parent: { actor: 'agent-a', session_id: 'main-1' },
    })
    const sameHostGrand = sessionFixture({
      actor: 'agent-b',
      session_id: 'worker-1.sub',
      last_host: 'host-b',
      parent: { actor: 'agent-b', session_id: 'worker-1' },
    })

    const groups = buildHostSessionTrees([parent, remoteChild, sameHostGrand])
    expect(groups).toHaveLength(1)
    expect(groups[0].host).toBe('host-a')
    expect(groups[0].totalCount).toBe(3)
    const childNode = groups[0].roots[0].children[0]
    expect(childNode.session.session_id).toBe('worker-1')
    expect(childNode.crossHost).toBe(true)
    expect(childNode.children[0].crossHost).toBe(false)
  })

  it('형제는 대기 세션 먼저, 활성 먼저, 마지막 호출 내림차순으로 정렬한다', () => {
    const staleNew = sessionFixture({
      session_id: 'stale-new',
      stale: true,
      last_seen_at: '2026-10-17T09:00:00.000Z',
    })
    const activeOld = sessionFixture({
      session_id: 'active-old',
      stale: false,
      last_seen_at: '2026-10-17T07:00:00.000Z',
    })
    const activeNew = sessionFixture({
      session_id: 'active-new',
      stale: false,
      last_seen_at: '2026-10-17T08:00:00.000Z',
    })
    const waitingStale = sessionFixture({
      session_id: 'waiting-stale',
      stale: true,
      state: 'waiting_approval',
      state_reason: '배포 승인 필요',
      last_seen_at: '2026-10-17T05:00:00.000Z',
    })
    const waitingActive = sessionFixture({
      session_id: 'waiting-active',
      stale: false,
      state: 'waiting_input',
      state_reason: '옵션 선택 필요',
      last_seen_at: '2026-10-17T06:00:00.000Z',
    })

    const groups = buildHostSessionTrees([staleNew, activeOld, activeNew, waitingStale, waitingActive])
    expect(groups[0].roots.map((r) => r.session.session_id)).toEqual([
      'waiting-active',
      'waiting-stale',
      'active-new',
      'active-old',
      'stale-new',
    ])
  })

  it('호스트 그룹은 대기 보유, 활성 보유, 호스트명 오름차순으로 정렬한다', () => {
    const staleHostA = sessionFixture({ session_id: 's1', last_host: 'host-a', stale: true })
    const activeHostC = sessionFixture({ session_id: 's2', last_host: 'host-c', stale: false })
    const activeHostB = sessionFixture({ session_id: 's3', last_host: 'host-b', stale: false })
    const waitingHostParent = sessionFixture({ session_id: 's4', last_host: 'host-z', stale: false })
    const waitingHostChild = sessionFixture({
      session_id: 's4.sub',
      last_host: 'host-z',
      parent: { actor: 'agent-a', session_id: 's4' },
      state: 'waiting_external',
      state_reason: 'CI 결과 대기',
    })

    const groups = buildHostSessionTrees([staleHostA, activeHostC, activeHostB, waitingHostParent, waitingHostChild])
    expect(groups.map((g) => g.host)).toEqual(['host-z', 'host-b', 'host-c', 'host-a'])
    expect(groups[0].waitingCount).toBe(1)
    expect(groups[0].activeCount).toBe(2)
    expect(groups[0].conflictCount).toBe(0)
  })

  it('호스트 그룹의 conflictCount는 루트와 자식을 포함해 위치 충돌이 있는 세션 수를 집계한다', () => {
    const cleanRoot = sessionFixture({ actor: 'agent-a', session_id: 'clean-root', last_host: 'host-a' })
    const conflictRoot = sessionFixture({
      actor: 'agent-a',
      session_id: 'conflict-root',
      last_host: 'host-a',
      location_conflicts: [
        { actor: 'agent-b', session_id: 'other-1' },
        { actor: 'agent-c', session_id: 'other-2' },
      ],
    })
    const cleanChild = sessionFixture({
      actor: 'agent-a',
      session_id: 'conflict-root.clean-sub',
      last_host: 'host-a',
      parent: { actor: 'agent-a', session_id: 'conflict-root' },
    })
    const conflictCrossHostChild = sessionFixture({
      actor: 'agent-b',
      session_id: 'conflict-root.remote-sub',
      last_host: 'host-b',
      parent: { actor: 'agent-a', session_id: 'conflict-root' },
      location_conflicts: [{ actor: 'agent-c', session_id: 'other-3' }],
    })
    const otherHostClean = sessionFixture({
      actor: 'agent-d',
      session_id: 'other-host-clean',
      last_host: 'host-c',
    })

    const groups = buildHostSessionTrees([
      cleanRoot,
      conflictRoot,
      cleanChild,
      conflictCrossHostChild,
      otherHostClean,
    ])
    const hostA = groups.find((g) => g.host === 'host-a')!
    const hostC = groups.find((g) => g.host === 'host-c')!
    expect(hostA.totalCount).toBe(4)
    expect(hostA.conflictCount).toBe(2)
    expect(hostC.conflictCount).toBe(0)
  })
})

describe('세션 요약·필터·경로 유틸', () => {
  const active = sessionFixture({ session_id: 'a', stale: false, state: null })
  const waiting = sessionFixture({ session_id: 'w', stale: false, state: 'waiting_input' })
  const stale = sessionFixture({ session_id: 's', stale: true, state: null })

  it('전체·활성·대기·멈춤 건수를 계산한다', () => {
    expect(summarizeSessions([active, waiting, stale])).toEqual({ total: 3, active: 2, waiting: 1, stale: 1 })
  })

  it('상태 필터별로 세션을 거른다', () => {
    const list = [active, waiting, stale]
    expect(filterSessionsByStatus(list, 'all').map((s) => s.session_id)).toEqual(['a', 'w', 's'])
    expect(filterSessionsByStatus(list, 'active').map((s) => s.session_id)).toEqual(['a', 'w'])
    expect(filterSessionsByStatus(list, 'waiting').map((s) => s.session_id)).toEqual(['w'])
    expect(filterSessionsByStatus(list, 'stale').map((s) => s.session_id)).toEqual(['s'])
  })

  it('잘못된 상태 필터 값은 all로 처리한다', () => {
    expect(parseSessionStatusFilter('waiting')).toBe('waiting')
    expect(parseSessionStatusFilter('unknown')).toBe('all')
    expect(parseSessionStatusFilter(null)).toBe('all')
  })

  it('세션 상세 경로를 인코딩해 만든다', () => {
    expect(sessionDetailPath({ actor: 'agent-a', session_id: 'sess-1' })).toBe('/sessions?actor=agent-a&session=sess-1')
  })

  it('상태 필터별로 서버 세션 목록 질의 매개변수를 만든다', () => {
    expect(sessionListParamsFor({ project: 'APP', named: false, status: 'all', limit: 200 })).toEqual({
      project: 'APP',
      named: undefined,
      limit: 200,
    })
    expect(sessionListParamsFor({ project: 'APP', named: true, status: 'active', limit: 200 })).toEqual({
      project: 'APP',
      named: true,
      active: true,
      limit: 200,
    })
    expect(sessionListParamsFor({ project: undefined, named: false, status: 'stale', limit: 200 })).toEqual({
      project: undefined,
      named: undefined,
      active: false,
      limit: 200,
    })
    expect(sessionListParamsFor({ project: 'APP', named: true, status: 'waiting', limit: 200 })).toEqual({
      project: 'APP',
      named: true,
      waiting: true,
      limit: 200,
    })
  })
})
