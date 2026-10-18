export const ISSUES_PATH = '/issues'

/** 이슈 상세 주소 `/issues/<키>`. 라우트 매칭처럼 대소문자와 끝 슬래시를 구분하지 않는다. */
const ISSUE_PATH = new RegExp(`^${ISSUES_PATH}/[^/]+/?$`, 'i')

export interface PathLike {
  pathname: string
  search: string
  hash: string
}

export interface LocationLike extends PathLike {
  state: unknown
}

/** 화면에서 이슈를 열 때 history state에 싣는 값. 오버레이 밑에 그릴 화면의 위치다. */
export interface OverlayState {
  backgroundLocation: PathLike
}

const ISSUE_LIST: PathLike = { pathname: ISSUES_PATH, search: '', hash: '' }

export function issuePath(key: string): string {
  return `${ISSUES_PATH}/${key}`
}

export function isIssuePath(pathname: string): boolean {
  return ISSUE_PATH.test(pathname)
}

function savedBackground(state: unknown): PathLike | null {
  return (state as Partial<OverlayState> | null)?.backgroundLocation ?? null
}

/**
 * 이슈 오버레이 밑에 그릴 화면의 위치. 오버레이가 열려 있지 않으면(이슈 주소가 아니면) null이다.
 * 화면에서 열었으면 그 화면이고, 이슈 주소로 바로 들어왔으면 이슈 목록이다.
 */
export function backgroundOf(location: LocationLike): PathLike | null {
  if (!isIssuePath(location.pathname)) return null
  return savedBackground(location.state) ?? ISSUE_LIST
}

/** 화면에서 연 오버레이면 true다. 이때 history에서 한 칸 돌아가면 그 화면이다. */
export function openedFromScreen(location: LocationLike): boolean {
  return isIssuePath(location.pathname) && savedBackground(location.state) !== null
}

/**
 * 이슈로 가는 링크의 이동 방식.
 * 화면에서는 현재 위치를 배경으로 담아 새 항목으로 열고, 오버레이 안에서는 배경 정보를 그대로 둔 채
 * 현재 항목을 바꾼다. 그래서 오버레이는 어디서 열었든 history 항목 하나만 차지한다.
 */
export function issueLinkProps(location: LocationLike, key: string): { to: string; state: unknown; replace: boolean } {
  const to = issuePath(key)
  if (isIssuePath(location.pathname)) return { to, state: location.state, replace: true }
  const { pathname, search, hash } = location
  const state: OverlayState = { backgroundLocation: { pathname, search, hash } }
  return { to, state, replace: false }
}
