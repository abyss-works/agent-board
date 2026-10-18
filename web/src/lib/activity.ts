import type { Activity } from '@/api/types'
import { fieldLabel, statusLabel, typeLabel } from './labels'

export interface ChangeLine {
  field: string
  label: string
  /** 표시용 값. 본문처럼 펼치지 않는 필드는 null. */
  from: string | null
  to: string | null
  /** 상태 변경 줄에 붙는 사유. 자르지 않은 전체 문장이다. */
  reason?: string
}

const MAX = 60

/** 기록의 값을 사람이 읽는 말로 바꾸는 데 쓰는 조회. 주제 ID는 이름을 아는 쪽에서 알려 준다. */
export interface DescribeOptions {
  topicName?: (id: number) => string | undefined
}

/** 활동 기록의 값 하나를 한 줄 문자열로 만든다. */
export function formatValue(value: unknown, field?: string, opts?: DescribeOptions): string {
  if (value === null || value === undefined) return '없음'
  if (Array.isArray(value)) return value.length ? value.map((v) => formatValue(v)).join(', ') : '없음'
  if (field === 'topic' && typeof value === 'number') return opts?.topicName?.(value) ?? `주제 #${value}`
  const text = typeof value === 'string' ? value : JSON.stringify(value)
  if (field === 'acknowledged') return value === true ? '확인됨' : '미확인'
  if (field === 'status') return statusLabel(text)
  if (field === 'type') return typeLabel(text)
  return text.length > MAX ? `${text.slice(0, MAX)}…` : text
}

/** 기록 한 건의 변경 내용을 줄 목록으로 만든다. created는 제목만, commented는 빈 목록이다. */
export function describeChanges(a: Activity, opts?: DescribeOptions): ChangeLine[] {
  if (a.kind === 'commented' || !a.changes) return []
  if (a.kind === 'created') {
    return [{ field: 'title', label: fieldLabel('title'), from: null, to: formatValue(a.changes.title?.to) }]
  }
  const changes = a.changes
  // 상태가 바뀐 기록의 사유는 상태 줄에 붙인다. 상태 변경 없이 사유만 있으면 별도 줄로 둔다.
  const reason = typeof changes.status_reason?.to === 'string' ? changes.status_reason.to : undefined
  return Object.entries(changes)
    .filter(([field]) => !(field === 'status_reason' && 'status' in changes))
    .map(([field, change]) => {
      if (field === 'body') return { field, label: fieldLabel(field), from: null, to: null }
      const line: ChangeLine = {
        field,
        label: fieldLabel(field),
        from: formatValue(change.from, field, opts),
        to: formatValue(change.to, field, opts),
      }
      if (field === 'status' && reason) line.reason = reason
      return line
    })
}

/** 요약이 없는 댓글을 한 줄로 보일 때 남기는 본문 앞부분의 글자 수(코드 포인트) */
const EXCERPT_MAX = 20

/**
 * 댓글 본문의 앞부분. 공백과 줄바꿈을 한 칸 공백으로 합치고 앞뒤 공백을 뗀 뒤 앞 20자(코드 포인트 기준)만 남긴다.
 * 잘렸으면 끝에 `…`를 붙인다.
 */
export function bodyExcerpt(body: string): string {
  const chars = Array.from(body.replace(/\s+/g, ' ').trim())
  return chars.length > EXCERPT_MAX ? `${chars.slice(0, EXCERPT_MAX).join('')}…` : chars.join('')
}

/** 피드 한 줄에 들어갈 요약 문구. 댓글은 요약(summary)이 있으면 요약, 없으면 본문 앞부분이다. */
export function summarizeActivity(a: Activity, opts?: DescribeOptions): string {
  if (a.kind === 'commented') return a.summary || bodyExcerpt(a.body ?? '')
  const lines = describeChanges(a, opts)
  if (a.kind === 'created') return lines[0]?.to ?? ''
  return lines
    .map((l) => {
      if (l.to === null) return `${l.label} 수정`
      return `${l.label}: ${l.from} → ${l.to}${l.reason ? ` (사유: ${formatValue(l.reason)})` : ''}`
    })
    .join(' · ')
}
