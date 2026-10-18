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
export function summarizeActivity(a: Activity): string {
  if (a.kind === 'commented') return a.summary || bodyExcerpt(a.body ?? '')
  const lines = describeChanges(a)
  if (a.kind === 'created') return lines[0]?.to ?? ''
  return lines.map((l) => (l.to === null ? `${l.label} 수정` : `${l.label}: ${l.from} → ${l.to}`)).join(' · ')
}
