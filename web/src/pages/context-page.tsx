import { ArrowLeftIcon, ArrowRightIcon, SearchIcon } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useContextSearch } from '@/api/context-queries'
import type { ContextItem, ContextNeighbor } from '@/api/context-types'
import { useAppState } from '@/app/state'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { PageHeader } from '@/components/layout/page-header'
import { Markdown } from '@/components/markdown'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { EDGE_KIND_LABEL, itemPath, scorePercent, splitNeighbors } from '@/lib/context'

function nodePath(n: Pick<ContextNeighbor, 'node_type' | 'key_or_name'>, project: string): string | null {
  return itemPath({ node_type: n.node_type, project, key_or_name: n.key_or_name })
}

function NeighborRow({ n, project }: { n: ContextNeighbor; project: string }) {
  const to = nodePath(n, project)
  const label = `${n.key_or_name} ${n.title}`.trim()
  return (
    <li className="flex items-center gap-2 py-0.5 text-xs">
      <Badge variant="outline">{EDGE_KIND_LABEL[n.kind] ?? n.kind}</Badge>
      <span className="text-[0.625rem] text-muted-foreground">{n.node_type}</span>
      {to ? (
        <Link to={to} className="truncate hover:underline">{label}</Link>
      ) : (
        <span className="truncate">{label}</span>
      )}
      {n.hop && n.hop > 1 ? <span className="text-[0.625rem] text-muted-foreground">{n.hop}단계</span> : null}
      {n.origin === 'inferred' ? <Badge variant="warning">추정</Badge> : null}
    </li>
  )
}

/** 항목에 닿은 간선을 두 방향으로 나눠 보인다. 나가는 간선과 들어오는 간선이 모두 같은 힘으로 보인다. */
export function NeighborGraph({ item }: { item: ContextItem }) {
  const { inbound, outbound } = splitNeighbors(item.neighbors)
  if (inbound.length === 0 && outbound.length === 0) return <p className="text-xs text-muted-foreground">확정된 연결이 없습니다.</p>
  return (
    <div className="grid grid-cols-2 gap-4" aria-label="양방향 연결">
      <div>
        <h4 className="mb-1 flex items-center gap-1 text-[0.6875rem] font-medium text-muted-foreground"><ArrowLeftIcon className="size-3" />들어오는 연결 {inbound.length}</h4>
        <ul>{inbound.map((n) => <NeighborRow key={`${n.edge_id}-${n.direction}`} n={n} project={item.project} />)}</ul>
      </div>
      <div>
        <h4 className="mb-1 flex items-center gap-1 text-[0.6875rem] font-medium text-muted-foreground"><ArrowRightIcon className="size-3" />나가는 연결 {outbound.length}</h4>
        <ul>{outbound.map((n) => <NeighborRow key={`${n.edge_id}-${n.direction}`} n={n} project={item.project} />)}</ul>
      </div>
    </div>
  )
}

function ItemCard({ item }: { item: ContextItem }) {
  const to = itemPath(item)
  const title = item.title || item.key_or_name
  return (
    <article className="space-y-3 border-b border-border/60 px-4 py-4 last:border-b-0">
      <header className="flex items-center gap-2">
        <Badge variant="outline">{item.node_type}</Badge>
        <span className="font-mono text-[0.6875rem] text-muted-foreground">{item.project}</span>
        {to ? <Link to={to} className="truncate text-[0.8125rem] font-medium hover:underline">{title}</Link> : <span className="truncate text-[0.8125rem] font-medium">{title}</span>}
        <Badge variant="info" className="ml-auto" title={`점수 ${item.score.toFixed(3)}`}>{scorePercent(item.score)}%</Badge>
      </header>
      {item.superseded_by ? (
        <p className="text-xs text-amber-400">
          대체됨 → {nodePath(item.superseded_by, item.project) ? <Link className="underline" to={nodePath(item.superseded_by, item.project)!}>{item.superseded_by.key_or_name}</Link> : item.superseded_by.key_or_name}
        </p>
      ) : null}
      {item.summary ? <p className="text-xs text-muted-foreground">{item.summary}</p> : null}
      <NeighborGraph item={item} />
      {item.content ? (
        <details className="rounded-md border border-border/60 px-3 py-2">
          <summary className="cursor-pointer text-xs text-muted-foreground">본문</summary>
          <Markdown source={item.content} className="mt-2" />
        </details>
      ) : null}
    </article>
  )
}

export function ContextPage() {
  const { project } = useAppState()
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const [draft, setDraft] = useState(q)
  const [allProjects, setAllProjects] = useState(false)
  const [superseded, setSuperseded] = useState(false)
  const req = q.trim() === '' ? null : { query: q, project: allProjects || !project ? undefined : project, include_superseded: superseded || undefined, expand_hops: 1 }
  const res = useContextSearch(req)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setParams(draft.trim() ? { q: draft.trim() } : {})
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader title="통합 조회" meta="키워드와 의미 검색을 합쳐 연결된 문서까지 한 번에" />
      <form onSubmit={submit} className="flex flex-wrap items-center gap-3 border-b px-4 py-3">
        <div className="relative min-w-64 flex-1">
          <SearchIcon className="pointer-events-none absolute top-2 left-2.5 size-3.5 text-muted-foreground" />
          <Input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder="질문이나 핵심어" aria-label="질문" className="pl-8" maxLength={4096} />
        </div>
        <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <input type="checkbox" checked={allProjects} onChange={(e) => setAllProjects(e.target.checked)} />모든 프로젝트
        </label>
        <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <input type="checkbox" checked={superseded} onChange={(e) => setSuperseded(e.target.checked)} />대체된 문서 포함
        </label>
        <Button type="submit" size="sm">찾기</Button>
      </form>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {req === null ? <EmptyState>질문을 입력하세요.</EmptyState> : null}
        {req !== null && res.isPending ? <Loading /> : null}
        {res.isError ? <QueryError error={res.error} onRetry={() => void res.refetch()} /> : null}
        {res.data ? (
          <>
            {res.data.fallback_used ? <p className="border-b bg-amber-500/10 px-4 py-2 text-xs text-amber-400">일부 단계가 실패해 {res.data.fallback_mode} 경로로 대신했습니다. 순위가 덜 정확할 수 있습니다.</p> : null}
            {res.data.cutoff_applied ? <p className="border-b px-4 py-2 text-xs text-muted-foreground">1위가 2위보다 {res.data.score_gap_1_2.toFixed(2)} 앞서 1건만 보입니다.</p> : null}
            {res.data.items.length === 0 ? <EmptyState>관련 항목이 없습니다.</EmptyState> : res.data.items.map((it) => <ItemCard key={`${it.node_type}-${it.node_id}`} item={it} />)}
          </>
        ) : null}
      </div>
    </div>
  )
}
