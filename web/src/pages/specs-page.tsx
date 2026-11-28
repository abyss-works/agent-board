import { Link, useParams } from 'react-router'
import { useSpecRevisions, useSpecs } from '@/api/context-queries'
import { useAppState } from '@/app/state'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { PageHeader } from '@/components/layout/page-header'
import { Markdown } from '@/components/markdown'
import { Badge } from '@/components/ui/badge'

export function SpecsPage() {
  const { project } = useAppState()
  const specs = useSpecs(project)
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader title="스펙" meta={project ?? '프로젝트를 고르세요'} />
      <div className="min-h-0 flex-1 overflow-y-auto">
        {specs.isPending && project ? <Loading /> : null}
        {specs.isError ? <QueryError error={specs.error} onRetry={() => void specs.refetch()} /> : null}
        {specs.data && specs.data.items.length === 0 ? <EmptyState>스펙 문서가 없습니다.</EmptyState> : null}
        <ul>
          {specs.data?.items.map((s) => (
            <li key={s.slug}>
              <Link to={`/specs/${encodeURIComponent(s.project)}/${encodeURIComponent(s.slug)}`} className="flex items-center gap-3 border-b border-border/60 px-4 py-2 hover:bg-accent/40">
                <span className="truncate text-[0.8125rem]">{s.title || s.slug}</span>
                <span className="font-mono text-[0.6875rem] text-muted-foreground">{s.slug}</span>
                <Badge variant="outline" className="ml-auto">개정 {s.current_revision_no}</Badge>
              </Link>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

export function SpecHistoryPage() {
  const { project = '', slug = '' } = useParams()
  const revs = useSpecRevisions(project, slug)
  const items = [...(revs.data?.items ?? [])].sort((a, b) => b.revision_no - a.revision_no)
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader title={`스펙 개정 이력 · ${slug}`} meta={project}>
        <Link to="/specs" className="text-xs text-muted-foreground hover:underline">목록</Link>
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {revs.isPending ? <Loading /> : null}
        {revs.isError ? <QueryError error={revs.error} onRetry={() => void revs.refetch()} /> : null}
        {revs.data && items.length === 0 ? <EmptyState>개정이 없습니다.</EmptyState> : null}
        {items.map((r, i) => (
          <details key={r.id} open={i === 0} className="border-b border-border/60 px-4 py-3">
            <summary className="flex cursor-pointer items-center gap-2 text-xs">
              <Badge variant={i === 0 ? 'success' : 'outline'}>개정 {r.revision_no}{i === 0 ? ' · 현재' : ''}</Badge>
              <span className="truncate">{r.change_summary || r.title}</span>
              <span className="ml-auto text-[0.6875rem] text-muted-foreground">{r.created_by} · {r.created_at}</span>
            </summary>
            <Markdown source={r.content} className="mt-3" />
          </details>
        ))}
      </div>
    </div>
  )
}
