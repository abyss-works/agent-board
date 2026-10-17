import { describe, expect, it } from 'vitest'
import { changedKeys } from './highlight'

describe('changedKeys', () => {
  it('첫 로드(이전 스냅샷 없음)에는 아무것도 강조하지 않는다', () => {
    expect(changedKeys(null, new Map([['AGB-1', 'v1']]))).toEqual(new Set())
  })
  it('새로 생긴 항목과 버전이 바뀐 항목만 돌려준다', () => {
    const prev = new Map([
      ['AGB-1', '2026-10-17T00:00:00.000Z'],
      ['AGB-2', '2026-10-17T00:00:00.000Z'],
      ['AGB-3', '2026-10-17T00:00:00.000Z'],
    ])
    const next = new Map([
      ['AGB-1', '2026-10-17T00:00:00.000Z'],
      ['AGB-2', '2026-10-17T00:01:00.000Z'],
      ['AGB-4', '2026-10-17T00:02:00.000Z'],
    ])
    expect(changedKeys(prev, next)).toEqual(new Set(['AGB-2', 'AGB-4']))
  })
  it('숫자 키(활동 id)도 된다', () => {
    const prev = new Map([[1, ''], [2, '']])
    const next = new Map([[2, ''], [3, '']])
    expect(changedKeys(prev, next)).toEqual(new Set([3]))
  })
})
