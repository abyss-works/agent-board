import { CornerDownRightIcon, GitBranchIcon, ServerIcon, XIcon } from 'lucide-react'
import { useMemo, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import { useActivity, useSession, useSessions } from '@/api/queries'
import type { Session, SessionLocation, SessionRef } from '@/api/types'
import { useAppState } from '@/app/state'
import { ActivityItem } from '@/components/activity/activity-item'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { IssueLink } from '@/components/issue/issue-link'
import { ActorChip, IssueKey } from '@/components/issue/meta'
import { PageHeader } from '@/components/layout/page-header'
import { SessionRoleBadge, SessionStatusBadge, WaitingStateBadge } from '@/components/session/session-meta'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  SESSION_STATUS_FILTERS,
  buildHostSessionTrees,
  filterSessionsByStatus,
  parseSessionStatusFilter,
  sessionKey,
  sessionRefLabel,
  summarizeSessions,
  type HostSessionGroup,
  type SessionStatusFilter,
  type SessionTreeNode,
} from '@/lib/session-tree'
import { formatKst } from '@/lib/time'
import { cn } from '@/lib/utils'

const SESSION_LIST_LIMIT = 200
const SESSION_ACTIVITY_LIMIT = 30

function SummaryStat({
  label,
  value,
  tone,
  highlight,
}: {
  label: string
  value: number
  tone?: string
  highlight?: boolean
}) {
  return (
    <div className={cn('rounded-lg border bg-card px-3 py-2.5', highlight && 'border-amber-500/40 bg-amber-500/[0.08]')}>
      <div className={cn('text-[0.6875rem] text-muted-foreground', highlight && 'font-medium text-amber-300')}>{label}</div>
      <div className={cn('mt-0.5 text-xl font-semibold tabular-nums tracking-tight', tone)}>{value}</div>
    </div>
  )
}

function StatusSwitch({ value, onChange }: { value: SessionStatusFilter; onChange: (v: SessionStatusFilter) => void }) {
  return (
    <div className="flex gap-0.5 rounded-md bg-muted p-0.5" role="group" aria-label="상태 필터">
      {SESSION_STATUS_FILTERS.map((opt) => (
        <button
          key={opt.value}
          type="button"
          aria-pressed={value === opt.value}
          onClick={() => onChange(opt.value)}
          className={cn(
            'cursor-pointer rounded px-2.5 py-0.5 text-xs transition-colors',
            value === opt.value ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
          )}
        >
          {opt.label}
        </button>
      ))}
    </div>
  )
}

function LocationSummary({ location }: { location: SessionLocation | null }) {
  if (!location) return null
  const hasBranch = Boolean(location.branch)
  const dirty = location.dirty_count ?? 0
  if (!hasBranch && dirty <= 0) return null
  return (
    <span className="inline-flex items-center gap-1.5 font-mono text-[0.6875rem] text-muted-foreground">
      {hasBranch ? (
        <span className="inline-flex max-w-44 items-center gap-1 truncate" title={location.branch ?? undefined}>
          <GitBranchIcon className="size-3 shrink-0" />
          <span className="truncate">{location.branch}</span>
        </span>
      ) : null}
      {dirty > 0 ? <span className="text-amber-400 tabular-nums">변경 {dirty}</span> : null}
    </span>
  )
}

