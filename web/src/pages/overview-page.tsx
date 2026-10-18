import { ArrowRightIcon, FolderIcon, LockIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { useOverview, useProjects } from '@/api/queries'
import { ISSUE_STATUSES, ISSUE_TYPES, PRIORITIES, type Activity, type IssueSummary, type Overview, type QueueItem } from '@/api/types'
import { useAppState } from '@/app/state'
import { ActivityItem } from '@/components/activity/activity-item'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { IssueRow } from '@/components/issue/issue-row'
import { PriorityIcon, StatusIcon, TypeIcon } from '@/components/issue/meta'
import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { useFlash } from '@/hooks/use-flash'
import { PRIORITY_LABEL, PROJECT_STATUS_LABEL, STATUS_LABEL, TYPE_LABEL } from '@/lib/labels'
import { formatKst, formatKstTime } from '@/lib/time'
import { cn } from '@/lib/utils'

const STATUS_BAR: Record<string, string> = {
  backlog: 'bg-zinc-600',
  todo: 'bg-zinc-400',
  doing: 'bg-amber-400',
  review: 'bg-emerald-400',
  done: 'bg-indigo-400',
  cancelled: 'bg-zinc-700',
  deleted: 'bg-rose-400/60',
}

function Stat({ label, value, tone }: { label: string; value: number; tone?: string }) {
  return (
    <div className="rounded-lg border bg-card px-3 py-2.5">
      <div className="text-[0.6875rem] text-muted-foreground">{label}</div>
      <div className={cn('mt-0.5 text-xl font-semibold tabular-nums tracking-tight', tone)}>{value}</div>
    </div>
  )
}

// 사용자가 볼 칸. 항목이 있을 때만 강조한다.
const ACCENT = {
  review: {
    section: 'border-emerald-500/40 bg-emerald-500/[0.04]',
    header: 'border-emerald-500/30 bg-emerald-500/10',
    title: 'text-emerald-300',
    badge: 'success',
  },
  decision: {
    section: 'border-amber-500/40 bg-amber-500/[0.04]',
    header: 'border-amber-500/30 bg-amber-500/10',
    title: 'text-amber-300',
    badge: 'warning',
  },
  blocked: {
    section: 'border-rose-500/40 bg-rose-500/[0.04]',
    header: 'border-rose-500/30 bg-rose-500/10',
    title: 'text-rose-300',
    badge: 'destructive',
  },
} as const

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="space-y-2">
      <h2 className="text-[0.6875rem] font-medium text-muted-foreground">{title}</h2>
      {children}
    </div>
  )
}

function Section({
  title,
  total,
  to,
  icon,
  accent,
  children,
}: {
  title: string
  total: number
  to?: string
  icon?: ReactNode
  accent?: keyof typeof ACCENT
  children: ReactNode
}) {
  const tone = accent && total > 0 ? ACCENT[accent] : null
  return (
    <section className={cn('flex min-h-0 flex-col overflow-hidden rounded-lg border bg-card/40', tone?.section)}>
      <header className={cn('flex h-9 items-center gap-2 border-b px-3', tone?.header)}>
        {icon}
        <h3 className={cn('text-xs font-semibold', tone?.title)}>{title}</h3>
        {tone ? (
          <Badge variant={tone.badge} className="tabular-nums">
            {total}
          </Badge>
        ) : (
          <span className="text-[0.6875rem] tabular-nums text-muted-foreground">{total}</span>
        )}
        {to ? (
          <Link to={to} className="ml-auto inline-flex items-center gap-1 text-[0.6875rem] text-muted-foreground hover:text-foreground">
            전체 보기 <ArrowRightIcon className="size-3" />
          </Link>
        ) : null}
      </header>
      <div className="min-h-0 overflow-y-auto">{children}</div>
    </section>
  )
}

function IssueList({ items, flash, showReady }: { items: (IssueSummary | QueueItem)[]; flash: Set<string>; showReady?: boolean }) {
  if (items.length === 0) return <EmptyState className="py-6">없음</EmptyState>
  return (
    <div>
      {items.map((i) => (
        <IssueRow key={i.key} issue={i} compact flash={flash.has(i.key)} showReady={showReady} />
      ))}
    </div>
  )
}

