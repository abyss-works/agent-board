import type { Activity } from '@/api/types'
import { fieldLabel, statusLabel, typeLabel } from './labels'

export interface ChangeLine {
  field: string
  label: string
  /** 표시용 값. 본문처럼 펼치지 않는 필드는 null. */
  from: string | null
  to: string | null
}

const MAX = 60

/** 활동 기록의 값 하나를 한 줄 문자열로 만든다. */
export function formatValue(value: unknown, field?: string): string {
  if (value === null || value === undefined) return '없음'
  if (Array.isArray(value)) return value.length ? value.map((v) => formatValue(v)).join(', ') : '없음'
  const text = typeof value === 'string' ? value : JSON.stringify(value)
  if (field === 'status') return statusLabel(text)
  if (field === 'type') return typeLabel(text)
  return text.length > MAX ? `${text.slice(0, MAX)}…` : text
}

/** 기록 한 건의 변경 내용을 줄 목록으로 만든다. created는 제목만, commented는 빈 목록이다. */
export function describeChanges(a: Activity): ChangeLine[] {
  if (a.kind === 'commented' || !a.changes) return []
  if (a.kind === 'created') {
    return [{ field: 'title', label: fieldLabel('title'), from: null, to: formatValue(a.changes.title?.to) }]
  }
  return Object.entries(a.changes).map(([field, change]) => {
    if (field === 'body') return { field, label: fieldLabel(field), from: null, to: null }
    return {
      field,
      label: fieldLabel(field),
      from: formatValue(change.from, field),
      to: formatValue(change.to, field),
    }
  })
}

/** 피드 한 줄에 들어갈 요약 문구 */
export function summarizeActivity(a: Activity): string {
  if (a.kind === 'commented') return a.body?.split('\n')[0] ?? ''
  const lines = describeChanges(a)
  if (a.kind === 'created') return lines[0]?.to ?? ''
  return lines.map((l) => (l.to === null ? `${l.label} 수정` : `${l.label}: ${l.from} → ${l.to}`)).join(' · ')
}
