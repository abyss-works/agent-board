import { describe, expect, it } from 'vitest'
import { backgroundOf, isIssuePath, issueLinkProps, issuePath, openedFromScreen, type LocationLike } from './overlay'

function loc(pathname: string, partial: Partial<LocationLike> = {}): LocationLike {
  return { pathname, search: '', hash: '', state: null, ...partial }
}

const board = { pathname: '/board', search: '', hash: '' }
const doingList = { pathname: '/issues', search: '?status=doing&q=api', hash: '' }

describe('issuePath', () => {
  it('이슈 키로 상세 주소를 만든다', () => {
    expect(issuePath('AGB-12')).toBe('/issues/AGB-12')
  })
})

describe('isIssuePath', () => {
  it('/issues/<키>는 이슈 주소다', () => {
    expect(isIssuePath('/issues/AGB-1')).toBe(true)
    expect(isIssuePath('/issues/AGB-1/')).toBe(true)
  })
  it('목록, 다른 화면, 더 깊은 경로는 이슈 주소가 아니다', () => {
    expect(isIssuePath('/issues')).toBe(false)
    expect(isIssuePath('/issues/')).toBe(false)
    expect(isIssuePath('/board')).toBe(false)
    expect(isIssuePath('/overview')).toBe(false)
    expect(isIssuePath('/')).toBe(false)
    expect(isIssuePath('/issues/AGB-1/comments')).toBe(false)
  })
  it('라우터처럼 대소문자를 구분하지 않는다', () => {
    expect(isIssuePath('/Issues/AGB-1')).toBe(true)
  })
})

describe('backgroundOf', () => {
  it('화면에서 열었으면 state에 담긴 그 화면의 위치를 돌려준다', () => {
    expect(backgroundOf(loc('/issues/AGB-1', { state: { backgroundLocation: board } }))).toEqual(board)
  })
  it('목록의 필터(search)도 그대로 돌려준다', () => {
    expect(backgroundOf(loc('/issues/AGB-1', { state: { backgroundLocation: doingList } }))).toEqual(doingList)
  })
  it('이슈 주소로 바로 들어왔으면 이슈 목록을 배경으로 한다', () => {
    expect(backgroundOf(loc('/issues/AGB-1'))).toEqual({ pathname: '/issues', search: '', hash: '' })
  })
  it('state에 배경 위치가 없으면 이슈 목록을 배경으로 한다', () => {
    expect(backgroundOf(loc('/issues/AGB-1', { state: { other: 1 } }))?.pathname).toBe('/issues')
  })
  it('이슈 주소가 아니면 오버레이가 없으므로 null이다', () => {
    expect(backgroundOf(loc('/board'))).toBeNull()
    expect(backgroundOf(loc('/issues'))).toBeNull()
  })
  it('이슈 주소가 아니면 state에 배경 위치가 남아 있어도 null이다', () => {
    expect(backgroundOf(loc('/overview', { state: { backgroundLocation: board } }))).toBeNull()
  })
})

describe('openedFromScreen', () => {
  it('배경 위치가 state에 있으면 화면에서 연 것이다', () => {
    expect(openedFromScreen(loc('/issues/AGB-1', { state: { backgroundLocation: board } }))).toBe(true)
  })
  it('이슈 주소로 바로 들어왔으면 아니다', () => {
    expect(openedFromScreen(loc('/issues/AGB-1'))).toBe(false)
    expect(openedFromScreen(loc('/issues/AGB-1', { state: { other: 1 } }))).toBe(false)
  })
  it('이슈 주소가 아니면 아니다', () => {
    expect(openedFromScreen(loc('/board', { state: { backgroundLocation: board } }))).toBe(false)
  })
})

describe('issueLinkProps', () => {
  it('화면에서는 현재 위치를 배경으로 담아 새 history 항목으로 연다', () => {
    const here = loc('/issues', { search: '?status=doing&q=api', hash: '#top' })
    expect(issueLinkProps(here, 'AGB-3')).toEqual({
      to: '/issues/AGB-3',
      state: { backgroundLocation: { pathname: '/issues', search: '?status=doing&q=api', hash: '#top' } },
      replace: false,
    })
  })
  it('배경 위치에는 pathname, search, hash만 담는다', () => {
    const here = loc('/board', { state: { stale: true } })
    expect(issueLinkProps(here, 'AGB-3').state).toEqual({ backgroundLocation: board })
  })
  it('오버레이 안에서는 배경 state를 그대로 두고 현재 항목을 바꾼다', () => {
    const state = { backgroundLocation: board }
    expect(issueLinkProps(loc('/issues/AGB-1', { state }), 'AGB-2')).toEqual({ to: '/issues/AGB-2', state, replace: true })
  })
  it('바로 들어온 오버레이에서는 state 없이 현재 항목을 바꾼다', () => {
    expect(issueLinkProps(loc('/issues/AGB-1'), 'AGB-2')).toEqual({ to: '/issues/AGB-2', state: null, replace: true })
  })
  it('같은 이슈로 가도 오버레이 안이면 항목을 늘리지 않는다', () => {
    const state = { backgroundLocation: board }
    expect(issueLinkProps(loc('/issues/AGB-1', { state }), 'AGB-1').replace).toBe(true)
  })
})
