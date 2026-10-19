import { describe, expect, it } from 'vitest'
import { normalizeUserCode } from './device'

describe('normalizeUserCode', () => {
  it('XXXX-XXXX 꼴은 그대로 돌려준다', () => {
    expect(normalizeUserCode('BDFG-HJKL')).toBe('BDFG-HJKL')
  })

  it('소문자를 대문자로 바꾼다', () => {
    expect(normalizeUserCode('bdfg-hjkl')).toBe('BDFG-HJKL')
    expect(normalizeUserCode('BdFg-hJkL')).toBe('BDFG-HJKL')
  })

  it('하이픈이 없으면 4자씩 나눠 하이픈을 붙인다', () => {
    expect(normalizeUserCode('BDFGHJKL')).toBe('BDFG-HJKL')
    expect(normalizeUserCode('bdfghjkl')).toBe('BDFG-HJKL')
  })

  it('하이픈은 어느 자리에 있어도 무시한다', () => {
    expect(normalizeUserCode('BD-FGHJ-KL')).toBe('BDFG-HJKL')
    expect(normalizeUserCode('-BDFG-HJKL-')).toBe('BDFG-HJKL')
    expect(normalizeUserCode('B-D-F-G-H-J-K-L')).toBe('BDFG-HJKL')
  })

  it('공백, 탭, 줄바꿈은 앞뒤와 가운데 어디에 있어도 무시한다', () => {
    expect(normalizeUserCode(' BDFG-HJKL ')).toBe('BDFG-HJKL')
    expect(normalizeUserCode('bdfg hjkl')).toBe('BDFG-HJKL')
    expect(normalizeUserCode('BDFG - HJKL')).toBe('BDFG-HJKL')
    expect(normalizeUserCode('BDFG\tHJKL\n')).toBe('BDFG-HJKL')
  })

  it('전각 공백과 줄 바꿈 없는 공백도 공백으로 본다', () => {
    expect(normalizeUserCode('BDFG　HJKL')).toBe('BDFG-HJKL')
    expect(normalizeUserCode('BDFG HJKL')).toBe('BDFG-HJKL')
  })

  it('정규화한 값을 다시 정규화해도 같다', () => {
    const once = normalizeUserCode(' bdfg hjkl ')
    expect(once).not.toBeNull()
    expect(normalizeUserCode(once as string)).toBe(once)
  })

  it('하이픈과 공백을 뺀 글자 수가 8이 아니면 null이다', () => {
    expect(normalizeUserCode('')).toBeNull()
    expect(normalizeUserCode('BDFG')).toBeNull()
    expect(normalizeUserCode('BDFG-HJK')).toBeNull()
    expect(normalizeUserCode('BDFG-HJKLM')).toBeNull()
    expect(normalizeUserCode('BDFG-HJKL-BDFG')).toBeNull()
  })

  it('하이픈과 공백뿐이면 null이다', () => {
    expect(normalizeUserCode('-')).toBeNull()
    expect(normalizeUserCode('   ')).toBeNull()
    expect(normalizeUserCode('---- ----')).toBeNull()
  })

  it('영문자와 숫자가 아닌 글자가 있으면 null이다', () => {
    expect(normalizeUserCode('BDFG-HJK!')).toBeNull()
    expect(normalizeUserCode('BDFG_HJKL')).toBeNull()
    expect(normalizeUserCode('BDFG.HJKL')).toBeNull()
    expect(normalizeUserCode('ㅠㅇㄹㅁ-ㅊㅍㅂㅈ')).toBeNull()
    expect(normalizeUserCode('abc\u{1F600}xyz')).toBeNull()
  })

  it('주소 경로를 바꿀 수 있는 글자는 받지 않는다', () => {
    expect(normalizeUserCode('BDFG/HJKL')).toBeNull()
    expect(normalizeUserCode('../issue')).toBeNull()
    expect(normalizeUserCode('BDFG%2FHJ')).toBeNull()
    expect(normalizeUserCode('BDFG?HJKL')).toBeNull()
  })

  it('형식만 본다: 영문자와 숫자 8자면 서버가 쓰지 않는 글자라도 조회 대상이다', () => {
    expect(normalizeUserCode('bdfg-hjk1')).toBe('BDFG-HJK1')
    expect(normalizeUserCode('1234-5678')).toBe('1234-5678')
  })
})
