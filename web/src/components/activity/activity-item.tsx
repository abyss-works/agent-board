import { MessageSquareIcon, PencilIcon, PlusIcon } from 'lucide-react'
import type { ComponentType, SVGProps } from 'react'
import type { Activity, ActivityKind } from '@/api/types'
import { IssueLink } from '@/components/issue/issue-link'
import { ActorChip, IssueKey } from '@/components/issue/meta'
import { Markdown } from '@/components/markdown'
import { describeChanges, summarizeActivity } from '@/lib/activity'
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
  /** 이슈 상세처럼 이미 이슈가 정해진 곳에서는 끈다. compact에서는 이슈 키를 항상 보인다. */
  showIssue?: boolean
  /** 현황의 최근 활동처럼 좁은 곳. 이슈 행처럼 한 줄로 그리고, 누르면 이슈 오버레이를 연다. */
  compact?: boolean
}

/** 한 줄 행. 시각, 행위자, 종류 아이콘, 이슈 키, 요약 문구 순이고 행 전체가 이슈 링크다. */
function CompactActivityRow({ activity: a, flash }: { activity: Activity; flash?: boolean }) {
  const Icon = KIND_ICON[a.kind]
  const text = summarizeActivity(a)
  return (
    <li className={cn('border-b border-border/60 last:border-b-0', flash && 'animate-flash')}>
      <IssueLink issueKey={a.issue} className="flex h-8 items-center gap-2.5 px-3 transition-colors hover:bg-accent/40">
        <time dateTime={a.at} title={`${formatKst(a.at)} KST`} className="w-14 shrink-0 font-mono text-[0.6875rem] tabular-nums text-muted-foreground">
          {formatKstTime(a.at)}
        </time>
        <ActorChip actor={a.actor} className="shrink-0" />
        <Icon className={cn('size-3.5 shrink-0', KIND_COLOR[a.kind])} aria-label={KIND_LABEL[a.kind]} />
        <IssueKey issueKey={a.issue} className="shrink-0" />
        <span className="min-w-0 flex-1 truncate text-[0.8125rem]" title={text}>
          {text}
        </span>
      </IssueLink>
    </li>
  )
}

export function ActivityItem({ activity: a, flash, showIssue = true, compact }: Props) {
  if (compact) return <CompactActivityRow activity={a} flash={flash} />
  const Icon = KIND_ICON[a.kind]
  const lines = describeChanges(a)
  return (
    <li className={cn('flex gap-3 border-b border-border/60 px-4 py-2.5 last:border-b-0', flash && 'animate-flash')}>
      <time
        dateTime={a.at}
        title={`${formatKst(a.at)} KST`}
        className="mt-0.5 w-14 shrink-0 font-mono text-[0.6875rem] tabular-nums text-muted-foreground"
      >
        {formatKstTime(a.at)}
      </time>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
          <ActorChip actor={a.actor} />
          <span className={cn('inline-flex items-center gap-1 text-[0.6875rem]', KIND_COLOR[a.kind])}>
            <Icon className="size-3" />
            {KIND_LABEL[a.kind]}
          </span>
          {showIssue ? (
            <IssueLink issueKey={a.issue} className="hover:underline">
              <IssueKey issueKey={a.issue} className="text-foreground/80" />
            </IssueLink>
          ) : null}
          <span className="ml-auto truncate font-mono text-[0.625rem] text-muted-foreground/70" title={`세션 ${a.session_id} · 기기 ${a.client_host}`}>
            {a.session_id.length > 18 ? `${a.session_id.slice(0, 18)}…` : a.session_id} · {a.client_host}
          </span>
        </div>
        {a.kind === 'commented' && a.body ? (
          <>
            {a.summary ? (
              <div className="mt-1.5 truncate text-xs font-semibold" title={a.summary}>
                {a.summary}
              </div>
            ) : null}
            <Markdown source={a.body} className="mt-1.5 rounded-md border border-border/60 bg-card/60 px-3 py-2" />
          </>
        ) : lines.length > 0 ? (
          <ul className="mt-1 space-y-0.5 text-[0.75rem] text-muted-foreground">
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
                {l.reason ? <span className="basis-full text-[0.6875rem] break-words text-muted-foreground">사유: {l.reason}</span> : null}
              </li>
            ))}
          </ul>
        ) : null}
      </div>
    </li>
  )
}
