import {
  MILESTONE_STATUSES,
  type Milestone,
  type MilestoneCreate,
  type MilestonePatch,
  type MilestoneProgress,
  type MilestoneStatus,
} from '@/api/types'
import type { QueryParams } from '@/api/client'

/** 서버 규칙과 같다. 이름은 1~50자, 설명은 2000자까지다. */
export const MILESTONE_NAME_MAX = 50
export const MILESTONE_DESCRIPTION_MAX = 2000

/** 마일스톤 화면에서 고르는 보기. 서버의 status 필터와 같은 말을 쓴다. */
export type MilestoneView = 'open' | 'closed' | 'all'

export const MILESTONE_VIEWS: ReadonlyArray<{ value: MilestoneView; label: string }> = [
  { value: 'open', label: '열림' },
  { value: 'closed', label: '닫힘' },
  { value: 'all', label: '전체' },
]

/** 주소의 status 값에서 보기를 읽는다. 없거나 모르는 값이면 기본인 열림이다. */
export function parseMilestoneView(param: string | null): MilestoneView {
  return param === 'closed' || param === 'all' ? param : 'open'
}

/** 보기가 보여 주는 상태. 전체는 삭제된 마일스톤까지 포함한다. */
export function viewStatuses(view: MilestoneView): MilestoneStatus[] {
  return view === 'all' ? [...MILESTONE_STATUSES] : [view]
}

/**
 * 마일스톤 목록 요청의 질의 값. 열린 것만 받는 것은 서버 기본값이라 status를 보내지 않는다.
 * 여러 상태는 열림, 닫힘, 삭제 순으로 쉼표 OR로 잇고, 세 상태가 모두 있으면 all이다.
 */
export function milestoneListParams(statuses: readonly MilestoneStatus[]): QueryParams {
  const picked = MILESTONE_STATUSES.filter((s) => statuses.includes(s))
  if (picked.length === 0 || (picked.length === 1 && picked[0] === 'open')) return {}
  if (picked.length === MILESTONE_STATUSES.length) return { status: 'all' }
  return { status: picked.join(',') }
}

/**
 * 완료 비율의 정수 퍼센트. 반올림하되 모두 끝나기 전에는 100으로, 하나라도 끝났으면 0으로 두지 않는다.
 * 대상 이슈가 없으면 0이다.
 */
export function progressPercent({ total, done }: MilestoneProgress): number {
  if (total <= 0) return 0
  if (done >= total) return 100
  const rounded = Math.round((done / total) * 100)
  return Math.min(99, Math.max(done > 0 ? 1 : 0, rounded))
}

/** 진행 막대 옆에 보이는 문구. `2/5 · 40%`, 대상 이슈가 없으면 `이슈 없음`이다. */
export function progressLabel(progress: MilestoneProgress): string {
  if (progress.total <= 0) return '이슈 없음'
  return `${progress.done}/${progress.total} · ${progressPercent(progress)}%`
}

/** 마일스톤의 이슈만 거른 이슈 목록 주소 */
export function milestoneIssuesPath(id: number): string {
  return `/issues?milestone=${id}`
}

/** 마일스톤 ID에서 이름을 찾는 표 */
export function milestoneNameMap(items: readonly Milestone[]): Map<number, string> {
  return new Map(items.map((m) => [m.id, m.name]))
}

/** 만들기·수정 대화상자의 입력값. 목표일이 없으면 빈 문자열이다. */
export interface MilestoneForm {
  name: string
  description: string
  targetDate: string
}

export type MilestoneFormErrors = Partial<Record<'name' | 'description' | 'target_date', string>>

export function formFromMilestone(milestone?: Milestone): MilestoneForm {
  return {
    name: milestone?.name ?? '',
    description: milestone?.description ?? '',
    targetDate: milestone?.target_date ?? '',
  }
}

function runeCount(text: string): number {
  return Array.from(text).length
}

/** `YYYY-MM-DD`이면서 달력에 있는 날짜인지. 2026-02-30은 거절한다. */
function isCalendarDate(text: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(text)) return false
  const d = new Date(`${text}T00:00:00Z`)
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === text
}

/** 입력값의 위반을 필드 이름(서버의 details[].field와 같은 이름)별로 돌려준다. 모두 맞으면 빈 객체다. */
export function validateMilestoneForm(form: MilestoneForm): MilestoneFormErrors {
  const errors: MilestoneFormErrors = {}
  const nameLength = runeCount(form.name.trim())
  if (nameLength < 1 || nameLength > MILESTONE_NAME_MAX) errors.name = `1~${MILESTONE_NAME_MAX}자여야 합니다.`
  if (runeCount(form.description.trim()) > MILESTONE_DESCRIPTION_MAX) errors.description = `${MILESTONE_DESCRIPTION_MAX}자 이하여야 합니다.`
  if (form.targetDate !== '' && !isCalendarDate(form.targetDate)) errors.target_date = 'YYYY-MM-DD 형식의 날짜여야 합니다.'
  return errors
}

/** POST 본문. 비어 있는 설명과 목표일은 보내지 않는다. */
export function milestoneCreateBody(form: MilestoneForm): MilestoneCreate {
  const description = form.description.trim()
  return {
    name: form.name.trim(),
    ...(description ? { description } : {}),
    ...(form.targetDate ? { target_date: form.targetDate } : {}),
  }
}

/** 입력값이 저장된 값과 다를 때만 앞뒤 공백을 뗀 값을 돌려준다. 건드리지 않았거나 공백만 바꿨으면 undefined다. */
function changedText(input: string, stored: string): string | undefined {
  if (input === stored) return undefined
  const trimmed = input.trim()
  return trimmed === stored ? undefined : trimmed
}

/** PATCH 본문. 바뀐 필드만 담고, 목표일을 비우면 null을 보낸다. 바뀐 것이 없으면 빈 객체다. */
export function milestonePatchBody(current: Milestone, form: MilestoneForm): MilestonePatch {
  const name = changedText(form.name, current.name)
  const description = changedText(form.description, current.description)
  const targetDate = form.targetDate === '' ? null : form.targetDate
  return {
    ...(name !== undefined ? { name } : {}),
    ...(description !== undefined ? { description } : {}),
    ...(targetDate !== current.target_date ? { target_date: targetDate } : {}),
  }
}
