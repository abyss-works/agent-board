import { toast } from 'sonner'
import { useEpics, useSetMilestoneStage, useStageMilestones } from '@/api/context-queries'
import type { StageMilestone } from '@/api/context-types'
import { useAppState } from '@/app/state'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { STAGES, groupByStage } from '@/lib/context'
import { toastError } from '@/lib/toast'

function MilestoneCard({ m, next, nextLabel }: { m: StageMilestone; next: (typeof STAGES)[number]['next']; nextLabel: string | null }) {
  const set = useSetMilestoneStage()
  const percent = m.progress.total > 0 ? Math.round((m.progress.done / m.progress.total) * 100) : 0
  return (
    <li className="space-y-2 rounded-md border border-border/60 p-3">
      <div className="truncate text-[0.8125rem] font-medium">{m.name}</div>
      {m.target_date ? <div className="text-[0.6875rem] text-muted-foreground">목표 {m.target_date}</div> : null}
      <div className="flex items-center gap-2">
        <div role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent} className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
          <div className="h-full rounded-full bg-indigo-400" style={{ width: `${percent}%` }} />
        </div>
        <span className="text-[0.6875rem] tabular-nums text-muted-foreground">{m.progress.done}/{m.progress.total}</span>
      </div>
      {next ? (
        <Button
          size="xs"
          variant="secondary"
          disabled={set.isPending}
          onClick={() =>
            set.mutate(
              { id: m.id, status: next },
              { onSuccess: () => toast.success(`${m.name}: ${nextLabel}`), onError: (err) => toastError(err, '단계를 바꾸지 못했습니다') },
            )
          }
        >
          {nextLabel}
        </Button>
      ) : null}
    </li>
  )
}

export function StagesPage() {
  const { project } = useAppState()
  const epics = useEpics(project)
  const milestones = useStageMilestones(project)
  const groups = groupByStage(milestones.data?.items ?? [])
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader title="에픽·마일스톤" meta={project ? `${project} · 에픽 → 마일스톤(계획·진행·완료) → 이슈` : '프로젝트를 고르세요'} />
      <div className="min-h-0 flex-1 space-y-6 overflow-y-auto p-4">
        <section aria-label="에픽">
          <h3 className="mb-2 text-xs font-medium text-muted-foreground">에픽</h3>
          {epics.isPending && project ? <Loading /> : null}
          {epics.isError ? <QueryError error={epics.error} onRetry={() => void epics.refetch()} /> : null}
          {epics.data && epics.data.items.length === 0 ? <EmptyState>에픽이 없습니다.</EmptyState> : null}
          <ul className="space-y-1">
            {epics.data?.items.map((e) => (
              <li key={e.key} className="flex items-center gap-2 text-xs">
                <span className="font-mono text-muted-foreground">{e.key}</span>
                <span className="truncate">{e.title}</span>
                <Badge variant={e.status === 'open' ? 'success' : 'default'}>{e.status === 'open' ? '열림' : '닫힘'}</Badge>
              </li>
            ))}
          </ul>
        </section>
        <section aria-label="마일스톤 단계" className="grid grid-cols-3 gap-4">
          {milestones.isError ? <QueryError error={milestones.error} onRetry={() => void milestones.refetch()} className="col-span-3" /> : null}
          {STAGES.map((s) => (
            <div key={s.value} className="space-y-2">
              <h3 className="flex items-center gap-2 text-xs font-medium text-muted-foreground">{s.label}<Badge variant="outline">{groups[s.value].length}</Badge></h3>
              <ul className="space-y-2">
                {groups[s.value].map((m) => <MilestoneCard key={m.id} m={m} next={s.next} nextLabel={s.nextLabel} />)}
              </ul>
              {milestones.data && groups[s.value].length === 0 ? <p className="text-[0.6875rem] text-muted-foreground">없음</p> : null}
            </div>
          ))}
        </section>
      </div>
    </div>
  )
}