function SessionNodeItem({
  node,
  depth,
  selectedKey,
  onSelect,
}: {
  node: SessionTreeNode
  depth: number
  selectedKey: string | null
  onSelect: (ref: SessionRef) => void
}) {
  const s = node.session
  const key = sessionKey(s)
  const selected = key === selectedKey
  const isWaiting = s.state !== null

  return (
    <li className="list-none">
      <div
        className={cn(
          'flex items-start gap-2 rounded-md border px-2.5 py-2 transition-colors',
          selected
            ? 'border-ring/60 bg-accent/60'
            : isWaiting
              ? 'border-amber-500/30 bg-amber-500/[0.04] hover:bg-amber-500/[0.08]'
              : 'border-transparent hover:bg-accent/40',
        )}
      >
        {depth > 0 ? <CornerDownRightIcon className="mt-1 size-3.5 shrink-0 text-muted-foreground/60" aria-hidden="true" /> : null}
        <button
          type="button"
          onClick={() => onSelect({ actor: s.actor, session_id: s.session_id })}
          aria-pressed={selected}
          className="min-w-0 flex-1 cursor-pointer text-left outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm"
        >
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <ActorChip actor={s.actor} />
            <span className="font-mono text-xs text-foreground/90 break-all">{s.session_id}</span>
            {!s.named ? <Badge variant="outline">기본 ID</Badge> : null}
            {s.role ? <SessionRoleBadge role={s.role} /> : null}
            <SessionStatusBadge session={s} />
            {node.crossHost ? (
              <Badge variant="info" title={`부모와 다른 호스트: ${s.last_host}`}>
                <ServerIcon className="size-3" />
                {s.last_host || '알 수 없음'}
              </Badge>
            ) : null}
          </div>
          {isWaiting && s.state_reason ? (
            <p className="mt-1 truncate text-xs text-amber-200/90" title={s.state_reason}>
              {s.state_reason}
            </p>
          ) : null}
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[0.6875rem] text-muted-foreground">
            {s.last_project ? <span className="font-mono text-foreground/70">{s.last_project}</span> : null}
            <LocationSummary location={s.location} />
            <time dateTime={s.last_seen_at} title={`${formatKst(s.last_seen_at)} KST`} className="ml-auto font-mono tabular-nums">
              {formatKst(s.last_seen_at)}
            </time>
          </div>
        </button>
        {s.issue ? (
          <IssueLink
            issueKey={s.issue}
            className="mt-0.5 shrink-0 rounded border border-border/80 bg-background/60 px-1.5 py-0.5 transition-colors hover:border-ring hover:bg-accent"
            title={`이슈 ${s.issue} 열기`}
          >
            <IssueKey issueKey={s.issue} className="text-foreground/90" />
          </IssueLink>
        ) : null}
      </div>
      {node.children.length > 0 ? (
        <ul className="mt-1 ml-3.5 space-y-1 border-l border-border/70 pl-3">
          {node.children.map((child) => (
            <SessionNodeItem key={sessionKey(child.session)} node={child} depth={depth + 1} selectedKey={selectedKey} onSelect={onSelect} />
          ))}
        </ul>
      ) : null}
    </li>
  )
}

function HostGroupCard({
  group,
  selectedKey,
  onSelect,
}: {
  group: HostSessionGroup
  selectedKey: string | null
  onSelect: (ref: SessionRef) => void
}) {
  const hasWaiting = group.waitingCount > 0
  return (
    <section
      className={cn(
        'overflow-hidden rounded-lg border bg-card/40',
        hasWaiting && 'border-amber-500/40 bg-amber-500/[0.02]',
      )}
    >
      <header
        className={cn(
          'flex h-9 items-center gap-2 border-b px-3',
          hasWaiting && 'border-amber-500/30 bg-amber-500/10',
        )}
      >
        <ServerIcon className={cn('size-3.5 shrink-0 text-muted-foreground', hasWaiting && 'text-amber-300')} />
        <h3 className={cn('font-mono text-xs font-semibold', hasWaiting && 'text-amber-200')}>{group.host || '알 수 없음'}</h3>
        <span className="text-[0.6875rem] tabular-nums text-muted-foreground">전체 {group.totalCount}</span>
        <span className="text-[0.6875rem] tabular-nums text-emerald-400">활성 {group.activeCount}</span>
        {group.waitingCount > 0 ? (
          <Badge variant="warning" className="ml-auto tabular-nums">
            대기 {group.waitingCount}
          </Badge>
        ) : null}
      </header>
      <ul className="space-y-1 p-2">
        {group.roots.map((root) => (
          <SessionNodeItem key={sessionKey(root.session)} node={root} depth={0} selectedKey={selectedKey} onSelect={onSelect} />
        ))}
      </ul>
    </section>
  )
}

function DetailField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[6.5rem_1fr] gap-2 py-1 text-xs">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words text-foreground/90">{children}</dd>
    </div>
  )
}

