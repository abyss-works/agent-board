/** 맨 앞의 YAML 프런트매터(`---`로 열고 닫는 블록)와 본문으로 나눈다. 닫히지 않으면 전체가 본문이다. */
export function splitFrontMatter(content: string): { frontMatter: string; body: string } {
  const lines = content.split('\n')
  if (lines[0]?.trimEnd() !== '---') return { frontMatter: '', body: content }
  const end = lines.findIndex((l, i) => i > 0 && l.trimEnd() === '---')
  if (end < 0) return { frontMatter: '', body: content }
  return { frontMatter: lines.slice(1, end).join('\n'), body: lines.slice(end + 1).join('\n').replace(/^\n+/, '') }
}

/** 프런트매터 `issue:` 값에 적힌 이슈 키. 예: `issue: AGB-6, AGB-23`. */
export function frontMatterIssueKeys(frontMatter: string): string[] {
  const line = frontMatter.split('\n').find((l) => /^issue\s*:/.test(l))
  if (!line) return []
  return line.slice(line.indexOf(':') + 1).match(/\b[A-Z][A-Z0-9]*-\d+\b/g) ?? []
}

/** 기록 문서 상세 주소 */
export function documentPath(project: string, name: string): string {
  return `/documents/${project}/${encodeURIComponent(name)}`
}
