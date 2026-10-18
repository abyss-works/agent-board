import { describe, expect, it } from 'vitest'
import { MENU_SHORTCUTS, isEditableTarget, shortcutRoute, type KeyEventLike } from './shortcuts'

function event(key: string, partial: Partial<KeyEventLike> = {}): KeyEventLike {
  return { key, ctrlKey: false, altKey: false, metaKey: false, target: { tagName: 'BODY' }, ...partial }
}

describe('isEditableTarget', () => {
  it('입력 칸, 텍스트 영역, 셀렉트, 편집 가능 요소는 참이다', () => {
    expect(isEditableTarget({ tagName: 'INPUT' })).toBe(true)
    expect(isEditableTarget({ tagName: 'TEXTAREA' })).toBe(true)
    expect(isEditableTarget({ tagName: 'SELECT' })).toBe(true)
    expect(isEditableTarget({ tagName: 'DIV', isContentEditable: true })).toBe(true)
  })
  it('일반 요소와 null은 거짓이다', () => {
    expect(isEditableTarget({ tagName: 'DIV', isContentEditable: false })).toBe(false)
    expect(isEditableTarget({ tagName: 'BUTTON' })).toBe(false)
    expect(isEditableTarget(null)).toBe(false)
  })
})

describe('MENU_SHORTCUTS', () => {
  it('현황, 보드, 이슈, 활동, 마일스톤 순서이고 키는 1부터 5다', () => {
    expect(MENU_SHORTCUTS.map((s) => `${s.key}:${s.label}:${s.path}`)).toEqual([
      '1:현황:/overview',
      '2:보드:/board',
      '3:이슈:/issues',
      '4:활동:/activity',
      '5:마일스톤:/milestones',
    ])
  })
  it('키와 경로가 겹치지 않는다', () => {
    expect(new Set(MENU_SHORTCUTS.map((s) => s.key)).size).toBe(MENU_SHORTCUTS.length)
    expect(new Set(MENU_SHORTCUTS.map((s) => s.path)).size).toBe(MENU_SHORTCUTS.length)
  })
})

describe('shortcutRoute', () => {
  it('1~5는 사이드바 메뉴 경로다', () => {
    expect(shortcutRoute(event('1'))).toBe('/overview')
    expect(shortcutRoute(event('2'))).toBe('/board')
    expect(shortcutRoute(event('3'))).toBe('/issues')
    expect(shortcutRoute(event('4'))).toBe('/activity')
    expect(shortcutRoute(event('5'))).toBe('/milestones')
  })
  it('다른 키는 null이다', () => {
    expect(shortcutRoute(event('0'))).toBeNull()
    expect(shortcutRoute(event('6'))).toBeNull()
    expect(shortcutRoute(event('g'))).toBeNull()
    expect(shortcutRoute(event('Enter'))).toBeNull()
  })
  it('입력 칸에 포커스가 있으면 동작하지 않는다', () => {
    expect(shortcutRoute(event('1', { target: { tagName: 'INPUT' } }))).toBeNull()
    expect(shortcutRoute(event('2', { target: { tagName: 'TEXTAREA' } }))).toBeNull()
    expect(shortcutRoute(event('3', { target: { tagName: 'DIV', isContentEditable: true } }))).toBeNull()
    expect(shortcutRoute(event('5', { target: { tagName: 'INPUT' } }))).toBeNull()
  })
  it('Ctrl, Alt, Meta가 눌려 있으면 동작하지 않는다', () => {
    expect(shortcutRoute(event('1', { ctrlKey: true }))).toBeNull()
    expect(shortcutRoute(event('1', { altKey: true }))).toBeNull()
    expect(shortcutRoute(event('1', { metaKey: true }))).toBeNull()
    expect(shortcutRoute(event('5', { ctrlKey: true }))).toBeNull()
  })
  it('이슈 오버레이가 열려 있으면 1~5도 동작하지 않는다', () => {
    for (const key of ['1', '2', '3', '4', '5']) expect(shortcutRoute(event(key), true)).toBeNull()
  })
  it('이슈 오버레이가 닫혀 있으면 기존대로 이동한다', () => {
    expect(shortcutRoute(event('1'), false)).toBe('/overview')
    expect(shortcutRoute(event('4'), false)).toBe('/activity')
    expect(shortcutRoute(event('5'), false)).toBe('/milestones')
  })
})
