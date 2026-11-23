import { ArrowRightIcon, FolderIcon, HourglassIcon, LockIcon, ScaleIcon, ShieldAlertIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { useAgenda, useOverview, useProjects } from '@/api/queries'
import { ISSUE_STATUSES, ISSUE_TYPES, PRIORITIES, type Activity, type DecisionItem, type DoingItem, type IssueSummary, type Overview, type QueueItem, type Session } from '@/api/types'
import { useAppState } from '@/app/state'
import { ActivityItem } from '@/components/activity/activity-item'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { IssueLink } from '@/components/issue/issue-link'
import { IssueRow } from '@/components/issue/issue-row'
import { ActorChip, IssueKey, PriorityIcon, StatusIcon, TypeIcon } from '@/components/issue/meta'
import { PageHeader } from '@/components/layout/page-header'
import { LocationConflictBadge, WaitingStateBadge } from '@/components/session/session-meta'
import { Badge } from '@/components/ui/badge'
import { useFlash } from '@/hooks/use-flash'
import { unverifiedListPath } from '@/lib/verification'
import { PRIORITY_LABEL, PROJECT_STATUS_LABEL, STATUS_LABEL, TYPE_LABEL } from '@/lib/labels'
import { sessionDetailPath, sessionKey } from '@/lib/session-tree'
import { formatKst, formatKstTime } from '@/lib/time'
import { cn } from '@/lib/utils'

const STATUS_BAR: Record<string, string> = {
  backlog: 'bg-zinc-600',
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
  unverified: {
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
  extra,
  children,
}: {
  title: string
  total?: number
  to?: string
  icon?: ReactNode
  accent?: keyof typeof ACCENT
  extra?: ReactNode
  children: ReactNode
}) {
  const tone = accent && total !== undefined && total > 0 ? ACCENT[accent] : null
  return (
    <section className={cn('flex min-h-0 flex-col overflow-hidden rounded-lg border bg-card/40', tone?.section)}>
      <header className={cn('flex h-9 items-center gap-2 border-b px-3', tone?.header)}>
        {icon}
        <h3 className={cn('text-xs font-semibold', tone?.title)}>{title}</h3>
        {total === undefined ? null : tone ? (
          <Badge variant={tone.badge} className="tabular-nums">
            {total}
          </Badge>
        ) : (
          <span className="text-[0.6875rem] tabular-nums text-muted-foreground">{total}</span>
        )}
        {extra}
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

// 칸 높이를 이슈 행(h-8) 5개와 행 사이 구분선 높이로 고정하고, 넘치면 칸 안에서 스크롤한다.
const LIST_ROWS = 5
const LIST_HEIGHT = `calc(${LIST_ROWS} * 2rem + ${LIST_ROWS - 1}px)`

function DecisionWaitingSessionRow({ session }: { session: Session }) {
  return (
    <li className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded px-2 py-1 text-xs transition-colors hover:bg-accent/40">
      <ActorChip actor={session.actor} className="shrink-0" />
      <Link to={sessionDetailPath(session)} className="font-mono text-foreground/90 break-all hover:underline">
        {session.session_id}
      </Link>
      {session.state ? <WaitingStateBadge state={session.state} /> : null}
      <LocationConflictBadge conflicts={session.location_conflicts} />
      {session.state_reason ? (
        <span className="min-w-0 max-w-full truncate text-[0.6875rem] text-muted-foreground" title={session.state_reason}>
          {session.state_reason}
        </span>
      ) : null}
    </li>
  )
}

/** 결정 필요 이슈와 연결된 대기 세션을 그린다. 세션 링크는 IssueRow의 <a> 밖에 형제로 둔다. */
function DecisionList({ items, flash }: { items: DecisionItem[]; flash: Set<string> }) {
  return (
    <div className="overflow-y-auto" style={{ height: LIST_HEIGHT }}>
      {items.length === 0 ? (
        <EmptyState className="flex h-full items-center justify-center py-0">없음</EmptyState>
      ) : (
        <ul className="divide-y divide-border/60">
          {items.map((item) => {
            const waiting = item.waiting_sessions ?? []
            return (
              <li key={item.key} className={waiting.length > 0 ? '[&>a]:border-b-0' : undefined}>
                <IssueRow issue={item} compact flash={flash.has(item.key)} />
                {waiting.length > 0 ? (
                  <ul className="mr-2 mb-1.5 ml-4 space-y-0.5 border-l border-amber-500/30 pl-2">
                    {waiting.map((s) => (
                      <DecisionWaitingSessionRow key={sessionKey(s)} session={s} />
                    ))}
                  </ul>
                ) : null}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

function IssueList({ items, flash, showReady }: { items: (IssueSummary | QueueItem)[]; flash: Set<string>; showReady?: boolean }) {
  return (
    <div className="overflow-y-auto" style={{ height: LIST_HEIGHT }}>
      {items.length === 0 ? (
        <EmptyState className="flex h-full items-center justify-center py-0">없음</EmptyState>
      ) : (
        items.map((i) => <IssueRow key={i.key} issue={i} compact flash={flash.has(i.key)} showReady={showReady} />)
      )}
    </div>
  )
}

/** 검증 필요 이슈 목록. verification_note가 있으면 행 아래에 한 줄 미리보기를 붙인다. */
function UnverifiedList({ items, flash }: { items: IssueSummary[]; flash: Set<string> }) {
  return (
    <div className="overflow-y-auto" style={{ height: LIST_HEIGHT }}>
      {items.length === 0 ? (
        <EmptyState className="flex h-full items-center justify-center py-0">없음</EmptyState>
      ) : (
        <ul className="divide-y divide-border/60">
          {items.map((item) => (
            <li key={item.key} className={item.verification_note ? '[&>a]:border-b-0' : undefined}>
              <IssueRow issue={item} compact flash={flash.has(item.key)} />
              {item.verification_note ? (
                <p className="mr-3 mb-1.5 ml-9 truncate text-[0.6875rem] text-muted-foreground" title={item.verification_note}>
                  {item.verification_note}
                </p>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** 진행 중 이슈를 점유한 행위자별로 묶는다. 묶음 순서는 그 행위자의 첫 이슈 순서다. */
function DoingList({ items, flash }: { items: DoingItem[]; flash: Set<string> }) {
  const groups = new Map<string, DoingItem[]>()
  for (const i of items) groups.set(i.holder.actor, [...(groups.get(i.holder.actor) ?? []), i])
  return (
    <div className="overflow-y-auto" style={{ height: LIST_HEIGHT }}>
      {items.length === 0 ? (
        <EmptyState className="flex h-full items-center justify-center py-0">없음</EmptyState>
      ) : (
        [...groups].map(([actor, list]) => {
          // 같은 세션이면 마지막 활동이 같고, 세션이 여럿이면 가장 최근 것을 보인다.
          const last = list.reduce((a, b) => (a.holder.last_active_at > b.holder.last_active_at ? a : b)).holder
          return (
            <div key={actor}>
              <div className="flex items-center gap-2 px-2 pt-2 pb-1 text-[0.6875rem] text-muted-foreground">
                <span className="font-medium text-foreground/80">{actor}</span>
                <span className="tabular-nums" title={`${formatKst(last.last_active_at)} KST`}>
                  마지막 활동 {formatKstTime(last.last_active_at)}
                </span>
                {last.stale ? <Badge variant="warning">멈춤</Badge> : null}
              </div>
              {list.map((i) => (
                <IssueRow key={i.key} issue={i} compact flash={flash.has(i.key)} />
              ))}
            </div>
          )
        })
      )}
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

function WaitingSessionList({ items }: { items: Session[] }) {
  return (
    <ul className="max-h-60 divide-y divide-border/60 overflow-y-auto">
      {items.map((s) => (
        <li key={sessionKey(s)} className="flex flex-wrap items-center gap-x-2.5 gap-y-1 px-3 py-2 text-xs transition-colors hover:bg-accent/40">
          <ActorChip actor={s.actor} className="shrink-0" />
          <Link to={sessionDetailPath(s)} className="font-mono text-foreground/90 hover:underline">
            {s.session_id}
          </Link>
          {s.state ? <WaitingStateBadge state={s.state} /> : null}
          {s.state_reason ? (
            <span className="min-w-0 flex-1 truncate text-muted-foreground" title={s.state_reason}>
              {s.state_reason}
            </span>
          ) : (
            <span className="flex-1" />
          )}
          {s.issue ? (
            <IssueLink issueKey={s.issue} className="shrink-0 hover:underline">
              <IssueKey issueKey={s.issue} className="text-foreground/80" />
            </IssueLink>
          ) : null}
          <time
            dateTime={s.state_at ?? s.last_seen_at}
            title={`${formatKst(s.state_at ?? s.last_seen_at)} KST`}
            className="shrink-0 font-mono text-[0.6875rem] tabular-nums text-muted-foreground"
          >
            {formatKstTime(s.state_at ?? s.last_seen_at)}
          </time>
        </li>
      ))}
    </ul>
  )
}

function ProjectOverview({ projectKey }: { projectKey: string }) {
  const q = useOverview(projectKey)
  const agendaQ = useAgenda(projectKey)
  const o = q.data
  const waitingSection = agendaQ.data?.projects[0]?.user_turn.waiting_sessions
  const waitingSessions = waitingSection?.items ?? []
  const waitingTotal = waitingSection?.total ?? 0
  const issues = o
    ? [...o.doing.items, ...o.review.items, ...o.blocked.items, ...o.decisions.items, ...o.next.items, ...o.unverified.items]
    : undefined
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
        <Group title="요약">
          <div className="grid grid-cols-3 gap-3 lg:grid-cols-6">
            <Stat label="전체" value={o.counts.total} />
            <Stat label="열린 이슈" value={o.counts.open} />
            <Stat label="진행" value={o.doing.total} tone="text-amber-400" />
            <Stat label="결정 필요" value={o.decisions.total} tone={o.decisions.total > 0 ? 'text-amber-300' : undefined} />
            <Stat label="검토" value={o.review.total} tone="text-emerald-400" />
            <Stat label="막힘" value={o.blocked.total} tone={o.blocked.total > 0 ? 'text-rose-400' : undefined} />
          </div>
        </Group>
        <Group title="내 차례">
          {agendaQ.isError ? (
            <Section title="대기 세션" to="/sessions?status=waiting" icon={<HourglassIcon className="size-3.5 text-amber-400" />}>
              <QueryError error={agendaQ.error} onRetry={() => void agendaQ.refetch()} className="m-3" />
            </Section>
          ) : waitingTotal > 0 ? (
            <Section title="대기 세션" total={waitingTotal} to="/sessions?status=waiting" icon={<HourglassIcon className="size-3.5 text-amber-400" />} accent="decision">
              <WaitingSessionList items={waitingSessions} />
            </Section>
          ) : null}
          <div className="grid gap-3 xl:grid-cols-3">
            <Section title="결정 필요" total={o.decisions.total} icon={<ScaleIcon className="size-3.5 text-amber-400" />} accent="decision">
              <DecisionList items={o.decisions.items} flash={flash} />
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
        <Group title="에이전트 작업">
          <div className="grid gap-3 xl:grid-cols-3">
            <Section title="진행 중" total={o.doing.total} to="/issues?status=doing">
              <DoingList items={o.doing.items} flash={flash} />
            </Section>
            <Section title="다음 할 일" total={o.next.total} to="/issues?status=backlog">
              <IssueList items={o.next.items} flash={flash} showReady />
            </Section>
            <Section
              title="검증 필요"
              total={o.unverified.total}
              to={unverifiedListPath()}
              icon={<ShieldAlertIcon className="size-3.5 text-rose-400" />}
              accent="unverified"
              extra={o.unverified.total > 0 ? <span className="text-[0.6875rem] text-rose-300/80">릴리즈 차단</span> : null}
            >
              <UnverifiedList items={o.unverified.items} flash={flash} />
            </Section>
          </div>
        </Group>
        <Group title="기록">
          <Section title="최근 활동" total={o.recent_activity.total} to="/activity">
            {o.recent_activity.items.length === 0 ? (
              <EmptyState className="py-6">없음</EmptyState>
            ) : (
              <ul className="max-h-[32rem] overflow-y-auto">
                {o.recent_activity.items.map((a) => (
                  <ActivityItem key={a.id} activity={a} flash={flashActivity.has(a.id)} />
                ))}
              </ul>
            )}
          </Section>
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
