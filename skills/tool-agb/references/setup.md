# agb 설치와 접속 준비

새 기기에서 agb를 처음 쓰거나, CLI와 스킬을 다시 설치하거나, 접속이 되지 않을 때 이 문서를 따른다. 기기마다 한 번 하고, 릴리즈가 바뀌면 1절과 2절을 다시 한다.

- 대상 환경: Windows(PowerShell, Git Bash), Linux(amd64, arm64, 화면 없는 서버 포함)
- 제품 저장소: https://github.com/abyss-works/agent-board
- 서버 주소: `<서버 주소>` (API `/v1`, 웹 화면은 같은 주소)
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

스킬은 CLI 바이너리에 내장되어 있다. 원본은 제품 저장소의 `skills/tool-agb/`이고, `agb skill install`이 CLI와 같은 릴리즈의 스킬을 전역 스킬 폴더에 `tool-agb/`로 쓴다. 폴더에는 `SKILL.md`와 이 문서(`references/setup.md`), 설치한 agb 버전을 적은 `.version`이 있다.

1. 설치한다.
   ```bash
   agb skill install
   ```
   - 기본 위치는 `~/.agents/skills/tool-agb/`다. 에이전트 도구가 다른 폴더의 스킬을 읽으면 `--dir <그 도구의 전역 스킬 폴더>`를 준다.
   - Expected: `설치했습니다: <위치> (이전 <버전>, 새 <버전>)` 또는 `변경 없음: <위치> (<버전>)`
   - 설치 위치가 링크(정션, 심볼릭 링크)이면 링크만 지우고 복사본을 둔다. 링크가 가리키던 파일은 바뀌지 않는다. 링크를 유지해야 하는 기기에서는 설치하지 않고 `agb skill check`만 쓴다.
   - `agb: 모르는 명령 "skill"`이 나오면 CLI가 v0.4.0 이하다. 1절로 CLI를 다시 설치한다.
   - `agb skill check`는 아무것도 바꾸지 않고 CLI 판을 서버 릴리즈와, 설치된 스킬(링크이면 가리키는 대상)을 내장 스킬과 견준다. 종료 코드 0은 맞거나 비교를 건너뜀(개발 빌드, 서버가 릴리즈를 알리지 않음, 서버에 닿지 못함), 1은 CLI가 서버보다 오래됐거나 스킬이 없거나 다름이다. 출력된 `갱신:`이나 `맞추기:` 줄을 따른다.
2. 확인한다.
   ```bash
   head -3 ~/.agents/skills/tool-agb/SKILL.md
   cat ~/.agents/skills/tool-agb/.version
   ```
   - Expected: `name: tool-agb`가 있고, `.version`이 `agb version`의 버전과 같다.

Claude Code를 쓰는 기기에서는 `agb skill install` 대신 `agb setup claude`를 실행한다. 플러그인과 스킬을 함께 설치한다.

```bash
agb setup claude --actor <ID>     # AGB_ACTOR 가 있으면 --actor 없이 실행
```

- 설치를 실행하는 행위자가 있어야 한다(`--actor`, `AGB_ACTOR`, 설정 파일의 `actor` 순서). 훅은 이 명령을 실행한 셸의 환경변수 없이 실행되므로, 그 행위자가 쓸 토큰과 서버 주소가 설정 파일에 있어야 한다. `AGB_TOKEN`, `AGB_URL`, `--url`로만 준 값은 세지 않는다. 행위자, 토큰, 주소 중 하나가 없으면 아무 단계도 실행하지 않고 종료 코드 2로 끝난다. 토큰이 없으면 `agb login --actor <ID>`를, 주소가 없으면 `agb config set url <주소>`를 먼저 한다. 훅은 기본 설정 파일만 읽으므로 `--config`를 주면 아무 단계도 실행하지 않고 종료 코드 2로 끝난다.
- 하는 일: `claude plugin marketplace add abyss-works/agent-board#main`, `claude plugin install agb@agent-board`, 스킬 설치, 훅 행위자 저장, 훅 점검을 차례로 한다. 스킬은 `~/.agents/skills/tool-agb/`에 쓰고, `~/.claude/skills`가 다른 폴더이면 거기에도 쓴다. `~/.claude/skills/tool-agb`가 링크이면 그대로 둔다.
- 훅 행위자: 설정 파일의 `hook_actors`에 `claude`의 값으로 이 실행의 행위자를 쓴다. 훅만 이 값을 쓰고, 다른 명령의 행위자는 바뀌지 않는다. 다시 실행하면 그 실행의 행위자로 바꾼다.
- Expected: 단계마다 `claude`의 출력과 `설치했습니다:` 또는 `변경 없음:` 줄, `훅 행위자 저장: claude → <ID>` 줄, `훅 점검:` 줄이 나오고 종료 코드 0이다. 다시 실행해도 된다.
- `훅 점검:` 줄은 훅과 같은 조회를 현재 폴더에서 한 번 한 결과다. 환경변수와 옵션 없이 설정 파일의 값(훅 행위자, 토큰, 주소)만으로 조회한다. 종료 코드를 바꾸지 않는다.
  - `프로젝트 <KEY>의 확인하지 않은 메시지 N건`: 이 폴더에서 시작한 세션에 그 N건이 보인다. 0건이면 훅은 아무것도 출력하지 않는다.
  - `이 폴더에서는 프로젝트를 판정할 수 없습니다`: 현재 폴더 위에 `.agb.json`이 없다.
  - `서버에 닿지 않습니다`, `서버가 오류를 돌려주었습니다 (...)`, `서버의 응답을 읽지 못했습니다`, 설정 오류 문구: 그 상태로는 세션에 알림이 나오지 않는다.
