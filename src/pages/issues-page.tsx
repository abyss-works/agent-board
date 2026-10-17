import { ArrowUpDownIcon, FilterIcon, PlusIcon, SearchIcon, XIcon } from 'lucide-react'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import type { IssueListFilters } from '@/api/client'
import { useIssues, useLabels } from '@/api/queries'
import { ISSUE_STATUSES, ISSUE_TYPES, PRIORITIES, type IssueStatus, type IssueType, type Priority } from '@/api/types'
import { useAppState } from '@/app/state'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { IssueCreateDialog } from '@/components/issue/issue-create-dialog'
import { IssueRow } from '@/components/issue/issue-row'
import { LabelChip, PriorityIcon, StatusIcon, TypeIcon } from '@/components/issue/meta'
import { PageHeader } from '@/components/layout/page-header'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { useFlash } from '@/hooks/use-flash'
import { PRIORITY_LABEL, STATUS_LABEL, TYPE_LABEL } from '@/lib/labels'
import { cn } from '@/lib/utils'

const LIMIT = 200

const SORTS: { value: string; label: string }[] = [
  { value: '', label: '우선순위' },
  { value: '-updated_at', label: '최근 수정' },
  { value: '-created_at', label: '최근 생성' },
  { value: 'created_at', label: '오래된 생성' },
  { value: 'key', label: '키' },
]

const STATUS_PRESETS: { value: string; label: string }[] = [
  { value: '', label: '열린 이슈' },
  { value: 'all', label: '전체' },
  { value: 'closed', label: '닫힌 이슈' },
]

function csv(v: string | null): string[] {
  return v ? v.split(',').filter(Boolean) : []
}

