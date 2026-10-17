import { describe, expect, it } from 'vitest'
import { instancesFor } from './instances'

describe('instancesFor', () => {
  it('개발 환경은 기록용과 스테이징 두 인스턴스를 프록시 경로로 둔다', () => {
    const list = instancesFor(true, 'localhost:5173')
    expect(list.map((i) => [i.id, i.baseUrl])).toEqual([
      ['local', '/api/local'],
      ['staging', '/api/staging'],
    ])
    expect(list[0].label).toBe('기록용')
    expect(list[1].label).toBe('스테이징')
  })
  it('운영 빌드는 같은 출처의 /v1을 부르는 인스턴스 하나다', () => {
    const list = instancesFor(false, 'ab.example.ts.net')
    expect(list).toHaveLength(1)
    expect(list[0].baseUrl).toBe('')
    expect(list[0].label).toBe('ab.example.ts.net')
  })
})
