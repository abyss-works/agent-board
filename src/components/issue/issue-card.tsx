import { useDraggable } from '@dnd-kit/core'
import type { IssueSummary } from '@/api/types'
import { formatRelative } from '@/lib/time'
import { cn } from '@/lib/utils'
import { IssueLink } from './issue-link'
import { IssueKey, LabelChip, PriorityIcon, TypeIcon } from './meta'

interface Props {
  issue: IssueSummary
  flash?: boolean
  /** 드래그 오버레이에 그릴 때 */
  overlay?: boolean
}

export function IssueCardBody({ issue, flash, overlay, className }: Props & { className?: string }) {
  return (
    <div
      className={cn(
        'rounded-md border bg-card p-2.5 text-card-foreground shadow-sm transition-colors',
        overlay ? 'rotate-1 cursor-grabbing border-ring/60 shadow-2xl' : 'hover:border-ring/40',
        flash && 'animate-flash',
        className,
      )}
    >
      <div className="flex items-center gap-2">
        <IssueKey issueKey={issue.key} />
        <span className="ml-auto flex items-center gap-1.5">
          <TypeIcon type={issue.type} className="size-3" />
          <PriorityIcon priority={issue.priority} className="size-3" />
        </span>
      </div>
      <IssueLink
        issueKey={issue.key}
        className={cn(
          'mt-1 line-clamp-3 block text-[0.8125rem] leading-snug hover:underline',
          issue.status === 'cancelled' ? 'text-muted-foreground line-through' : 'text-foreground',
        )}
      >
        {issue.title}
      </IssueLink>
      <div className="mt-2 flex flex-wrap items-center gap-1">
        {issue.labels.slice(0, 3).map((l) => (
          <LabelChip key={l} label={l} />
        ))}
        {issue.labels.length > 3 ? <span className="text-[0.625rem] text-muted-foreground">+{issue.labels.length - 3}</span> : null}
        <span className="ml-auto text-[0.625rem] tabular-nums text-muted-foreground">{formatRelative(issue.updated_at)}</span>
      </div>
    </div>
  )
}

/** 보드의 끌 수 있는 카드. 끄는 동안 원래 자리는 흐리게 남고 움직이는 모습은 DragOverlay가 그린다. */
export function IssueCard({ issue, flash }: Props) {
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({ id: issue.key, data: { issue } })
  return (
    <div
      ref={setNodeRef}
      className={cn('cursor-grab touch-none outline-none select-none', isDragging && 'opacity-30')}
      {...listeners}
      {...attributes}
    >
      <IssueCardBody issue={issue} flash={flash} />
    </div>
  )
}
