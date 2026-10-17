import { Fragment, useMemo } from 'react'
import { useSearchParams } from 'react-router'
import { useActivity, useActors } from '@/api/queries'
import { ACTIVITY_KINDS, type ActivityKind } from '@/api/types'
import { useAppState } from '@/app/state'
import { ActivityItem } from '@/components/activity/activity-item'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { ActorChip } from '@/components/issue/meta'
import { PropertySelect, type SelectOption } from '@/components/issue/property-select'
import { PageHeader } from '@/components/layout/page-header'
import { useFlash } from '@/hooks/use-flash'
import { KIND_LABEL } from '@/lib/labels'
import { formatKstDate } from '@/lib/time'

function Feed({ project, actor, kind }: { project: string | null; actor: string | null; kind: string | null }) {
  const q = useActivity({ project, actor, kind })
  const items = q.data?.items
  const flash = useFlash(items, (a) => a.id, () => '')
  if (q.isPending) return <Loading />
  if (q.isError) return <QueryError error={q.error} onRetry={() => void q.refetch()} />
  if (!items || items.length === 0) return <EmptyState>활동 기록이 없습니다.</EmptyState>

  return (
    <ul>
      {items.map((a, idx) => {
        const date = formatKstDate(a.at)
        const prev = idx > 0 ? items[idx - 1] : null
        const header = !prev || formatKstDate(prev.at) !== date
        return (
          <Fragment key={a.id}>
            {header ? (
              <li className="sticky top-0 z-10 border-b bg-background/95 px-4 py-1 text-[11px] font-medium text-muted-foreground backdrop-blur">{date}</li>
            ) : null}
            <ActivityItem activity={a} flash={flash.has(a.id)} />
          </Fragment>
        )
      })}
      {q.data.total > items.length ? <li className="px-4 py-3 text-center text-[11px] text-muted-foreground">총 {q.data.total}건 중 최근 {items.length}건</li> : null}
    </ul>
  )
}

export function ActivityPage() {
  const { project } = useAppState()
  const [params, setParams] = useSearchParams()
  const actors = useActors()
  const actor = params.get('actor') || null
  const kind = params.get('kind') || null

  const actorOptions = useMemo<SelectOption<string>[]>(
    () => [
      { value: '', label: '모든 행위자' },
      ...(actors.data?.items ?? []).map((a) => ({ value: a.id, label: a.id, icon: <ActorChip actor={a.id} className="text-inherit" /> })),
    ],
    [actors.data],
  )
  const kindOptions: SelectOption<string>[] = [
    { value: '', label: '모든 종류' },
    ...ACTIVITY_KINDS.map((k: ActivityKind) => ({ value: k, label: KIND_LABEL[k] })),
  ]

  const update = (name: string, value: string) => {
    const next = new URLSearchParams(params)
    if (value) next.set(name, value)
    else next.delete(name)
    setParams(next, { replace: true })
  }

  return (
    <>
      <PageHeader title="활동" meta={project ? <span className="font-mono text-foreground/80">{project}</span> : '전체 프로젝트'}>
        <PropertySelect value={actor ?? ''} options={actorOptions} onChange={(v) => update('actor', v)} title="행위자" align="end" className="border-border text-xs" />
        <PropertySelect value={kind ?? ''} options={kindOptions} onChange={(v) => update('kind', v)} title="종류" align="end" className="border-border text-xs" />
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <Feed key={`${project ?? '*'}|${actor ?? ''}|${kind ?? ''}`} project={project} actor={actor} kind={kind} />
      </div>
    </>
  )
}
