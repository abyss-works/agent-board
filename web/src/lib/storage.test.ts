import { describe, expect, it } from 'vitest'
import { AppStorage, memoryStore, newSessionId } from './storage'

describe('newSessionId', () => {
  it('web- 접두어와 16자리 16진수로 만들고 헤더 규칙에 맞는다', () => {
    const id = newSessionId(() => new Uint8Array([0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15]))
    expect(id).toBe('web-000102030405060708090a0b0c0d0e0f')
    expect(id).toMatch(/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/)
  })
})

describe('AppStorage', () => {
  it('토큰을 저장하고 지운다', () => {
    const s = new AppStorage(memoryStore())
    expect(s.getToken()).toBeNull()
    s.setToken('aaa')
    expect(s.getToken()).toBe('aaa')
    s.clearToken()
    expect(s.getToken()).toBeNull()
  })

  it('세션 ID는 처음 한 번 만들고 그 뒤로는 같은 값을 돌려준다', () => {
    const store = memoryStore()
    const a = new AppStorage(store)
    const first = a.getSessionId()
    expect(first).toMatch(/^web-[0-9a-f]{32}$/)
    expect(a.getSessionId()).toBe(first)
    // 같은 브라우저 저장소를 다시 열어도 같다.
    expect(new AppStorage(store).getSessionId()).toBe(first)
    // 다른 저장소(다른 브라우저)는 다른 값이다.
    expect(new AppStorage(memoryStore()).getSessionId()).not.toBe(first)
  })

  it('선택한 프로젝트를 기억한다', () => {
    const s = new AppStorage(memoryStore())
    expect(s.getProject()).toBeNull()
    s.setProject('AGB')
    expect(s.getProject()).toBe('AGB')
    s.setProject(null)
    expect(s.getProject()).toBeNull()
  })

  it('저장소가 예외를 던져도 동작한다', () => {
    const broken = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
      removeItem: () => {
        throw new Error('blocked')
      },
    }
    const s = new AppStorage(broken)
    expect(s.getToken()).toBeNull()
    expect(() => s.setToken('x')).not.toThrow()
    expect(s.getSessionId()).toMatch(/^web-[0-9a-f]{32}$/)
  })
})
