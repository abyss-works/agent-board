import { describe, expect, it } from 'vitest'
import { decisionItem, sessionFixture } from './test-fixtures'
import { ISSUE_STATUSES, ISSUE_TYPES, OPEN_STATUSES, type AgendaProject, type DecisionItem, type Overview } from './types'

describe('이슈 상태와 종류 목록', () => {
  it('상태는 서버가 돌려주는 여섯 상태의 순서다', () => {
    expect(ISSUE_STATUSES).toEqual(['backlog', 'doing', 'review', 'done', 'cancelled', 'deleted'])
  })
  it('열린 상태는 backlog, doing, review다', () => {
    expect(OPEN_STATUSES).toEqual(['backlog', 'doing', 'review'])
  })
  it('종류는 다섯 가지다', () => {
    expect(ISSUE_TYPES).toEqual(['feature', 'bug', 'design', 'idea', 'observation'])
  })
})

describe('DecisionItem과 기본 픽스처', () => {
  it('decisionItem은 waiting_sessions를 빈 배열로 초기화하고 현황·아젠다 타입과 호환된다', () => {
    const empty: DecisionItem = decisionItem({ key: 'APP-10', decision_needed: '방식을 골라 주세요' })
    expect(empty.waiting_sessions).toEqual([])
    expect(empty.decision_needed).toBe('방식을 골라 주세요')

    const waiting = sessionFixture({ actor: 'agent-a', session_id: 'sess-1', state: 'waiting_approval' })
    const withWaiting: DecisionItem = decisionItem({
      key: 'APP-11',
      decision_needed: '배포를 진행할까요',
      waiting_sessions: [waiting],
    })
    const overviewSection: Overview['decisions'] = { total: 2, items: [empty, withWaiting] }
    const agendaSection: AgendaProject['user_turn']['decisions'] = overviewSection
    expect(agendaSection.items[1].waiting_sessions).toHaveLength(1)
  })
})
