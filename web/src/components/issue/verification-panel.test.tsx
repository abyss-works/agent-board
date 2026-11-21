import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { issueSummary } from '@/api/test-fixtures'
import type { Issue } from '@/api/types'
import { VerificationBanner } from './verification-panel'

function makeIssue(partial: Partial<Issue> = {}): Issue {
  return {
    ...issueSummary(partial),
    body: '',
    ...partial,
  }
}

const noop = () => {}

describe('VerificationBanner', () => {
  it('none 상태이고 지정 입력이 닫혀 있으면 아무 배너도 그리지 않는다', () => {
    const html = renderToStaticMarkup(
      <VerificationBanner
        issue={makeIssue({ verification_state: 'none' })}
        requesting={false}
        onCancelRequest={noop}
        onRequestPending={noop}
        onVerify={noop}
        onUnverify={noop}
        onClear={noop}
        onSaveNote={noop}
      />,
    )
    expect(html).toBe('')
  })

  it('none 상태에서 지정 입력이 열리면 메모 입력과 지정 버튼을 보인다', () => {
    const html = renderToStaticMarkup(
      <VerificationBanner
        issue={makeIssue({ verification_state: 'none' })}
        requesting
        onCancelRequest={noop}
        onRequestPending={noop}
        onVerify={noop}
        onUnverify={noop}
        onClear={noop}
        onSaveNote={noop}
      />,
    )
    expect(html).toContain('검증 필요 지정')
    expect(html).toContain('확인할 내용 (선택)')
  })

  it('pending 상태이면 릴리즈 차단 배너, 메모, 검증 완료·해제 버튼을 보인다', () => {
    const html = renderToStaticMarkup(
      <VerificationBanner
        issue={makeIssue({ verification_state: 'pending', verification_note: '스테이징 배포 후 결제 흐름 확인' })}
        requesting={false}
        onCancelRequest={noop}
        onRequestPending={noop}
        onVerify={noop}
        onUnverify={noop}
        onClear={noop}
        onSaveNote={noop}
      />,
    )
    expect(html).toContain('검증 필요')
    expect(html).toContain('릴리즈 차단')
    expect(html).toContain('스테이징 배포 후 결제 흐름 확인')
    expect(html).toContain('검증 완료')
    expect(html).toContain('메모 편집')
    expect(html).toContain('해제')
  })

  it('verified 상태이면 검증 완료 배지, 검증자·시각·메모와 되돌리기·해제 버튼을 보인다', () => {
    const html = renderToStaticMarkup(
      <VerificationBanner
        issue={makeIssue({
          verification_state: 'verified',
          verification_note: '실기기 확인 완료',
          verified_by: 'owner',
          verified_at: '2026-10-17T07:00:00.000Z',
        })}
        requesting={false}
        onCancelRequest={noop}
        onRequestPending={noop}
        onVerify={noop}
        onUnverify={noop}
        onClear={noop}
        onSaveNote={noop}
      />,
    )
    expect(html).toContain('검증 완료')
    expect(html).toContain('owner')
    expect(html).toContain('실기기 확인 완료')
    expect(html).toContain('다시 검증 필요로 되돌리기')
    expect(html).toContain('해제')
  })
})
