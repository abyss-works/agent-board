import { useState } from 'react'
import { useSearchParams } from 'react-router'
import { useActorTokens } from '@/api/queries'
import type { ActorToken, ActorTokenFilter } from '@/api/types'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { ActorChip } from '@/components/issue/meta'
import { PageHeader } from '@/components/layout/page-header'
import { TokenRevokeDialog } from '@/components/token/token-revoke-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { formatKst } from '@/lib/time'
import { TOKEN_VIEWS, parseTokenView, tokenLabel } from '@/lib/tokens'
import { cn } from '@/lib/utils'

const EMPTY_TEXT: Record<ActorTokenFilter, string> = {
  active: '활성 토큰이 없습니다.',
  revoked: '폐기된 토큰이 없습니다.',
  all: '발급된 토큰이 없습니다.',
}

function ViewSwitch({ view, onChange }: { view: ActorTokenFilter; onChange: (view: ActorTokenFilter) => void }) {
  return (
    <div className="flex gap-0.5 rounded-md bg-muted p-0.5" role="group" aria-label="보기">
      {TOKEN_VIEWS.map((v) => (
        <button
          key={v.value}
          type="button"
          aria-pressed={view === v.value}
          onClick={() => onChange(v.value)}
          className={cn(
            'cursor-pointer rounded px-2.5 py-0.5 text-xs transition-colors',
            view === v.value ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
          )}
        >
          {v.label}
        </button>
      ))}
    </div>
  )
}

function Time({ iso }: { iso: string }) {
  return (
    <time dateTime={iso} className="text-xs whitespace-nowrap tabular-nums">
      {formatKst(iso)}
    </time>
  )
}

/** 활성이면 배지만, 폐기됐으면 폐기 시각과 폐기한 행위자를 함께 보인다. */
function TokenStatus({ token: t }: { token: ActorToken }) {
  if (t.revoked_at === null) return <Badge variant="success">활성</Badge>
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <Badge variant="outline">폐기됨</Badge>
      <span className="text-muted-foreground">
        <Time iso={t.revoked_at} />
        {t.revoked_by ? <span className="text-xs"> · {t.revoked_by}</span> : null}
      </span>
    </div>
  )
}

function TokenRow({ token: t, onRevoke }: { token: ActorToken; onRevoke: (t: ActorToken) => void }) {
  return (
    <tr className="border-b border-border/60 transition-colors last:border-b-0 hover:bg-accent/40">
      <td className="py-2.5 pr-3 pl-4">
        <ActorChip actor={t.actor} />
      </td>
      <td className="px-3 py-2.5 font-mono text-xs break-all">{t.client_host}</td>
      <td className="px-3 py-2.5">
        <Time iso={t.created_at} />
      </td>
      <td className="px-3 py-2.5">
        <ActorChip actor={t.created_by} />
      </td>
      <td className="px-3 py-2.5">{t.last_used_at ? <Time iso={t.last_used_at} /> : <span className="text-muted-foreground/70">-</span>}</td>
      <td className="px-3 py-2.5">
        <TokenStatus token={t} />
      </td>
      <td className="py-2.5 pr-4 pl-3 text-right">
        {t.revoked_at === null ? (
          <Button variant="destructive" size="xs" aria-label={`${tokenLabel(t)} 토큰 폐기`} onClick={() => onRevoke(t)}>
            폐기
          </Button>
        ) : null}
      </td>
    </tr>
  )
}

function TokenTable({ view, onRevoke }: { view: ActorTokenFilter; onRevoke: (t: ActorToken) => void }) {
  const q = useActorTokens(view)
  if (q.isPending) return <Loading />
  if (q.isError) return <QueryError error={q.error} onRetry={() => void q.refetch()} />
  const items = q.data.items
  if (items.length === 0) return <EmptyState>{EMPTY_TEXT[view]}</EmptyState>
  return (
    <table className="w-full text-[0.8125rem]">
      <thead>
        <tr className="border-b text-left text-[0.6875rem] text-muted-foreground">
          <th className="py-2 pr-3 pl-4 font-medium">행위자</th>
          <th className="px-3 py-2 font-medium">기기</th>
          <th className="px-3 py-2 font-medium">발급 시각</th>
          <th className="px-3 py-2 font-medium">발급자</th>
          <th className="px-3 py-2 font-medium">마지막 사용</th>
          <th className="px-3 py-2 font-medium">상태</th>
          <th className="py-2 pr-4 pl-3">
            <span className="sr-only">작업</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {items.map((t) => (
          <TokenRow key={t.id} token={t} onRevoke={onRevoke} />
        ))}
      </tbody>
    </table>
  )
}

export function TokensPage() {
  const [params, setParams] = useSearchParams()
  const view = parseTokenView(params.get('status'))
  const [revoking, setRevoking] = useState<ActorToken | null>(null)

  const changeView = (next: ActorTokenFilter) => {
    const nextParams = new URLSearchParams(params)
    if (next === 'active') nextParams.delete('status')
    else nextParams.set('status', next)
    setParams(nextParams, { replace: true })
  }

  return (
    <>
      <PageHeader title="접속 토큰">
        <ViewSwitch view={view} onChange={changeView} />
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <TokenTable key={view} view={view} onRevoke={setRevoking} />
      </div>
      <TokenRevokeDialog open={revoking !== null} onOpenChange={(o) => !o && setRevoking(null)} token={revoking} />
    </>
  )
}