- `claude 실행 파일이 PATH 에 없습니다`가 나오면 아무것도 설치되지 않은 것이다. Claude Code를 설치한 뒤 다시 실행한다.
- 플러그인은 세션을 시작할 때 `agb hook session-start --tool claude`를 불러, 작업 폴더의 저장소 `.agb.json` 프로젝트가 확인하지 않은 메시지를 알린다. 새 세션부터 동작한다.
  - 훅의 행위자는 `--actor`, `AGB_ACTOR`, 설정 파일 `hook_actors`의 `claude` 값, 설정 파일의 `actor` 순서로 정한다. 훅의 세션 ID는 Claude Code가 훅에 준 `session_id`다.
  - 알림이 없으면 메시지가 없거나, `.agb.json`, 훅 행위자, 토큰, 서버 접속 중 하나가 없는 것이다. 세션의 작업 폴더에서 `agb setup claude --actor <ID>`를 다시 실행해 `훅 점검:` 줄을 보고, `agb config show`의 `hook_actors`와 `actor_tokens`를 확인한다. 플러그인이 옛 판이면 아래 순서로 갱신한다.
- 플러그인은 Bash 도구를 호출하기 전에 `agb hook pre-commit --tool claude`를 불러, 호출이 `git commit`이면 그 저장소(최상위에 `.agb.json`이 있는 저장소)의 기록 문서를 `agb doc check`와 같은 검사로 확인하고 문제가 있으면 커밋을 사유와 함께 막는다.
  - 검사하는 파일은 `agb doc check`에 파일을 주지 않았을 때와 같다. 새 파일이거나 `HEAD`에서 바뀐 기록 문서이고, 스테이징하지 않은 파일도 포함한다.
  - 커밋이 막히면 사유에 적힌 파일의 문제를 고친 뒤 다시 커밋한다. 사유는 4,000자까지이고 넘는 문제는 남은 건수로 적힌다. 전체는 그 저장소에서 `agb doc check`로 본다. 검사를 피하려고 커밋 명령의 형태를 바꾸지 않는다.
  - 커밋으로 보는 것은 `git commit`, `git -C <경로> commit`, `X=y git commit`, `<다른 명령> && git commit`, `cd <경로> && git commit` 형태다. 저장소는 명령 줄 첫 명령의 `cd <경로>`와 `-C`의 경로를 따르고, 사유의 첫 줄에 그 저장소의 경로가 적힌다.
  - 검사하지 않는 것: here-document의 본문과 `#` 주석과 따옴표 안에 든 `git commit`, `git commit --dry-run`과 `--help`, 따옴표나 here-document가 닫히지 않은 명령 줄. 커밋이 실행될 폴더를 정할 수 없을 때도 검사하지 않는다. 첫 명령이 아닌 `cd`나 옵션을 준 `cd`, `pushd`, `popd`가 커밋 앞에 있거나, 커밋이 괄호나 중괄호 묶음의 안이나 뒤에 있거나, `bash -c` 같은 중첩 셸 안에 있을 때다(`-C`가 절대 경로이면 그 경로로 정한다). `~`로 시작하거나 `$`가 든 경로와, Windows에서 `/c/...`처럼 `/`로 시작하는 경로도 풀지 않으므로 검사하지 않는다.
  - Bash 도구의 `git commit`이 아니거나, git 저장소가 아니거나, `.agb.json`이 없거나 읽지 못하거나, 검사가 실패하면 막지 않는다. 행위자나 토큰이 없거나 서버에 5초 안에 닿지 않거나 서버가 오류를 돌려주면 관계(`supersedes`, `related`, `issue`) 확인만 건너뛰고, 파일만으로 찾은 문제는 그대로 막는다.
