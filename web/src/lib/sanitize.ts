const SAFE_PROTOCOLS = ['http:', 'https:', 'mailto:']

/**
 * 링크·이미지 주소를 거른다. http(s), mailto, 상대 경로만 두고 javascript:, data:, vbscript: 등은 비운다.
 * react-markdown은 빈 문자열을 받으면 href를 넣지 않는다.
 */
export function safeUrl(url: string): string {
  const trimmed = url.trim()
  const colon = trimmed.indexOf(':')
  if (colon === -1) return trimmed
  // 콜론 앞에 /, ?, # 가 먼저 나오면 프로토콜이 아니라 상대 경로다.
  const before = trimmed.slice(0, colon)
  if (/[/?#]/.test(before)) return trimmed
  const protocol = `${before.toLowerCase()}:`
  return SAFE_PROTOCOLS.includes(protocol) ? trimmed : ''
}

export function isExternalUrl(href: string): boolean {
  return /^(https?:|mailto:)/i.test(href)
}
