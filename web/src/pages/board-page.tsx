import {
  DndContext,
  DragOverlay,
  PointerSensor,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from '@dnd-kit/core'
import { useQueryClient } from '@tanstack/react-query'
import { CheckIcon, PlusIcon } from 'lucide-react'
import { useState } from 'react'
import { useBoard, usePatchIssue } from '@/api/queries'
import type { Board, BoardColumn, IssueStatus, IssueSummary } from '@/api/types'
import { useAppState } from '@/app/state'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { IssueCard, IssueCardBody } from '@/components/issue/issue-card'
import { IssueCreateDialog } from '@/components/issue/issue-create-dialog'
import { StatusIcon } from '@/components/issue/meta'
import { ReasonDialog } from '@/components/issue/reason-dialog'
import { PageHeader } from '@/components/layout/page-header'
import { Button } from '@/components/ui/button'
import { useFlash } from '@/hooks/use-flash'
import { boardColumns, moveIssue } from '@/lib/board'
import { STATUS_LABEL } from '@/lib/labels'
import { reasonRequired, statusChangePatch } from '@/lib/status'
import { toastError } from '@/lib/toast'
import { cn } from '@/lib/utils'

function Column({ column, flash, onAdd }: { column: BoardColumn; flash: Set<string>; onAdd: (status: IssueStatus) => void }) {
  const { setNodeRef, isOver } = useDroppable({ id: column.status })
  const closed = column.status === 'done' || column.status === 'cancelled'
  return (
    <section className="flex w-72 shrink-0 flex-col rounded-lg bg-sidebar/60">
      <header className="flex h-9 shrink-0 items-center gap-2 px-3">
        <StatusIcon status={column.status} />
        <h3 className="text-xs font-semibold">{STATUS_LABEL[column.status]}</h3>
        <span className="text-[0.6875rem] tabular-nums text-muted-foreground">{column.total}</span>
        {closed ? <span className="text-[0.625rem] text-muted-foreground/70">최근 7일</span> : null}
        {column.status !== 'cancelled' ? (
          <Button variant="ghost" size="icon-sm" className="ml-auto" onClick={() => onAdd(column.status)} title={`${STATUS_LABEL[column.status]}에 새 이슈`}>
            <PlusIcon />
          </Button>
        ) : null}
      </header>
      <div
        ref={setNodeRef}
        className={cn('min-h-0 flex-1 space-y-2 overflow-y-auto rounded-b-lg border border-transparent p-2 pt-0 transition-colors', isOver && 'border-ring/40 bg-primary/5')}
      >
        {column.issues.length === 0 ? (
          <div className="rounded-md border border-dashed border-border/60 py-6 text-center text-[0.6875rem] text-muted-foreground/70">비어 있음</div>
        ) : (
          column.issues.map((i) => <IssueCard key={i.key} issue={i} flash={flash.has(i.key)} />)
        )}
        {column.issues.length < column.total ? (
          <div className="py-1 text-center text-[0.6875rem] text-muted-foreground">{column.total - column.issues.length}건 더 있음</div>
        ) : null}
      </div>
    </section>
  )
}

function BoardView({ projectKey }: { projectKey: string | null }) {
  const { instance } = useAppState()
  const qc = useQueryClient()
  const board = useBoard(projectKey)
  const patch = usePatchIssue()
  const [showCancelled, setShowCancelled] = useState(false)
  const [active, setActive] = useState<IssueSummary | null>(null)
  const [addStatus, setAddStatus] = useState<IssueStatus | null>(null)
  // 취소·삭제 열로 끈 카드. 사유를 받을 때까지 카드를 옮기지 않는다.
  const [pendingMove, setPendingMove] = useState<{ issue: IssueSummary; to: IssueStatus } | null>(null)
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }))

  const allIssues = board.data?.columns.flatMap((c) => c.issues)
  const flash = useFlash(allIssues, (i) => i.key, (i) => i.updated_at)

  const onDragStart = (e: DragStartEvent) => {
    setActive((e.active.data.current as { issue: IssueSummary } | undefined)?.issue ?? null)
  }

  const onDragEnd = (e: DragEndEvent) => {
    setActive(null)
    const issue = (e.active.data.current as { issue: IssueSummary } | undefined)?.issue
    const to = e.over?.id as IssueStatus | undefined
    if (!issue || !to || to === issue.status) return
    if (reasonRequired(to)) setPendingMove({ issue, to })
    else move(issue, to)
  }

  const move = (issue: IssueSummary, to: IssueStatus, reason?: string) => {
    const key = [instance.id, 'board', projectKey]
    // 낙관적으로 카드를 옮기고, 실패하면 onSettled의 invalidate가 서버 상태로 되돌린다.
    qc.setQueryData<Board>(key, (cur) => (cur ? moveIssue(cur, issue.key, to) : cur))
    patch.mutate(
      { key: issue.key, patch: statusChangePatch(to, reason), version: issue.version },
      { onError: (err) => toastError(err, `${issue.key} 상태를 바꾸지 못했습니다`) },
    )
  }

  const columns = board.data ? boardColumns(board.data, showCancelled) : []

  return (
    <>
      <PageHeader title="보드" meta={projectKey ? <span className="font-mono text-foreground/80">{projectKey}</span> : '전체 프로젝트'}>
        <Button variant={showCancelled ? 'secondary' : 'ghost'} size="xs" onClick={() => setShowCancelled((v) => !v)}>
          {showCancelled ? <CheckIcon /> : null}
          취소 열 보기
        </Button>
        <Button size="xs" onClick={() => setAddStatus('backlog')}>
          <PlusIcon />새 이슈
        </Button>
      </PageHeader>
      {board.isPending ? <Loading /> : null}
      {board.isError ? <QueryError error={board.error} onRetry={() => void board.refetch()} /> : null}
      {board.data ? (
        <DndContext sensors={sensors} onDragStart={onDragStart} onDragEnd={onDragEnd} onDragCancel={() => setActive(null)}>
          <div className="flex min-h-0 flex-1 gap-3 overflow-x-auto p-4">
            {columns.map((c) => (
              <Column key={c.status} column={c} flash={flash} onAdd={setAddStatus} />
            ))}
            {columns.every((c) => c.total === 0) ? <EmptyState className="self-center">이슈가 없습니다.</EmptyState> : null}
          </div>
          <DragOverlay dropAnimation={null}>{active ? <div className="w-68"><IssueCardBody issue={active} overlay /></div> : null}</DragOverlay>
        </DndContext>
      ) : null}
      <IssueCreateDialog open={addStatus !== null} onOpenChange={(o) => !o && setAddStatus(null)} initialStatus={addStatus ?? undefined} />
      <ReasonDialog
        open={pendingMove !== null}
        onOpenChange={(o) => !o && setPendingMove(null)}
        title={pendingMove ? `${pendingMove.issue.key}를 ${STATUS_LABEL[pendingMove.to]} 상태로 바꿀까요?` : ''}
        description="취소와 삭제는 사유가 필요합니다. 사유는 이슈의 상태 사유와 활동 기록에 남습니다."
        submitLabel="바꾸기"
        onSubmit={(reason) => {
          if (!pendingMove) return
          move(pendingMove.issue, pendingMove.to, reason)
          setPendingMove(null)
        }}
      />
    </>
  )
}

export function BoardPage() {
  const { project } = useAppState()
  return <BoardView key={project ?? '*'} projectKey={project} />
}
