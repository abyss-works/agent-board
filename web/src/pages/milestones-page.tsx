import { CircleCheckIcon, EllipsisIcon, PencilIcon, PlusIcon, RotateCcwIcon, Trash2Icon } from 'lucide-react'
import { useState, type ComponentProps } from 'react'
import { Link, useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { useMilestones, usePatchMilestone } from '@/api/queries'
import type { Milestone, MilestoneProgress, MilestoneStatus } from '@/api/types'
import { useAppState } from '@/app/state'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { PageHeader } from '@/components/layout/page-header'
import { MilestoneDeleteDialog } from '@/components/milestone/milestone-delete-dialog'
import { MilestoneDialog } from '@/components/milestone/milestone-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useFlash } from '@/hooks/use-flash'
import { MILESTONE_STATUS_LABEL } from '@/lib/labels'
import { MILESTONE_VIEWS, milestoneIssuesPath, parseMilestoneView, progressLabel, progressPercent, viewStatuses, type MilestoneView } from '@/lib/milestones'
import { toastError } from '@/lib/toast'
import { cn } from '@/lib/utils'

const STATUS_BADGE: Record<MilestoneStatus, ComponentProps<typeof Badge>['variant']> = {
  open: 'success',
  closed: 'default',
  deleted: 'destructive',
}

const EMPTY_TEXT: Record<MilestoneView, string> = {
  open: '열린 마일스톤이 없습니다. 위의 새 마일스톤으로 만드세요.',
  closed: '닫힌 마일스톤이 없습니다.',
  all: '마일스톤이 없습니다. 위의 새 마일스톤으로 만드세요.',
}

function ProgressBar({ progress }: { progress: MilestoneProgress }) {
  const percent = progressPercent(progress)
  return (
    <div className="flex w-64 shrink-0 items-center gap-2.5" title={`완료 ${progress.done}건 / 대상 ${progress.total}건 (취소·삭제한 이슈는 세지 않음)`}>
      <div
        role="progressbar"
        aria-label="진행률"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
        className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted"
      >
        <div className="h-full rounded-full bg-indigo-400" style={{ width: `${percent}%` }} />
      </div>
      <span className="w-24 shrink-0 text-right text-[0.6875rem] tabular-nums text-muted-foreground">{progressLabel(progress)}</span>
    </div>
  )
}

function RowMenu({ milestone: m, onEdit, onDelete }: { milestone: Milestone; onEdit: (m: Milestone) => void; onDelete: (m: Milestone) => void }) {
  const patch = usePatchMilestone()
  const closing = m.status === 'open'
  const toggle = () =>
    patch.mutate(
      { id: m.id, patch: { status: closing ? 'closed' : 'open' } },
      {
        onSuccess: () => toast.success(`마일스톤 ${m.name}을(를) ${closing ? '닫았습니다' : '다시 열었습니다'}`),
        onError: (err) => toastError(err, closing ? '마일스톤을 닫지 못했습니다' : '마일스톤을 다시 열지 못했습니다'),
      },
    )
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label={`${m.name} 작업`} title="작업">
          <EllipsisIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={() => onEdit(m)}>
          <PencilIcon />
          수정
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={toggle} disabled={patch.isPending}>
          {closing ? <CircleCheckIcon /> : <RotateCcwIcon />}
          {closing ? '닫기' : '다시 열기'}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onSelect={() => onDelete(m)}>
          <Trash2Icon />
          삭제
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** 행 전체가 그 마일스톤의 이슈 목록으로 가는 링크이고, 오른쪽 끝의 메뉴가 수정·닫기·삭제를 연다. */
function MilestoneRow({ milestone: m, flash, onEdit, onDelete }: { milestone: Milestone; flash: boolean; onEdit: (m: Milestone) => void; onDelete: (m: Milestone) => void }) {
  const deleted = m.status === 'deleted'
  return (
    <li className={cn('flex items-center border-b border-border/60 transition-colors last:border-b-0 hover:bg-accent/40', flash && 'animate-flash')}>
      <Link to={milestoneIssuesPath(m.id)} className="flex min-w-0 flex-1 items-center gap-5 py-2.5 pr-2 pl-4" title="이 마일스톤의 이슈 보기">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className={cn('truncate text-[0.8125rem] font-medium', deleted && 'text-muted-foreground line-through')}>{m.name}</span>
            <Badge variant={STATUS_BADGE[m.status]}>{MILESTONE_STATUS_LABEL[m.status]}</Badge>
          </div>
          {m.description ? <div className="mt-0.5 truncate text-[0.6875rem] text-muted-foreground">{m.description}</div> : null}
        </div>
        <span className={cn('w-32 shrink-0 text-[0.6875rem] tabular-nums', m.target_date ? 'text-foreground/80' : 'text-muted-foreground/70')} title="목표일">
          {m.target_date ? `목표 ${m.target_date}` : '목표일 없음'}
        </span>
        <ProgressBar progress={m.progress} />
      </Link>
      <div className="flex w-10 shrink-0 justify-center">{deleted ? null : <RowMenu milestone={m} onEdit={onEdit} onDelete={onDelete} />}</div>
    </li>
  )
}

function ViewSwitch({ view, onChange }: { view: MilestoneView; onChange: (view: MilestoneView) => void }) {
  return (
    <div className="flex gap-0.5 rounded-md bg-muted p-0.5" role="group" aria-label="보기">
      {MILESTONE_VIEWS.map((v) => (
        <button
          key={v.value}
          type="button"
          aria-pressed={view === v.value}
          onClick={() => onChange(v.value)}
          className={cn(
            'cursor-pointer rounded px-2.5 py-0.5 text-xs transition-colors',
            view === v.value ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
          )}
        >
          {v.label}
        </button>
      ))}
    </div>
  )
}

function MilestoneList({ projectKey, view, onEdit, onDelete }: { projectKey: string; view: MilestoneView; onEdit: (m: Milestone) => void; onDelete: (m: Milestone) => void }) {
  const q = useMilestones(projectKey, viewStatuses(view))
  const items = q.data?.items
  // 진행 수가 바뀌어도 강조하도록 수정 시각에 진행을 더해 비교한다.
  const flash = useFlash(items, (m) => m.id, (m) => `${m.updated_at}|${m.status}|${m.progress.done}/${m.progress.total}`)
  if (q.isPending) return <Loading />
  if (q.isError) return <QueryError error={q.error} onRetry={() => void q.refetch()} />
  if (!items || items.length === 0) return <EmptyState>{EMPTY_TEXT[view]}</EmptyState>
  return (
    <ul>
      {items.map((m) => (
        <MilestoneRow key={m.id} milestone={m} flash={flash.has(m.id)} onEdit={onEdit} onDelete={onDelete} />
      ))}
    </ul>
  )
}

function ProjectMilestones({ projectKey }: { projectKey: string }) {
  const [params, setParams] = useSearchParams()
  const view = parseMilestoneView(params.get('status'))
  const count = useMilestones(projectKey, viewStatuses(view)).data?.items.length
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<Milestone | null>(null)
  const [deleting, setDeleting] = useState<Milestone | null>(null)

  const changeView = (next: MilestoneView) => {
    const nextParams = new URLSearchParams(params)
    if (next === 'open') nextParams.delete('status')
    else nextParams.set('status', next)
    setParams(nextParams, { replace: true })
  }

  return (
    <>
      <PageHeader title="마일스톤" meta={<span className="font-mono text-foreground/80">{projectKey}</span>}>
        {count !== undefined ? <span className="text-[0.6875rem] tabular-nums text-muted-foreground">{count}개</span> : null}
        <ViewSwitch view={view} onChange={changeView} />
        <Button size="xs" onClick={() => setCreating(true)}>
          <PlusIcon />새 마일스톤
        </Button>
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <MilestoneList key={view} projectKey={projectKey} view={view} onEdit={setEditing} onDelete={setDeleting} />
      </div>
      <MilestoneDialog open={creating} onOpenChange={setCreating} projectKey={projectKey} />
      <MilestoneDialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)} projectKey={projectKey} milestone={editing ?? undefined} />
      <MilestoneDeleteDialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)} milestone={deleting} />
    </>
  )
}

export function MilestonesPage() {
  const { project } = useAppState()
  if (!project) {
    return (
      <>
        <PageHeader title="마일스톤" meta="프로젝트를 고르면 마일스톤을 보여 줍니다" />
        <EmptyState>사이드바의 프로젝트 메뉴에서 프로젝트를 고르세요.</EmptyState>
      </>
    )
  }
  return <ProjectMilestones key={project} projectKey={project} />
}
