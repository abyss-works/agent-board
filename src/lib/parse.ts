/** 공백·쉼표로 나눈 라벨 목록. 소문자로 맞추고 중복은 하나로 합친다. */
export function parseLabels(text: string): string[] {
  return [...new Set(text.split(/[\s,]+/).map((s) => s.trim().toLowerCase()).filter(Boolean))]
}

/** 공백·쉼표로 나눈 이슈 키 목록. 대문자로 맞추고 중복은 하나로 합친다. */
export function parseIssueKeys(text: string): string[] {
  return [...new Set(text.split(/[\s,]+/).map((s) => s.trim().toUpperCase()).filter(Boolean))]
}

/** 공백으로 나눈 참조(`종류:값`) 목록. 중복은 하나로 합친다. */
export function parseRefs(text: string): string[] {
  return [...new Set(text.split(/\s+/).map((s) => s.trim()).filter(Boolean))]
}
