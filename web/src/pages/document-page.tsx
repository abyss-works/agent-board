import { ArrowLeftIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link, useParams } from 'react-router'
import { useDocument } from '@/api/queries'
import type { RecordDocument } from '@/api/types'
import { Loading, QueryError } from '@/components/feedback'
import { IssueLink } from '@/components/issue/issue-link'
import { LabelChip } from '@/components/issue/meta'
import { PageHeader } from '@/components/layout/page-header'
import { Markdown } from '@/components/markdown'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { frontMatterIssueKeys, splitFrontMatter } from '@/lib/documents'
import { DOCUMENT_KIND_LABEL } from '@/lib/labels'
import { formatKst } from '@/lib/time'

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex gap-3 py-1">
      <dt className="w-16 shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 flex-1 break-all">{children}</dd>
    </div>
  )
}

function Meta({ doc, issueKeys }: { doc: RecordDocument; issueKeys: string[] }) {
  return (
    <dl className="text-[0.6875rem]">
      <Field label="프로젝트">
        <span className="font-mono">{doc.project}</span>
      </Field>
      <Field label="종류">{DOCUMENT_KIND_LABEL[doc.kind]}</Field>
      <Field label="날짜">{doc.date ?? '없음'}</Field>
      <Field label="위치">{doc.location}</Field>
      {doc.tags.length > 0 ? (
        <Field label="태그">
          <div className="flex flex-wrap gap-1">
            {doc.tags.map((t) => (
              <LabelChip key={t} label={t} />
            ))}
          </div>
        </Field>
      ) : null}
      {issueKeys.length > 0 ? (
        <Field label="이슈">
          <div className="flex flex-wrap gap-2">
            {issueKeys.map((k) => (
              <IssueLink key={k} issueKey={k} className="font-mono text-primary hover:underline">
                {k}
              </IssueLink>
            ))}
          </div>
        </Field>
      ) : null}
      <Field label="파일">
        <span className="font-mono">{doc.name}</span>
      </Field>
      <Field label="등록">
        {doc.created_by} · {formatKst(doc.created_at)} KST
      </Field>
      <Field label="SHA-256">
        <span className="font-mono" title={doc.sha256}>
          {doc.sha256.slice(0, 12)}
        </span>
      </Field>
    </dl>
  )
}

export function DocumentPage() {
  const { project = '', name = '' } = useParams()
  const q = useDocument(project, name)
  const doc = q.data
  const { frontMatter, body } = splitFrontMatter(doc?.content ?? '')

  return (
    <>
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            <Button variant="ghost" size="icon-sm" asChild>
              <Link to="/documents" aria-label="기록 목록으로" title="기록 목록으로">
                <ArrowLeftIcon />
              </Link>
            </Button>
            기록
          </span>
        }
        meta={doc ? <Badge variant="outline">{DOCUMENT_KIND_LABEL[doc.kind]}</Badge> : null}
      />
      <div className="min-h-0 flex-1 overflow-y-auto">
        {q.isPending ? <Loading /> : null}
        {q.isError ? <QueryError error={q.error} onRetry={() => void q.refetch()} /> : null}
        {doc ? (
          <div className="mx-auto flex max-w-6xl gap-8 px-6 py-5">
            <article className="min-w-0 flex-1">
              <Markdown source={body} />
            </article>
            <aside className="w-64 shrink-0">
              <Meta doc={doc} issueKeys={frontMatterIssueKeys(frontMatter)} />
            </aside>
          </div>
        ) : null}
      </div>
    </>
  )
}
