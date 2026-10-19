import { describe, expect, it } from 'vitest'
import { unacknowledgedKeys, unacknowledgedListPath, UNACKNOWLEDGED_STATUSES } from './acknowledge'

describe('unacknowledgedKeys', () => {
  it('아직 확인하지 않은 이슈의 키만 순서대로 돌려준다', () => {
    const items = [
      { key: 'AGB-1', acknowledged_at: null },
      { key: 'AGB-2', acknowledged_at: '2026-10-18T06:00:00.000Z' },
      { key: 'AGB-3', acknowledged_at: null },
    ]
    expect(unacknowledgedKeys(items)).toEqual(['AGB-1', 'AGB-3'])
  })
  it('빈 목록은 빈 배열이다', () => {
    expect(unacknowledgedKeys([])).toEqual([])
  })
})

describe('unacknowledgedListPath', () => {
  it('삭제를 뺀 모든 상태에서 미확인 이슈를 보는 이슈 목록 주소다', () => {
    expect(UNACKNOWLEDGED_STATUSES).toEqual(['backlog', 'doing', 'review', 'done', 'cancelled'])
    expect(unacknowledgedListPath()).toBe('/issues?acknowledged=false&status=backlog%2Cdoing%2Creview%2Cdone%2Ccancelled')
  })
  it('이슈 목록 화면이 읽는 매개변수로 되돌려 읽힌다', () => {
    const params = new URL(unacknowledgedListPath(), 'http://x').searchParams
    expect(params.get('acknowledged')).toBe('false')
    expect(params.get('status')?.split(',')).toEqual([...UNACKNOWLEDGED_STATUSES])
  })
})
