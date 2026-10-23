import { describe, expect, it } from 'vitest'
import { documentPath, frontMatterIssueKeys, splitFrontMatter } from './documents'

describe('splitFrontMatter', () => {
  it('프런트매터와 본문을 나눈다', () => {
    const r = splitFrontMatter('---\nkind: decision\nissue: AGB-6, AGB-23\n---\n\n# 제목\n본문\n')
    expect(r.frontMatter).toBe('kind: decision\nissue: AGB-6, AGB-23')
    expect(r.body).toBe('# 제목\n본문\n')
  })

  it('닫히지 않았거나 없으면 전체가 본문이다', () => {
    expect(splitFrontMatter('---\nkind: x\n# 제목')).toEqual({ frontMatter: '', body: '---\nkind: x\n# 제목' })
    expect(splitFrontMatter('# 제목')).toEqual({ frontMatter: '', body: '# 제목' })
  })

  it('CRLF 줄의 구분선도 알아본다', () => {
    expect(splitFrontMatter('---\r\nkind: x\r\n---\r\n본문').body).toBe('본문')
  })
})

describe('frontMatterIssueKeys', () => {
  it('issue 줄의 키만 읽는다', () => {
    expect(frontMatterIssueKeys('kind: decision\nissue: AGB-6, AGB-23\nsupersedes: none')).toEqual(['AGB-6', 'AGB-23'])
    expect(frontMatterIssueKeys('title: AGB-1 관련\nissue: none')).toEqual([])
    expect(frontMatterIssueKeys('kind: decision')).toEqual([])
  })
})

it('documentPath는 이름을 인코딩한다', () => {
  expect(documentPath('AGB', '2026-10-21-1130-decision-a.md')).toBe('/documents/AGB/2026-10-21-1130-decision-a.md')
})
