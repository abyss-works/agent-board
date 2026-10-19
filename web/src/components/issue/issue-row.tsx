import { LockIcon } from 'lucide-react'
import { Fragment } from 'react'
import type { IssueSummary, QueueItem } from '@/api/types'
import { formatKst, formatRelative } from '@/lib/time'
import { cn } from '@/lib/utils'
import { IssueLink } from './issue-link'
import { DecisionBadge, IssueKey, LabelChip, PriorityIcon, StatusIcon, TypeIcon } from './meta'

/** FTS 스니펫의 `[일치]` 부분을 강조해 그린다. */
export function SnippetText({ snippet }: { snippet: string }) {
  const parts = snippet.split(/(\[[^\]]*\])/g)
  return (
    <span className="text-[0.6875rem] text-muted-foreground">
      {parts.map((p, i) =>
        p.startsWith('[') && p.endsWith(']') ? (
          <mark key={i} className="rounded-sm bg-primary/25 px-0.5 text-foreground">
            {p.slice(1, -1)}
          </mark>
        ) : (
          <Fragment key={i}>{p}</Fragment>
        ),
      )}
    </span>
  )
}

interface Props {
  issue: IssueSummary | QueueItem
  flash?: boolean
  compact?: boolean
  /** 큐 항목이면 막힘 표시 */
  showReady?: boolean
}

export function IssueRow({ issue, flash, compact, showReady }: Props) {
  const queue = 'ready' in issue ? issue : null
  const labels = issue.labels.slice(0, compact ? 2 : 4)
  const more = issue.labels.length - labels.length
  return (
    <IssueLink
      issueKey={issue.key}
      className={cn(
        'group flex items-center gap-2.5 border-b border-border/60 px-4 transition-colors last:border-b-0 hover:bg-accent/40',
        compact ? 'h-8 px-3' : 'min-h-9 py-1.5',
        flash && 'animate-flash',
      )}
    >
      <PriorityIcon priority={issue.priority} />
      <StatusIcon status={issue.status} />
      <IssueKey issueKey={issue.key} className="w-16 shrink-0" />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className={cn('truncate text-[0.8125rem]', issue.status === 'cancelled' || issue.status === 'deleted' ? 'text-muted-foreground line-through' : 'text-foreground')}>
            {issue.title}
          </span>
          {showReady && queue && !queue.ready ? (
            <span className="inline-flex shrink-0 items-center gap-1 text-[0.625rem] text-amber-400" title={`선행 이슈 ${queue.blocked_by.join(', ')}가 열려 있음`}>
              <LockIcon className="size-3" />
              {queue.blocked_by.join(', ')}
            </span>
          ) : null}
          {issue.decision_needed ? <DecisionBadge question={issue.decision_needed} /> : null}
        </div>
        {issue.snippet ? (
          <div className="truncate">
            <SnippetText snippet={issue.snippet} />
          </div>
        ) : null}
      </div>
      {!compact && issue.topic_path ? (
        <span className="hidden max-w-44 shrink-0 truncate text-[0.625rem] text-muted-foreground lg:inline" title={`주제 ${issue.topic_path}`}>
          {issue.topic_path}
        </span>
      ) : null}
      {labels.length > 0 ? (
        <div className="hidden shrink-0 items-center gap-1 md:flex">
          {labels.map((l) => (
            <LabelChip key={l} label={l} />
          ))}
          {more > 0 ? <span className="text-[0.625rem] text-muted-foreground">+{more}</span> : null}
        </div>
      ) : null}
      {!compact ? <TypeIcon type={issue.type} className="shrink-0" /> : null}
      <time
        dateTime={issue.updated_at}
        title={`수정 ${formatKst(issue.updated_at)} KST`}
        className="w-14 shrink-0 text-right text-[0.6875rem] tabular-nums text-muted-foreground"
      >
        {formatRelative(issue.updated_at)}
      </time>
    </IssueLink>
  )
}
