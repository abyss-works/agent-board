import { MessageSquareIcon, PencilIcon, PlusIcon } from 'lucide-react'
import type { ComponentType, SVGProps } from 'react'
import { Link } from 'react-router'
import type { Activity, ActivityKind } from '@/api/types'
import { ActorChip, IssueKey } from '@/components/issue/meta'
import { Markdown } from '@/components/markdown'
import { describeChanges } from '@/lib/activity'
import { KIND_LABEL } from '@/lib/labels'
import { formatKst, formatKstTime } from '@/lib/time'
import { cn } from '@/lib/utils'

const KIND_ICON: Record<ActivityKind, ComponentType<SVGProps<SVGSVGElement>>> = {
  created: PlusIcon,
  updated: PencilIcon,
  commented: MessageSquareIcon,
}

const KIND_COLOR: Record<ActivityKind, string> = {
  created: 'text-emerald-400',
  updated: 'text-sky-400',
  commented: 'text-amber-300',
}

interface Props {
  activity: Activity
  flash?: boolean
  /** 이슈 상세처럼 이미 이슈가 정해진 곳에서는 끈다 */
  showIssue?: boolean
  /** 현황의 최근 활동처럼 좁은 곳 */
  compact?: boolean
}

export function ActivityItem({ activity: a, flash, showIssue = true, compact }: Props) {
  const Icon = KIND_ICON[a.kind]
  const lines = describeChanges(a)
  return (
    <li className={cn('flex gap-3 border-b border-border/60 px-4 py-2.5 last:border-b-0', flash && 'animate-flash', compact && 'px-3 py-2')}>
      <time
        dateTime={a.at}
        title={`${formatKst(a.at)} KST`}
        className="mt-0.5 w-14 shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground"
      >
        {formatKstTime(a.at)}
      </time>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
          <ActorChip actor={a.actor} />
          <span className={cn('inline-flex items-center gap-1 text-[11px]', KIND_COLOR[a.kind])}>
            <Icon className="size-3" />
            {KIND_LABEL[a.kind]}
          </span>
          {showIssue ? (
            <Link to={`/issues/${a.issue}`} className="hover:underline">
              <IssueKey issueKey={a.issue} className="text-foreground/80" />
            </Link>
          ) : null}
          {!compact ? (
            <span className="ml-auto truncate font-mono text-[10px] text-muted-foreground/70" title={`세션 ${a.session_id} · 기기 ${a.client_host}`}>
              {a.session_id.length > 18 ? `${a.session_id.slice(0, 18)}…` : a.session_id} · {a.client_host}
            </span>
          ) : null}
        </div>
        {a.kind === 'commented' && a.body ? (
          <Markdown source={a.body} className={cn('mt-1.5 rounded-md border border-border/60 bg-card/60 px-3 py-2', compact && 'line-clamp-3 px-2 py-1 text-xs')} />
        ) : lines.length > 0 ? (
          <ul className={cn('mt-1 space-y-0.5 text-[12px] text-muted-foreground', compact && 'text-[11px]')}>
            {lines.map((l) => (
              <li key={l.field} className="flex min-w-0 flex-wrap items-baseline gap-x-1.5">
                <span className="shrink-0 text-foreground/70">{l.label}</span>
                {l.to === null ? (
                  <span>수정</span>
                ) : l.from === null ? (
                  <span className="truncate text-foreground/90">{l.to}</span>
                ) : (
                  <>
                    <span className="truncate line-through decoration-muted-foreground/60">{l.from}</span>
                    <span className="text-muted-foreground/60">→</span>
                    <span className="truncate text-foreground/90">{l.to}</span>
                  </>
                )}
              </li>
            ))}
          </ul>
        ) : null}
      </div>
    </li>
  )
}
