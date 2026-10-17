import { ISSUE_STATUSES, ISSUE_TYPES, PRIORITIES, type IssueStatus, type IssueType, type Priority } from '@/api/types'
import { PRIORITY_LABEL, STATUS_LABEL, TYPE_LABEL } from '@/lib/labels'
import { PriorityIcon, StatusIcon, TypeIcon } from './meta'
import type { SelectOption } from './property-select'

export const STATUS_OPTIONS: SelectOption<IssueStatus>[] = ISSUE_STATUSES.map((s) => ({
  value: s,
  label: STATUS_LABEL[s],
  icon: <StatusIcon status={s} />,
}))

export const PRIORITY_OPTIONS: SelectOption<Priority>[] = PRIORITIES.map((p) => ({
  value: p,
  label: PRIORITY_LABEL[p],
  icon: <PriorityIcon priority={p} />,
}))

export const TYPE_OPTIONS: SelectOption<IssueType>[] = ISSUE_TYPES.map((t) => ({
  value: t,
  label: TYPE_LABEL[t],
  icon: <TypeIcon type={t} />,
}))
