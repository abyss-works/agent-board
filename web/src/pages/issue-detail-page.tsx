import { ArrowLeftIcon, CheckIcon, CopyIcon, PencilIcon, Trash2Icon } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router'
import { toast } from 'sonner'
import { useCreateComment, useDeleteIssue, useIssue, useIssueActivity, useMilestones, usePatchIssue, useSetAcknowledged, useTopics } from '@/api/queries'
import { MILESTONE_STATUSES, type Issue, type IssuePatch, type IssueStatus, type IssueType } from '@/api/types'
import { ActivityItem } from '@/components/activity/activity-item'
import { EmptyState, Loading, QueryError } from '@/components/feedback'
import { DecisionNeeded } from '@/components/issue/decision-needed'
import { InlineInput, MarkdownArea } from '@/components/issue/inline-edit'
import { IssueLink } from '@/components/issue/issue-link'
import { parseIssueKeys, parseLabels, parseRefs } from '@/lib/parse'
import { ActorChip, LabelChip } from '@/components/issue/meta'
import { MilestoneSelect } from '@/components/issue/milestone-select'
import { PRIORITY_OPTIONS, STATUS_OPTIONS, TYPE_OPTIONS } from '@/components/issue/options'
import { PropertySelect } from '@/components/issue/property-select'
import { ReasonDialog } from '@/components/issue/reason-dialog'
import { TopicSelect } from '@/components/issue/topic-select'
import { Markdown } from '@/components/markdown'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { useFlash } from '@/hooks/use-flash'
import { decisionPatch } from '@/lib/decision'
import { STATUS_LABEL } from '@/lib/labels'
import { milestoneNameMap } from '@/lib/milestones'
import { ISSUES_PATH, openedFromScreen } from '@/lib/overlay'
import { isEditableTarget } from '@/lib/shortcuts'
import { reasonRequired, statusChangePatch } from '@/lib/status'
import { formatKst, formatRelative } from '@/lib/time'
import { topicNameMap } from '@/lib/topics'
import { toastError } from '@/lib/toast'
import { cn } from '@/lib/utils'

function Property({ label, children, note }: { label: string; children: ReactNode; note?: ReactNode }) {
  return (
    <div className="grid grid-cols-[5.5rem_1fr] items-start gap-2 py-1">
      <div className="pt-1.5 text-[0.6875rem] text-muted-foreground">{label}</div>
      <div className="min-w-0">
        {children}
        {note ? <p className="pb-0.5 text-[0.6875rem] leading-snug break-words text-muted-foreground">{note}</p> : null}
      </div>
    </div>
  )
}

function Meta({ label, actor, at }: { label: string; actor?: string; at: string | null }) {
  if (!at) return null
  return (
    <div className="flex items-center gap-1.5 text-[0.6875rem] text-muted-foreground" title={`${formatKst(at)} KST`}>
      <span className="w-14 shrink-0">{label}</span>
      {actor ? <ActorChip actor={actor} className="text-[0.6875rem]" /> : null}
      <span className="ml-auto tabular-nums">{formatRelative(at)}</span>
    </div>
  )
}

