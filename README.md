# agent-board

agent-board(`agb`)는 사용자 한 사람과 그 사용자의 AI 코딩 에이전트들이 여러 기기와 세션에 걸쳐 같은 맥락으로 일하게 하는 지식 저장소입니다. 이슈, 결정, 기록 문서를 한곳에 남기고 한 번에 찾습니다. 이슈 트래커는 이 저장소에 지식을 쌓는 쓰기 경로입니다.

- 에이전트는 CLI(`agb`)와 HTTP API로 이슈를 만들고 옮기며, 결정과 발견을 댓글과 기록 문서로 남깁니다.
- 사용자는 에이전트와의 대화에서 결정하고 승인합니다. 웹 화면은 현황을 읽고 필요할 때 직접 고치는 곳입니다.
- 서버, CLI, 웹 클라이언트, 에이전트용 사용 절차(스킬)가 한 저장소에 있고, 서버와 CLI는 같은 Go 바이너리입니다.

화면 문구, CLI 도움말, 오류 메시지는 한국어입니다.

## 무엇을 하는가

- 프로젝트별로 이슈를 만들고 상태(백로그, 진행, 검토, 완료, 취소, 삭제)를 옮기며, 댓글과 변경 이력을 남깁니다.
- 모든 쓰기에 행위자(사용자 또는 에이전트), 세션 ID, 기기 이름을 남깁니다. 어느 세션이 어떤 이슈를 진행 중인지(`holder`), 그 세션이 2시간 넘게 활동이 없는지(`stale`)를 활동 기록으로 계산해, 멈춘 작업을 다른 세션이 이어받을 수 있게 합니다.
- 사용자가 답할 것(검토 대기, 결정이 필요한 질문, 아직 확인하지 않은 이슈, 사용자가 직접 할 일)과 에이전트가 집어 갈 다음 작업(우선순위 큐)을 나눠 보여 줍니다(`agb agenda`).
- 결정, 사고, 보고 같은 마크다운 기록 문서를 원문 그대로 보관합니다. 올린 원문은 고칠 수 없고, 아카이브 표시와 문서 사이의 관계(대체, 관련, 이슈 연결)만 바꿀 수 있습니다.
- 이슈, 댓글, 기록 문서를 키워드(SQLite FTS5)로 찾고, 임베딩 API를 설정하면 의미 검색을 섞어 찾습니다. 이슈와 문서의 연결을 따라가 선례를 모아 봅니다(`agb recall`, `agb graph`).
- 다른 세션이나 기기에 짧은 맥락을 넘기는 만료형 메시지 공간을 둡니다(`agb msg`). 한 메시지를 여러 쪽이 읽을 수 있고, 읽은 쪽은 처리한 뒤 자기 이름으로 확인 표시를 남겨 확인하지 않은 메시지만 받아 봅니다.

## 설계 원칙

- API가 먼저입니다. CLI와 웹은 같은 REST API를 쓰는 클라이언트이고, 업무 규칙은 서버에만 있습니다.
- 서버가 데이터를 단독으로 소유합니다. SQLite 파일 하나를 서버만 열고, 여러 기기는 API로만 접근합니다.
- 기록은 지우지 않습니다. 이슈, 프로젝트, 주제, 마일스톤의 변경은 행위자, 세션, 기기, 시각, 바뀐 필드의 이전 값과 함께 남습니다. 문서의 아카이브와 관계 변경도 같은 방식으로 남고, 메시지 삭제는 삭제한 행위자와 시각만 남깁니다. 이슈, 프로젝트, 주제, 마일스톤의 삭제는 행을 지우지 않는 상태 변경이라 다른 상태로 되돌릴 수 있습니다. 메시지는 행을 지우지 않지만, 짧게 사는 만료형 데이터라 변경의 이전 값을 남기지 않고 삭제를 되돌리는 경로도 두지 않습니다. 이 두 가지가 보존 원칙의 예외입니다. 지운 메시지와 기간이 지난 메시지는 기본 목록에서만 빠지고, `agb msg list --all`과 `agb msg get`으로 볼 수 있습니다. 메시지의 확인 표시(`agb msg ack`)는 읽은 쪽 이름마다 한 번 남기고 고치거나 지우지 않으며, 메시지를 지우지도 않습니다.
- 데이터를 주고, 판단은 하지 않습니다. agb는 저장하고 꺼내 줍니다. 반복되는 문제를 규칙으로 올릴지 같은 판단은 읽는 쪽(사용자나 에이전트)이 합니다.
- 외부 도구가 아는 상태는 저장하지 않습니다. git 브랜치, 커밋, PR은 참조(`refs`)로만 가리킵니다.
- 규범은 저장소에 둡니다. 에이전트가 따를 규칙과 명세는 각 프로젝트의 git 저장소가 정본이고, agb에는 선례와 근거가 되는 기록만 모읍니다.
- 어떤 프로젝트에도 의존하지 않습니다. 문서 형식과 메타데이터는 agb가 정하고, 맞지 않는 입력은 거절합니다.