function SessionLocationDetails({ location }: { location: SessionLocation }) {
  return (
    <div className="rounded-md border border-border/70 bg-background/40 px-3 py-2">
      <div className="mb-1 text-[0.6875rem] font-medium text-muted-foreground">작업 위치</div>
      <dl className="divide-y divide-border/40">
        <DetailField label="원격 저장소">
          <span className="font-mono text-[0.75rem] break-all">{location.repo_remote ?? '-'}</span>
        </DetailField>
        <DetailField label="브랜치">
          <span className="font-mono text-[0.75rem]">{location.branch ?? '-'}</span>
        </DetailField>
        <DetailField label="워크트리">
          <span className="font-mono text-[0.75rem] break-all">{location.worktree ?? '-'}</span>
        </DetailField>
        <DetailField label="커밋">
          <span className="font-mono text-[0.75rem]">{location.head ?? '-'}</span>
        </DetailField>
        <DetailField label="변경 파일">
          {location.dirty_count === null ? (
            '-'
          ) : location.dirty_count > 0 ? (
            <span className="font-mono text-amber-400 tabular-nums">{location.dirty_count}개</span>
          ) : (
            <span className="text-muted-foreground">0개</span>
          )}
        </DetailField>
        <DetailField label="보고 시각">
          {location.reported_at ? (
            <time dateTime={location.reported_at} className="font-mono tabular-nums">
              {formatKst(location.reported_at)}
            </time>
          ) : (
            '-'
          )}
        </DetailField>
      </dl>
    </div>
  )
}

