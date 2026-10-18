import { ChevronDownIcon } from 'lucide-react'
import type { Milestone } from '@/api/types'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'

interface Props {
  /** 고를 수 있는 열린 마일스톤 */
  milestones: readonly Milestone[]
  value: number | null
  /** 이슈의 마일스톤이 닫혔거나 삭제되어 목록에 없을 때 버튼에 보일 이름 */
  fallbackLabel?: string | null
  /** 값이 없을 때 버튼에 보일 말 */
  emptyLabel?: string
  onChange: (id: number | null) => void
  disabled?: boolean
  className?: string
}

/** 이슈의 마일스톤을 고르는 드롭다운. 열린 마일스톤 중 하나이거나 없음이다. */
export function MilestoneSelect({ milestones, value, fallbackLabel, emptyLabel = '없음', onChange, disabled, className }: Props) {
  const current = milestones.find((m) => m.id === value)
  const label = value === null ? null : (current?.name ?? fallbackLabel ?? `마일스톤 #${value}`)
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
        <DropdownMenuLabel>마일스톤</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={value === null ? '' : String(value)} onValueChange={(v) => onChange(v === '' ? null : Number(v))}>
          <DropdownMenuRadioItem value="">없음</DropdownMenuRadioItem>
          {milestones.map((m) => (
            <DropdownMenuRadioItem key={m.id} value={String(m.id)}>
              {m.name}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        {milestones.length === 0 ? <div className="px-2 py-1.5 text-[0.6875rem] text-muted-foreground">열린 마일스톤이 없습니다. 마일스톤 화면에서 만드세요.</div> : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