## 하지 않는 것

- 세션 안의 작업 큐(에이전트 도구의 할 일 목록)를 대신하지 않습니다.
- 다른 팀 트래커와 동기화하지 않습니다.
- 담당자 지정, 사이클(스프린트), 알림, 모바일 화면을 두지 않습니다.
- 세션을 저장 단위로 두지 않습니다. 세션 ID는 추적 정보이고, 맥락의 단위는 이슈입니다.

## 핵심 개념과 데이터 모델

저장소는 SQLite 파일 하나(`<AGB_DATA_DIR>/agb.db`)입니다. 스키마는 `internal/store/migrations/`의 SQL로 만들어지며 서버가 시작할 때 빠진 마이그레이션을 순서대로 적용합니다.

| 엔티티 | 주요 필드 | 상태 | 관계 |
|---|---|---|---|
| 행위자 (actor) | `id`(`^[a-z0-9]{1,32}$`), `kind`(`user`/`agent`) | 없음. 서버 환경변수로 선언 | 모든 쓰기의 `created_by`, `updated_by` |
| 프로젝트 (project) | `key`(`^[A-Z][A-Z0-9]{1,9}$`), `name` | `active`, `archived`, `deleted` | 이슈, 주제, 마일스톤, 문서, 메시지를 가짐 |
| 주제 (topic) | `name`, `parent`, `path`(`대주제/주제`) | `active`, `deleted` | 프로젝트 안 두 단계 분류. 이슈가 하나를 가리킴 |
| 마일스톤 (milestone) | `name`, `description`, `target_date`, `progress` | `open`, `closed`, `deleted` | 이슈가 하나를 가리킴. 진행률은 읽을 때 계산 |
| 이슈 (issue) | `key`(`APP-12` 형식), `title`, `body`, `type`, `priority`, `labels`, `refs`, `decision_needed`, `recheck_condition`, `version` | `backlog`, `doing`, `review`, `done`, `cancelled`, `deleted` | 상위 이슈(`parent`), 선행 이슈(`depends_on`), 참조(`refs`), 주제, 마일스톤 |
| 활동 (activity) | `kind`(`created`/`updated`/`commented`), `changes`, `summary`, `comment_kind`, `body` | 추가만 함 | 이슈 하나에 속함. 댓글도 활동 행 |
| 기록 문서 (document) | `name`, `kind`, `date`, `title`, `tags`, `archived`, `imported`, `sha256`, 원문 | `current`, `superseded`, `archived`(계산값) | `supersedes`, `related`(문서), `about`(이슈) |
| 메시지 (message) | `to`, `title`, `body`, `expires_at`, `reads`(읽은 쪽별 확인 표시) | `active`, `expired`, `deleted` | 프로젝트에 속함 |
| 토큰 (token) | `actor`, `client_host`, `last_used_at`, `revoked_at` | 활성, 폐기 | 에이전트 행위자 하나 |
| 임베딩, 유사 문서 | 청크별 벡터, 문서 쌍과 점수 | 파생 데이터 | 의미 검색과 `similar` 그래프 간선 |

### 이슈

- 종류: `feature`, `bug`, `design`, `idea`, `observation`. `observation`은 다시 볼 조건(`recheck_condition`)이 필수입니다.
- 종류, 상태, 결정 필요, 확인은 서로 독립된 네 축입니다.
  - 결정 필요(`decision_needed`): 사용자의 답을 기다리는 질문 한 문장입니다.
  - 확인(`ack`): 사용자가 지금의 이슈 내용을 알고 있는지 나타냅니다. 작업 흐름을 막지 않으며, 제목·본문·종류가 바뀌거나 `review`에서 `doing`으로 돌아가면 다시 미확인이 됩니다. 사용자가 만든 이슈는 만들 때 확인된 것으로 표시됩니다.
- 우선순위: `P0`~`P3`. 지정하지 않으면 `bug`는 P1, `idea`와 `observation`은 P3, 나머지는 P2입니다. P0(장애, 데이터 손실, 보안 결함)은 서버가 자동으로 매기지 않습니다.
- 에이전트가 만드는 이슈는 본문이 종류별 템플릿(`agb template show <종류>`)의 `##` 제목을 순서대로 모두 갖춰야 합니다.
- `cancelled`와 `deleted`로 바꿀 때는 사유가 필수입니다. 삭제는 상태 변경이며 다른 상태로 되돌릴 수 있습니다.
- 수정은 JSON Merge Patch이고, `ETag`/`If-Match`로 동시 수정을 막습니다(불일치 시 412).
- 30일 넘게 활동이 없는 열린 이슈는 읽을 때 `dormant`로 표시합니다. 저장하지 않으며 자동으로 닫지 않습니다.
- 라벨 `external`은 외부를 기다리는 이슈(선행 이슈로 걸어 대기를 표현), `user-task`는 사용자가 직접 할 일입니다. 둘 다 에이전트 큐에 나오지 않습니다.
- 댓글에는 종류(`decision`, `finding`, `lesson`)와 한 줄 요약을 붙일 수 있습니다. 종류가 없는 댓글은 진행 기록입니다.
- 참조 종류: `repo`, `file`, `url`, `pr`, `issue`, `doc`, `commit`, `jira`, `mr`, `adr`. `issue:`와 `doc:` 참조는 대상이 없으면 거절합니다.

