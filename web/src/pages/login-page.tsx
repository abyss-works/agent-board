import { useQuery } from '@tanstack/react-query'
import { EyeIcon, EyeOffIcon, KeyRoundIcon, ServerIcon } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import type { Health } from '@/api/types'
import { errorMessage, fetchUserActorIds, isUnauthorized } from '@/api/client'
import { QUERY_ROOT } from '@/api/queries'
import { BASE_URL, CLIENT_HOST, useAppState } from '@/app/state'
import { ActorChoice } from '@/components/actor-choice'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LEGACY_ACTOR, NO_USER_ACTOR_MESSAGE, resolveActor } from '@/lib/actors'
import { cn } from '@/lib/utils'

/** 인증 없이 부르는 /v1/health. 토큰을 넣기 전에 서버가 켜져 있는지 보여 준다. */
function useHealth() {
  return useQuery({
    queryKey: [QUERY_ROOT, 'health'],
    queryFn: async ({ signal }): Promise<Health> => {
      const res = await fetch(`${BASE_URL}/v1/health`, { signal })
      if (!res.ok || !(res.headers.get('content-type') ?? '').includes('application/json')) throw new Error(`HTTP ${res.status}`)
      return (await res.json()) as Health
    },
    refetchInterval: 5000,
    retry: false,
  })
}

export function LoginPage() {
  const { actor, login, authNotice, sessionId } = useAppState()
  const [token, setToken] = useState('')
  const [show, setShow] = useState(false)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState<string | null>(null)
  /** 사용자 행위자가 여럿일 때 고를 후보. 토큰을 고치면 비운다. */
  const [candidates, setCandidates] = useState<string[] | null>(null)
  const health = useHealth()

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const value = token.trim()
    if (!value || checking) return
    setChecking(true)
    setError(null)
    try {
      const users = await fetchUserActorIds({ baseUrl: BASE_URL, token: value, sessionId, clientHost: CLIENT_HOST })
      const resolution = resolveActor(users, [actor, LEGACY_ACTOR])
      if (resolution.kind === 'use') login(value, resolution.actor)
      else if (resolution.kind === 'choose') setCandidates(resolution.users)
      else setError(NO_USER_ACTOR_MESSAGE)
    } catch (err) {
      setError(
        isUnauthorized(err)
          ? '서버가 토큰을 거부했습니다(401). 공용 토큰을 확인해 주세요.'
          : `행위자 목록을 받지 못했습니다. ${errorMessage(err)}`,
      )
    } finally {
      setChecking(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-6">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center gap-2.5">
          <div className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <span className="text-sm font-bold">ab</span>
          </div>
          <div>
            <h1 className="text-sm font-semibold tracking-tight">agent-board</h1>
            <p className="text-[0.6875rem] text-muted-foreground">공용 토큰으로 로그인합니다</p>
          </div>
        </div>

        <form onSubmit={(e) => void submit(e)} className="space-y-4 rounded-xl border bg-card p-5 shadow-xl">
          <div className="space-y-1.5">
            <Label>서버</Label>
            <div className="flex h-8 items-center gap-2 rounded-md border border-input px-2.5 text-[0.8125rem]">
              <ServerIcon className="size-3.5 text-muted-foreground" />
              <span className="font-mono text-xs">{window.location.host}</span>
            </div>
            <p className="flex items-center gap-1.5 text-[0.6875rem] text-muted-foreground">
              <span
                className={cn(
                  'size-1.5 rounded-full',
                  health.isPending ? 'bg-zinc-500' : health.isError ? 'bg-rose-400' : 'bg-emerald-400',
                )}
              />
              {health.isPending
                ? '서버 상태 확인 중'
                : health.isError
                  ? '서버에 연결할 수 없습니다. 꺼져 있거나 주소가 다릅니다.'
                  : `서버 켜짐 · v${health.data.version} · API v${health.data.api_version}`}
            </p>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="token">공용 토큰</Label>
            <div className="relative">
              <KeyRoundIcon className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                id="token"
                type={show ? 'text' : 'password'}
                value={token}
                onChange={(e) => {
                  setToken(e.target.value)
                  setCandidates(null)
                }}
                placeholder="AGB_TOKEN 값을 붙여 넣으세요"
                autoComplete="off"
                autoFocus
                spellCheck={false}
                className="px-8 font-mono"
              />
              <button
                type="button"
                onClick={() => setShow((s) => !s)}
                className="absolute top-1/2 right-2 -translate-y-1/2 cursor-pointer text-muted-foreground hover:text-foreground"
                aria-label={show ? '토큰 숨기기' : '토큰 보기'}
              >
                {show ? <EyeOffIcon className="size-3.5" /> : <EyeIcon className="size-3.5" />}
              </button>
            </div>
            <p className="text-[0.6875rem] text-muted-foreground">
              토큰은 이 브라우저의 localStorage에 저장되며 요청마다 <code className="font-mono">Authorization: Bearer</code>로
              보냅니다. 요청의 행위자는 서버에 선언된 사용자 행위자입니다.
            </p>
          </div>

          {candidates ? <ActorChoice users={candidates} onPick={(actor) => login(token, actor)} /> : null}

          {error ?? authNotice ? (
            <p className="rounded-md bg-destructive/10 px-3 py-2 text-[0.75rem] text-destructive">{error ?? authNotice}</p>
          ) : null}

          {candidates ? null : (
            <Button type="submit" className="w-full" disabled={!token.trim() || checking}>
              {checking ? '확인 중' : '로그인'}
            </Button>
          )}
        </form>
      </div>
    </div>
  )
}
