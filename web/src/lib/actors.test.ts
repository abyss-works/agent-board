import { describe, expect, it } from 'vitest'
import { resolveActor, userActorIds } from './actors'

describe('userActorIds', () => {
  it('kind가 user인 행위자의 ID만 선언 순서대로 돌려준다', () => {
    expect(
      userActorIds([
        { id: 'me', kind: 'user' },
        { id: 'claude', kind: 'agent' },
        { id: 'boss', kind: 'user' },
      ]),
    ).toEqual(['me', 'boss'])
  })
  it('목록이 비면 빈 배열이다', () => {
    expect(userActorIds([])).toEqual([])
  })
})

describe('resolveActor', () => {
  it('사용자 행위자가 없으면 none이다', () => {
    expect(resolveActor([], null)).toEqual({ kind: 'none' })
    expect(resolveActor([], 'owner')).toEqual({ kind: 'none' })
  })
  it('하나뿐이면 그것을 쓴다', () => {
    expect(resolveActor(['me'], null)).toEqual({ kind: 'use', actor: 'me' })
    expect(resolveActor(['me'], 'owner')).toEqual({ kind: 'use', actor: 'me' })
  })
  it('여럿이고 선호하는 값이 없으면 고르게 한다', () => {
    expect(resolveActor(['me', 'boss'], null)).toEqual({ kind: 'choose', users: ['me', 'boss'] })
  })
  it('선호하는 값이 목록에 있으면 그것을 쓴다', () => {
    expect(resolveActor(['owner', 'boss'], 'boss')).toEqual({ kind: 'use', actor: 'boss' })
    expect(resolveActor(['owner', 'boss'], 'owner')).toEqual({ kind: 'use', actor: 'owner' })
  })
  it('선호하는 값이 목록에 없으면 고르게 한다', () => {
    expect(resolveActor(['me', 'boss'], 'owner')).toEqual({ kind: 'choose', users: ['me', 'boss'] })
  })
})