### 기록 문서

- 일반 문서는 이름이 `yyyy-mm-dd-HHMM-<종류>-<slug>.md`이고 `title`이 필수입니다. 가져온 문서(`imported`)는 저장소 기준 상대 경로를 이름으로 씁니다.
- 종류는 형식(`^[a-z][a-z0-9_]*$`, 32자 이하)만 검사합니다. 어떤 종류를 쓸지는 문서를 올리는 저장소가 정합니다.
- 같은 이름에 다른 원문을 올리면 409(`immutable`)로 거절합니다. 1MiB를 넘는 문서와 `_`로 시작하거나 `.example.md`로 끝나는 서식 파일은 받지 않습니다.
- 상태는 저장하지 않고 계산합니다. 아카이브 표시가 있으면 `archived`, 다른 문서의 `supersedes`가 가리키면 `superseded`, 아니면 `current`입니다.
- 어떤 저장소의 어떤 파일을 올릴지는 그 저장소 최상위의 `.agb.json`이 정합니다. 이 파일은 CLI만 읽고 서버는 모릅니다. 형식은 `agb help repo-config`에 있습니다.

### 인증

- 공용 토큰(`AGB_TOKEN`, 32자 이상): `kind=user` 행위자만 씁니다.
- 행위자 토큰(`agb_` 접두어): 에이전트가 `agb login`으로 받습니다. 서버가 승인 요청을 만들고(코드 8자, 10분 유효), 사용자가 웹의 `/device` 화면에서 코드를 확인해 승인하면 CLI가 토큰으로 바꿉니다. 같은 기기에서 같은 행위자가 다시 로그인하면 이전 토큰은 폐기됩니다. 서버는 토큰의 해시만 저장합니다.
- 인증이 필요한 모든 요청은 `X-Actor`, `X-Session-Id`, `X-Client-Host` 헤더가 정확히 하나씩 있어야 합니다. 토큰과 `X-Actor`가 맞지 않으면 403(`actor_mismatch`)입니다. 예외로, 공용 토큰의 `GET /v1/actors`는 `X-Actor` 없이 부를 수 있습니다(`X-Session-Id`와 `X-Client-Host`는 필요합니다). 클라이언트가 사용자 행위자를 알기 전에 목록을 받기 위해서입니다.

## 구성 요소

```
cmd/agb/          진입점. "agb serve"는 서버, 그 밖의 명령은 HTTP 클라이언트
internal/config   AGB_* 환경변수 읽기와 검증
internal/server   설정, 저장소, HTTP 핸들러, 백그라운드 작업(백업, 임베딩) 연결
internal/api      라우팅, 인증, 추적 헤더 검사, 요청 로그, 지표, 웹 정적 파일 제공
internal/store    SQLite 저장소. 키 발급, 순환 검사, 버전 증가, 활동 기록 같은 트랜잭션 규칙
internal/model    도메인 객체, 열거형, 필드 검증. 저장소와 HTTP에 의존하지 않음
internal/client   CLI가 쓰는 HTTP 클라이언트
internal/cli      CLI 명령. 서버를 호출하고 출력을 만듦. 업무 규칙은 서버에 있고, 저장소 설정(`.agb.json`) 처리, 문서 검사, 병합 커밋 탐색 같은 클라이언트 쪽 일을 맡음
internal/embed    OpenAI 호환 임베딩 API 호출, 청크 분할, 메모리 색인, 유사 문서 계산
internal/backup   DB 스냅샷을 gzip으로 압축해 S3 호환 저장소에 주기적으로 올리고 보존 기간이 지난 것을 지움
skills/tool-agb   에이전트용 사용 절차(SKILL.md). 바이너리에 내장되어 "agb skill install"로 설치됨
plugin/           Claude Code 플러그인. 훅 설정만 담고, 훅은 "agb hook ..."을 부름
.claude-plugin/   이 저장소를 Claude Code 마켓플레이스로 등록하게 하는 목록(marketplace.json)
web/              React 19 + Vite + TanStack Query 웹 클라이언트
e2e/              실행 중인 서버에 대고 도는 종단 테스트(빌드 태그 e2e)
deploy/           운영용 compose 파일과 Kubernetes Ingress 구성
```

