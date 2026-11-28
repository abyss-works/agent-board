import { describe, expect, it } from 'vitest'
import type { ContextNeighbor, EdgeCandidate, StageMilestone } from '@/api/context-types'
import { candidateTarget, groupByStage, itemPath, scorePercent, sortCandidates, splitNeighbors } from './context'

const ms = (id: number, status: StageMilestone['status']): StageMilestone => ({
  id, project: 'APP', name: `m${id}`, description: '', target_date: null, status, progress: { total: 0, done: 0 },
})

describe('groupByStage', () => {
  it('3단계로 나누고 삭제된 것은 뺀다', () => {
    const g = groupByStage([ms(1, 'planned'), ms(2, 'active'), ms(3, 'closed'), ms(4, 'deleted'), ms(5, 'planned')])
    expect(g.planned.map((m) => m.id)).toEqual([1, 5])
    expect(g.active.map((m) => m.id)).toEqual([2])
    expect(g.closed.map((m) => m.id)).toEqual([3])
  })
})

describe('splitNeighbors', () => {
  it('방향별로 나눈다', () => {
    const n = (id: number, direction: ContextNeighbor['direction']) => ({ edge_id: id, direction }) as ContextNeighbor
    const g = splitNeighbors([n(1, 'inbound'), n(2, 'outbound'), n(3, 'inbound')])
    expect(g.inbound.map((x) => x.edge_id)).toEqual([1, 3])
    expect(g.outbound.map((x) => x.edge_id)).toEqual([2])
  })
})

describe('scorePercent', () => {
  it('범위를 맞춘다', () => {
    expect(scorePercent(0.916)).toBe(92)
    expect(scorePercent(-1)).toBe(0)
    expect(scorePercent(7)).toBe(100)
    expect(scorePercent(Number.NaN)).toBe(0)
  })
})

describe('itemPath', () => {
  it('종류별 화면 경로', () => {
    expect(itemPath({ node_type: 'issue', project: 'APP', key_or_name: 'APP-1' })).toBe('/issues/APP-1')
    expect(itemPath({ node_type: 'spec', project: 'APP', key_or_name: 'api v2' })).toBe('/specs/APP/api%20v2')
    expect(itemPath({ node_type: 'record', project: 'APP', key_or_name: 'a/b.md' })).toBe('/documents/APP/a%2Fb.md')
    expect(itemPath({ node_type: 'epic', project: 'APP', key_or_name: 'E-1' })).toBeNull()
  })
})

describe('sortCandidates', () => {
  it('대체 후보가 먼저, 같은 종류 안에서는 확신도 순이다', () => {
    const c = (id: number, kind: EdgeCandidate['kind'], confidence?: number) => ({ id, kind, confidence }) as EdgeCandidate
    const sorted = sortCandidates([c(1, 'related', 0.99), c(2, 'supersedes', 0.4), c(3, 'supersedes', 0.9), c(4, 'supersedes')])
    expect(sorted.map((x) => x.id)).toEqual([3, 2, 4, 1])
  })
})

describe('candidateTarget', () => {
  it('해석된 대상이 없으면 원문을 보인다', () => {
    expect(candidateTarget({ target_key_or_name: 'x' })).toBe('x')
    expect(candidateTarget({ unresolved_target_text: 'raw' })).toBe('raw')
    expect(candidateTarget({})).toBe('-')
  })
})