function IssueDetail({ issue }: { issue: Issue }) {
  const navigate = useNavigate()
  const patch = usePatchIssue()
  const del = useDeleteIssue()
  const acknowledge = useSetAcknowledged()
  const comment = useCreateComment()
  const activity = useIssueActivity(issue.key)
  // 고를 수 있는 주제는 활성 주제, 활동 기록의 주제 이름은 삭제된 주제까지 포함한 전체에서 찾는다.
  const topics = useTopics(issue.project)
  const allTopics = useTopics(issue.project, 'all')
  const topicNames = topicNameMap(allTopics.data?.items ?? [])
  // 고를 수 있는 마일스톤은 열린 것만이고, 활동 기록의 마일스톤 이름은 닫히거나 삭제된 것까지 포함한 전체에서 찾는다.
  const milestones = useMilestones(issue.project)
  const allMilestones = useMilestones(issue.project, MILESTONE_STATUSES)
  const milestoneNames = milestoneNameMap(allMilestones.data?.items ?? [])
  const [editingBody, setEditingBody] = useState(false)
  const [commentText, setCommentText] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)
  // 취소·삭제로 바꾸려고 고른 상태. 사유를 받을 때까지 요청을 보내지 않는다.
  const [reasonFor, setReasonFor] = useState<IssueStatus | null>(null)
  // 종류를 관측으로 바꿀 때 재검토 조건이 없으면 먼저 받는다.
  const [pendingRecheck, setPendingRecheck] = useState<string | null>(null)

  // 서버가 최신 기록부터 돌려준다.
  const items = activity.data?.items
  const flash = useFlash(items, (a) => a.id, () => '')

  const save = (body: IssuePatch, label: string) =>
    patch.mutate({ key: issue.key, patch: body, version: issue.version }, { onError: (err) => toastError(err, `${label}을(를) 저장하지 못했습니다`) })

  const setAcknowledged = (acknowledged: boolean) =>
    acknowledge.mutate({ key: issue.key, acknowledged }, { onError: (err) => toastError(err, '확인 표시를 바꾸지 못했습니다') })

  const changeStatus = (status: IssueStatus) => {
    if (status === issue.status) return
    if (reasonRequired(status)) setReasonFor(status)
    else save(statusChangePatch(status), '상태')
  }

  const changeType = (type: IssueType) => {
    if (type === issue.type) return
    if (type === 'observation') {
      if (issue.recheck_condition) save({ type }, '종류')
      else setPendingRecheck('')
      return
    }
    save(issue.type === 'observation' ? { type, recheck_condition: null } : { type }, '종류')
  }

  const submitComment = () => {
    const body = commentText.trim()
    if (!body) return
    comment.mutate(
      { key: issue.key, body },
      {
        onSuccess: () => setCommentText(''),
        onError: (err) => toastError(err, '댓글을 남기지 못했습니다'),
      },
    )
  }

  const closed = issue.status === 'done' || issue.status === 'cancelled' || issue.status === 'deleted'

  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
          <Button variant="ghost" size="icon-sm" onClick={() => navigate(-1)} title="뒤로">
            <ArrowLeftIcon />
          </Button>
          <Link to="/issues" className="text-xs text-muted-foreground hover:text-foreground">
            이슈
          </Link>
          <span className="text-muted-foreground/50">/</span>
          <span className="font-mono text-xs font-medium">{issue.key}</span>
          <Button
            variant="ghost"
            size="icon-sm"
            title="키 복사"
            onClick={() => {
              void navigator.clipboard?.writeText(issue.key).then(() => toast.success(`${issue.key}를 복사했습니다`))
            }}
          >
            <CopyIcon className="size-3" />
          </Button>
          {issue.status === 'deleted' ? <Badge variant="destructive">삭제됨</Badge> : null}
          <div className="ml-auto flex items-center gap-2">
            {issue.acknowledged_at === null ? (
              <>
                <Badge variant="info">미확인</Badge>
                <Button size="xs" onClick={() => setAcknowledged(true)} disabled={acknowledge.isPending} title="이 이슈를 확인했다고 표시합니다. 작업 흐름에는 영향이 없습니다.">
                  <CheckIcon />
                  확인
                </Button>
              </>
            ) : (
              <Button
                variant="ghost"
                size="xs"
                onClick={() => setAcknowledged(false)}
                disabled={acknowledge.isPending}
                title={`${issue.acknowledged_by ?? '마이그레이션'}이 ${formatKst(issue.acknowledged_at)} KST에 확인함`}
              >
                확인 취소
              </Button>
            )}
            <span className="font-mono text-[0.625rem] text-muted-foreground">v{issue.version}</span>
          </div>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto max-w-3xl px-6 py-5">
            <InlineInput
              value={issue.title}
              onCommit={(v) => v && save({ title: v }, '제목')}
              className={cn('h-auto px-1 text-lg leading-snug font-semibold tracking-tight', closed && 'text-muted-foreground')}
              placeholder="제목"
            />
            <div className="mt-4">
              {editingBody ? (
                <MarkdownArea
                  value={issue.body}
                  saving={patch.isPending}
                  onCancel={() => setEditingBody(false)}
                  onCommit={(v) => {
                    setEditingBody(false)
                    if (v !== issue.body) save({ body: v }, '본문')
                  }}
                  placeholder="본문(마크다운)"
                />
              ) : (
                <div className="group relative">
                  {issue.body ? (
                    <Markdown source={issue.body} />
                  ) : (
                    <p className="text-[0.8125rem] text-muted-foreground/70 italic">본문이 없습니다.</p>
                  )}
                  <Button
                    variant="outline"
                    size="xs"
                    className="absolute -top-1 right-0 opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100"
                    onClick={() => setEditingBody(true)}
                  >
                    <PencilIcon />
                    편집
                  </Button>
                </div>
              )}
            </div>

            <section className="mt-8">
              <h3 className="mb-2 text-xs font-semibold text-muted-foreground">활동 {activity.data ? `· ${activity.data.total}건` : ''}</h3>
              <div className="mb-3 space-y-2">
                <Textarea
                  value={commentText}
                  onChange={(e) => setCommentText(e.target.value)}
                  placeholder="댓글(마크다운)"
                  className="min-h-20 text-[0.8125rem]"
                  disabled={comment.isPending}
                />
                <div className="flex items-center justify-between">
                  <span className="text-[0.6875rem] text-muted-foreground">owner로 남깁니다. 삭제된 이슈에도 댓글을 달 수 있습니다.</span>
                  <Button size="sm" onClick={submitComment} disabled={!commentText.trim() || comment.isPending}>
                    댓글 달기
                  </Button>
                </div>
              </div>
              <div className="rounded-lg border">
                {activity.isPending ? <Loading /> : null}
                {activity.isError ? <QueryError error={activity.error} className="m-2" /> : null}
                {items && items.length === 0 ? <EmptyState className="py-6">기록이 없습니다.</EmptyState> : null}
                {items && items.length > 0 ? (
                  <ul>
                    {items.map((a) => (
                      <ActivityItem key={a.id} activity={a} showIssue={false} flash={flash.has(a.id)} topicName={(id) => topicNames.get(id)} milestoneName={(id) => milestoneNames.get(id)} />
                    ))}
                  </ul>
                ) : null}
              </div>
            </section>
          </div>
        </div>
      </div>

      <aside className="flex w-72 shrink-0 flex-col overflow-y-auto border-l px-4 py-3">
        <div className="mb-1 text-[0.6875rem] font-medium text-muted-foreground">속성</div>
        <DecisionNeeded value={issue.decision_needed} onCommit={(v) => save(decisionPatch(v), '결정 필요')} />
        <Property label="상태" note={issue.status_reason}>
          <PropertySelect value={issue.status} options={STATUS_OPTIONS} onChange={changeStatus} title="상태" className="-ml-1.5" />
        </Property>
        <Property label="우선순위">
          <PropertySelect value={issue.priority} options={PRIORITY_OPTIONS} onChange={(p) => p !== issue.priority && save({ priority: p }, '우선순위')} title="우선순위" className="-ml-1.5" />
        </Property>
        <Property label="종류">
          <PropertySelect value={issue.type} options={TYPE_OPTIONS} onChange={changeType} title="종류" className="-ml-1.5" />
        </Property>
        {pendingRecheck !== null ? (
          <div className="my-1 rounded-md border border-amber-500/40 bg-amber-500/10 p-2">
            <div className="text-[0.6875rem] text-amber-300">관측에는 재검토 조건이 필요합니다.</div>
            <Input value={pendingRecheck} onChange={(e) => setPendingRecheck(e.target.value)} placeholder="언제 다시 볼지" className="mt-1.5 h-7 text-xs" autoFocus maxLength={500} />
            <div className="mt-1.5 flex justify-end gap-1">
              <Button variant="ghost" size="xs" onClick={() => setPendingRecheck(null)}>
                취소
              </Button>
              <Button
                size="xs"
                disabled={!pendingRecheck.trim()}
                onClick={() => {
                  save({ type: 'observation', recheck_condition: pendingRecheck.trim() }, '종류')
                  setPendingRecheck(null)
                }}
              >
                저장
              </Button>
            </div>
          </div>
        ) : null}
        {issue.type === 'observation' ? (
          <Property label="재검토 조건">
            <InlineInput value={issue.recheck_condition ?? ''} onCommit={(v) => v && save({ recheck_condition: v }, '재검토 조건')} placeholder="필수" />
          </Property>
        ) : null}
        <Property label="주제">
          <TopicSelect
            topics={topics.data?.items ?? []}
            value={issue.topic}
            fallbackLabel={issue.topic_path}
            onChange={(id) => id !== issue.topic && save({ topic: id }, '주제')}
            className="-ml-1.5"
          />
        </Property>
        <Property label="마일스톤">
          <MilestoneSelect
            milestones={milestones.data?.items ?? []}
            value={issue.milestone}
            fallbackLabel={issue.milestone_name}
            onChange={(id) => id !== issue.milestone && save({ milestone: id }, '마일스톤')}
            className="-ml-1.5"
          />
        </Property>
        <Property label="라벨">
          <InlineInput
            value={issue.labels.join(' ')}
            onCommit={(v) => save({ labels: parseLabels(v) }, '라벨')}
            placeholder="라벨 추가"
            mono
            render={(v) => (
              <span className="flex flex-wrap gap-1 py-0.5">
                {parseLabels(v).map((l) => (
                  <LabelChip key={l} label={l} />
                ))}
              </span>
            )}
          />
        </Property>
        <Property label="상위 이슈">
          <InlineInput
            value={issue.parent ?? ''}
            onCommit={(v) => save({ parent: v ? v.toUpperCase() : null }, '상위 이슈')}
            placeholder="없음"
            mono
            render={(v) => (
              <IssueLink issueKey={v} className="text-primary hover:underline" onClick={(e) => e.stopPropagation()}>
                {v}
              </IssueLink>
            )}
          />
        </Property>
        <Property label="선행 이슈">
          <InlineInput
            value={issue.depends_on.join(' ')}
            onCommit={(v) => save({ depends_on: parseIssueKeys(v) }, '선행 이슈')}
            placeholder="없음"
            mono
            render={(v) => (
              <span className="flex flex-wrap gap-x-2 gap-y-0.5">
                {v.split(' ').map((k) => (
                  <IssueLink key={k} issueKey={k} className="text-primary hover:underline" onClick={(e) => e.stopPropagation()}>
                    {k}
                  </IssueLink>
                ))}
              </span>
            )}
          />
        </Property>
        <Property label="참조">
          <InlineInput
            value={issue.refs.join(' ')}
            onCommit={(v) => save({ refs: parseRefs(v) }, '참조')}
            placeholder="종류:값 (repo, file, url, pr, issue, doc)"
            mono
            className="h-auto items-start py-1 break-all whitespace-normal"
            render={(v) => (
              <span className="flex flex-col gap-0.5">
                {v.split(' ').map((r) => (
                  <span key={r} className="text-[0.6875rem] break-all">
                    {r}
                  </span>
                ))}
              </span>
            )}
          />
        </Property>

        <div className="mt-4 mb-1 text-[0.6875rem] font-medium text-muted-foreground">기록</div>
        <div className="space-y-1.5">
          <Meta label="생성" actor={issue.created_by} at={issue.created_at} />
          <Meta label="수정" actor={issue.updated_by} at={issue.updated_at} />
          <Meta label={`${STATUS_LABEL[issue.status]}로`} actor={issue.status_changed_by} at={issue.status_changed_at} />
          <Meta label="닫힘" at={issue.closed_at} />
          <div className="flex items-center gap-1.5 text-[0.6875rem] text-muted-foreground">
            <span className="w-14 shrink-0">프로젝트</span>
            <span className="font-mono">{issue.project}</span>
            <span className="ml-auto font-mono">#{issue.number}</span>
          </div>
        </div>

        <div className="mt-auto pt-6">
          {issue.status !== 'deleted' ? (
            <Button variant="destructive" size="sm" className="w-full" onClick={() => setConfirmDelete(true)}>
              <Trash2Icon />
              이슈 삭제
            </Button>
          ) : (
            <Button variant="outline" size="sm" className="w-full" onClick={() => save(statusChangePatch('backlog'), '상태')}>
              백로그로 되살리기
            </Button>
          )}
        </div>
      </aside>

      <ReasonDialog
        open={reasonFor !== null}
        onOpenChange={(o) => !o && setReasonFor(null)}
        title={`${issue.key}를 ${STATUS_LABEL[reasonFor ?? issue.status]} 상태로 바꿀까요?`}
        description="취소와 삭제는 사유가 필요합니다. 사유는 이슈의 상태 사유와 활동 기록에 남습니다."
        submitLabel="바꾸기"
        pending={patch.isPending}
        onSubmit={(reason) => {
          if (!reasonFor) return
          patch.mutate(
            { key: issue.key, patch: statusChangePatch(reasonFor, reason), version: issue.version },
            {
              onSuccess: () => setReasonFor(null),
              onError: (err) => toastError(err, '상태를 저장하지 못했습니다'),
            },
          )
        }}
      />

      <ReasonDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`${issue.key}를 삭제할까요?`}
        description="소프트 삭제입니다. 상태가 '삭제'로 바뀌고 기록은 남습니다. 상태를 바꾸면 되살릴 수 있습니다. 삭제 사유를 남깁니다."
        submitLabel="삭제"
        destructive
        pending={del.isPending}
        onSubmit={(reason) =>
          del.mutate(
            { key: issue.key, version: issue.version, reason },
            {
              onSuccess: () => {
                toast.success(`${issue.key}를 삭제했습니다`)
                setConfirmDelete(false)
              },
              onError: (err) => toastError(err, '이슈를 삭제하지 못했습니다'),
            },
          )
        }
      />
    </div>
  )
}

