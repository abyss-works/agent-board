import { Link, useLocation, type LinkProps } from 'react-router'
import { issueLinkProps } from '@/lib/overlay'

type Props = Omit<LinkProps, 'to' | 'state' | 'replace'> & { issueKey: string }

/** 이슈 상세를 지금 보는 화면 위 오버레이로 여는 링크. 오버레이 안에서는 같은 오버레이의 내용을 바꾼다. */
export function IssueLink({ issueKey, ...props }: Props) {
  const location = useLocation()
  return <Link {...issueLinkProps(location, issueKey)} {...props} />
}
