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

/** 사용자 행위자가 둘 이상이어야 화면에서 바꿀 곳이 있다. */
export function canSwitchActor(users: string[]): boolean {
  return users.length >= 2
}

/**
 * 서버에 선언된 사용자 행위자 가운데 쓸 것을 정한다. 하나뿐이면 그것을 쓰고, 여럿이면 `preferred`를
 * 앞에서부터 보아 목록에 있는 첫 값을 쓰며 하나도 없으면 고르게 한다. 사용자 행위자가 없으면 none이다.
 */
export function resolveActor(users: string[], preferred: (string | null)[]): ActorResolution {
  if (users.length === 0) return { kind: 'none' }
  if (users.length === 1) return { kind: 'use', actor: users[0] }
  const found = preferred.find((p) => p !== null && users.includes(p))
  if (found) return { kind: 'use', actor: found }
  return { kind: 'choose', users }
}