function SessionActivityTimeline({ selected }: { selected: SessionRef }) {
  const q = useActivity({
    project: null,
    actor: selected.actor,
    kind: null,
    session: selected.session_id,
    limit: SESSION_ACTIVITY_LIMIT,
  })
  const items = q.data?.items

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-semibold">활동 타임라인</h4>
        {q.data ? <span className="text-[0.6875rem] tabular-nums text-muted-foreground">{q.data.total}건</span> : null}
      </div>
      {q.isPending ? <Loading className="py-4" /> : null}
      {q.isError ? <QueryError error={q.error} onRetry={() => void q.refetch()} className="m-0" /> : null}
      {items && items.length === 0 ? <EmptyState className="py-6">이 세션의 활동 기록이 없습니다.</EmptyState> : null}
      {items && items.length > 0 ? (
        <div className="overflow-hidden rounded-md border border-border/70 bg-background/40">
          <ul>
            {items.map((a) => (
              <ActivityItem key={a.id} activity={a} />
            ))}
          </ul>
          {q.data && q.data.total > items.length ? (
            <div className="border-t border-border/60 px-3 py-2 text-center text-[0.6875rem] text-muted-foreground">
              총 {q.data.total}건 중 최근 {items.length}건
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

function SessionDetailBody({ session, onSelect }: { session: Session; onSelect: (ref: SessionRef) => void }) {
  const children = session.children ?? []
  return (
    <div className="space-y-4">
      {session.state !== null ? (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 space-y-1.5">
          <div className="flex flex-wrap items-center gap-2">
            <WaitingStateBadge state={session.state} />
            {session.state_at ? (
              <time dateTime={session.state_at} className="font-mono text-[0.6875rem] tabular-nums text-amber-200/80">
                대기 시작 {formatKst(session.state_at)}
              </time>
            ) : null}
          </div>
          {session.state_reason ? <p className="text-xs text-amber-100">{session.state_reason}</p> : null}
        </div>
      ) : null}

      <div className="rounded-md border border-border/70 bg-background/40 px-3 py-2">
        <dl className="divide-y divide-border/40">
          <DetailField label="호스트">
            <span className="font-mono text-[0.75rem]">{session.last_host || '-'}</span>
          </DetailField>
          <DetailField label="프로젝트">
            {session.last_project ? <span className="font-mono text-[0.75rem]">{session.last_project}</span> : '-'}
          </DetailField>
          <DetailField label="맡은 이슈">
            {session.issue ? (
              <IssueLink issueKey={session.issue} className="inline-flex items-center hover:underline">
                <IssueKey issueKey={session.issue} className="text-foreground/90" />
              </IssueLink>
            ) : (
              '-'
            )}
          </DetailField>
          <DetailField label="첫 호출">
            <time dateTime={session.first_seen_at} className="font-mono tabular-nums">
              {formatKst(session.first_seen_at)}
            </time>
          </DetailField>
          <DetailField label="마지막 호출">
            <time dateTime={session.last_seen_at} className="font-mono tabular-nums">
              {formatKst(session.last_seen_at)}
            </time>
          </DetailField>
          <DetailField label="부모 세션">
            {session.parent ? (
              <button
                type="button"
                onClick={() => onSelect(session.parent!)}
                className="cursor-pointer font-mono text-[0.75rem] text-sky-400 hover:underline text-left break-all"
              >
                {sessionRefLabel(session.parent)}
              </button>
            ) : (
              '-'
            )}
          </DetailField>
        </dl>
      </div>

      {session.location ? <SessionLocationDetails location={session.location} /> : null}

      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <h4 className="text-xs font-semibold">자식 세션</h4>
          <span className="text-[0.6875rem] tabular-nums text-muted-foreground">{children.length}개</span>
        </div>
        {children.length === 0 ? (
          <div className="rounded-md border border-border/60 bg-background/30 px-3 py-2 text-xs text-muted-foreground">없음</div>
        ) : (
          <ul className="divide-y divide-border/50 overflow-hidden rounded-md border border-border/70 bg-background/40">
            {children.map((child) => (
              <li key={sessionKey(child)}>
                <button
                  type="button"
                  onClick={() => onSelect({ actor: child.actor, session_id: child.session_id })}
                  className="flex w-full cursor-pointer flex-wrap items-center gap-1.5 px-3 py-2 text-left text-xs transition-colors hover:bg-accent/40"
                >
                  <ActorChip actor={child.actor} />
                  <span className="font-mono text-[0.75rem] break-all">{child.session_id}</span>
                  {child.role ? <SessionRoleBadge role={child.role} /> : null}
                  <SessionStatusBadge session={child} className="ml-auto" />
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      <SessionActivityTimeline selected={{ actor: session.actor, session_id: session.session_id }} />
    </div>
  )
}

function SessionDetailPanel({
  selected,
  onSelect,
  onClose,
}: {
  selected: SessionRef
  onSelect: (ref: SessionRef) => void
  onClose: () => void
}) {
  const q = useSession(selected.actor, selected.session_id)
  const s = q.data

  return (
    <aside
      aria-label="세션 상세"
      className="flex min-h-0 max-h-[50vh] w-full shrink-0 flex-col overflow-hidden border-t bg-card/30 lg:max-h-none lg:w-[27rem] lg:border-t-0 lg:border-l xl:w-[31rem]"
    >
      <header className="flex shrink-0 items-start gap-2 border-b px-4 py-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <ActorChip actor={selected.actor} />
            <span className="font-mono text-xs font-semibold break-all">{selected.session_id}</span>
          </div>
          {s ? (
            <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
              {!s.named ? <Badge variant="outline">기본 ID</Badge> : null}
              {s.role ? <SessionRoleBadge role={s.role} /> : null}
              <SessionStatusBadge session={s} />
            </div>
          ) : null}
        </div>
        <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="상세 닫기">
          <XIcon />
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {q.isPending ? <Loading /> : null}
        {q.isError ? <QueryError error={q.error} onRetry={() => void q.refetch()} className="m-0" /> : null}
        {s ? <SessionDetailBody session={s} onSelect={onSelect} /> : null}
      </div>
    </aside>
  )
}

export function SessionsPage() {
  const { project } = useAppState()
  const [params, setParams] = useSearchParams()

  const statusFilter = parseSessionStatusFilter(params.get('status'))
  const allProjects = project === null || params.get('scope') === 'all'
  const effectiveProject = allProjects ? undefined : project ?? undefined
  const namedOnly = params.get('named') === 'true'

  const selectedActor = params.get('actor')
  const selectedSessionId = params.get('session')
  const selected: SessionRef | null = selectedActor && selectedSessionId ? { actor: selectedActor, session_id: selectedSessionId } : null
  const selectedKey = selected ? sessionKey(selected) : null

  const updateParams = (patch: Record<string, string | null>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v)
      else next.delete(k)
    }
    setParams(next, { replace: true })
  }

  const sessionsQuery = useSessions({
    project: effectiveProject,
    named: namedOnly ? true : undefined,
    limit: SESSION_LIST_LIMIT,
  })

  const allItems = sessionsQuery.data?.items
  const summary = useMemo(() => summarizeSessions(allItems ?? []), [allItems])
  const filteredItems = useMemo(() => filterSessionsByStatus(allItems ?? [], statusFilter), [allItems, statusFilter])
  const hostGroups = useMemo(() => buildHostSessionTrees(filteredItems), [filteredItems])

  const selectSession = (ref: SessionRef) => {
    updateParams({ actor: ref.actor, session: ref.session_id })
  }

  const closeDetail = () => {
    updateParams({ actor: null, session: null })
  }

  return (
    <>
      <PageHeader title="세션" meta={effectiveProject ? <span className="font-mono text-foreground/80">{effectiveProject}</span> : '전체 프로젝트'}>
        {sessionsQuery.data ? <span className="text-[0.6875rem] tabular-nums text-muted-foreground">{sessionsQuery.data.total}개</span> : null}
      </PageHeader>

      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b px-4 py-2">
        <StatusSwitch value={statusFilter} onChange={(v) => updateParams({ status: v === 'all' ? null : v })} />
        {project !== null ? (
          <div className="flex gap-0.5 rounded-md bg-muted p-0.5" role="group" aria-label="프로젝트 범위">
            <button
              type="button"
              aria-pressed={!allProjects}
              onClick={() => updateParams({ scope: null })}
              className={cn(
                'cursor-pointer rounded px-2.5 py-0.5 text-xs transition-colors',
                !allProjects ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
              )}
            >
              현재 프로젝트 ({project})
            </button>
            <button
              type="button"
              aria-pressed={allProjects}
              onClick={() => updateParams({ scope: 'all' })}
              className={cn(
                'cursor-pointer rounded px-2.5 py-0.5 text-xs transition-colors',
                allProjects ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
              )}
            >
              전체 프로젝트
            </button>
          </div>
        ) : null}
        <Button
          variant={namedOnly ? 'secondary' : 'outline'}
          size="xs"
          aria-pressed={namedOnly}
          onClick={() => updateParams({ named: namedOnly ? null : 'true' })}
        >
          이름 있는 세션만
        </Button>
      </div>

      <div className="flex min-h-0 flex-1 flex-col overflow-hidden lg:flex-row">
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
          {sessionsQuery.isPending ? <Loading /> : null}
          {sessionsQuery.isError ? <QueryError error={sessionsQuery.error} onRetry={() => void sessionsQuery.refetch()} className="m-0" /> : null}
          {sessionsQuery.data ? (
            <>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                <SummaryStat label="전체" value={summary.total} />
                <SummaryStat label="활성" value={summary.active} tone={summary.active > 0 ? 'text-emerald-400' : undefined} />
                <SummaryStat
                  label="대기 중"
                  value={summary.waiting}
                  tone={summary.waiting > 0 ? 'text-amber-300' : undefined}
                  highlight={summary.waiting > 0}
                />
                <SummaryStat label="멈춤" value={summary.stale} tone="text-muted-foreground" />
              </div>

              {hostGroups.length === 0 ? (
                <EmptyState>
                  {summary.total === 0 ? '기록된 세션이 없습니다.' : '조건에 맞는 세션이 없습니다.'}
                </EmptyState>
              ) : (
                <div className={cn('space-y-3', sessionsQuery.isPlaceholderData && 'opacity-70 transition-opacity')}>
                  {hostGroups.map((group) => (
                    <HostGroupCard key={group.host} group={group} selectedKey={selectedKey} onSelect={selectSession} />
                  ))}
                  {sessionsQuery.data.total > sessionsQuery.data.items.length ? (
                    <div className="py-2 text-center text-[0.6875rem] text-muted-foreground">
                      총 {sessionsQuery.data.total}개 중 최근 {sessionsQuery.data.items.length}개 표시
                    </div>
                  ) : null}
                </div>
              )}
            </>
          ) : null}
        </div>

        {selected ? <SessionDetailPanel selected={selected} onSelect={selectSession} onClose={closeDetail} /> : null}
      </div>
    </>
  )
}