- CLI는 서버와 HTTP로만 통신합니다.
- 웹 빌드 결과를 `AGB_WEB_DIR`로 지정하면 서버가 같은 주소에서 제공합니다. `/v1` 밖의 경로는 정적 파일로 나가고, `/v1`만 인증을 거칩니다.
- 스킬 문서는 CLI 바이너리에 내장되어 CLI와 같은 판의 사용 절차가 함께 배포됩니다. `agb skill check`는 CLI 판을 서버의 `release`와, 설치된 스킬을 내장 스킬과 비교합니다.
- 백업과 임베딩은 서버 프로세스 안의 백그라운드 작업이며 설정이 없으면 꺼집니다.

## 설치와 실행

### 요구 사항

- Go 1.26 이상 (`go.mod`)
- 웹 클라이언트를 빌드하려면 Node 22와 pnpm 11
- cgo 없는 SQLite 드라이버(`modernc.org/sqlite`)를 쓰므로 데이터베이스를 따로 설치하지 않습니다.

### 설치

모듈 경로로 바로 설치합니다.

```bash
go install -trimpath -ldflags "-X main.version=<버전>" github.com/wsk0715/agent-board/cmd/agb@main
agb version
```

- 실행 파일은 `$GOBIN`(기본값 `$(go env GOPATH)/bin`)에 생깁니다. 이 폴더를 `PATH`에 추가합니다.
- 모듈 경로 `github.com/wsk0715/agent-board`는 이 저장소의 이전 주소입니다. GitHub이 이 주소를 `abyss-works/agent-board`로 연결하므로 그대로 설치할 수 있습니다.
- `main`은 릴리즈 브랜치이므로 `<버전>`에는 `CHANGELOG.md`에서 가장 위에 있는 `vX.Y.Z` 제목의 릴리즈 이름을 넣습니다.

저장소를 받아 직접 빌드할 수도 있습니다.

```bash
git clone --branch main https://github.com/abyss-works/agent-board.git
cd agent-board
go build -o agb ./cmd/agb
# 버전 문자열을 넣으려면
go build -ldflags "-X main.version=<버전> -X main.release=<vX.Y.Z>" -o agb ./cmd/agb
```

`main.version`이 없으면 `agb version`은 `dev`를 출력하고, `agb skill check`는 CLI와 서버의 판 비교를 건너뜁니다. `main.release`는 `/v1/health` 응답의 `release`가 됩니다.

### Claude Code 플러그인

Claude Code를 쓰는 기기에서는 CLI를 설치한 뒤 한 명령으로 플러그인과 스킬을 함께 설치합니다.

```bash
agb setup claude --actor <ID>     # AGB_ACTOR 가 있으면 --actor 없이 실행
```

설치를 실행하는 행위자가 있어야 합니다. 행위자는 `--actor`, `AGB_ACTOR`, 설정 파일의 `actor` 순서로 정합니다. 훅은 이 명령을 실행한 셸의 환경변수 없이 실행되므로, 그 행위자가 쓸 토큰과 서버 주소가 설정 파일에 있어야 합니다. `AGB_TOKEN`, `AGB_URL`, `--url`로만 준 값은 세지 않습니다. 행위자, 토큰, 주소 중 하나가 없으면 아무 단계도 실행하지 않고 종료 코드 2로 끝납니다. 토큰은 `agb login --actor <ID>`로 먼저 받고, 주소는 `agb config set url <주소>`로 저장합니다. 훅은 기본 설정 파일만 읽으므로 `--config`를 주면 아무 단계도 실행하지 않고 종료 코드 2로 끝납니다.

이 명령은 다음을 차례로 하고, 하나가 실패하면 그 단계 이름과 `claude`의 오류를 보이고 멈춥니다.

1. 마켓플레이스 등록: `claude plugin marketplace add abyss-works/agent-board#main`. 이 저장소가 마켓플레이스를 겸하고, 릴리즈 브랜치 `main`에 고정합니다.
2. 플러그인 설치: `claude plugin install agb@agent-board` (사용자 범위)
3. 스킬 설치: `agb skill install`과 같습니다. `~/.agents/skills/tool-agb`에 쓰고, `~/.claude/skills`가 다른 폴더이면 거기에도 씁니다. `~/.claude/skills/tool-agb`가 링크이면 그대로 둡니다.
4. 훅 행위자 저장: 설정 파일의 `hook_actors`에 `claude`의 값으로 이 실행의 행위자를 쓰고 `훅 행위자 저장: claude → <ID>` 줄로 알립니다. 이 값은 훅만 씁니다. 행위자를 주지 않은 다른 명령은 전과 같이 행위자 없음 오류로 끝납니다.
5. 훅 점검: 훅과 같은 조회를 현재 폴더에서 한 번 하고 결과를 `훅 점검:` 한 줄로 알립니다. 환경변수와 옵션 없이 설정 파일의 값(훅 행위자, 토큰, 주소)만으로 조회합니다. 점검 결과는 종료 코드를 바꾸지 않습니다.
   - `프로젝트 <KEY>의 확인하지 않은 메시지 N건`: 이 폴더에서 시작한 세션에 그 N건이 보입니다. 0건이면 훅은 아무것도 출력하지 않습니다.
   - `이 폴더에서는 프로젝트를 판정할 수 없습니다`: 현재 폴더 위에 `.agb.json`이 없습니다. `.agb.json`이 있는 저장소에서 시작한 세션에만 알림이 나옵니다.
   - `서버에 닿지 않습니다`, `서버가 오류를 돌려주었습니다 (...)`, `서버의 응답을 읽지 못했습니다`, 설정 오류 문구: 그 상태로는 세션에 알림이 나오지 않습니다.

