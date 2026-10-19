import type { ActorToken, ActorTokenFilter } from '@/api/types'

/** 접속 토큰 화면에서 고르는 보기. 서버의 status 필터와 같은 말을 쓴다. */
export const TOKEN_VIEWS: ReadonlyArray<{ value: ActorTokenFilter; label: string }> = [
  { value: 'active', label: '활성' },
  { value: 'revoked', label: '폐기됨' },
  { value: 'all', label: '전체' },
]

/** 주소의 status 값에서 보기를 읽는다. 없거나 모르는 값이면 기본인 활성이다. */
export function parseTokenView(param: string | null): ActorTokenFilter {
  return param === 'revoked' || param === 'all' ? param : 'active'
}

/** 토큰을 가리키는 이름. `행위자 (기기)` 꼴이다. */
export function tokenLabel(token: Pick<ActorToken, 'actor' | 'client_host'>): string {
  return `${token.actor} (${token.client_host})`
}
