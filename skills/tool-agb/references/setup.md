# agb 설치와 접속 준비

새 기기에서 agb를 처음 쓰거나, CLI와 스킬을 다시 설치하거나, 접속이 되지 않을 때 이 문서를 따른다. 기기마다 한 번 하고, 릴리즈가 바뀌면 1절과 2절을 다시 한다.

- 대상 환경: Windows(PowerShell, Git Bash), Linux(amd64, arm64, 화면 없는 서버 포함)
- 제품 저장소: https://github.com/abyss-works/agent-board
- 운영 인스턴스: https://agb.abyssworks.dev (API `/v1`, 웹 화면은 같은 주소)
- 자리표시자: `<REF>`는 설치할 릴리즈(기본 `main`, 고정하려면 커밋), `<VER>`는 `agb version`이 출력할 버전 문자열(`CHANGELOG.md` 맨 위 버전 이름)이다.

## 1. CLI 설치

1. 선행 조건을 확인한다.
   ```bash
   go version
   command -v agb && agb version
   ```
   - Expected: `go1.26` 이상. agb가 이미 있고 버전이 운영 서버와 같으면 2절로 넘어간다.
   - Go가 없으면 먼저 설치한다(https://go.dev/dl, Windows는 `scoop install go`).
   - `$(go env GOPATH)/bin`(기본 `~/go/bin`)이 `PATH`에 있어야 한다.
2. 설치할 릴리즈를 정한다. 기본은 운영 서버와 같은 최신 릴리즈(`main`)다. 운영 서버의 버전은 6절로 본다.
3. 설치한다. Windows와 Linux가 같다.
   ```bash
   REF=main
   VER=<VER>
   go install -trimpath -ldflags "-X main.version=${VER}" github.com/wsk0715/agent-board/cmd/agb@${REF}
   ```
   - 모듈 경로 `github.com/wsk0715/agent-board`는 저장소의 이전 주소다. GitHub이 `abyss-works/agent-board`로 연결한다.
   - `@latest`를 쓰지 않는다. 저장소에 버전 태그가 없어 `@latest`는 기본 브랜치(`dev`, 출시 전)를 받는다. 릴리즈는 `@main`이나 커밋으로 받는다.
   - `-ldflags`를 빼면 `agb version`이 `dev`로 나온다.
4. 확인한다.
   ```bash
   agb version
   ```
   - Expected: `agb <VER>`

출시 전 버전이 필요한 개발 기기에서는 제품 저장소의 `dev` 체크아웃에서 설치한다.

```bash
go install -trimpath -ldflags "-X main.version=<VER>-dev.$(git rev-parse --short HEAD)" ./cmd/agb
```

## 2. 스킬 설치와 갱신

스킬은 에이전트 도구의 전역 스킬 폴더에 `tool-agb/`로 둔다. 원본은 제품 저장소의 `skills/tool-agb/`이고, CLI와 같은 릴리즈의 것을 쓴다. 폴더에는 `SKILL.md`와 이 문서(`references/setup.md`)가 있다.

1. 릴리즈본을 받는다. `SKILLS_DIR`에 도구의 전역 스킬 폴더를 넣는다.
   ```bash
   REF=main
   SKILLS_DIR="<전역 스킬 폴더>"
   BASE="https://raw.githubusercontent.com/abyss-works/agent-board/${REF}/skills/tool-agb"
   mkdir -p "$SKILLS_DIR/tool-agb/references"
   curl -fsSL -o "$SKILLS_DIR/tool-agb/SKILL.md" "$BASE/SKILL.md"
   curl -fsSL -o "$SKILLS_DIR/tool-agb/references/setup.md" "$BASE/references/setup.md"
   ```
   ```powershell
   $REF = 'main'
   $SKILLS_DIR = '<전역 스킬 폴더>'
   $BASE = "https://raw.githubusercontent.com/abyss-works/agent-board/$REF/skills/tool-agb"
   New-Item -ItemType Directory -Force "$SKILLS_DIR\tool-agb\references" | Out-Null
   Invoke-WebRequest -UseBasicParsing -OutFile "$SKILLS_DIR\tool-agb\SKILL.md" "$BASE/SKILL.md"
   Invoke-WebRequest -UseBasicParsing -OutFile "$SKILLS_DIR\tool-agb\references\setup.md" "$BASE/references/setup.md"
   ```
   - CLI를 다시 설치하면 스킬도 같은 `REF`로 다시 받는다.
   - v0.1.1 이하 릴리즈에서는 원본이 `skills/agent-board/SKILL.md` 하나이고 `references/`가 없다.
2. 제품 저장소를 체크아웃한 개발 기기에서는 복사 대신 전역 스킬 폴더의 `tool-agb`를 저장소의 `skills/tool-agb/`로 향하는 정션이나 심볼릭 링크로 만든다. 스킬은 체크아웃한 브랜치를 따른다.
   ```powershell
   New-Item -ItemType Junction -Path "$SKILLS_DIR\tool-agb" -Target "<제품 저장소>\skills\tool-agb"
   ```
3. 확인한다.
   - Expected: `SKILL.md`의 첫 줄이 `---`이고 `name: tool-agb`가 있다. v0.1.1 이하 릴리즈본은 `name: agent-board`다.

## 3. 접속 설정

토큰 값은 채팅, 이슈, 커밋, 로그에 남기지 않는다.

1. 서버 주소를 넣는다.
   ```bash
   agb config set url https://agb.abyssworks.dev
   ```
2. 토큰을 받는다. 토큰은 행위자마다 다르다.
   - 에이전트: `SKILL.md`의 "로그인이 필요할 때"를 따라 `agb --actor <자기 ID> login`으로 받는다. 사용자가 웹에서 승인한다.
   - 사용자(`owner`): 공용 토큰은 사용자 행위자 전용이다. 사용자가 터미널에서 `agb config set-token`을 실행하고 토큰을 붙여 넣는다. 입력은 화면에 보이지 않는다. 에이전트는 공용 토큰을 넣거나 쓰지 않는다.
3. 행위자와 세션 ID는 설정 파일에 두지 않는다.
   - 에이전트마다 값이 다르므로 명령마다 옵션이나 환경변수(`AGB_ACTOR`, `AGB_SESSION_ID`)로 준다.
   - 설정 파일이나 셸 프로필에 `owner`를 두지 않는다. 에이전트가 `--actor`를 빠뜨리면 사용자 이름으로 기록되기 때문이다. 사람이 터미널에서 쓸 때는 `--actor owner`를 붙인다.

## 4. 접속 확인

읽기 명령만 쓴다. 확인 단계에서 이슈를 만들거나 바꾸지 않는다.

```bash
agb config show
agb health
agb --actor <자기 ID> whoami
agb --actor <자기 ID> project list
agb --actor <자기 ID> project overview <KEY>
```

- Expected
  - `config show`: `url`이 운영 주소이고 `actor_tokens`에 자기 ID가 있다.
  - `health`: `"status":"ok"`
  - `whoami`: `id`가 자기 행위자다.
  - `project list`, `project overview`: 종료 코드 0이고, 목록에 프로젝트 `AGENTS.md`의 "이슈 추적" 칸에 적힌 키가 있다.

새 기기는 1절, 2절, 3절, 4절 순서로 한다.

## 5. 실패 처리

`SKILL.md`의 "출력과 종료 코드", "접속할 수 없을 때"에 더해 다음을 따른다.

| 증상 | 조치 |
|---|---|
| `go install`이 모듈을 찾지 못한다 | `REF`가 맞는지, 네트워크가 GitHub에 닿는지 확인한다. 다른 경로로 우회 설치하지 않고 사용자에게 알린다. |
| `agb health`가 실패하거나 502 | 서버를 직접 띄우거나 다시 시작하지 않는다. 등록할 항목을 최종 보고에 모으고 사용자에게 알린다. |
| 400과 `details[].reason`의 `unknown_actor` | 행위자 ID가 서버에 선언되어 있지 않다. 다른 행위자로 바꾸지 않고 사용자에게 알린다. |
| 400 `If-Match` 관련 오류 | 설치된 CLI가 서버보다 오래되었을 수 있다. `agb version`을 확인하고 1절로 다시 설치한다. |

## 6. 최신 버전 확인

```bash
curl -s https://agb.abyssworks.dev/v1/health
gh api repos/abyss-works/agent-board/commits/main --jq '.sha[0:7] + " " + .commit.message'
```

- 운영 서버의 `version`은 `git-<커밋>`이다. `main`의 최신 커밋 메시지가 릴리즈 버전(`vX.Y.Z`)이다.
- 두 커밋이 같으면 운영 서버가 최신 릴리즈다. 설치한 `agb version`이 그 버전과 같아야 한다. 릴리즈별 변경은 `CHANGELOG.md`에 있다.

## 7. 운영 인스턴스

- 운영 인스턴스는 제품 저장소의 `main`에 푸시하면 GitHub Actions가 배포한다. 수동으로 다시 시작하거나 다시 띄우지 않는다.
- 운영 인스턴스에 e2e나 시험 데이터를 보내지 않는다. e2e는 로컬이나 스테이징 인스턴스에 대고 한다.
- 기록은 운영 인스턴스에만 한다. 로컬과 스테이징 인스턴스에는 기록하지 않는다.
