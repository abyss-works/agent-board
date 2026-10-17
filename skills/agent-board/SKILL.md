---
name: agent-board
description: agent-board 트래커를 agb CLI로 쓸 때 적용한다. 세션을 시작해 프로젝트 현황과 다음 할 일을 볼 때, 작업을 시작하거나 마치며 이슈 상태를 바꿀 때, 아이디어·다음 할 일·발견한 문제·결정 대기를 등록할 때, 최종 보고 전에 남은 일을 확인할 때 쓴다. 에이전트 도구 안의 할 일 목록(To-Do)과는 다르다.
---

# agent-board

agent-board는 프로젝트의 진행 상황과 목록형 상태(아이디어, 다음 할 일, 발견한 문제, 결정 대기)를 기록하는 트래커다. 에이전트는 `agb` CLI로 읽고 쓴다.

## 세션마다 정하는 값

- 프로젝트 키: 프로젝트 `AGENTS.md`에 적힌 트래커 프로젝트 키를 쓴다.
  - 키가 "없음"이면 이 스킬을 쓰지 않는다.
  - 키가 적혀 있는데 서버에 없으면 `agb project create <KEY> "<이름>"`으로 만든다.
- 행위자: `agb actors`에 선언된 값 중 자기 도구 이름이다. 예: `claude`, `codex`. 사람의 행위자(`owner`)는 쓰지 않는다.
- 세션 ID: 도구가 알려 주는 세션 ID를 쓴다. 모르면 세션을 시작할 때 `<행위자>-<yyyymmdd-HHMM>`을 만들어 세션 끝까지 같은 값을 쓴다.
- 행위자와 세션 ID는 모든 명령에 붙인다.
  - 셸 환경이 명령 사이에 유지되면 `AGB_ACTOR`, `AGB_SESSION_ID`를 한 번 export한다.
  - 유지되지 않으면 명령마다 옵션으로 붙인다: `agb --actor <행위자> --session-id <세션 ID> <명령>`
  - 아래 절차의 명령은 이 옵션을 생략하고 적었다.

## 출력과 종료 코드

- 출력이 파이프나 파일이면 서버 응답 JSON이 그대로 나온다. 에이전트는 JSON으로 읽는다. 사람에게 보여 줄 때만 `--table`을 붙인다.
- 종료 코드: 0 성공, 1 API·네트워크 오류, 2 사용법 오류. 1이면 stderr의 `error.code`와 `details`를 읽고 고친다.
- 긴 본문과 댓글은 `--body-file -`(표준 입력)이나 `--body-file <경로>`로 넘긴다. 셸 인용 때문에 내용이 바뀌지 않는다.

## 1. 세션을 시작할 때

1. 서버를 확인한다.
   ```
   agb health
   ```
   - Expected: `"status":"ok"`
   - 실패하면 "접속할 수 없을 때"를 따른다.
2. 프로젝트 현황을 본다.
   ```
   agb project overview <KEY>
   ```
   - 진행 중, 검토, 차단, 결정 대기, 다음 할 일, 최근 활동이 나온다.
3. 이번 작업 대상을 다른 세션이 진행 중인지 확인한다.
   ```
   agb issue list -p <KEY> --status doing
   agb issue activity <ISSUE>
   ```
   - 같은 이슈를 다른 세션이 `doing`으로 두었으면 그 이슈를 맡지 않고 사용자에게 알린다.

## 2. 등록할 때

아이디어, 다음 할 일, 발견한 문제, 결정 대기는 발견한 즉시 등록한다.

1. 같은 내용이 이미 있는지 찾는다.
   ```
   agb search "<핵심어>" -p <KEY> --status all
   ```
   - 있으면 새로 만들지 않고 그 이슈에 댓글을 단다.
2. 종류를 고른다.

   | 기록할 것 | `--type` |
   |---|---|
   | 다음 할 일 | `feature`, `bug`, `design` |
   | 아이디어 | `idea` |
   | 발견했지만 지금 고치지 않는 문제 | `observation`. 다시 볼 조건 `--recheck`가 필수다 |
   | 사용자 결정 대기 | `decision` |