function IssueDetailContent({ issueKey }: { issueKey: string }) {
  const q = useIssue(issueKey)
  if (q.isPending) return <Loading />
  if (q.isError) {
    return (
      <div className="p-2">
        <QueryError error={q.error} onRetry={() => void q.refetch()} />
        <Link to="/issues" className="ml-4 text-xs text-primary hover:underline">
          이슈 목록으로
        </Link>
      </div>
    )
  }
  return <IssueDetail key={q.data.key} issue={q.data} />
}

/** 이슈 상세를 보던 화면 위의 오른쪽 패널로 연다. 라우터가 이 컴포넌트를 그리는 동안 패널이 열려 있다. */
export function IssueDetailPage() {
  const { key = '' } = useParams()
  const location = useLocation()
  const navigate = useNavigate()

  // 화면에서 열었으면 history에서 한 칸 돌아가 그 화면으로, 주소로 바로 들어왔으면 이슈 목록으로 간다.
  const close = () => {
    if (openedFromScreen(location)) navigate(-1)
    else navigate(ISSUES_PATH, { replace: true })
  }

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) close()
      }}
    >
      <SheetContent
        // 입력 칸에서 누른 Esc는 입력 칸이 쓴다(편집 취소). 입력 칸 밖에서만 패널을 닫는다.
        onEscapeKeyDown={(e) => {
          if (isEditableTarget(e.target)) e.preventDefault()
        }}
      >
        <SheetTitle className="sr-only">이슈 {key}</SheetTitle>
        <IssueDetailContent issueKey={key} />
      </SheetContent>
    </Sheet>
  )
}
