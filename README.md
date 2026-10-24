# agent-board

에이전트가 CLI와 REST API로 프로젝트의 할 일과 진행 상황을 기록하고, 사용자가 웹 클라이언트로 확인하는 프로젝트 트래커다. 아이디어, 다음 할 일, 발견한 문제, 사용자 결정이 필요한 질문처럼 계속 쌓이고 바뀌는 목록을 한 서버에 모은다. 여러 기기와 여러 에이전트가 같은 서버를 읽고 쓰므로 세션이 바뀌어도 같은 현황에서 작업을 이어 갈 수 있다.

사용자 한 사람과 그 사용자의 에이전트들이 쓰는 도구다. 행위자(사용자 또는 에이전트)는 서버 환경변수로 선언한다. 인증은 사용자 행위자가 쓰는 공용 토큰과, 에이전트 행위자가 기기 승인으로 받는 행위자별 토큰으로 나뉜다. 버전별 변경 사항은 [CHANGELOG.md](CHANGELOG.md)에 있다.

## 구성 요소

| 구성 요소 | 위치 | 역할 |
|---|---|---|
| 서버 | `cmd/agb`, `internal/` | `/v1` 아래의 REST API를 제공하고 SQLite 파일 하나에 저장한다. 데이터베이스는 서버만 연다. |
| CLI | `cmd/agb`, `internal/cli`, `internal/client` | 서버의 REST API를 HTTP로만 호출한다. 업무 규칙은 서버에 있다. |
| 웹 클라이언트 | `web/` | 브라우저에서 REST API를 직접 호출하는 SPA다. 현황, 보드, 이슈 목록, 활동, 기록 문서 화면이 있다. |
| 에이전트 스킬 | `skills/tool-agb/` | 에이전트가 CLI로 트래커를 쓰는 시점과 방법을 정한 문서다. |

서버와 CLI는 바이너리 하나(`agb`)다. `agb serve`가 서버를 실행하고, 그 밖의 명령은 서버에 접속하는 클라이언트로 동작한다. 서버에 `AGB_WEB_DIR`을 지정하면 웹 클라이언트 빌드를 `/v1` 밖의 경로에서 서빙하며, 이때 API와 웹 클라이언트는 같은 주소를 쓴다.

## 주요 기능

- 이슈: 키는 프로젝트 키에 번호를 붙인 `DEMO-1` 형식이다. 종류는 기능, 버그, 설계, 아이디어, 관측이고, 상태는 백로그, 진행, 검토, 완료, 취소다. 삭제는 상태를 바꾸는 소프트 삭제다. 우선순위(P0부터 P3), 라벨, 상위·선행 이슈, 참조, 재검토 조건, 결정 필요(사용자의 답을 기다리는 질문)를 둔다. 확인된 이슈의 제목, 본문, 종류가 바뀌거나 검토에서 진행으로 되돌아가면 서버가 미확인으로 되돌린다.
- 동시 수정: 수정은 JSON Merge Patch이고 `If-Match` 버전으로 낙관적 잠금을 건다. 선행 이슈가 순환하는 수정은 거부한다.
- 우선순위 큐: 우선순위, 선행 이슈 완료 여부, 생성 순서로 다음 할 일을 정한다.
- 조회: 상태별 보드, 프로젝트 현황(진행, 검토, 막힘, 결정 필요, 다음 할 일, 미확인, 최근 활동), 라벨별 사용 개수, SQLite FTS5 전문 검색을 제공한다.
- 활동 기록: 생성, 변경, 댓글을 덧붙이기만 한다. 변경에는 바뀐 필드의 이전 값과 새 값을 남기고, 모든 기록에 행위자, 세션 ID, 클라이언트 호스트를 함께 남긴다.
- 이슈 템플릿: 종류별 본문 템플릿을 제공한다. 에이전트의 요청은 본문에 템플릿의 `##` 제목이 순서대로 모두 있는지 서버가 검사한다.
- 주제: 프로젝트마다 대주제와 주제 두 단계 목록을 두고, 이슈에 주제 하나를 붙인다.
- 마일스톤: 프로젝트마다 출시나 목표 단위의 마일스톤을 두고, 이슈에 마일스톤 하나를 붙인다. 목표일을 정할 수 있고 닫거나 삭제할 수 있다. 진행은 취소·삭제한 이슈를 뺀 이슈 중 완료한 수로 조회할 때 계산한다.
- 사유와 요약: 상태를 바꿀 때 사유를 남기고(취소와 삭제는 필수), 댓글에는 한 줄 요약을 붙인다.
- 사용자 확인 표시: 에이전트가 만든 이슈는 미확인으로 시작한다. 사용자가 웹 클라이언트에서 확인하거나, 사용자가 대화에서 확인하면 에이전트가 표시한다. 확인 여부는 작업 흐름에 영향을 주지 않는다.
- 기록 문서: 관리 저장소의 기록 문서를 프로젝트마다 원문 그대로 보존하고, 여러 프로젝트에 걸쳐 FTS5로 검색한다. 한번 올린 문서는 고치거나 지울 수 없고 위치(`history`, `archive`)만 바꿀 수 있다.
- 백업: 데이터베이스 스냅샷을 S3 호환 오브젝트 저장소(Cloudflare R2)에 주기적으로 올린다.
- 웹 클라이언트: 보드에서 카드를 끌어 상태를 바꾸고, 3초마다 화면을 다시 불러와 바뀐 항목을 잠깐 강조한다.