3. 등록한다.
   ```
   agb issue create -p <KEY> -t "<제목>" --type <종류> [--priority P1] [--label <라벨>] [--ref <종류:값>] --body-file - <<'EOF'
   <배경, 확인한 사실, 완료 조건>
   EOF
   ```
   - 제목은 무엇을 할지 한 줄로 쓴다.
   - 우선순위를 정할 근거가 없으면 기본값(P2)으로 둔다.
   - 참조는 `종류:값`이다. 종류는 `repo`, `file`, `url`, `pr`, `issue`, `doc`이다.
   - Expected: 종료 코드 0과 새 이슈 키(`<KEY>-<번호>`)

## 3. 작업을 시작할 때

1. 맡을 이슈를 `doing`으로 바꾼다. 이슈가 없으면 2절로 먼저 등록한다.
   ```
   agb issue update <ISSUE> --status doing
   ```
   - Expected: 응답의 `status`가 `doing`. 상태 변경에 행위자와 세션 ID가 함께 남는다.

## 4. 진행 중

- 상태가 실제로 바뀔 때만 댓글을 단다. 예: 중간 결과, 범위 변경, 막힘. 같은 내용을 되풀이하지 않는다.
  ```
  agb issue comment <ISSUE> --body-file - <<'EOF'
  <내용>
  EOF
  ```
- 막혔다고 판단하면 원인을 확인한 근거(실행한 명령과 결과)를 댓글에 쓴다.
- 라벨, 선행 이슈, 참조를 하나씩 더하거나 뺄 때는 `--add-*`, `--remove-*`를 쓴다. 현재 값을 읽고 `If-Match`로 보내므로 다른 세션의 변경을 덮어쓰지 않는다. 412로 실패하면 `agb issue get <ISSUE>`로 다시 읽고 고친다.

## 5. 마칠 때

1. 끝난 이슈를 닫고 결과를 댓글로 남긴다. 결과에는 커밋 해시와 검증 명령의 결과를 쓴다.
   ```
   agb issue update <ISSUE> --status done
   agb issue comment <ISSUE> --body-file - <<'EOF'
   <결과와 증거>
   EOF
   ```
   - 사용자의 확인이 필요한 결과는 `done` 대신 `review`로 둔다.
2. 결정 대기 이슈에 결정이 나면 결정 기록 파일명을 참조로 넣고 닫는다.
   ```
   agb issue update <ISSUE> --add-ref doc:<결정 기록 파일명> --status done
   ```
3. 최종 보고 전에 현황을 다시 본다.
   ```
   agb project overview <KEY>
   ```
   - 이번 작업에서 남은 이슈의 개수와 키를 보고의 결론에 쓴다.

## 지키는 것

- 이슈와 프로젝트를 지우지 않는다. `agb issue delete`, `agb project delete`를 쓰지 않는다. 필요 없어진 이슈는 `cancelled`로 바꾼다.
- 활동 기록은 수정 요청마다 바뀐 필드의 이전 값과 새 값을 모두 남긴다. `agb issue activity <ISSUE>`로 본다.
- 스테이징이나 테스트 서버에 기록하지 않는다. 기록은 설정 파일의 서버 하나에만 한다.

## 접속할 수 없을 때

- 등록할 항목을 최종 보고에 모으고 사용자에게 알린다. 접속되면 등록한다.
- 서버를 직접 띄우거나 다시 시작하지 않는다.

## Windows

- Git Bash의 heredoc은 UTF-8로 넘어간다.
- PowerShell 5.1에서 파이프로 본문을 넘기면 셸 인코딩 때문에 한글이 깨질 수 있다. 본문을 UTF-8 파일로 저장하고 `--body-file <경로>`로 넘긴다.
- JSON 출력을 다른 프로그램으로 읽을 때는 UTF-8로 디코딩한다.
