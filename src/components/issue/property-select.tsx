import { ChevronDownIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'

export interface SelectOption<T extends string> {
  value: T
  label: string
  icon?: ReactNode
}

interface Props<T extends string> {
  value: T
  options: ReadonlyArray<SelectOption<T>>
  onChange: (value: T) => void
  title?: string
  disabled?: boolean
  className?: string
  /** 트리거를 직접 그릴 때 */
  trigger?: ReactNode
  align?: 'start' | 'end'
}

/** 상태·우선순위·종류 같은 열거형 속성을 고르는 드롭다운 */
export function PropertySelect<T extends string>({ value, options, onChange, title, disabled, className, trigger, align = 'start' }: Props<T>) {
  const current = options.find((o) => o.value === value)
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild disabled={disabled}>
        {trigger ?? (
          <button
            type="button"
            className={cn(
              'inline-flex h-7 cursor-pointer items-center gap-1.5 rounded-md border border-transparent px-1.5 text-[13px] text-foreground transition-colors outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default disabled:opacity-60',
              className,
            )}
          >
            {current?.icon}
            <span className="truncate">{current?.label ?? value}</span>
            <ChevronDownIcon className="size-3 text-muted-foreground" />
          </button>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent align={align}>
        {title ? <DropdownMenuLabel>{title}</DropdownMenuLabel> : null}
        <DropdownMenuRadioGroup value={value} onValueChange={(v) => onChange(v as T)}>
          {options.map((o) => (
            <DropdownMenuRadioItem key={o.value} value={o.value}>
              {o.icon}
              {o.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
