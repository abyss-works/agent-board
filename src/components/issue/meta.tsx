import { BugIcon, DraftingCompassIcon, EyeIcon, LightbulbIcon, PuzzleIcon, ScaleIcon } from 'lucide-react'
import type { ComponentType, SVGProps } from 'react'
import type { IssueStatus, IssueType, Priority } from '@/api/types'
import { hashColor } from '@/lib/colors'
import { PRIORITY_LABEL, STATUS_LABEL, TYPE_LABEL } from '@/lib/labels'
import { cn } from '@/lib/utils'

// 상태 아이콘. 원의 채움 정도로 진행을 나타낸다.
const STATUS_COLOR: Record<IssueStatus, string> = {
  backlog: 'text-zinc-500',
  todo: 'text-zinc-300',
  doing: 'text-amber-400',
  review: 'text-emerald-400',
  done: 'text-indigo-400',
  cancelled: 'text-zinc-500',
  deleted: 'text-rose-400/70',
}

export function StatusIcon({ status, className }: { status: IssueStatus; className?: string }) {
  const cls = cn('size-3.5 shrink-0', STATUS_COLOR[status], className)
  const label = STATUS_LABEL[status]
  switch (status) {
    case 'backlog':
      return (
        <svg viewBox="0 0 14 14" className={cls} aria-label={label} role="img">
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="2.2 2" />
        </svg>
      )
    case 'todo':
      return (
        <svg viewBox="0 0 14 14" className={cls} aria-label={label} role="img">
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
        </svg>
      )
    case 'doing':
      return (
        <svg viewBox="0 0 14 14" className={cls} aria-label={label} role="img">
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <path d="M7 3.5 A3.5 3.5 0 0 1 7 10.5 Z" fill="currentColor" />
        </svg>
      )
    case 'review':
      return (
        <svg viewBox="0 0 14 14" className={cls} aria-label={label} role="img">
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <path d="M7 3.5 A3.5 3.5 0 1 1 3.5 7 L7 7 Z" fill="currentColor" />
        </svg>
      )
    case 'done':
      return (
        <svg viewBox="0 0 14 14" className={cls} aria-label={label} role="img">
          <circle cx="7" cy="7" r="6" fill="currentColor" />
          <path d="M4.2 7.2 L6.2 9.2 L9.9 5.4" fill="none" stroke="var(--background)" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      )
    case 'cancelled':
      return (
        <svg viewBox="0 0 14 14" className={cls} aria-label={label} role="img">
          <circle cx="7" cy="7" r="6" fill="currentColor" />
          <path d="M4.8 4.8 L9.2 9.2 M9.2 4.8 L4.8 9.2" stroke="var(--background)" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      )
    case 'deleted':
      return (
        <svg viewBox="0 0 14 14" className={cls} aria-label={label} role="img">
          <circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="1.5 1.5" />
          <path d="M4.8 4.8 L9.2 9.2 M9.2 4.8 L4.8 9.2" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
        </svg>
      )
  }
}

// 우선순위 아이콘. 막대 개수로 높낮이를, P0은 느낌표로 긴급을 나타낸다.
const PRIORITY_COLOR: Record<Priority, string> = {
  P0: 'text-rose-400',
  P1: 'text-orange-400',
  P2: 'text-zinc-300',
  P3: 'text-zinc-500',
}

export function PriorityIcon({ priority, className }: { priority: Priority; className?: string }) {
  const cls = cn('size-3.5 shrink-0', PRIORITY_COLOR[priority], className)
  const label = PRIORITY_LABEL[priority]
  if (priority === 'P0') {
    return (
      <svg viewBox="0 0 14 14" className={cls} aria-label={label} role="img">
        <rect x="1" y="1" width="12" height="12" rx="2.5" fill="currentColor" />
        <path d="M7 3.6 V8" stroke="var(--background)" strokeWidth="1.7" strokeLinecap="round" />
        <circle cx="7" cy="10.4" r="0.95" fill="var(--background)" />
      </svg>
    )
  }
  const bars = priority === 'P1' ? 3 : priority === 'P2' ? 2 : 1
  return (
    <svg viewBox="0 0 14 14" className={cls} aria-label={label} role="img">
      <rect x="1.5" y="8.5" width="2.6" height="4" rx="0.8" fill="currentColor" opacity={bars >= 1 ? 1 : 0.25} />
      <rect x="5.7" y="5.5" width="2.6" height="7" rx="0.8" fill="currentColor" opacity={bars >= 2 ? 1 : 0.25} />
      <rect x="9.9" y="2" width="2.6" height="10.5" rx="0.8" fill="currentColor" opacity={bars >= 3 ? 1 : 0.25} />
    </svg>
  )
}

const TYPE_ICON: Record<IssueType, ComponentType<SVGProps<SVGSVGElement>>> = {
  feature: PuzzleIcon,
  bug: BugIcon,
  design: DraftingCompassIcon,
  decision: ScaleIcon,
  idea: LightbulbIcon,
  observation: EyeIcon,
}

const TYPE_COLOR: Record<IssueType, string> = {
  feature: 'text-sky-400',
  bug: 'text-rose-400',
  design: 'text-violet-400',
  decision: 'text-amber-400',
  idea: 'text-yellow-300',
  observation: 'text-teal-400',
}

export function TypeIcon({ type, className }: { type: IssueType; className?: string }) {
  const Icon = TYPE_ICON[type]
  return <Icon className={cn('size-3.5 shrink-0', TYPE_COLOR[type], className)} aria-label={TYPE_LABEL[type]} />
}

export function TypeBadge({ type, className }: { type: IssueType; className?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-1 text-[0.6875rem] text-muted-foreground', className)}>
      <TypeIcon type={type} className="size-3" />
      {TYPE_LABEL[type]}
    </span>
  )
}

export function LabelChip({ label, className, onClick }: { label: string; className?: string; onClick?: () => void }) {
  const Comp = onClick ? 'button' : 'span'
  return (
    <Comp
      type={onClick ? 'button' : undefined}
      onClick={onClick}
      className={cn(
        'inline-flex h-5 max-w-40 items-center gap-1.5 rounded-full border border-border px-1.5 text-[0.6875rem] text-muted-foreground',
        onClick && 'cursor-pointer hover:bg-accent hover:text-accent-foreground',
        className,
      )}
    >
      <span className={cn('size-1.5 shrink-0 rounded-full', hashColor(label))} />
      <span className="truncate">{label}</span>
    </Comp>
  )
}

export function ActorChip({ actor, className }: { actor: string; className?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-[0.75rem] font-medium text-foreground/90', className)}>
      <span className={cn('size-2 shrink-0 rounded-full', hashColor(actor))} />
      {actor}
    </span>
  )
}

export function IssueKey({ issueKey, className }: { issueKey: string; className?: string }) {
  return <span className={cn('font-mono text-[0.6875rem] tracking-tight text-muted-foreground', className)}>{issueKey}</span>
}