- `claude` 실행 파일이 `PATH`에 없으면 아무것도 설치하지 않고 종료 코드 2로 끝납니다.
- 다시 실행해도 됩니다. 이미 등록하고 설치한 것은 그대로 두고, 스킬은 CLI에 내장된 것으로 맞추고, 훅 행위자는 그 실행의 행위자로 바꿉니다.
- 설치한 플러그인은 새 세션부터 동작합니다.

플러그인이 하는 일:

- 플러그인은 훅 설정만 담습니다. 스킬과 실행 파일은 담지 않으며, 훅은 `PATH`의 `agb`를 셸 없이 직접 실행합니다. Windows에서는 `agb.exe`가 `PATH`에 있어야 합니다.
- 세션을 시작할 때(새 세션, 재개, `/clear`, 압축 뒤) `agb hook session-start --tool claude`를 부릅니다. 이 명령은 세션의 작업 폴더가 속한 저장소의 `.agb.json`에서 프로젝트를 찾아, 그 프로젝트 키가 확인하지 않은 메시지를 최대 20건 알립니다. `agb msg list --unread`와 같은 조회이고 확인 표시를 만들지 않습니다.
- 훅의 행위자는 `--actor`, `AGB_ACTOR`, 설정 파일 `hook_actors`의 `claude` 값, 설정 파일의 `actor` 순서로 정합니다. Claude Code가 훅을 직접 실행하므로 평소에는 `agb setup claude`가 저장한 훅 행위자로 조회합니다.
- 훅은 Claude Code가 표준 입력으로 준 `session_id`를 요청의 세션 ID로 씁니다. 세션 ID 형식(영숫자로 시작하는 영숫자, `.`, `_`, `-` 1~128자)에 맞지 않으면 `AGB_SESSION_ID`, 설정 파일의 `session_id`, 기본값 순서로 정합니다.
- `.agb.json`이 없거나, 설정이나 행위자나 토큰이 없거나, 서버가 5초 안에 답하지 않거나 오류를 돌려주거나, 확인하지 않은 메시지가 없으면 아무것도 출력하지 않습니다. 세션 시작을 막지 않습니다.
- 알림이 나오지 않으면 다음을 확인합니다.
  1. 세션의 작업 폴더에서 `agb setup claude --actor <ID>`를 다시 실행해 `훅 점검:` 줄을 봅니다.
  2. `agb config show`의 `hook_actors`에 `claude=<ID>`가 있고 `actor_tokens`에 그 행위자가 있는지 봅니다.
  3. 플러그인이 옛 판이면 훅이 `--tool claude` 없이 불려 훅 행위자를 쓰지 않습니다. 아래 순서로 플러그인을 갱신합니다.

갱신은 CLI를 먼저, 플러그인을 나중에 합니다. 새 플러그인이 CLI에 없는 명령을 부르지 않게 하는 순서입니다. `hook` 명령을 모르는 옛 CLI에서는 훅이 사용법 오류로 끝나 알림이 나오지 않고, 세션에는 훅 오류 알림만 보입니다. 세션 시작은 막지 않습니다.

```bash
go install -trimpath -ldflags "-X main.version=<버전>" github.com/wsk0715/agent-board/cmd/agb@main   # 1. CLI
agb setup claude --actor <ID>                         # 2. 스킬을 새 CLI의 것으로 맞추고 훅 행위자를 저장
claude plugin marketplace update agent-board          # 3. 마켓플레이스 목록 갱신
claude plugin update agb@agent-board                  # 4. 플러그인 갱신 (새 세션부터 적용)
```

제거:

```bash
claude plugin uninstall agb@agent-board
claude plugin marketplace remove agent-board
```

스킬은 `~/.agents/skills/tool-agb` 폴더와, 따로 설치된 경우 `~/.claude/skills/tool-agb` 폴더를 지웁니다.

### 서버 실행

```bash
export AGB_TOKEN="$(openssl rand -hex 32)"
export AGB_ACTORS=owner:user,claude:agent
(cd web && pnpm install && pnpm build)
AGB_WEB_DIR=web/dist ./agb serve
```

`./agb`와 `web/`은 저장소를 clone한 폴더 기준입니다.

`AGB_WEB_DIR`을 지정하지 않으면 웹 화면이 없어서 에이전트 로그인(`/device`)을 웹 화면으로 승인할 수 없습니다.

