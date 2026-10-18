import { ChevronDownIcon } from 'lucide-react'
import type { Topic } from '@/api/types'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { flattenTopics, groupTopics, topicOptionLabel } from '@/lib/topics'
import { cn } from '@/lib/utils'

interface Props {
  /** 고를 수 있는 활성 주제 */
  topics: readonly Topic[]
  value: number | null
  /** 이슈의 주제가 삭제되어 목록에 없을 때 버튼에 보일 경로 */
  fallbackLabel?: string | null
  /** 값이 없을 때 버튼에 보일 말 */
  emptyLabel?: string
  onChange: (id: number | null) => void
  disabled?: boolean
  className?: string
}

/** 이슈의 주제를 고르는 드롭다운. 대주제 아래에 그 주제를 들여써서 묶고, 대주제와 주제 어느 쪽이든 고를 수 있다. */
export function TopicSelect({ topics, value, fallbackLabel, emptyLabel = '없음', onChange, disabled, className }: Props) {
  const flat = flattenTopics(groupTopics(topics))
  const current = flat.find((f) => f.topic.id === value)
  const label = value === null ? null : (current?.topic.path ?? fallbackLabel ?? `주제 #${value}`)
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild disabled={disabled}>
        <button
          type="button"
          className={cn(
            'inline-flex h-7 max-w-full cursor-pointer items-center gap-1.5 rounded-md border border-transparent px-1.5 text-[0.8125rem] text-foreground transition-colors outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default disabled:opacity-60',
            className,
          )}
        >
          <span className={cn('truncate', label === null && 'text-muted-foreground/70')}>{label ?? emptyLabel}</span>
          <ChevronDownIcon className="size-3 shrink-0 text-muted-foreground" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 min-w-48 overflow-y-auto">
        <DropdownMenuLabel>주제</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={value === null ? '' : String(value)} onValueChange={(v) => onChange(v === '' ? null : Number(v))}>
          <DropdownMenuRadioItem value="">없음</DropdownMenuRadioItem>
          {flat.map((f) => (
            <DropdownMenuRadioItem key={f.topic.id} value={String(f.topic.id)} className={f.depth === 1 ? 'pl-10' : 'font-medium'}>
              {topicOptionLabel(f)}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        {flat.length === 0 ? (
          <div className="px-2 py-1.5 text-[0.6875rem] text-muted-foreground">주제가 없습니다. 프로젝트 메뉴의 주제 관리에서 만드세요.</div>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