function Counts({ overview }: { overview: Overview }) {
  const { counts } = overview
  const statusTotal = Math.max(1, counts.total)
  return (
    <div className="grid gap-3 lg:grid-cols-3">
      <div className="rounded-lg border bg-card/40 p-3">
        <div className="mb-2 text-[0.6875rem] font-medium text-muted-foreground">상태별 (전체 {counts.total})</div>
        <div className="flex h-2 w-full overflow-hidden rounded-full bg-muted">
          {ISSUE_STATUSES.map((s) => {
            const n = counts.by_status[s] ?? 0
            return n > 0 ? <div key={s} className={STATUS_BAR[s]} style={{ width: `${(n / statusTotal) * 100}%` }} title={`${STATUS_LABEL[s]} ${n}`} /> : null
          })}
        </div>
        <ul className="mt-2.5 grid grid-cols-2 gap-x-3 gap-y-1">
          {ISSUE_STATUSES.map((s) => (
            <li key={s} className="flex items-center gap-1.5 text-[0.6875rem]">
              <StatusIcon status={s} className="size-3" />
              <span className="text-muted-foreground">{STATUS_LABEL[s]}</span>
              <span className="ml-auto tabular-nums">{counts.by_status[s] ?? 0}</span>
            </li>
          ))}
        </ul>
      </div>
      <div className="rounded-lg border bg-card/40 p-3">
        <div className="mb-2 text-[0.6875rem] font-medium text-muted-foreground">종류별 (열린 이슈 {counts.open})</div>
        <ul className="grid grid-cols-2 gap-x-3 gap-y-1">
          {ISSUE_TYPES.map((t) => (
            <li key={t} className="flex items-center gap-1.5 text-[0.6875rem]">
              <TypeIcon type={t} className="size-3" />
              <span className="text-muted-foreground">{TYPE_LABEL[t]}</span>
              <span className="ml-auto tabular-nums">{counts.by_type[t] ?? 0}</span>
            </li>
          ))}
        </ul>
      </div>
      <div className="rounded-lg border bg-card/40 p-3">
        <div className="mb-2 text-[0.6875rem] font-medium text-muted-foreground">우선순위별 (열린 이슈)</div>
        <ul className="space-y-1">
          {PRIORITIES.map((p) => (
            <li key={p} className="flex items-center gap-1.5 text-[0.6875rem]">
              <PriorityIcon priority={p} className="size-3" />
              <span className="text-muted-foreground">{PRIORITY_LABEL[p]}</span>
              <span className="ml-auto tabular-nums">{counts.by_priority[p] ?? 0}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

function ProjectOverview({ projectKey }: { projectKey: string }) {
  const q = useOverview(projectKey)
  const o = q.data
  const issues = o ? [...o.doing.items, ...o.review.items, ...o.blocked.items, ...o.decisions.items, ...o.next.items] : undefined
  const flash = useFlash(issues, (i) => i.key, (i) => i.updated_at)
  const flashActivity = useFlash(o?.recent_activity.items, (a: Activity) => a.id, () => '')

  if (q.isPending) return <Loading />
  if (q.isError) return <QueryError error={q.error} onRetry={() => void q.refetch()} />
  if (!o) return null

  return (
    <>
      <PageHeader
        title="현황"
        meta={
          <span className="inline-flex items-center gap-2">
            <span className="font-mono text-foreground/80">{o.project.key}</span>
            {o.project.name}
            {o.project.status !== 'active' ? <Badge variant="warning">{PROJECT_STATUS_LABEL[o.project.status]}</Badge> : null}
          </span>
        }
      >
        <span className="text-[0.6875rem] tabular-nums text-muted-foreground" title={`${formatKst(o.generated_at)} KST`}>
          집계 {formatKstTime(o.generated_at)}
        </span>
      </PageHeader>
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
        <Group title="분포">
          <Counts overview={o} />
        </Group>
        <div className="grid grid-cols-3 gap-3 lg:grid-cols-6">
          <Stat label="전체" value={o.counts.total} />
          <Stat label="열린 이슈" value={o.counts.open} />
          <Stat label="진행" value={o.doing.total} tone="text-amber-400" />
          <Stat label="결정 대기" value={o.decisions.total} tone={o.decisions.total > 0 ? 'text-amber-300' : undefined} />
          <Stat label="검토" value={o.review.total} tone="text-emerald-400" />
          <Stat label="막힘" value={o.blocked.total} tone={o.blocked.total > 0 ? 'text-rose-400' : undefined} />
        </div>
        <Group title="내 차례">
          <div className="grid gap-3 xl:grid-cols-3">
            <Section
              title="결정 대기"
              total={o.decisions.total}
              to="/issues?type=decision"
              icon={<TypeIcon type="decision" className="size-3.5" />}
              accent="decision"
            >
              <IssueList items={o.decisions.items} flash={flash} />
            </Section>
            <Section
              title="검토"
              total={o.review.total}
              to="/issues?status=review"
              icon={<StatusIcon status="review" className="size-3.5" />}
              accent="review"
            >
              <IssueList items={o.review.items} flash={flash} />
            </Section>
            <Section title="막힘(선행 이슈 열림)" total={o.blocked.total} icon={<LockIcon className="size-3.5" />} accent="blocked">
              <IssueList items={o.blocked.items} flash={flash} showReady />
            </Section>
          </div>
        </Group>
        <Group title="흐름">
          <div className="grid gap-3 xl:grid-cols-3">
            <Section title="진행 중" total={o.doing.total} to="/issues?status=doing">
              <IssueList items={o.doing.items} flash={flash} />
            </Section>
            <Section title="다음 할 일" total={o.next.total} to="/issues?status=todo">
              <IssueList items={o.next.items} flash={flash} showReady />
            </Section>
            <Section title="최근 활동" total={o.recent_activity.total} to="/activity">
              {o.recent_activity.items.length === 0 ? (
                <EmptyState className="py-6">없음</EmptyState>
              ) : (
                <ul>
                  {o.recent_activity.items.map((a) => (
                    <ActivityItem key={a.id} activity={a} compact flash={flashActivity.has(a.id)} />
                  ))}
                </ul>
              )}
            </Section>
          </div>
        </Group>
      </div>
    </>
  )
}

function ProjectPicker() {
  const { selectProject } = useAppState()
  const projects = useProjects()
  return (
    <>
      <PageHeader title="현황" meta="프로젝트를 고르면 현황을 보여 줍니다" />
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {projects.isPending ? <Loading /> : null}
        {projects.isError ? <QueryError error={projects.error} onRetry={() => void projects.refetch()} /> : null}
        {projects.data ? (
          projects.data.items.length === 0 ? (
            <EmptyState>프로젝트가 없습니다. 사이드바에서 새 프로젝트를 만드세요.</EmptyState>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {projects.data.items.map((p) => (
                <button
                  key={p.key}
                  type="button"
                  onClick={() => selectProject(p.key)}
                  className="flex cursor-pointer items-start gap-3 rounded-lg border bg-card p-3 text-left transition-colors hover:border-ring/50 hover:bg-accent/30"
                >
                  <FolderIcon className="mt-0.5 size-4 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-xs font-semibold">{p.key}</span>
                      {p.status !== 'active' ? <Badge variant="outline">{PROJECT_STATUS_LABEL[p.status]}</Badge> : null}
                    </div>
                    <div className="truncate text-[0.8125rem]">{p.name}</div>
                    <div className="mt-1 text-[0.6875rem] text-muted-foreground">수정 {formatKst(p.updated_at)}</div>
                  </div>
                </button>
              ))}
            </div>
          )
        ) : null}
      </div>
    </>
  )
}

export function OverviewPage() {
  const { project } = useAppState()
  if (!project) return <ProjectPicker />
  return <ProjectOverview key={project} projectKey={project} />
}
