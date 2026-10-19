import { describe, expect, it } from 'vitest'
import { ISSUE_STATUSES, ISSUE_TYPES, OPEN_STATUSES } from './types'

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
