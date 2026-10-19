import type { UseQueryResult } from '@tanstack/react-query'
import { useState, type ComponentProps, type FormEvent, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import { isNotFound, isNotPending } from '@/api/client'
import { useDecideDeviceAuthorization, useDeviceAuthorization } from '@/api/queries'
import type { DeviceAuthorization, DeviceAuthorizationPatch, DeviceAuthorizationStatus } from '@/api/types'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { ActorChip } from '@/components/issue/meta'
import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useNow } from '@/hooks/use-now'
import { normalizeUserCode } from '@/lib/device'
import { DEVICE_STATUS_LABEL } from '@/lib/labels'
import { formatKst, formatRemaining } from '@/lib/time'
import { toastError } from '@/lib/toast'

const STATUS_BADGE: Record<DeviceAuthorizationStatus, ComponentProps<typeof Badge>['variant']> = {
  pending: 'warning',
  approved: 'success',
  denied: 'destructive',
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[5.5rem_1fr] items-center gap-2 py-2">
      <dt className="text-[0.6875rem] text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-[0.8125rem]">{children}</dd>
    </div>
  )
}

/** 만료까지 남은 시간. 1초마다 줄어든다. */
function Remaining({ expiresAt }: { expiresAt: string }) {
  const now = useNow(1000)
  return <span className="tabular-nums">{formatRemaining(expiresAt, new Date(now))}</span>
}

/**
 * 조회한 요청의 코드, 행위자, 기기, 시각, 상태. 대기 중일 때만 승인과 거부 버튼을 보인다.
 * 승인과 거부는 이 버튼을 눌렀을 때만 일어난다.
 */
function AuthorizationCard({ auth, onStale }: { auth: DeviceAuthorization; onStale: () => void }) {
  const decide = useDecideDeviceAuthorization()
  const answer = (status: DeviceAuthorizationPatch['status']) =>
    decide.mutate(
      { userCode: auth.user_code, status },
      {
        onError: (err) => {
          // 그 사이 만료됐거나 다른 곳에서 이미 결정했다. 현재 상태를 다시 읽어 보인다.
          if (isNotFound(err) || isNotPending(err)) onStale()
          else toastError(err, status === 'approved' ? '승인하지 못했습니다' : '거부하지 못했습니다')
        },
      },
    )

  return (
    <section className="rounded-lg border bg-card p-4">
      <dl className="divide-y divide-border/60">
        <Row label="코드">
          <span className="font-mono text-sm font-semibold">{auth.user_code}</span>
        </Row>
        <Row label="행위자">
          <ActorChip actor={auth.actor} />
        </Row>
        <Row label="기기">
          <span className="font-mono text-xs break-all">{auth.client_host}</span>
        </Row>
        <Row label="요청 시각">
          <time dateTime={auth.created_at} className="tabular-nums">
            {formatKst(auth.created_at)}
          </time>
        </Row>
        <Row label="남은 시간">
          <Remaining expiresAt={auth.expires_at} />
        </Row>
        <Row label="상태">
          <Badge variant={STATUS_BADGE[auth.status]}>{DEVICE_STATUS_LABEL[auth.status]}</Badge>
        </Row>
      </dl>
      {auth.status === 'pending' ? (
        <div className="mt-4 flex gap-2">
          <Button disabled={decide.isPending} onClick={() => answer('approved')}>
            승인
          </Button>
          <Button variant="outline" disabled={decide.isPending} onClick={() => answer('denied')}>
            거부
          </Button>
        </div>
      ) : (
        <p className="mt-4 text-[0.8125rem]">{auth.status === 'approved' ? 'CLI로 돌아가면 로그인이 끝납니다.' : '거부했습니다.'}</p>
      )}
    </section>
  )
}

function LookupResult({ query }: { query: UseQueryResult<DeviceAuthorization> }) {
  if (query.isPending) return <Loading />
  if (query.isError) {
    if (isNotFound(query.error)) return <EmptyState>코드가 없거나 만료되었습니다.</EmptyState>
    return <QueryError error={query.error} onRetry={() => void query.refetch()} className="m-0" />
  }
  return <AuthorizationCard key={query.data.user_code} auth={query.data} onStale={() => void query.refetch()} />
}

export function DevicePage() {
  const [params, setParams] = useSearchParams()
  const urlCode = params.get('code') ?? ''
  const code = normalizeUserCode(urlCode)
  const query = useDeviceAuthorization(code)

  const [input, setInput] = useState(urlCode)
  const [syncedUrlCode, setSyncedUrlCode] = useState(urlCode)
  // 주소의 code가 바뀌면(조회, 뒤로 가기) 입력 칸도 그 값으로 맞춘다.
  if (urlCode !== syncedUrlCode) {
    setSyncedUrlCode(urlCode)
    setInput(urlCode)
  }
  const inputCode = normalizeUserCode(input)

  const lookup = (e: FormEvent) => {
    e.preventDefault()
    if (inputCode === null) return
    setInput(inputCode)
    if (inputCode === code) void query.refetch()
    else setParams({ code: inputCode }, { replace: true })
  }

  return (
    <>
      <PageHeader title="기기 승인" />
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        <div className="mx-auto w-full max-w-md space-y-4">
          <form onSubmit={lookup} className="flex items-end gap-2">
            <div className="min-w-0 flex-1 space-y-1.5">
              <Label htmlFor="device-code">코드</Label>
              <Input
                id="device-code"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder="XXXX-XXXX"
                autoComplete="off"
                autoFocus={urlCode === ''}
                spellCheck={false}
                className="font-mono"
              />
            </div>
            <Button type="submit" disabled={inputCode === null}>
              조회
            </Button>
          </form>
          {code !== null ? <LookupResult query={query} /> : null}
        </div>
      </div>
    </>
  )
}
