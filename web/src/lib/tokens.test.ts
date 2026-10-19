import { describe, expect, it } from 'vitest'
import { TOKEN_VIEWS, parseTokenView, tokenLabel } from './tokens'

describe('보기 전환', () => {
  it('활성, 폐기됨, 전체 순서로 세 보기를 둔다', () => {
    expect(TOKEN_VIEWS.map((v) => `${v.value}:${v.label}`)).toEqual(['active:활성', 'revoked:폐기됨', 'all:전체'])
  })

  it('주소의 status가 없거나 모르는 값이면 활성이다', () => {
    expect(parseTokenView(null)).toBe('active')
    expect(parseTokenView('')).toBe('active')
    expect(parseTokenView('active')).toBe('active')
    expect(parseTokenView('bogus')).toBe('active')
    expect(parseTokenView('ALL')).toBe('active')
  })

  it('주소의 revoked와 all은 그대로 읽는다', () => {
    expect(parseTokenView('revoked')).toBe('revoked')
    expect(parseTokenView('all')).toBe('all')
  })
})

describe('tokenLabel', () => {
  it('행위자와 기기를 `행위자 (기기)`로 적는다', () => {
    expect(tokenLabel({ actor: 'codex', client_host: 'DESKTOP-RPLTFHM' })).toBe('codex (DESKTOP-RPLTFHM)')
  })
})
