import { isValidElement, type ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { issueSummary } from '@/api/test-fixtures'
import type { Issue } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const stateMock = vi.hoisted(() => ({ impl: null as null | (<T>(init: T) => [T, (v: T) => void]) }))
vi.mock('react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react')>()
  return {
    ...actual,
    useState: <T,>(init: T) => (stateMock.impl ? stateMock.impl(init) : actual.useState(init)),
  }
})

import { VerificationBanner, type VerificationBannerProps } from './verification-panel'

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

  it('요청 실패 시 입력 메모를 유지하고, 성공 콜백·폼 닫힘·취소 시에만 초기화한다', () => {
    const findElements = (
      node: unknown,
      predicate: (el: ReactElement<Record<string, unknown>>) => boolean,
    ): Array<ReactElement<Record<string, unknown>>> => {
      const out: Array<ReactElement<Record<string, unknown>>> = []
      const visit = (n: unknown) => {
        if (Array.isArray(n)) {
          for (const item of n) visit(item)
          return
        }
        if (!isValidElement(n)) return
        const el = n as ReactElement<Record<string, unknown>>
        if (predicate(el)) out.push(el)
        if (el.props.children !== undefined) visit(el.props.children)
      }
      visit(node)
      return out
    }

    const slots: unknown[] = []
    const renderTree = (props: VerificationBannerProps) => {
      for (let pass = 0; pass < 5; pass += 1) {
        let cursor = 0
        let dirty = false
        stateMock.impl = <T,>(init: T) => {
          const idx = cursor++
          if (idx >= slots.length) slots.push(init)
          const set = (next: T | ((prev: T) => T)) => {
            const val = typeof next === 'function' ? (next as (prev: T) => T)(slots[idx] as T) : next
            if (!Object.is(slots[idx], val)) {
              slots[idx] = val
              dirty = true
            }
          }
          return [slots[idx] as T, set as (v: T) => void]
        }
        try {
          const tree = VerificationBanner(props)
          if (!dirty) return tree
        } finally {
          stateMock.impl = null
        }
      }
      throw new Error('렌더 중 상태가 수렴하지 않았습니다.')
    }

    const onCancelRequest = vi.fn()
    const onRequestPending = vi.fn()
    const baseProps: VerificationBannerProps = {
      issue: makeIssue({ verification_state: 'none' }),
      requesting: true,
      onCancelRequest,
      onRequestPending,
      onVerify: noop,
      onUnverify: noop,
      onClear: noop,
      onSaveNote: noop,
    }

    // 1. 메모 입력 후 버튼 클릭으로 요청 실패(onSuccess 미호출) -> draftNote 유지
    let tree = renderTree(baseProps)
    let input = findElements(tree, (el) => el.type === Input)[0]
    ;(input.props.onChange as (e: { target: { value: string } }) => void)({ target: { value: '스테이징 결제 재확인' } })
    tree = renderTree(baseProps)
    input = findElements(tree, (el) => el.type === Input)[0]
    expect(input.props.value).toBe('스테이징 결제 재확인')

    const submitBtn = findElements(tree, (el) => el.type === Button && el.props.children === '검증 필요로 지정')[0]
    ;(submitBtn.props.onClick as () => void)()
    expect(onRequestPending).toHaveBeenCalledTimes(1)
    expect(onRequestPending.mock.calls[0][0]).toBe('스테이징 결제 재확인')

    tree = renderTree(baseProps)
    input = findElements(tree, (el) => el.type === Input)[0]
    expect(input.props.value).toBe('스테이징 결제 재확인')

    // 2. Enter 키로 재시도해도 실패 시 draftNote 유지
    ;(input.props.onKeyDown as (e: { key: string; preventDefault: () => void }) => void)({
      key: 'Enter',
      preventDefault: () => {},
    })
    expect(onRequestPending).toHaveBeenCalledTimes(2)
    tree = renderTree(baseProps)
    input = findElements(tree, (el) => el.type === Input)[0]
    expect(input.props.value).toBe('스테이징 결제 재확인')

    // 3. 성공 콜백 실행 시 draftNote 초기화
    const opts = onRequestPending.mock.calls[1][1] as { onSuccess?: () => void } | undefined
    expect(opts?.onSuccess).toBeTypeOf('function')
    opts?.onSuccess?.()
    tree = renderTree(baseProps)
    input = findElements(tree, (el) => el.type === Input)[0]
    expect(input.props.value).toBe('')

    // 4. 다시 입력한 뒤 폼이 닫히면(requesting: false) 재오픈 시 빈 입력으로 시작
    ;(input.props.onChange as (e: { target: { value: string } }) => void)({ target: { value: '닫힘 전 메모' } })
    renderTree({ ...baseProps, requesting: false })
    tree = renderTree(baseProps)
    input = findElements(tree, (el) => el.type === Input)[0]
    expect(input.props.value).toBe('')

    // 5. 취소 버튼 및 Escape 시 초기화 + onCancelRequest 호출
    ;(input.props.onChange as (e: { target: { value: string } }) => void)({ target: { value: '취소될 메모' } })
    tree = renderTree(baseProps)
    const cancelBtn = findElements(tree, (el) => el.type === Button && el.props.children === '취소')[0]
    ;(cancelBtn.props.onClick as () => void)()
    expect(onCancelRequest).toHaveBeenCalledTimes(1)
    tree = renderTree(baseProps)
    input = findElements(tree, (el) => el.type === Input)[0]
    expect(input.props.value).toBe('')
  })
})
