import { CheckIcon, PencilIcon, RotateCcwIcon, ShieldAlertIcon, ShieldCheckIcon, XIcon } from 'lucide-react'
import { useState } from 'react'
import type { Issue } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { formatKst } from '@/lib/time'

export interface VerificationBannerProps {
  issue: Issue
  /** none 상태에서 사용자가 "검증 필요 지정" 입력을 열었는지 여부 */
  requesting: boolean
  pending?: boolean
  onCancelRequest: () => void
  onRequestPending: (note: string) => void
  onVerify: () => void
  onUnverify: () => void
  onClear: () => void
  onSaveNote: (note: string) => void
}

/**
 * 이슈 상세 상단의 사용자 검증 게이트 배너.
 * - none: 기본은 아무것도 그리지 않고, 지정 입력이 열릴 때만 메모 입력 칸을 보인다.
 * - pending: 릴리즈 차단 배너와 메모, 검증 완료·메모 편집·해제 동작을 보인다.
 * - verified: 은은한 검증 완료 표시(검증자·시각·메모)와 되돌리기·해제 동작을 보인다.
 */
export function VerificationBanner({
  issue,
  requesting,
  pending,
  onCancelRequest,
  onRequestPending,
  onVerify,
  onUnverify,
  onClear,
  onSaveNote,
}: VerificationBannerProps) {
  const [draftNote, setDraftNote] = useState('')
  const [editingNote, setEditingNote] = useState(false)
  const [noteInput, setNoteInput] = useState(issue.verification_note ?? '')

  if (issue.verification_state === 'none') {
    if (!requesting) return null
    return (
      <div className="mb-4 rounded-lg border border-rose-500/35 bg-rose-500/[0.06] p-3">
        <div className="flex items-center gap-2 text-xs font-medium text-rose-300">
          <ShieldAlertIcon className="size-3.5 shrink-0" />
          검증 필요 지정
          <span className="text-[0.6875rem] font-normal text-muted-foreground">검증 완료 전까지 마일스톤 닫기와 릴리즈가 차단됩니다.</span>
        </div>
        <Input
          value={draftNote}
          onChange={(e) => setDraftNote(e.target.value)}
          placeholder="확인할 내용 (선택)"
          className="mt-2 h-8 text-xs"
          maxLength={500}
          autoFocus
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              onRequestPending(draftNote)
              setDraftNote('')
            } else if (e.key === 'Escape') {
              setDraftNote('')
              onCancelRequest()
            }
          }}
        />
        <div className="mt-2 flex justify-end gap-1.5">
          <Button
            variant="ghost"
            size="xs"
            disabled={pending}
            onClick={() => {
              setDraftNote('')
              onCancelRequest()
            }}
          >
            취소
          </Button>
          <Button
            size="xs"
            disabled={pending}
            onClick={() => {
              onRequestPending(draftNote)
              setDraftNote('')
            }}
          >
            검증 필요로 지정
          </Button>
        </div>
      </div>
    )
  }

  if (issue.verification_state === 'pending') {
    return (
      <div className="mb-4 rounded-lg border border-rose-500/40 bg-rose-500/10 p-3">
        <div className="flex flex-wrap items-center gap-2">
          <ShieldAlertIcon className="size-4 shrink-0 text-rose-400" />
          <span className="text-xs font-semibold text-rose-200">검증 필요</span>
          <Badge variant="destructive">릴리즈 차단</Badge>
          <div className="ml-auto flex flex-wrap items-center gap-1.5">
            <Button size="xs" onClick={onVerify} disabled={pending} title="사용자 검증을 완료 처리합니다">
              <CheckIcon />
              검증 완료
            </Button>
            {!editingNote ? (
              <Button
                variant="outline"
                size="xs"
                disabled={pending}
                onClick={() => {
                  setNoteInput(issue.verification_note ?? '')
                  setEditingNote(true)
                }}
              >
                <PencilIcon />
                메모 편집
              </Button>
            ) : null}
            <Button variant="ghost" size="xs" onClick={onClear} disabled={pending} title="검증 대상을 해제합니다(해당 없음)">
              <XIcon />
              해제
            </Button>
          </div>
        </div>
        {editingNote ? (
          <div className="mt-2 space-y-2">
            <Input
              value={noteInput}
              onChange={(e) => setNoteInput(e.target.value)}
              placeholder="확인할 내용 (선택)"
              className="h-8 text-xs"
              maxLength={500}
              autoFocus
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  onSaveNote(noteInput)
                  setEditingNote(false)
                } else if (e.key === 'Escape') {
                  setEditingNote(false)
                }
              }}
            />
            <div className="flex justify-end gap-1.5">
              <Button variant="ghost" size="xs" disabled={pending} onClick={() => setEditingNote(false)}>
                취소
              </Button>
              <Button
                size="xs"
                disabled={pending}
                onClick={() => {
                  onSaveNote(noteInput)
                  setEditingNote(false)
                }}
              >
                저장
              </Button>
            </div>
          </div>
        ) : issue.verification_note ? (
          <p className="mt-1.5 text-xs leading-relaxed break-words text-rose-100/90">{issue.verification_note}</p>
        ) : (
          <p className="mt-1 text-[0.6875rem] text-rose-200/70">검증 완료 전까지 이 이슈가 속한 마일스톤을 닫거나 릴리즈할 수 없습니다.</p>
        )}
      </div>
    )
  }

  return (
    <div className="mb-4 rounded-lg border border-emerald-500/30 bg-emerald-500/[0.06] px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <ShieldCheckIcon className="size-3.5 shrink-0 text-emerald-400" />
        <Badge variant="success">검증 완료</Badge>
        <span className="text-[0.6875rem] text-muted-foreground">
          {issue.verified_by ?? '사용자'}
          {issue.verified_at ? ` · ${formatKst(issue.verified_at)} KST` : ''}
        </span>
        <div className="ml-auto flex flex-wrap items-center gap-1">
          <Button variant="ghost" size="xs" onClick={onUnverify} disabled={pending} title="검증 완료를 취소하고 다시 검증 필요 상태로 되돌립니다">
            <RotateCcwIcon />
            다시 검증 필요로 되돌리기
          </Button>
          <Button variant="ghost" size="xs" onClick={onClear} disabled={pending} title="검증 대상을 해제합니다(해당 없음)">
            해제
          </Button>
        </div>
      </div>
      {issue.verification_note ? <p className="mt-1 text-xs break-words text-foreground/80">{issue.verification_note}</p> : null}
    </div>
  )
}
