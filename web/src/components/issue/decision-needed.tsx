import { cn } from '@/lib/utils'
import { InlineInput } from './inline-edit'

interface Props {
  /** 이슈의 decision_needed. 결정을 기다리지 않으면 null이다. */
  value: string | null
  /** 고친 질문. 비웠으면 빈 문자열이다. */
  onCommit: (value: string) => void
}

/**
 * 이슈 상세 속성 맨 위의 결정 필요 질문. 있으면 눈에 띄는 블록으로, 없으면 다른 속성처럼 비어 있는 줄로 보인다.
 * 눌러서 고치고, 비우면 지운다.
 */
export function DecisionNeeded({ value, onCommit }: Props) {
  const set = !!value
  return (
    <div className={set ? 'my-1 rounded-md border border-amber-500/40 bg-amber-500/10 p-2' : 'grid grid-cols-[5.5rem_1fr] items-start gap-2 py-1'}>
      <div className={cn('text-[0.6875rem]', set ? 'text-amber-300' : 'pt-1.5 text-muted-foreground')}>결정 필요</div>
      <div className="min-w-0">
        <InlineInput value={value ?? ''} onCommit={onCommit} placeholder="없음" className={cn(set && 'mt-1 items-start py-1 break-words whitespace-normal')} />
      </div>
    </div>
  )
}