## 설치

### CLI

Go 1.26 이상이 필요하다. SQLite 드라이버가 순수 Go 구현이므로 C 컴파일러는 필요하지 않다.

```bash
go install -trimpath -ldflags "-X main.version=<버전>" github.com/wsk0715/agent-board/cmd/agb@main
agb version
```

- `<버전>`은 `agb version`이 출력하는 문자열이다. `main` 브랜치는 릴리즈 브랜치이므로 CHANGELOG.md 맨 위의 버전 이름(예: `v0.1.1`)을 넣는다. `-ldflags`를 생략하면 `dev`로 출력된다.
- 모듈 경로 `github.com/wsk0715/agent-board`는 이 저장소의 이전 주소다. GitHub이 이 주소를 `abyss-works/agent-board`로 연결하므로 그대로 설치할 수 있다.
- 실행 파일은 `$GOBIN`(기본값 `$(go env GOPATH)/bin`)에 생긴다. 이 폴더를 `PATH`에 추가한다.

### 에이전트 스킬

스킬은 CLI 바이너리에 내장되어 있다. CLI를 설치한 뒤 다음 명령으로 전역 스킬 폴더(`~/.agents/skills/tool-agb/`)에 설치한다. 다른 폴더에 설치하려면 `--dir <폴더>`를 준다.

```bash
agb skill install
```

설치된 스킬이 내장 스킬과 같으면 아무것도 바꾸지 않는다. CLI를 새 릴리즈로 설치한 뒤 다시 실행하면 스킬도 같은 릴리즈로 바뀐다. 설치 위치가 링크이면 링크를 지우고 복사본을 둔다.

`references/setup.md`는 CLI와 스킬의 설치·갱신, 접속 설정, 접속 확인, 실패 처리를 정한다. PowerShell 명령도 이 문서에 있다.

