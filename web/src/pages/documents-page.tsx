import { ArchiveIcon, FileTextIcon, SearchIcon, XIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useDocuments, type DocumentFilters } from '@/api/queries'
import { DOCUMENT_KINDS, DOCUMENT_LOCATIONS, type DocumentKind, type DocumentLocation, type RecordDocument } from '@/api/types'
import { useAppState } from '@/app/state'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { LabelChip } from '@/components/issue/meta'
import { SnippetText } from '@/components/issue/issue-row'
import { PageHeader } from '@/components/layout/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { documentPath } from '@/lib/documents'
import { DOCUMENT_KIND_LABEL, DOCUMENT_LOCATION_LABEL } from '@/lib/labels'
import { cn } from '@/lib/utils'
import { FilterMenu } from './issues-page'

function csv(v: string | null): string[] {
  return v ? v.split(',').filter(Boolean) : []
}

function DocumentRow({ doc, showProject }: { doc: RecordDocument; showProject: boolean }) {
  return (
    <Link
      to={documentPath(doc.project, doc.name)}
      className="flex items-start gap-3 border-b border-border/60 px-4 py-2 transition-colors last:border-b-0 hover:bg-accent/40"
    >
      <span className="w-28 shrink-0 pt-px text-[0.6875rem] tabular-nums text-muted-foreground">{doc.date ?? doc.name.slice(0, 10)}</span>
      {showProject ? <span className="w-10 shrink-0 pt-px font-mono text-[0.6875rem] text-muted-foreground">{doc.project}</span> : null}
      <Badge variant="outline" className="shrink-0">
        {DOCUMENT_KIND_LABEL[doc.kind]}
      </Badge>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-[0.8125rem] text-foreground">{doc.title ?? doc.name}</span>
          {doc.location === 'archive' ? (
            <span className="inline-flex shrink-0 items-center gap-1 text-[0.625rem] text-muted-foreground" title="archive 폴더의 기록">
              <ArchiveIcon className="size-3" />
              archive
            </span>
          ) : null}
        </div>
        {doc.snippet ? (
          <div className="line-clamp-2">
            <SnippetText snippet={doc.snippet} />
          </div>
        ) : null}
      </div>
      {doc.tags.length > 0 ? (
        <div className="hidden shrink-0 items-center gap-1 md:flex">
          {doc.tags.slice(0, 4).map((t) => (
            <LabelChip key={t} label={t} />
          ))}
          {doc.tags.length > 4 ? <span className="text-[0.625rem] text-muted-foreground">+{doc.tags.length - 4}</span> : null}
        </div>
      ) : null}
    </Link>
  )
}

function DocumentList({ filters }: { filters: DocumentFilters }) {
  const q = useDocuments(filters)
  const items = q.data?.items
  if (q.isPending) return <Loading />
  if (q.isError) return <QueryError error={q.error} onRetry={() => void q.refetch()} />
  if (!items || items.length === 0) {
    let text = '기록 문서가 없습니다. 에이전트가 agb doc sync로 올립니다.'
    if (filters.q) text = '검색 결과가 없습니다. 붙여 쓴 말은 중간을 찾지 못하니 단어를 줄여 다시 찾으세요.'
    else if (filters.kind.length + filters.location.length > 0) text = '조건에 맞는 기록 문서가 없습니다.'
    return <EmptyState>{text}</EmptyState>
  }
  return (
    <div className={cn(q.isPlaceholderData && 'opacity-70 transition-opacity')}>
      {items.map((d) => (
        <DocumentRow key={`${d.project}/${d.name}`} doc={d} showProject={filters.project === null} />
      ))}
      {q.data.total > items.length ? (
        <div className="px-4 py-3 text-center text-[0.6875rem] text-muted-foreground">
          총 {q.data.total}건 중 {items.length}건 표시. 검색이나 필터로 범위를 좁히세요.
        </div>
      ) : null}
    </div>
  )
}

export function DocumentsPage() {
  const { project } = useAppState()
  const [params, setParams] = useSearchParams()
  const kind = csv(params.get('kind'))
  const location = csv(params.get('location'))
  const q = params.get('q') ?? ''

  const update = (patch: Record<string, string | null>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v)
      else next.delete(k)
    }
    setParams(next, { replace: true })
  }

  // 검색 입력은 300ms 뒤에 URL의 q로 옮긴다.
  const [search, setSearch] = useState(q)
  useEffect(() => {
    if (search.trim() === q.trim()) return
    const t = window.setTimeout(() => update({ q: search.trim() || null }), 300)
    return () => window.clearTimeout(t)
    // update와 q는 검색어가 바뀔 때만 다시 읽으면 된다.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search])

  const toggle = (name: 'kind' | 'location', current: readonly string[]) => (v: string, on: boolean) => {
    const set = new Set(current)
    if (on) set.add(v)
    else set.delete(v)
    update({ [name]: [...set].join(',') || null })
  }

  const filters: DocumentFilters = { project, q, kind, location }
  const total = useDocuments(filters).data?.total
  const hasFilter = kind.length + location.length > 0 || q !== ''

  return (
    <>
      <PageHeader title="기록" meta={project ? <span className="font-mono text-foreground/80">{project}</span> : '전체 프로젝트'}>
        {total !== undefined ? <span className="text-[0.6875rem] tabular-nums text-muted-foreground">{total}건</span> : null}
      </PageHeader>

      <div className="flex shrink-0 flex-wrap items-center gap-1 border-b px-3 py-1.5">
        <div className="relative mr-1">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="검색 (제목·태그·본문)" className="h-7 w-56 pl-7 text-xs" />
          {search ? (
            <button type="button" onClick={() => setSearch('')} className="absolute top-1/2 right-1.5 -translate-y-1/2 cursor-pointer text-muted-foreground hover:text-foreground" aria-label="검색어 지우기">
              <XIcon className="size-3.5" />
            </button>
          ) : null}
        </div>
        <FilterMenu
          label="종류"
          icon={<FileTextIcon className="size-3.5 text-muted-foreground" />}
          values={DOCUMENT_KINDS}
          selected={kind}
          onToggle={toggle('kind', kind)}
          render={(k: DocumentKind) => DOCUMENT_KIND_LABEL[k]}
        />
        <FilterMenu
          label="위치"
          icon={<ArchiveIcon className="size-3.5 text-muted-foreground" />}
          values={DOCUMENT_LOCATIONS}
          selected={location}
          onToggle={toggle('location', location)}
          render={(l: DocumentLocation) => DOCUMENT_LOCATION_LABEL[l]}
        />
        <div className="ml-auto flex items-center gap-1">
          {hasFilter ? (
            <Button
              variant="ghost"
              size="xs"
              onClick={() => {
                setSearch('')
                setParams(new URLSearchParams(), { replace: true })
              }}
            >
              <XIcon />
              필터 지우기
            </Button>
          ) : null}
          <span className="px-1 text-[0.6875rem] text-muted-foreground">{q ? '관련도 순' : '최신 순'}</span>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <DocumentList key={`${project ?? '*'}|${params.toString()}`} filters={filters} />
      </div>
    </>
  )
}
