import { useQuery } from '@tanstack/react-query'
import { EyeIcon, EyeOffIcon, KeyRoundIcon, ServerIcon } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import type { Health } from '@/api/types'
import { useAppState } from '@/app/state'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'

/** 인증 없이 부르는 /v1/health. 토큰을 넣기 전에 서버가 켜져 있는지 보여 준다. */
function useHealth(baseUrl: string, instanceId: string) {
  return useQuery({
    queryKey: [instanceId, 'health'],
    queryFn: async ({ signal }): Promise<Health> => {
      const res = await fetch(`${baseUrl}/v1/health`, { signal })
      if (!res.ok || !(res.headers.get('content-type') ?? '').includes('application/json')) throw new Error(`HTTP ${res.status}`)
      return (await res.json()) as Health
    },
    refetchInterval: 5000,
    retry: false,
  })
}

export function LoginPage() {
  const { instances, instance, selectInstance, login, authNotice } = useAppState()
  const [token, setToken] = useState('')
  const [show, setShow] = useState(false)
  const health = useHealth(instance.baseUrl, instance.id)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!token.trim()) return
    login(token)
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

        <form onSubmit={submit} className="space-y-4 rounded-xl border bg-card p-5 shadow-xl">
          <div className="space-y-1.5">
            <Label>인스턴스</Label>
            {instances.length > 1 ? (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button
                    type="button"
                    className="flex h-8 w-full cursor-pointer items-center gap-2 rounded-md border border-input px-2.5 text-[0.8125rem] outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <ServerIcon className="size-3.5 text-muted-foreground" />
                    <span className="font-medium">{instance.label}</span>
                    <span className="ml-auto font-mono text-[0.6875rem] text-muted-foreground">{instance.description}</span>
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="w-[var(--radix-dropdown-menu-trigger-width)]">
                  <DropdownMenuRadioGroup value={instance.id} onValueChange={selectInstance}>
                    {instances.map((i) => (
                      <DropdownMenuRadioItem key={i.id} value={i.id}>
                        <span className="font-medium">{i.label}</span>
                        <span className="ml-auto font-mono text-[0.6875rem] text-muted-foreground">{i.description}</span>
                      </DropdownMenuRadioItem>
                    ))}
                  </DropdownMenuRadioGroup>
                </DropdownMenuContent>
              </DropdownMenu>
            ) : (
              <div className="flex h-8 items-center gap-2 rounded-md border border-input px-2.5 text-[0.8125rem]">
                <ServerIcon className="size-3.5 text-muted-foreground" />
                <span className="font-mono text-xs">{instance.description}</span>
              </div>
            )}
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
                onChange={(e) => setToken(e.target.value)}
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
              토큰은 이 브라우저의 localStorage에 인스턴스별로 저장되며 요청마다 <code className="font-mono">Authorization: Bearer</code>로
              보냅니다. 행위자는 <code className="font-mono">owner</code>입니다.
            </p>
          </div>

          {authNotice ? <p className="rounded-md bg-destructive/10 px-3 py-2 text-[0.75rem] text-destructive">{authNotice}</p> : null}

          <Button type="submit" className="w-full" disabled={!token.trim()}>
            로그인
          </Button>
        </form>
      </div>
    </div>
  )
}