스킬은 세션 시작, 등록, 착수, 진행, 마침 단계별 `agb` 사용법과 이슈 종류별 제목 규칙, 본문 템플릿, 댓글 요약 규칙을 정한다. 프로젝트 키는 프로젝트의 AGENTS.md에 적힌 값을 쓰고, 행위자는 `agb actors`에 선언된 값 중 자기 도구에 맞는 것을 쓴다. 에이전트는 처음 한 번 `agb login`으로 기기 승인을 받아 행위자별 토큰을 저장한다([CLI 사용](#cli-사용) 참조).

## 서버 실행

서버는 환경변수 `AGB_TOKEN`(공용 토큰, 32자 이상)과 `AGB_ACTORS`(선언할 행위자)가 있어야 시작한다. `AGB_ACTORS`는 `id:kind`를 쉼표로 잇고, `kind`는 `user` 또는 `agent`다.

에이전트 도구마다 행위자를 하나씩 선언하면 활동 기록에서 변경한 쪽이 구분된다. 웹 클라이언트는 행위자 `owner`로 요청하므로 `owner:user`도 선언해야 한다. 공용 토큰은 `user` 종류 행위자만 쓰고, `agent` 종류 행위자는 CLI의 `agb login`으로 받은 행위자별 토큰만 쓴다.

### Docker Compose

```bash
git clone --branch main https://github.com/abyss-works/agent-board.git
cd agent-board
export AGB_TOKEN="$(openssl rand -hex 32)"
export AGB_ACTORS="owner:user,agent1:agent"
docker compose up -d --build
curl http://127.0.0.1:8420/v1/health
```

응답에 `"status":"ok"`가 있으면 서버가 떠 있다. 공용 토큰은 웹 클라이언트 로그인과 사용자 행위자의 CLI 설정에 쓰이므로 따로 보관한다.

- `compose.yaml`은 `Dockerfile`로 이미지를 빌드한다. 이미지에는 `agb`와 웹 클라이언트 빌드가 함께 들어 있으므로 `http://127.0.0.1:8420/`에서 웹 클라이언트가 열린다. 로그인 화면에 `AGB_TOKEN` 값을 붙여 넣는다.
- `AGB_TOKEN`이나 `AGB_ACTORS`가 비어 있으면 `docker compose`가 필수 변수가 없다는 오류로 멈춘다.
- 포트는 호스트의 `127.0.0.1:8420`에만 열린다. `AGB_BIND`(기본 `127.0.0.1`)와 `AGB_PORT`(기본 `8420`)로 바꾼다.
- 데이터는 이름 있는 볼륨 `agb-data`(컨테이너의 `/data`)에 남는다.
- 이미지를 빌드할 때 `AGB_VERSION`을 지정하면 그 값이 `agb version`과 `/v1/health` 응답의 `version`이 된다. 기본값은 `dev`다.
- 백업을 켜려면 `AGB_BACKUP_BUCKET`, `AGB_BACKUP_ENDPOINT`, `AGB_BACKUP_ACCESS_KEY_ID`, `AGB_BACKUP_SECRET_ACCESS_KEY`를 함께 지정한다.

### 바이너리 직접 실행

```bash
export AGB_TOKEN="$(openssl rand -hex 32)"
export AGB_ACTORS="owner:user,agent1:agent"
agb serve
```

기본값으로 `127.0.0.1:8420`에서 요청을 받고 데이터베이스를 `./data/agb.db`에 둔다. 서버 로그와 요청 로그는 JSON 줄로 표준 오류에 나온다. 웹 클라이언트도 서빙하려면 저장소 루트에서 빌드한 뒤(Node 22와 pnpm이 필요하다) `AGB_WEB_DIR`을 지정한다.

```bash
(cd web && pnpm install && pnpm build)
AGB_WEB_DIR=web/dist agb serve
```

| 변수 | 내용 | 기본값 |
|---|---|---|
| `AGB_ADDR` | 서버가 요청을 받을 주소 | `127.0.0.1:8420` |
| `AGB_DATA_DIR` | 데이터베이스 파일 `agb.db`를 두는 폴더 | `./data` |
| `AGB_TOKEN` | 공용 토큰. 32자 이상 | 필수 |
| `AGB_ACTORS` | 행위자 선언. `id:kind`를 쉼표로 잇는다. `id`는 소문자와 숫자 1~32자 | 필수 |
| `AGB_WEB_DIR` | 빌드한 웹 클라이언트 폴더. 지정하면 `/v1` 밖의 경로를 이 폴더에서 인증 없이 서빙한다. 없는 폴더를 지정하면 서버가 시작하지 않는다. | 비어 있음 |
| `AGB_BACKUP_BUCKET` | 백업 버킷 이름. 비어 있으면 백업이 꺼진다. | 비어 있음 |
| `AGB_BACKUP_ENDPOINT` | S3 호환 엔드포인트 URL(`http(s)://호스트[:포트]`). 버킷을 지정하면 필수 | 없음 |
| `AGB_BACKUP_ACCESS_KEY_ID`, `AGB_BACKUP_SECRET_ACCESS_KEY` | 백업 저장소 자격 증명. 버킷을 지정하면 필수 | 없음 |
| `AGB_BACKUP_PREFIX` | 객체 이름 접두어 | `agb` |
| `AGB_BACKUP_INTERVAL` | 백업 주기. Go duration 형식 | `1h` |
| `AGB_BACKUP_RETENTION` | 스냅샷 보관 기간. Go duration 형식 | `672h`(4주) |

백업을 켜면 서버가 시작한 직후와 이후 `AGB_BACKUP_INTERVAL`마다 데이터베이스의 일관된 스냅샷을 gzip으로 압축해 올린다. 마지막 업로드 이후 바뀐 것이 없으면 그 주기의 업로드를 건너뛰고, 보관 기간이 지난 스냅샷은 지운다. 복원 절차는 [deploy/README.md](deploy/README.md)에 있다.

## CLI 사용

CLI에 서버 주소와 행위자를 설정하고 로그인한다.

```bash
agb config set url http://127.0.0.1:8420
agb config set actor agent1
agb login
agb health
agb whoami
```

에이전트 행위자는 공용 토큰을 쓰지 않고 `agb login`이 기기 승인으로 받은 행위자별 토큰을 쓴다. `agb login`은 승인 요청을 만들어 승인 주소(`<서버 주소>/device?code=<코드>`)와 코드를 표준 오류에 보이고 브라우저로 주소를 연다(`--no-browser`를 붙이면 열지 않는다). 사용자가 웹 클라이언트에서 코드를 확인하고 승인하면 CLI가 토큰을 받아 설정 파일에 행위자별로 저장하고, 이후 그 행위자의 요청에 쓴다. 승인 요청은 10분 뒤 만료되며, 거부되거나 만료되면 종료 코드 1로 끝난다. 같은 행위자가 같은 기기에서 다시 로그인하면 서버가 이전 토큰을 폐기한다. `agb logout`은 서버에서 토큰을 폐기하고 설정 파일에서 지운다. 토큰 값은 어떤 출력에도 나오지 않는다.

공용 토큰은 `user` 종류 행위자(예: `owner`) 전용이다. 에이전트 행위자가 공용 토큰으로 요청하면 403이다. 사용자 행위자로 CLI를 쓸 때만 `agb config set-token`이나 `AGB_TOKEN` 환경변수로 공용 토큰을 준다. `agb config set-token`은 터미널에서 토큰을 입력받으며 입력이 화면에 표시되지 않고, 파이프로도 받을 수 있다(`echo "$AGB_TOKEN" | agb config set-token`). 에이전트의 셸에 `AGB_TOKEN`이 있으면 그 값이 먼저 쓰여 403이 나므로 해제한다. 설정 파일은 사용자만 읽을 수 있는 권한으로 만들어지고, 경로는 `agb config path`로 확인한다. `agb config show`는 토큰 값 대신 공용 토큰의 저장 여부와 토큰이 있는 행위자의 이름을 보인다.

설정 값은 옵션, 환경변수, 설정 파일 순으로 우선한다. 환경변수는 `AGB_URL`, `AGB_ACTOR`, `AGB_SESSION_ID`, `AGB_CLIENT_HOST`, `AGB_TOKEN`이고, 토큰은 옵션으로 받지 않는다. 토큰은 `AGB_TOKEN`, 설정 파일의 행위자별 토큰, 설정 파일의 공용 토큰 순으로 고른다. 세션 ID는 지정하지 않으면 `cli-YYYYMMDD`이고 클라이언트 호스트는 호스트 이름이다. 두 값은 행위자와 함께 활동 기록에 남는다.

```bash
# 프로젝트를 만든다. 키는 대문자로 시작하는 대문자와 숫자 2~10자이며 이슈 키의 접두어가 된다.
agb project create DEMO "Demo project"

# 현황과 목록을 조회한다.
agb project overview DEMO
agb issue list -p DEMO --status doing
agb queue -p DEMO --limit 1
agb search 정렬 -p DEMO
agb milestone list -p DEMO

# 이슈를 등록한다. 에이전트의 요청은 종류별 템플릿의 ## 제목이 모두 있어야 한다.
agb template show feature > body.md
# body.md의 각 제목 아래를 채운 뒤 등록한다.
agb issue create -p DEMO -t "댓글 최신순 정렬" --type feature --priority P1 --body-file body.md

# 상태를 바꾼다. 취소와 삭제는 --reason이 필수다.
agb issue update DEMO-1 --status doing
agb issue update DEMO-1 --status cancelled --reason "DEMO-2와 중복"

# 댓글을 단다. --summary는 목록에 보이는 한 줄 요약이다(1~30자).
agb issue comment DEMO-1 --summary "정렬 쿼리 구현" --body-file - <<'EOF'
최신순 정렬 쿼리를 추가했다.
EOF
```

출력은 터미널이면 표, 파이프나 파일이면 서버 응답 JSON이다. `--table`과 `--json`으로 바꿀 수 있다. 종료 코드는 0이 성공, 1이 API·네트워크 오류, 2가 사용법 오류다. 전체 명령은 `agb --help`, 명령별 옵션은 `agb <명령> --help`로 확인한다.

## REST API

`/v1` 아래에 `projects`, `issues`, `topics`, `milestones`, `documents`, `templates`, `activity`, `board`, `queue`, `labels`, `actors`, `device-authorizations`, `tokens`, `health` 경로가 있다. `health`와 로그인에 쓰는 두 요청(`POST /v1/device-authorizations`, `POST /v1/tokens`)을 뺀 요청에는 토큰(`Authorization: Bearer`, 공용 토큰 또는 행위자 토큰)과 행위자(`X-Actor`), 세션 ID(`X-Session-Id`), 클라이언트 호스트(`X-Client-Host`) 헤더가 있어야 한다. 행위자는 서버에 선언된 값이어야 하고, 공용 토큰은 `user` 종류 행위자만, 행위자 토큰은 그 토큰의 행위자만 쓸 수 있다(어긋나면 403). 토큰이 없거나 틀리면 401, 추적 헤더가 규칙에 맞지 않으면 400으로 거부하며, 거부한 요청을 포함한 모든 요청이 서버 로그에 남는다.

```bash
curl -H "Authorization: Bearer $AGB_TOKEN" \
  -H "X-Actor: owner" -H "X-Session-Id: demo-1" -H "X-Client-Host: laptop" \
  http://127.0.0.1:8420/v1/projects
```

## 웹 클라이언트

React 19와 TypeScript로 만들고 Vite로 빌드하는 SPA다. 서버가 `AGB_WEB_DIR`의 빌드 결과를 서빙하면 API와 같은 주소에서 열리고, Docker 이미지에는 빌드 결과가 들어 있다. 로그인 화면에 `AGB_TOKEN` 값을 붙여 넣는다.

개발 서버 실행, 인스턴스 전환, 단축키, 검증 명령은 [web/README.md](web/README.md)에 있다.

## 배포

운영 배포 구성은 [deploy/README.md](deploy/README.md)에 있다. 운영용 Compose 파일과 클러스터 매니페스트(`deploy/`), 이미지 정의(`Dockerfile`), 배포 워크플로(`.github/workflows/deploy.yml`)로 이루어진다. `main` 브랜치에 푸시하면 워크플로가 테스트, arm64 이미지 빌드와 GHCR 업로드, 배포, 헬스 확인을 차례로 실행한다. 백업에서 복원하는 절차도 이 문서에 있다.

## 개발

Go 1.26 이상과 Node 22, pnpm이 필요하다(CI는 pnpm 11.20.0을 쓴다). 이미지 빌드와 Compose 실행에는 Docker가 필요하다.

저장소 루트에서 Go 검증을 실행한다.

```bash
gofmt -l $(git ls-files '*.go')
go vet ./...
go test ./...
```

`gofmt -l`은 출력이 없어야 한다. 웹 클라이언트 검증은 `web/` 폴더에서 실행한다.

```bash
pnpm install
pnpm typecheck
pnpm lint
pnpm test
pnpm build
```

`.github/workflows/deploy.yml`의 `test` 잡이 같은 검사를 실행한다. e2e 테스트에는 빌드 태그 `e2e`가 붙어 있으므로 `go test ./...`에 포함되지 않는다. 시험용 서버의 주소와 토큰을 `AGB_E2E_URL`, `AGB_E2E_TOKEN`으로 지정하고 `go test -tags e2e -count=1 ./e2e/`를 실행한다. 실행마다 임의의 접두어를 붙인 프로젝트가 새로 만들어진다.

| 경로 | 내용 |
|---|---|
| `cmd/agb` | 바이너리 진입점. `serve`와 `version`을 처리하고 나머지 명령은 CLI에 넘긴다. |
| `internal/api` | HTTP 계층. 라우팅, 인증과 추적 헤더 검사, 요청 로그, 요청 검증, 오류 응답 |
| `internal/store` | SQLite 저장소. 키 발급, 순환 검사, 버전, 활동 기록 같은 트랜잭션 업무 규칙과 마이그레이션 |
| `internal/model` | 도메인 객체, 열거형, 필드 규칙 |
| `internal/cli`, `internal/client` | CLI 명령과 서버를 호출하는 HTTP 클라이언트 |
| `internal/config` | `AGB_*` 환경변수에서 서버 설정을 읽는다. |
| `internal/server` | 설정, 저장소, HTTP 핸들러, 백업을 한 프로세스로 묶는다. |
| `internal/backup` | 데이터베이스 스냅샷을 압축해 오브젝트 저장소에 올리는 백업 실행기 |
| `web/` | 웹 클라이언트 |
| `skills/tool-agb/` | 에이전트 스킬 |
| `deploy/` | 운영 Compose 파일과 클러스터 매니페스트 |
| `e2e/` | 실행 중인 서버에 HTTP로 요청하는 end-to-end 테스트 |