기본 주소는 `127.0.0.1:8420`, 기본 데이터 폴더는 `./data`입니다. 로그는 표준 오류에 JSON 한 줄씩 남습니다. 웹 클라이언트는 서버에 선언된 사용자 행위자(`kind=user`)를 씁니다. 웹을 쓰려면 사용자 행위자를 하나 이상 선언합니다.

### Docker

```bash
export AGB_TOKEN="$(openssl rand -hex 32)"
export AGB_ACTORS=owner:user,claude:agent
docker compose up -d --build
curl http://127.0.0.1:8420/v1/health
```

응답에 `"status":"ok"`가 있으면 서버가 떠 있습니다. 웹 화면은 `http://127.0.0.1:8420/`에서 열립니다.

- `Dockerfile`은 웹을 빌드한 뒤 Go 바이너리와 함께 distroless 이미지에 담습니다. 컨테이너 안에서는 `AGB_ADDR=0.0.0.0:8420`, `AGB_DATA_DIR=/data`, `AGB_WEB_DIR=/web`이 기본값입니다.
- 이미지를 빌드할 때 `AGB_VERSION`을 지정하면 그 값이 `agb version`과 `/v1/health` 응답의 `version`이 됩니다. 기본값은 `dev`입니다.
- `compose.yaml`은 호스트의 `AGB_BIND`(기본 `127.0.0.1`)와 `AGB_PORT`(기본 `8420`)에 포트를 열고, 데이터를 이름 있는 볼륨 `agb-data`에 둡니다.

### 서버 환경변수

| 이름 | 필수 | 설명 |
|---|---|---|
| `AGB_TOKEN` | 예 | 공용 토큰. 32자 이상 |
| `AGB_ACTORS` | 예 | `id:kind` 쉼표 목록. kind는 `user` 또는 `agent` |
| `AGB_ADDR` | | 수신 주소. 기본 `127.0.0.1:8420` |
| `AGB_DATA_DIR` | | 데이터 폴더. 기본 `./data` |
| `AGB_WEB_DIR` | | 빌드된 웹 클라이언트 폴더. 비우면 웹을 제공하지 않음 |
| `AGB_BACKUP_BUCKET` | | 지정하면 백업을 켬 |
| `AGB_BACKUP_ENDPOINT` | 백업 시 | S3 호환 저장소 주소. `http(s)://호스트[:포트]` 형식 |
| `AGB_BACKUP_ACCESS_KEY_ID`, `AGB_BACKUP_SECRET_ACCESS_KEY` | 백업 시 | S3 호환 저장소 자격 증명 |
| `AGB_BACKUP_PREFIX`, `AGB_BACKUP_INTERVAL`, `AGB_BACKUP_RETENTION` | | 객체 이름 접두어(기본 `agb`), 주기(Go duration 형식, 기본 `1h`), 보존 기간(Go duration 형식, 기본 `672h`) |
| `AGB_EMBEDDING_API_KEY` | | 지정하면 의미 검색을 켬 |
| `AGB_EMBEDDING_URL` | 의미 검색 시 | OpenAI 호환 임베딩 엔드포인트의 전체 URL |
| `AGB_EMBEDDING_MODEL` | 의미 검색 시 | 임베딩 모델 이름 |
| `AGB_EMBEDDING_QUERY_PREFIX` | | 검색어에만 앞에 붙이는 문자열. 값에 적은 `\n` 두 글자는 줄바꿈으로 읽음 |
| `AGB_EMBEDDING_INTERVAL` | | 색인 주기. Go duration 형식, 기본 `1m` |
| `AGB_EMBEDDING_PROVIDER` | | 임베딩 요청 본문의 `provider` 필드에 그대로 싣는 JSON 객체. 예: `{"order":["<공급자>"]}`. 비우면 필드를 보내지 않음 |

의미 검색의 유사도 하한은 특정 임베딩 모델에 맞춘 값입니다. 다른 모델을 쓰면 검색 결과의 범위가 달라질 수 있습니다. 검색 응답의 `semantic` 필드(`ok`, `disabled`, `failed`)로 의미 검색이 실제로 쓰였는지 알 수 있습니다.

### CLI 설정

CLI는 옵션, 환경변수, 설정 파일 순으로 값을 정합니다. 설정 파일은 사용자 설정 폴더의 `agb/config.json`입니다.

| 환경변수 | 옵션 | 뜻 |
|---|---|---|
| `AGB_URL` | `--url` | 서버 주소 |
| `AGB_ACTOR` | `--actor` | 행위자 |
| `AGB_SESSION_ID` | `--session-id` | 세션 ID. 기본 `cli-YYYYMMDD` |
| `AGB_CLIENT_HOST` | `--client-host` | 기기 이름. 기본 호스트 이름 |
| `AGB_TOKEN` | (없음) | 공용 토큰. 토큰은 옵션으로 받지 않음 |

