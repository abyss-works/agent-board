import { describe, expect, it } from 'vitest'
import { instancesFor } from './instances'

describe('instancesFor', () => {
  it('개발 환경은 프록시 경로 /api를 부르는 인스턴스 하나다', () => {
    const list = instancesFor(true, 'localhost:5173')
    expect(list).toHaveLength(1)
    expect(list[0].id).toBe('default')
    expect(list[0].baseUrl).toBe('/api')
    expect(list[0].label).toBe('localhost:5173')
  })
  it('운영 빌드는 같은 출처의 /v1을 부르는 인스턴스 하나다', () => {
    const list = instancesFor(false, 'board.example.com')
    expect(list).toHaveLength(1)
    expect(list[0].baseUrl).toBe('')
    expect(list[0].label).toBe('board.example.com')
  })
})
