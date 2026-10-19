const USER_CODE_INPUT = /^[A-Za-z0-9]{8}$/

/**
 * 입력한 사용자 코드를 `XXXX-XXXX`로 바꾼다. 대소문자를 가리지 않고, 하이픈과 공백은 어느 자리에 있어도 무시한다.
 * 영문자와 숫자 8자가 아니면 조회할 수 없는 입력이므로 null이다.
 */
export function normalizeUserCode(input: string): string | null {
  const compact = input.replace(/[-\s]/g, '')
  if (!USER_CODE_INPUT.test(compact)) return null
  const code = compact.toUpperCase()
  return `${code.slice(0, 4)}-${code.slice(4)}`
}
