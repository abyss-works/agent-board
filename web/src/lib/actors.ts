import type { Actor } from '@/api/types'

/** 행위자를 저장하기 전에 로그인한 브라우저가 쓰던 값. 목록에 있으면 기본으로 쓴다. */
export const LEGACY_ACTOR = 'owner'

/** 서버에 kind가 user인 행위자가 하나도 없을 때 보여 주는 안내. */
export const NO_USER_ACTOR_MESSAGE =
  '서버에 사용자 행위자가 없습니다. 서버의 AGB_ACTORS에 kind가 user인 행위자를 선언한 뒤 다시 시도해 주세요.'

export type ActorResolution =
  | { kind: 'use'; actor: string }
  | { kind: 'choose'; users: string[] }
  | { kind: 'none' }

export function userActorIds(items: Actor[]): string[] {
  return items.filter((a) => a.kind === 'user').map((a) => a.id)
}

/**
 * 서버에 선언된 사용자 행위자 가운데 쓸 것을 정한다. 하나뿐이면 그것을 쓰고, 여럿이면 `preferred`가
 * 목록에 있을 때만 그것을 쓰며 아니면 고르게 한다. 하나도 없으면 none이다.
 */
export function resolveActor(users: string[], preferred: string | null): ActorResolution {
  if (users.length === 0) return { kind: 'none' }
  if (users.length === 1) return { kind: 'use', actor: users[0] }
  if (preferred !== null && users.includes(preferred)) return { kind: 'use', actor: preferred }
  return { kind: 'choose', users }
}
