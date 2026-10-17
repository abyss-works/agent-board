import { CircleAlertIcon, Loader2Icon, RefreshCwIcon, WifiOffIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { ApiError, NetworkError, ServerUnavailableError, errorMessage } from '@/api/client'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export function Loading({ className }: { className?: string }) {
  return (
    <div className={cn('flex items-center gap-2 px-4 py-8 text-xs text-muted-foreground', className)}>
      <Loader2Icon className="size-3.5 animate-spin" />
      불러오는 중
    </div>
  )
}

export function EmptyState({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('px-4 py-10 text-center text-xs text-muted-foreground', className)}>{children}</div>
}

/** 조회 실패. 네트워크 오류, 서버 꺼짐, API 오류를 구분해 보여 준다. */
export function QueryError({ error, onRetry, className }: { error: unknown; onRetry?: () => void; className?: string }) {
  let title = '요청이 실패했습니다'
  let Icon = CircleAlertIcon
  if (error instanceof NetworkError) {
    title = '네트워크 오류'
    Icon = WifiOffIcon
  } else if (error instanceof ServerUnavailableError) {
    title = '서버가 꺼져 있습니다'
  } else if (error instanceof ApiError) {
    title = error.status === 404 ? '찾을 수 없습니다' : `오류 ${error.status} (${error.code})`
  }
  return (
    <div className={cn('m-4 flex items-start gap-3 rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-xs', className)}>
      <Icon className="mt-0.5 size-4 shrink-0 text-destructive" />
      <div className="min-w-0 flex-1">
        <div className="font-medium text-foreground">{title}</div>
        <div className="mt-0.5 text-muted-foreground">{errorMessage(error)}</div>
        {error instanceof ApiError && error.details.length > 0 ? (
          <ul className="mt-1.5 space-y-0.5 text-muted-foreground">
            {error.details.map((d, i) => (
              <li key={i}>
                {d.field ? <span className="font-mono text-foreground/80">{d.field}</span> : null}
                {d.field ? ': ' : ''}
                {d.message ?? d.reason}
              </li>
            ))}
          </ul>
        ) : null}
      </div>
      {onRetry ? (
        <Button variant="outline" size="xs" onClick={onRetry}>
          <RefreshCwIcon />
          다시 시도
        </Button>
      ) : null}
    </div>
  )
}

/** 입력 칸 아래 오류 문구 */
export function FieldError({ message }: { message: string | null | undefined }) {
  if (!message) return null
  return <p className="mt-1 text-[11px] text-destructive">{message}</p>
}