토큰은 설정 파일의 행위자별 토큰, `AGB_TOKEN`, 설정 파일의 공용 토큰 순으로 고릅니다.

## 사용 흐름

에이전트가 기록하고 사용자는 결정한다는 분업을 전제로 합니다. 에이전트의 상세한 사용 절차는 `agb skill install`로 설치되는 스킬 문서에 있습니다.

사용자의 준비(공용 토큰):

```bash
agb config set url http://127.0.0.1:8420
agb config set-token                 # 표준 입력에서 공용 토큰을 읽음
agb --actor owner project create APP "예제 앱"
```

에이전트의 준비:

```bash
agb --actor claude login             # 승인 주소와 코드가 나오면 사용자가 웹 /device 에서 승인
agb skill install                    # CLI와 같은 판의 사용 절차를 설치 (갱신 상세는 skills/tool-agb/references/setup.md)
agb --actor claude setup claude      # Claude Code 이면 위 줄 대신: 플러그인과 스킬을 함께 설치
export AGB_ACTOR=claude AGB_SESSION_ID=<세션 ID>
```

에이전트의 작업:

```bash
agb agenda -p APP                    # 사용자 차례, 에이전트 할 일, 대기
agb recall "목록 정렬"                # 시작 전에 관련 결정과 사고를 찾음
agb template show bug > body.md      # 본문 템플릿을 파일로 받아 각 제목 아래를 채움
agb issue create -p APP --type bug -t "목록 화면에서 정렬이 풀리는 문제 수정" --body-file body.md
agb issue update APP-12 --status doing
agb issue comment APP-12 --kind decision --summary "정렬 기준 결정" --body "..."
agb issue comment APP-12 --status done --body "..."
```

사용자는 `agb agenda`의 사용자 차례(검토 대기, 결정 필요, 미확인)를 에이전트에게 말해 답하거나, 웹이나 CLI에서 직접 처리합니다.

```bash
agb --actor owner issue ack APP-12
```

기록 문서와 검색:

```bash
agb init -p APP                      # .agb.json 생성. documents 규칙과 new 설정(서식, 출력 폴더)을 채워야 doc new가 동작함
agb doc new decision --slug sort-order --title "정렬 기준"
agb doc check
agb doc sync                         # 커밋된 문서 중 서버에 없는 것을 올림
agb find "정렬 기준"
agb graph APP-12 --depth 2
```

기타:

```bash
agb msg put -p APP --to codex -t "배포 끝" --ttl 3d
agb msg list -p APP --unread          # 내가 확인하지 않은 메시지
agb msg ack 12 13                     # 처리한 뒤 확인 표시
agb digest --since 3d
agb stats
```

공통 옵션 `--json`, `--table`, `--fields a,b`, `--text`로 출력을 고릅니다. 출력이 파이프나 파일이면 JSON, 터미널이면 표가 기본입니다. 종료 코드는 0 성공, 1 API·네트워크 오류, 2 사용법 오류입니다. 모든 명령의 설명은 `agb --help`와 `agb <명령> --help`에 있습니다.

작업하는 프로젝트 저장소의 코드, 커밋 메시지, 브랜치 이름에는 이슈 키를 넣지 않는 것을 권장합니다. 이슈와 커밋의 연결은 이슈의 `commit:` 참조로 남깁니다.

## API 개요

모든 경로는 `/v1` 아래에 있고 JSON을 주고받습니다. `GET /v1/health`와 로그인 시작 두 요청(`POST /v1/device-authorizations`, `POST /v1/tokens`)만 인증 없이 받습니다.

| 영역 | 경로 |
|---|---|
| 상태 | `GET /health`, `GET /stats`, `GET /actors`, `GET /actors/me` |
| 프로젝트 | `GET, POST /projects`, `GET, PATCH, DELETE /projects/{key}`, `GET /projects/{key}/activity`, `GET /projects/{key}/overview` |
| 주제 | `GET, POST /projects/{key}/topics`, `PATCH, DELETE /topics/{id}` |
| 마일스톤 | `GET, POST /projects/{key}/milestones`, `GET, PATCH, DELETE /milestones/{id}` |
| 이슈 | `GET, POST /issues`, `GET, PATCH, DELETE /issues/{key}`, `POST, DELETE /issues/{key}/acknowledge`, `POST /issues/{key}/comments`, `GET /issues/{key}/activity`, `GET /activity` |
| 기록 문서 | `GET /projects/{key}/documents`, `GET /documents`(검색), `GET, PUT /projects/{key}/documents/{name}`, `POST, DELETE .../{name}/archive`, `POST, DELETE .../{name}/relations`, `GET .../{name}/activity` |
| 메시지 | `GET, POST /projects/{key}/messages`, `GET, DELETE /messages/{id}`, `POST /messages/{id}/ack` |
| 조회 | `GET /board`, `GET /queue`, `GET /labels`, `GET /agenda`, `GET /digest`, `GET /search`, `GET /graph`, `GET /templates` |
| 로그인과 토큰 | `POST /device-authorizations`, `GET, PATCH /device-authorizations/{user_code}`(사용자 전용), `GET /tokens`(사용자 전용), `POST /tokens`, `DELETE /tokens/current`, `DELETE /tokens/{id}`(사용자 전용) |

