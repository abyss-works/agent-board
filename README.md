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
- 다른 세션이나 기기에 짧은 맥락을 넘기는 만료형 메시지 공간을 둡니다(`agb msg`).

## 설계 원칙

- API가 먼저입니다. CLI와 웹은 같은 REST API를 쓰는 클라이언트이고, 업무 규칙은 서버에만 있습니다.
- 서버가 데이터를 단독으로 소유합니다. SQLite 파일 하나를 서버만 열고, 여러 기기는 API로만 접근합니다.
- 기록은 지우지 않습니다. 이슈, 프로젝트, 주제, 마일스톤의 변경은 행위자, 세션, 기기, 시각, 바뀐 필드의 이전 값과 함께 남습니다. 문서의 아카이브와 관계 변경도 같은 방식으로 남고, 메시지 삭제는 삭제한 행위자와 시각을 남깁니다. 이슈, 프로젝트, 주제, 마일스톤의 삭제는 행을 지우지 않는 상태 변경이라 다른 상태로 되돌릴 수 있습니다. 메시지는 삭제 표시만 하고 되돌리는 경로는 없습니다.
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
| 메시지 (message) | `to`, `title`, `body`, `expires_at` | `active`, `expired`, `deleted` | 프로젝트에 속함 |
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
- 인증이 필요한 모든 요청은 `X-Actor`, `X-Session-Id`, `X-Client-Host` 헤더가 정확히 하나씩 있어야 합니다. 토큰과 `X-Actor`가 맞지 않으면 403(`actor_mismatch`)입니다.

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

### 서버 실행

```bash
export AGB_TOKEN="$(openssl rand -hex 32)"
export AGB_ACTORS=owner:user,claude:agent
(cd web && pnpm install && pnpm build)
AGB_WEB_DIR=web/dist ./agb serve
```

`./agb`와 `web/`은 저장소를 clone한 폴더 기준입니다.

`AGB_WEB_DIR`을 지정하지 않으면 웹 화면이 없어서 에이전트 로그인(`/device`)을 웹 화면으로 승인할 수 없습니다.

기본 주소는 `127.0.0.1:8420`, 기본 데이터 폴더는 `./data`입니다. 로그는 표준 오류에 JSON 한 줄씩 남습니다. 웹 클라이언트는 사용자 행위자를 `owner`로 보내므로, 웹을 쓰려면 `owner:user`를 선언합니다.

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
| 메시지 | `GET, POST /projects/{key}/messages`, `GET, DELETE /messages/{id}` |
| 조회 | `GET /board`, `GET /queue`, `GET /labels`, `GET /agenda`, `GET /digest`, `GET /search`, `GET /graph`, `GET /templates` |
| 로그인과 토큰 | `POST /device-authorizations`, `GET, PATCH /device-authorizations/{user_code}`(사용자 전용), `GET /tokens`(사용자 전용), `POST /tokens`, `DELETE /tokens/current`, `DELETE /tokens/{id}`(사용자 전용) |

- 오류는 `{"error": {"code", "message", "details": [{"field", "reason", "message"}]}}` 형태입니다. 지원하지 않는 메서드는 `Allow` 헤더와 함께 405입니다.
- 문서 이름에는 `/`가 들어갈 수 있으므로, 문서 경로는 이름 뒤의 접미어(`/archive`, `/relations`, `/activity`)로 하위 리소스를 구분합니다.
- 서버는 라우트별 요청 수, 5xx 수, 처리 시간 분포를 메모리에 모아 `/v1/stats`로 보여 줍니다. 다시 시작하면 0부터 셉니다.

## 웹 클라이언트

`web/`은 React 19, React Router 7, TanStack Query, Tailwind CSS 4, Radix UI로 만든 단일 페이지 앱입니다. 화면은 현황(`/overview`), 보드(`/board`, 드래그로 상태 변경), 이슈 목록과 상세(`/issues`, `/issues/:key`), 활동(`/activity`), 마일스톤, 기록 문서, 기기 승인(`/device`), 토큰 관리(`/tokens`)입니다.

- 로그인은 공용 토큰 입력이며, 요청의 행위자는 `owner`, 기기 이름은 `web`으로 고정됩니다.
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