- 갱신은 CLI(1절)를 먼저 하고 플러그인을 나중에 한다. 플러그인이 CLI에 없는 명령을 부르지 않게 하는 순서다.
  - 이 판부터의 CLI는 모르는 `agb hook <하위 명령>`을 아무것도 출력하지 않고 종료 코드 0으로 끝낸다. 플러그인이 CLI보다 새로워도 세션과 도구 호출을 막지 않고, 그 훅이 하는 일만 빠진다.
  - v0.14.1 이하의 CLI(v0.14.0과 v0.14.1, `hook` 명령이 없는 그 전 버전)는 그렇지 않다. 이 CLI에 커밋 전 훅이 든 플러그인이 붙으면 Bash 도구 호출이 모두 막히므로, 이 버전을 쓰는 기기는 CLI를 먼저 갱신한다. 옛 CLI는 모르는 하위 명령에 종료 코드 2로 끝나고, Claude Code는 도구 호출 전 훅의 종료 코드 2를 호출 거절로 처리한다. Bash 호출이 `모르는 명령 "pre-commit"`으로 막히면 사용자에게 터미널에서 CLI를 갱신하거나 `claude plugin disable agb@agent-board`로 플러그인을 끄도록 알린다.
  ```bash
  agb setup claude --actor <ID>
  claude plugin marketplace update agent-board
  claude plugin update agb@agent-board
  ```
- 제거: `claude plugin uninstall agb@agent-board`, `claude plugin marketplace remove agent-board`

제품 저장소의 개발 기기에서 스킬을 고쳤으면, `dev` 체크아웃에서 CLI를 다시 설치(1절의 개발 기기 명령)한 뒤 `agb skill install`을 실행해야 전역 스킬에 반영된다.

## 3. 접속 설정

토큰 값은 채팅, 이슈, 커밋, 로그에 남기지 않는다.

1. 서버 주소를 넣는다.
   ```bash
   agb config set url <서버 주소>
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
  - `project list`, `project overview`: 종료 코드 0이고, 목록에 작업할 프로젝트의 키(`.agb.json`의 `project` 또는 사용자가 알려 준 키)가 있다.

새 기기는 1절, 2절, 3절, 4절 순서로 한다.

## 5. 실패 처리

`SKILL.md`의 "출력과 종료 코드", "접속할 수 없을 때"에 더해 다음을 따른다.

| 증상 | 조치 |
|---|---|
| `go install`이 모듈을 찾지 못한다 | `REF`가 맞는지, 네트워크가 GitHub에 닿는지 확인한다. 다른 경로로 우회 설치하지 않고 사용자에게 알린다. |
| `agb health`가 실패하거나 502 | 서버를 직접 띄우거나 다시 시작하지 않는다. 등록할 항목을 최종 보고에 모으고 사용자에게 알린다. |
| 400과 `details[].reason`의 `unknown_actor` | 행위자 ID가 서버에 선언되어 있지 않다. 다른 행위자로 바꾸지 않고 사용자에게 알린다. |
| 400 `If-Match` 관련 오류 | 설치된 CLI가 서버보다 오래되었을 수 있다. `agb skill check`로 확인하고 1절로 다시 설치한다. |
| `agb: 모르는 명령 ...` | 설치된 CLI가 스킬이나 문서보다 오래되었거나, 스킬이 CLI보다 앞선 판일 수 있다. `agb skill check`를 실행하고 출력된 `갱신:`이나 `맞추기:` 줄을 따른다. |

## 6. 최신 버전 확인

```bash
curl -s <서버 주소>/v1/health
gh api repos/abyss-works/agent-board/commits/main --jq '.sha[0:7] + " " + .commit.message'
```

- 운영 서버의 `version`은 `git-<커밋>`이다. `main`의 최신 커밋 메시지가 릴리즈 버전(`vX.Y.Z`)이다.
- 두 커밋이 같으면 운영 서버가 최신 릴리즈다. 설치한 `agb version`이 그 버전과 같아야 한다. 릴리즈별 변경은 `CHANGELOG.md`에 있다.
- 운영 서버의 `release`(`vX.Y.Z`)가 배포된 릴리즈다. `agb skill check`가 이 값을 `agb version`과 견준다. 릴리즈 커밋이 아닌 빌드는 `release`가 없다.

## 7. 운영 서버

- 운영 서버는 배포 절차로만 바꾼다. 수동으로 다시 시작하거나 다시 띄우지 않는다.
- 운영 서버에 e2e나 시험 데이터를 보내지 않는다. e2e는 시험용 서버에 대고 한다.
- 기록은 실제로 쓰는 서버에 하고, 시험용 서버에는 시험 데이터만 둔다.
