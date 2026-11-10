import { useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { errorMessage, fetchUserActorIds, isUnauthorized } from '@/api/client'
import { QUERY_ROOT } from '@/api/queries'
import { BASE_URL, CLIENT_HOST, useAppState } from '@/app/state'
import { ActorChoice } from '@/components/actor-choice'
import { Button } from '@/components/ui/button'
import { LEGACY_ACTOR, NO_USER_ACTOR_MESSAGE, resolveActor } from '@/lib/actors'

/**
 * 토큰만 저장되고 행위자가 없는 브라우저(행위자를 저장하기 전에 로그인한 경우)가 거치는 화면.
 * 서버의 사용자 행위자가 하나이거나 `owner`가 있으면 묻지 않고 정한다.
 */
export function ActorPage() {
  const { token, sessionId, selectActor, logout } = useAppState()
  const actors = useQuery({
    queryKey: [QUERY_ROOT, 'login-actors', token],
    queryFn: () => fetchUserActorIds({ baseUrl: BASE_URL, token: token ?? '', sessionId, clientHost: CLIENT_HOST }),
    refetchInterval: false,
    retry: false,
  })
  const resolution = actors.data ? resolveActor(actors.data, [LEGACY_ACTOR]) : null
  const auto = resolution?.kind === 'use' ? resolution.actor : null

  useEffect(() => {
    if (auto !== null) selectActor(auto)
  }, [auto, selectActor])

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-6">
      <div className="w-full max-w-sm space-y-4 rounded-xl border bg-card p-5 shadow-xl">
        {actors.isError ? (
          <>
            <p className="rounded-md bg-destructive/10 px-3 py-2 text-[0.75rem] text-destructive">
              {isUnauthorized(actors.error)
                ? '서버가 토큰을 거부했습니다(401). 로그아웃한 뒤 토큰을 다시 입력해 주세요.'
                : `행위자 목록을 받지 못했습니다. ${errorMessage(actors.error)}`}
            </p>
            <div className="flex gap-2">
              <Button type="button" variant="outline" className="flex-1" onClick={() => void actors.refetch()}>
                다시 시도
              </Button>
              <Button type="button" variant="outline" className="flex-1" onClick={logout}>
                로그아웃
              </Button>
            </div>
          </>
        ) : resolution?.kind === 'choose' ? (
          <ActorChoice users={resolution.users} onPick={selectActor} />
        ) : resolution?.kind === 'none' ? (
          <>
            <p className="rounded-md bg-destructive/10 px-3 py-2 text-[0.75rem] text-destructive">{NO_USER_ACTOR_MESSAGE}</p>
            <Button type="button" variant="outline" className="w-full" onClick={logout}>
              로그아웃
            </Button>
          </>
        ) : (
          <p className="text-[0.8125rem] text-muted-foreground">행위자 확인 중</p>
        )}
      </div>
    </div>
  )
}