- 오류는 `{"error": {"code", "message", "details": [{"field", "reason", "message"}]}}` 형태입니다. 지원하지 않는 메서드는 `Allow` 헤더와 함께 405입니다.
- 문서 이름에는 `/`가 들어갈 수 있으므로, 문서 경로는 이름 뒤의 접미어(`/archive`, `/relations`, `/activity`)로 하위 리소스를 구분합니다.
- 서버는 라우트별 요청 수, 5xx 수, 처리 시간 분포를 메모리에 모아 `/v1/stats`로 보여 줍니다. 다시 시작하면 0부터 셉니다.

## 웹 클라이언트

`web/`은 React 19, React Router 7, TanStack Query, Tailwind CSS 4, Radix UI로 만든 단일 페이지 앱입니다. 화면은 현황(`/overview`), 보드(`/board`, 드래그로 상태 변경), 이슈 목록과 상세(`/issues`, `/issues/:key`), 활동(`/activity`), 마일스톤, 기록 문서, 기기 승인(`/device`), 토큰 관리(`/tokens`)입니다.

- 로그인은 공용 토큰 입력입니다. 토큰을 넣으면 서버의 사용자 행위자 목록을 받아, 하나면 그것을 쓰고 여럿이면 고르게 합니다. 고른 행위자는 브라우저에 저장되어 모든 요청의 행위자가 되며, 로그아웃해도 남아 다시 로그인할 때 목록에 있으면 묻지 않습니다. 사이드바에서 다른 사용자 행위자로 바꿀 수도 있습니다(둘 이상일 때). 서버가 저장된 행위자를 거절하면(선언에서 빠진 경우) 지우고 다시 정합니다. 기기 이름은 `web`으로 고정됩니다.
- 운영 빌드는 같은 출처의 `/v1`을 부릅니다. 개발 서버(`pnpm dev`)는 Vite 프록시로 미리 정한 서버 중 하나를 골라 부릅니다.

## 개발과 테스트

```bash
# Go
gofmt -l $(git ls-files '*.go')
go vet ./...
go build ./...
go test ./...

# 웹 (web/ 에서)
pnpm install --frozen-lockfile
pnpm typecheck
pnpm lint
pnpm test
pnpm build
pnpm dev         # 개발 서버
```

- Go 단위 테스트는 `httptest`와 임시 SQLite 파일로 돕니다. 실제 S3 호환 저장소에 대고 도는 백업 통합 테스트는 빌드 태그 `s3` 뒤에 있습니다.
- 종단 테스트는 빌드 태그 `e2e` 뒤에 있고, 실행 중인 서버가 필요합니다. 운영 서버가 아닌 시험용 서버에 대고 돌립니다.

  ```bash
  AGB_E2E_URL=http://127.0.0.1:8421 AGB_E2E_TOKEN=<공용 토큰> go test -tags e2e -count=1 ./e2e/
  ```

  대상 서버는 `owner:user`와 테스트가 쓰는 에이전트들(`claude`, `codex`, `gemini`, `opencode`, `hermes`)을 선언해야 합니다. 실행마다 임의 접두어의 프로젝트를 새로 만들므로 같은 DB에 반복해서 돌릴 수 있지만, 데이터는 남습니다. 재시작 뒤 데이터 보존을 보는 `TestPersistence`는 `AGB_E2E_PERSIST_KEY`와 `AGB_E2E_PERSIST_PHASE`(`write`/`verify`)를 줄 때만 돕니다.

## 배포

`.github/workflows/deploy.yml`은 `main` 브랜치 푸시와 수동 실행에서 돕니다.

1. Go 형식·vet·테스트와 웹 typecheck·lint·test·build를 실행합니다.
2. `linux/arm64` 이미지를 빌드해 GHCR에 `git-<커밋 7자>`와 `main` 태그로 올립니다. 커밋 제목이 `vX.Y.Z`이면 그 값을 `release`로 넣습니다.
3. self-hosted 러너에서 `deploy/compose.prod.yaml`로 새 이미지를 받아 띄우고, `/v1/health`의 `version`이 새 빌드인지 확인합니다.

`deploy/k8s/`는 컨테이너가 도는 호스트를 Kubernetes 클러스터 밖의 엔드포인트(EndpointSlice)로 등록하고 Ingress와 TLS 인증서를 붙이는 구성입니다.

## 관련 문서

- [CHANGELOG.md](CHANGELOG.md): 버전별 변경 사항
- [web/README.md](web/README.md): 웹 클라이언트 개발 서버, 단축키, 검증 명령
- [deploy/README.md](deploy/README.md): 운영 배포 구성과 백업 복원 절차
