import { AlertTriangleIcon, ClockIcon, HelpCircleIcon, ShieldAlertIcon } from 'lucide-react'
import type { Session, SessionRef, SessionRole, SessionState } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { SESSION_ROLE_LABEL, SESSION_STATE_LABEL } from '@/lib/labels'
import { sessionRefLabel } from '@/lib/session-tree'
import { cn } from '@/lib/utils'

const WAITING_STYLE: Record<SessionState, { variant: 'warning' | 'destructive' | 'info'; className: string }> = {
  waiting_approval: {
    variant: 'destructive',
    className: 'border-rose-500/40 bg-rose-500/15 text-rose-300',
  },
  waiting_input: {
    variant: 'warning',
    className: 'border-amber-500/40 bg-amber-500/15 text-amber-300',
  },
  waiting_external: {
    variant: 'info',
    className: 'border-sky-500/40 bg-sky-500/15 text-sky-300',
  },
}

function WaitingIcon({ state }: { state: SessionState }) {
  switch (state) {
    case 'waiting_approval':
      return <ShieldAlertIcon className="size-3" />
    case 'waiting_input':
      return <HelpCircleIcon className="size-3" />
    case 'waiting_external':
      return <ClockIcon className="size-3" />
  }
}

/** 대기 상태 배지. 세 가지 대기 종류를 강조 스타일로 구분한다. */
export function WaitingStateBadge({ state, className }: { state: SessionState; className?: string }) {
  const style = WAITING_STYLE[state]
  return (
    <Badge variant={style.variant} className={cn(style.className, className)}>
      <WaitingIcon state={state} />
      {SESSION_STATE_LABEL[state]}
    </Badge>
  )
}

/** 세션 상태 배지. 대기 상태가 있으면 강조 배지를, 없으면 멈춤/활성 배지를 보인다. */
export function SessionStatusBadge({ session, className }: { session: Pick<Session, 'state' | 'stale'>; className?: string }) {
  if (session.state !== null) return <WaitingStateBadge state={session.state} className={className} />
  if (session.stale) {
    return (
      <Badge variant="outline" className={className}>
        멈춤
      </Badge>
    )
  }
  return (
    <Badge variant="success" className={className}>
      활성
    </Badge>
  )
}

/** 세션 역할 배지(main / subagent / runner). */
export function SessionRoleBadge({ role, className }: { role: SessionRole; className?: string }) {
  const variant = role === 'main' ? 'primary' : 'default'
  return (
    <Badge variant={variant} className={cn('font-mono', className)}>
      {SESSION_ROLE_LABEL[role]}
    </Badge>
  )
}

/** 작업 위치 충돌 배지. 충돌이 없거나 누락·null이면 그리지 않는다. */
export function LocationConflictBadge({
  conflicts,
  className,
}: {
  conflicts: readonly SessionRef[] | null | undefined
  className?: string
}) {
  const list = conflicts ?? []
  if (list.length === 0) return null
  return (
    <Badge variant="destructive" title={list.map(sessionRefLabel).join(', ')} className={className}>
      <AlertTriangleIcon className="size-3" />
      위치 충돌 {list.length}
    </Badge>
  )
}
