import { describe, expect, it } from 'vitest'
import { parseIssueKeys, parseLabels, parseRefs } from './parse'

describe('parseLabels', () => {
  it('공백과 쉼표로 나누고 소문자·중복 제거를 한다', () => {
    expect(parseLabels('API, cli  api\nweb')).toEqual(['api', 'cli', 'web'])
  })
  it('빈 입력은 빈 배열이다', () => {
    expect(parseLabels('')).toEqual([])
    expect(parseLabels('  , ')).toEqual([])
  })
})

describe('parseIssueKeys', () => {
  it('대문자로 맞추고 중복을 없앤다', () => {
    expect(parseIssueKeys('agb-1, AGB-2 agb-1')).toEqual(['AGB-1', 'AGB-2'])
  })
})

describe('parseRefs', () => {
  it('공백으로만 나눈다(값에 쉼표가 올 수 있다)', () => {
    expect(parseRefs('repo:wsk0715/agent-board doc:api.md,notes')).toEqual(['repo:wsk0715/agent-board', 'doc:api.md,notes'])
  })
})