function FilterMenu<T extends string>({
  label,
  icon,
  values,
  selected,
  onToggle,
  render,
}: {
  label: string
  icon: ReactNode
  values: readonly T[]
  selected: readonly string[]
  onToggle: (v: T, on: boolean) => void
  render: (v: T) => ReactNode
}) {
  const n = selected.length
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant={n ? 'secondary' : 'ghost'} size="xs" className={cn(n && 'text-foreground')}>
          {icon}
          {label}
          {n ? <span className="rounded-full bg-primary/20 px-1.5 text-[10px] tabular-nums text-primary">{n}</span> : null}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80">
        <DropdownMenuLabel>{label}</DropdownMenuLabel>
        {values.length === 0 ? <div className="px-2 py-1.5 text-[11px] text-muted-foreground">없음</div> : null}
        {values.map((v) => (
          <DropdownMenuCheckboxItem key={v} checked={selected.includes(v)} onCheckedChange={(on) => onToggle(v, on === true)} onSelect={(e) => e.preventDefault()}>
            {render(v)}
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function IssueList({ filters }: { filters: IssueListFilters }) {
  const q = useIssues(filters)
  const items = q.data?.items
  const flash = useFlash(items, (i) => i.key, (i) => i.updated_at)
  if (q.isPending) return <Loading />
  if (q.isError) return <QueryError error={q.error} onRetry={() => void q.refetch()} />
  if (!items || items.length === 0) return <EmptyState>조건에 맞는 이슈가 없습니다.</EmptyState>
  return (
    <div className={cn(q.isPlaceholderData && 'opacity-70 transition-opacity')}>
      {items.map((i) => (
        <IssueRow key={i.key} issue={i} flash={flash.has(i.key)} />
      ))}
      {q.data.total > items.length ? (
        <div className="px-4 py-3 text-center text-[11px] text-muted-foreground">
          총 {q.data.total}건 중 {items.length}건 표시. 필터로 범위를 좁히세요.
        </div>
      ) : null}
    </div>
  )
}

export function IssuesPage() {
  const { project } = useAppState()
  const [params, setParams] = useSearchParams()
  const labels = useLabels(project)
  const [create, setCreate] = useState(false)

  const status = csv(params.get('status'))
  const type = csv(params.get('type'))
  const priority = csv(params.get('priority'))
  const label = params.getAll('label')
  const q = params.get('q') ?? ''
  const sort = params.get('sort') ?? ''

  const paramsKey = params.toString()

  const update = (patch: Record<string, string | string[] | null>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      next.delete(k)
      if (Array.isArray(v)) for (const x of v) next.append(k, x)
      else if (v) next.set(k, v)
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

  const toggle = (name: 'status' | 'type' | 'priority' | 'label', current: readonly string[]) => (v: string, on: boolean) => {
    const set = new Set(current.filter((x) => x !== 'all' && x !== 'closed'))
    if (on) set.add(v)
    else set.delete(v)
    const list = [...set]
    update({ [name]: name === 'label' ? list : list.join(',') || null })
  }

  const filters = useMemo<IssueListFilters>(
    () => ({ project, status, type, priority, label, q, sort, limit: LIMIT }),
    // 배열은 URL 문자열에서 매번 새로 만들어지므로 문자열로 비교한다.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [project, paramsKey],
  )
  const filterKey = `${project ?? '*'}|${paramsKey}`
  const hasFilter = status.length + type.length + priority.length + label.length > 0 || q !== ''
  const statusPreset = status.length === 1 && (status[0] === 'all' || status[0] === 'closed') ? status[0] : status.length === 0 ? '' : null
  const total = useIssues(filters).data?.total

  return (
    <>
      <PageHeader title="이슈" meta={project ? <span className="font-mono text-foreground/80">{project}</span> : '전체 프로젝트'}>
        {total !== undefined ? <span className="text-[11px] tabular-nums text-muted-foreground">{total}건</span> : null}
        <Button size="xs" onClick={() => setCreate(true)}>
          <PlusIcon />새 이슈
        </Button>
      </PageHeader>

      <div className="flex shrink-0 flex-wrap items-center gap-1 border-b px-3 py-1.5">
        <div className="relative mr-1">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="검색 (제목·본문·키)" className="h-7 w-56 pl-7 text-xs" />
          {search ? (
            <button type="button" onClick={() => setSearch('')} className="absolute top-1/2 right-1.5 -translate-y-1/2 cursor-pointer text-muted-foreground hover:text-foreground" aria-label="검색어 지우기">
              <XIcon className="size-3.5" />
            </button>
          ) : null}
        </div>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant={status.length ? 'secondary' : 'ghost'} size="xs">
              <FilterIcon />
              {statusPreset === null ? `상태 ${status.length}` : STATUS_PRESETS.find((p) => p.value === statusPreset)?.label}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuLabel>범위</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={statusPreset ?? 'custom'} onValueChange={(v) => update({ status: v || null })}>
              {STATUS_PRESETS.map((p) => (
                <DropdownMenuRadioItem key={p.value} value={p.value}>
                  {p.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>상태 고르기</DropdownMenuLabel>
            {ISSUE_STATUSES.map((s) => (
              <DropdownMenuCheckboxItem key={s} checked={status.includes(s)} onCheckedChange={(on) => toggle('status', status)(s, on === true)} onSelect={(e) => e.preventDefault()}>
                <StatusIcon status={s as IssueStatus} />
                {STATUS_LABEL[s]}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <FilterMenu label="종류" icon={<TypeIcon type="feature" className="size-3.5 text-muted-foreground" />} values={ISSUE_TYPES} selected={type} onToggle={toggle('type', type)} render={(t: IssueType) => (<><TypeIcon type={t} />{TYPE_LABEL[t]}</>)} />
        <FilterMenu label="우선순위" icon={<PriorityIcon priority="P2" className="size-3.5 text-muted-foreground" />} values={PRIORITIES} selected={priority} onToggle={toggle('priority', priority)} render={(p: Priority) => (<><PriorityIcon priority={p} />{PRIORITY_LABEL[p]}</>)} />
        <FilterMenu
          label="라벨"
          icon={<span className="size-2 rounded-full bg-muted-foreground/60" />}
          values={(labels.data?.items ?? []).map((l) => l.label)}
          selected={label}
          onToggle={toggle('label', label)}
          render={(l: string) => (
            <>
              <LabelChip label={l} />
              <span className="ml-auto text-[10px] tabular-nums text-muted-foreground">{labels.data?.items.find((x) => x.label === l)?.count}</span>
            </>
          )}
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
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="xs">
                <ArrowUpDownIcon />
                {q && !sort ? '관련도' : (SORTS.find((s) => s.value === sort)?.label ?? sort)}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuLabel>정렬</DropdownMenuLabel>
              <DropdownMenuRadioGroup value={sort} onValueChange={(v) => update({ sort: v || null })}>
                {q ? <DropdownMenuRadioItem value="">관련도</DropdownMenuRadioItem> : null}
                {SORTS.filter((s) => s.value !== '' || !q).map((s) => (
                  <DropdownMenuRadioItem key={s.value} value={s.value}>
                    {s.label}
                  </DropdownMenuRadioItem>
                ))}
                {q ? <DropdownMenuRadioItem value="priority,created_at">우선순위</DropdownMenuRadioItem> : null}
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      {label.length > 0 ? (
        <div className="flex flex-wrap items-center gap-1 border-b px-4 py-1.5 text-[11px] text-muted-foreground">
          라벨 AND:
          {label.map((l) => (
            <LabelChip key={l} label={l} onClick={() => toggle('label', label)(l, false)} />
          ))}
        </div>
      ) : null}

      <div className="min-h-0 flex-1 overflow-y-auto">
        <IssueList key={filterKey} filters={filters} />
      </div>
      <IssueCreateDialog open={create} onOpenChange={setCreate} />
    </>
  )
}
