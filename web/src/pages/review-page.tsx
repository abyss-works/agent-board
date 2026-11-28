import { CheckIcon, XIcon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useDecideEdge, useEdgeCandidates } from '@/api/context-queries'
import type { EdgeCandidate } from '@/api/context-types'
import { useAppState } from '@/app/state'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { EDGE_KIND_LABEL, candidateTarget, sortCandidates } from '@/lib/context'
import { toastError } from '@/lib/toast'

function CandidateRow({ c }: { c: EdgeCandidate }) {
  const decide = useDecideEdge()
  const act = (action: 'confirm' | 'reject') =>
    decide.mutate(
      { id: c.id, action },
      {
        onSuccess: () => toast.success(action === 'confirm' ? '연결을 확정했습니다' : '연결을 거절했습니다'),
        onError: (err) => toastError(err, '결정하지 못했습니다'),
      },
    )
  return (
    <li className="flex items-start gap-3 border-b border-border/60 px-4 py-3 last:border-b-0">
      <Badge variant={c.kind === 'supersedes' ? 'warning' : 'outline'}>{EDGE_KIND_LABEL[c.kind] ?? c.kind}</Badge>
      <div className="min-w-0 flex-1 space-y-1 text-xs">
        <div className="flex items-center gap-1.5">
          <span className="truncate font-medium">{c.source_key_or_name ?? `${c.source_type}:${c.source_id}`}</span>
          <span className="text-muted-foreground">→</span>
          <span className="truncate font-medium">{candidateTarget(c)}</span>
          {c.confidence != null ? <span className="text-[0.6875rem] tabular-nums text-muted-foreground">{Math.round(c.confidence * 100)}%</span> : null}
        </div>
        {c.evidence ? <p className="text-muted-foreground">{c.evidence}</p> : null}
      </div>
      <Button size="xs" variant="secondary" disabled={decide.isPending} onClick={() => act('confirm')} aria-label={`연결 ${c.id} 확정`}><CheckIcon />확정</Button>
      <Button size="xs" variant="ghost" disabled={decide.isPending} onClick={() => act('reject')} aria-label={`연결 ${c.id} 거절`}><XIcon />거절</Button>
    </li>
  )
}

export function ReviewPage() {
  const { project } = useAppState()
  const [onlySupersedes, setOnlySupersedes] = useState(true)
  const list = useEdgeCandidates(project, onlySupersedes ? 'supersedes' : null)
  const items = sortCandidates(list.data?.items ?? [])
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader title="연결 검토" meta={project ? `${project} · 확정한 연결만 조회 결과에 반영됩니다` : '프로젝트를 고르세요'}>
        <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <input type="checkbox" checked={onlySupersedes} onChange={(e) => setOnlySupersedes(e.target.checked)} />대체 후보만
        </label>
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {list.isPending ? <Loading /> : null}
        {list.isError ? <QueryError error={list.error} onRetry={() => void list.refetch()} /> : null}
        {list.data && items.length === 0 ? <EmptyState>검토할 연결 후보가 없습니다.</EmptyState> : null}
        <ul>{items.map((c) => <CandidateRow key={c.id} c={c} />)}</ul>
      </div>
    </div>
  )
}
