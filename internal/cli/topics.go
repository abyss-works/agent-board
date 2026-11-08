package cli

// helpTopic is a page of help that belongs to no single command: the
// reference of a format the commands share.
type helpTopic struct {
	name    string
	summary string
	text    string
}

// helpTopics are the pages that agb help <주제> prints. Their names must not
// be the name of a command, which agb help <명령> prints.
var helpTopics = []helpTopic{
	{"repo-config", "저장소 설정 .agb.json 의 키, 규칙, 예시", repoConfigHelp},
	{"doc-format", "기록 문서의 형식, 메타데이터, 상태, 참조 표기, 오류와 대처", docFormatHelp},
	{"migrate-v0.8", "v0.8 에서 v0.9 로 옮겨 오는 절차", migrateV08Help},
}

// helpTopicText is the text of the topic called name, or "" when there is none.
func helpTopicText(name string) string {
	for _, t := range helpTopics {
		if t.name == name {
			return t.text
		}
	}
	return ""
}

// repoConfigExample is a .agb.json for a repository that keeps record
// documents in history/, archives in archive/ and the documents of an older
// layout in other folders.
const repoConfigExample = `{
  "project": "AGB",
  "auto_sync": true,
  "documents": [
    {"glob": "history/*.md", "name": "base"},
    {"glob": "archive/*.md", "name": "base", "archived": true},
    {"glob": "archive/*/**/*.md", "name": "path", "imported": true, "archived": true},
    {"glob": "decisions/*.md", "name": "path", "imported": true, "kind": "decision"},
    {"glob": "reports/*.md", "name": "path", "imported": true, "kind": "report"},
    {"glob": "incidents/*.md", "name": "path", "imported": true, "kind": "incident"}
  ],
  "new": {
    "template": "history/_{kind}.md",
    "output": "history",
    "values": {"project": "agent-board", "author": "claude"}
  },
  "follow_up_headings": ["남은 것", "미확인", "후속"]
}
`

var repoConfigHelp = `저장소 설정 .agb.json

저장소 최상위에 둡니다. 이 파일이 있는 저장소가 agb 를 지식 저장소로 씁니다. 파일이 없으면 CLI 는 문서를
자동으로 올리지 않습니다. CLI 가 읽고 서버는 알지 못합니다. 저장소가 쓰는 폴더 이름은 이 파일에만 있고,
agb 는 아래 예시의 폴더 이름을 알지 못합니다.

만들기: agb init -p <KEY> 는 documents 가 빈 파일을 만듭니다. 규칙을 채우기 전에는 아무 파일도 올라가지
않습니다. 새로 쓰거나 고친 뒤에는 agb doc sync 로 서버에 없는 문서가 무엇인지 보고 올립니다.

키:
  project              agb 프로젝트 키 (필수)
  auto_sync            true 이면 agb agenda 와 agb issue comment --status done 이 커밋한 문서 중 서버에
                       없는 것을 올립니다 (기본 false)
  documents            올릴 파일의 규칙 목록. 위에서부터 처음 맞는 규칙을 쓰고, 어느 규칙에도 맞지 않는
                       파일은 올리지 않습니다
  new                  agb doc new 가 쓰는 서식과 채울 값
  follow_up_headings   후속 항목을 알리는 ## 절 제목 목록. 적은 말이 제목에 들면 그 절의 항목을 후속
                       항목으로 알립니다. 목록이 없으면 알리지 않습니다

documents 규칙의 키:
  glob                 저장소 기준 경로 패턴 (필수). / 로 나누고, * 는 한 조각 안의 글자, ? 는 글자 하나,
                       ** 는 0개 이상의 폴더입니다. ** 는 조각 하나를 통째로 차지해야 합니다
  name                 서버에 올릴 이름 (필수). base 는 파일 이름, path 는 저장소 기준 경로
  kind                 기본 종류 (선택). 종류의 형식만 검사합니다. other 는 imported 규칙에만 씁니다
  archived             아카이브로 올림 (선택, 기본 false)
  imported             가져온 문서로 올림 (선택, 기본 false)

new 의 키:
  template             서식 파일의 저장소 기준 경로 (필수). {kind} 는 종류로 바뀌며, 서식이 있는 종류를 저장소가 쓰는 종류로 봅니다
  output               새 파일을 둘 폴더 (필수)
  values               프런트매터의 같은 키를 채울 값. date 와 --title 로 준 title 은 항상 채워집니다

규칙의 동작:
- 규칙의 값은 agb doc put 의 옵션과 같은 필드입니다. 파일 하나를 올릴 때 규칙의 값이 기본이고 옵션이 있으면
  옵션이 이깁니다.
- 서버가 받지 않는 파일(서식, .md 가 아닌 파일, 1MB 초과)은 규칙과 상관없이 올리지 않습니다.
- 커밋하지 않았거나 커밋 뒤 바뀐 파일은 올리지 않습니다.
- 서버에 없는 파일만 이름순으로 올립니다. 이미 있는 파일의 메타데이터 차이는 고치지 않습니다.
- 두 파일이 같은 이름이 되면(name 이 base 인 규칙에서 폴더만 다른 같은 파일 이름) 앞선 것만 올리고 나머지는
  skipped 로 알립니다.
- agb doc check 는 documents 규칙에 맞고 가져온 문서가 아닌 파일을 서버에 올리기 전에 검사합니다.
  new.template 이 있으면 서식의 프런트매터 키와 ## 절도 맞춥니다.

잘못된 파일: 알 수 없는 키, 잘못된 값, 형식이 틀린 JSON 은 오류로 거절합니다. .agb.json 이 있는데 읽을 수
없으면 자동 등록은 경고만 하고 아무것도 올리지 않습니다. 파일이 없을 때 다른 설정 파일로 돌아가지 않습니다.

예시: history/ 에 일반 문서를, archive/ 바로 아래에 아카이브를, 이전 체계 폴더에 가져온 문서를 둔 저장소입니다.

` + repoConfigExample

var docFormatHelp = `기록 문서의 형식, 메타데이터, 상태, 참조 표기, 오류

서버가 받는 기록 문서(결정, 사고, 보고, 구현 기록, 조사 자료)의 규칙입니다. 올리는 쪽의 폴더 이름과 문서
규칙은 서버가 알지 못하므로, 맞지 않는 문서는 올리는 쪽이 아래 규칙대로 맞춥니다. 프로젝트 키는 저장소
.agb.json 의 project 이고, 없으면 -p <KEY> 로 줍니다(agb help repo-config).

받는 문서:
- 마크다운(.md), UTF-8, 1MB(1048576바이트) 이하인 파일만 받습니다. 마크다운이 아닌 첨부는 받지 않습니다.
- 서식(이름이 _ 로 시작하거나 .example.md 로 끝나는 파일)은 받지 않습니다.
- 올린 원문은 고치거나 지울 수 없습니다. 같은 이름에 다른 원문을 올리면 거절합니다. 올린 뒤 고칠 수 있는
  것은 아카이브 표시와 관계뿐입니다(agb doc archive, agb doc link).
- 결정 기록 프런트매터의 status, decided_by 는 서버 필드가 아닙니다. 원문에 남아 검색으로 찾을 수 있고,
  걸러 보려면 tags 로 넘깁니다.

문서는 두 가지입니다.
  일반 문서        이름 yyyy-mm-dd-HHMM-<종류>-<slug>.md (slug 는 영문 소문자, 숫자, 하이픈). 검증이 엄격합니다
  가져온 문서      이름 저장소에서 본 상대 경로(notes/design.md). --imported. 검증이 느슨합니다
종류는 영문 소문자로 시작하고 영문 소문자, 숫자, _ 로만 이루어진 1~32자입니다(하이픈은 쓸 수 없습니다. 파일
이름에서 종류와 slug 를 가르기 때문입니다). 서버는 이 형식만 검사하고, 어떤 종류를 쓸지는 저장소가 정합니다.
저장소가 쓰는 종류는 .agb.json 의 new.template 서식 파일이 있는 종류이고, agb doc check 가 그 서식이
없는 종류를 문제로 알립니다. other 는 가져온 문서에만 씁니다.

메타데이터: 필드마다 옵션, 원문 프런트매터, 대체값 순으로 정합니다. 옵션이 있으면 프런트매터를 대신합니다.
  필드        옵션                         프런트매터 키    대체값
  kind        --kind <종류>                kind            일반 문서는 이름의 종류. 가져온 문서는 other
  date        --date <yyyy-mm-dd[ HH:MM]>  date            이름 앞의 날짜. 이름에 없으면 비움
  title       --title <제목>               title           일반 문서는 없음(필수). 가져온 문서는 첫 "# " 제목줄, 없으면 비움
  tags        --tag <태그> (반복)          tags            빈 목록 (프런트매터는 [a, b] 목록)
  supersedes  --supersedes <이름> (반복)   supersedes      빈 목록
  related     --related <이름> (반복)      related         빈 목록
  issues      --issue <이슈 키> (반복)     issue, issues   빈 목록
  archived    --archived                   읽지 않음        false
  imported    --imported                   읽지 않음        false
  path        --path <상대 경로>           읽지 않음        비움. 정보용 값이고 서버는 판단에 쓰지 않음
- 옵션으로 준 목록은 프런트매터의 같은 목록을 통째로 대신합니다. 합치지 않습니다.
- 프런트매터에서 읽는 키는 kind, date, title, tags, supersedes, related, issue, issues 뿐이고 값은 한 줄입니다.
  archived 는 적어도 읽지 않습니다. 아카이브로 올리려면 --archived 를 주거나 .agb.json 의 규칙에 적습니다.
- 본문에 적은 이슈 키는 관계로 읽지 않습니다. 이슈와 이으려면 --issue 나 issue: 프런트매터를 씁니다.
- 일반 문서는 title 이 필수입니다. 프런트매터에 없으면 --title 로 줍니다.
- 일반 문서는 자리표시자(<...>)가 남은 값을 거절합니다(placeholder). 서식에서 만든 파일은 모두 채웁니다.

상태: 저장하지 않고 서버가 계산합니다. 우선순위는 아카이브, 대체됨, 유효 순입니다.
  current      유효. 지금 판단의 근거. 아래 둘에 해당하지 않음
  superseded   대체됨. 다른 문서의 supersedes 가 이 문서를 가리킴
  archived     아카이브. 판단 근거가 아닌 배경과 조사 자료. 올릴 때나 올린 뒤에 표시함
- 조회 명령은 기본으로 유효한 문서만 찾습니다. --state 로 넓힙니다(superseded, archived, all, 쉼표로 여럿).
  agb find 는 이에 더해 열린 이슈만, agb recall 은 지운 것을 뺀 모든 이슈를 기본으로 찾습니다(--status 로
  넓힙니다).
- agb graph 는 대체됨·아카이브 이웃을 접고 개수만 보입니다(--state all 로 펼칩니다). 시작 노드는 상태와
  관계없이 나옵니다.
- 결정 기록의 status(채택, 기각, 보류)는 문서 상태와 별개입니다.

참조 표기:
- 문서 참조는 같은 프로젝트에 올라간 문서의 이름입니다. 일반 문서는 파일 이름(2026-10-20-1544-decision-x.md),
  가져온 문서는 경로(notes/design.md)이고, 글자가 같아야 풀립니다.
- 이슈 참조는 이슈 키(AGB-12)입니다. 다른 프로젝트의 이슈도 됩니다.
- 한 값에는 이름 하나만 씁니다. 설명 문장을 붙이지 않습니다.
- 다른 프로젝트의 문서는 PROJECT/이름 입니다. 같은 프로젝트에 그 이름의 문서가 있으면 그 문서가 먼저이고,
  없을 때만 첫 / 앞을 프로젝트 키로 읽습니다.
- 이슈의 참조 doc:<문서 이름>, issue:<이슈 키>도 같은 이름과 키를 쓰고, 다른 프로젝트의 문서는
  doc:PROJECT/이름 입니다. 대상이 없으면 거절하므로 문서를 먼저 올리고 참조를 겁니다.
- supersedes 는 한 문서를 통째로 대신하는 전체 대체에만 씁니다. 일부만 대체하거나 보완하거나 참고하면
  related 에 씁니다. 문장이 섞인 supersedes 는 거절합니다(not_a_name).
- 관계가 없으면 supersedes, related 의 줄을 지우거나 값을 비우거나(supersedes:) none 이라고 씁니다. 소문자
  none 과 빈 값은 관계 없음이고, 목록 안의 none 과 빈 항목도 건너뜁니다. 일반 문서는 그 밖의 이름이 아닌
  값을 거절하고, 가져온 문서는 이름 검사를 하지 않습니다.

예:
  supersedes: 2026-10-18-0900-decision-queue-draft.md
  related: [2026-10-17-1000-report-queue-survey.md, 2026-10-19-1100-incident-queue-lock.md]
  issue: AGB-21

형식에 맞지 않는 문서 올리기: 원문은 고치지 않고 메타데이터를 옵션으로 넘겨 맞춥니다. 파일과 서버의 사본이
같으므로 다시 올려도 충돌하지 않습니다.
  1. 파일 이름이 일반 문서 형식이면 일반 문서로 올립니다. 프런트매터가 없거나 title 이 없으면 옵션으로 줍니다.
       agb doc put <파일> -p <KEY> --title "<제목>" --tag <태그> --issue <이슈 키>
  2. 파일 이름이 그 형식이 아니면 가져온 문서로 올립니다. 이름은 저장소 기준 상대 경로이고, 날짜는 이름 앞의
     날짜, 제목은 첫 "# " 제목줄로 채워집니다. 맞지 않으면 옵션으로 줍니다.
       agb doc put <파일> -p <KEY> --imported --kind decision --date 2026-10-17 --title "<제목>"
  3. 판단 근거가 아닌 문서는 아카이브로 올립니다.
       agb doc put <파일> -p <KEY> --imported --archived
  4. 새 기록이 이전 문서를 대체했으면 새 기록에 --supersedes <이전 문서 이름> 을 줍니다. 이전 문서가 서버에
     없으면 먼저 올립니다.
  5. 같은 설정을 여러 문서에 반복하면 옵션 대신 .agb.json 의 규칙으로 씁니다(agb help repo-config).

올린 뒤 고치기: 고칠 수 있는 것은 아카이브 표시와 관계(supersedes, related, 이슈)뿐입니다. 변경마다 활동
기록이 남습니다. 원문, 종류, 날짜, 제목, 태그는 고치지 않습니다.
  agb doc archive <PROJECT> <이름>      agb doc unarchive <PROJECT> <이름>
  agb doc link <PROJECT> <이름> --supersedes|--related <대상> | --issue <이슈 키>
  agb doc unlink <PROJECT> <이름> (link 와 같은 옵션)
지금 상태와 관계는 agb doc get <PROJECT> <이름>, 변경 이력은 agb doc activity 로 봅니다.

오류와 대처: 종료 코드가 1이면 표준 오류에 서버의 응답이 나옵니다. JSON 이면 code 와 details[].reason 을 봅니다.
  응답  reason             뜻                                     대처
  409   immutable          서버에 같은 이름의 다른 원문이 있음      원문을 고쳐 다시 올리지 않습니다. 뜻을 바꾼 기록이면
                                                                    새 기록을 쓰고 supersedes 로 잇습니다
  409   metadata_differs   원문은 같은데 메타데이터가 다름          details 에 다른 필드가 나옵니다. 아카이브와 관계는 위
                                                                    명령으로 고치고, kind, date, title, tags, imported,
                                                                    path 는 고칠 수 없으니 서버의 값을 그대로 둡니다
  409   cycle              supersedes 가 순환함                    대체 방향을 확인합니다. 새 기록이 이전 기록을 대체합니다
  409   project_not_active 프로젝트가 활성이 아님                  프로젝트 상태를 확인합니다
  422   unresolved         supersedes, related, issue 의 대상이     대상 문서를 먼저 올리거나 이슈 키를 확인합니다. 가져온
                           서버에 없음                              문서(--imported)는 오류 대신 경고로 등록되고, 대상이
                                                                    나중에 올라오면 풀립니다
  422   not_a_name         supersedes, related 의 값이 이름이 아님  이름만 씁니다. 부분 대체는 related 로 옮깁니다
  422   self_reference     자기 자신을 가리킴                      값을 지웁니다
  422   placeholder        값이 서식의 자리표시자(<...>)임          실제 값으로 채웁니다
  422   required           일반 문서에 title 이 없음                --title 로 주거나 프런트매터에 적습니다
  422   (필드별)           이름 형식, 종류, 날짜, 크기 위반         details 의 필드를 고칩니다. 이름 형식이 맞지 않는 기존
                                                                    문서는 위 "형식에 맞지 않는 문서 올리기"로 올립니다
  400   (location)         없어진 조회 매개변수 location            --state(상태), --imported(가져온 문서)로 바꿉니다
`

var migrateV08Help = `v0.8 에서 v0.9 로 옮겨 오기

v0.8 까지는 CLI 가 폴더 이름으로 문서의 위치(history, archive, legacy)를 정했습니다. v0.9 부터는 위치가
없고, 저장소의 .agb.json 과 문서의 메타데이터가 그 자리를 대신합니다.

1. 서버를 먼저 v0.9 로 올립니다. 서버가 시작할 때 스키마를 14 로 이관합니다. 이미 올라간 문서는 이름이 그대로
   이고 다음처럼 바뀝니다.
   - history: 일반 문서
   - archive: 일반 문서 + 아카이브
   - legacy: 가져온 문서(종류는 그대로). 이름이 archive/ 로 시작하면 아카이브도 함께
   - 프런트매터의 supersedes, related, issue 는 관계로 옮겨집니다. 문장이 섞인 supersedes 는 related 로
     내려갑니다. 이 때문에 대체됨이 된 문서가 있는지 agb doc list --state superseded 로 봅니다.
2. CLI 를 갱신합니다. 설치 방법은 스킬의 references/setup.md 에 있습니다.
3. 문서를 올리는 저장소마다 .agb.json 을 만듭니다. 파일이 없으면 CLI 가 더는 문서를 자동으로 올리지 않습니다.
     agb init -p <KEY>
   agb help repo-config 의 예시를 보고 documents 규칙을 v0.8 까지 CLI 가 하던 것과 같게 씁니다. 폴더 규칙이
   바뀐 곳은 저장소 소유자의 몫입니다.
4. agb doc sync 로 확인합니다. 서버에 이미 있는 문서는 unchanged 이고 서버에 없는 것만 uploaded 입니다. 서버의
   같은 이름 문서와 원문이 다르면 conflicts 입니다. 위치만 바뀌는 moved 는 없어졌습니다. 폴더를 옮겨도 서버의
   상태는 바뀌지 않으며, 아카이브는 agb doc archive 로 표시합니다.
5. 일반 문서 서식에서 supersedes: <... | none> 같은 값을 쓰던 저장소는 서식을 고칩니다. none 은 관계 없음으로
   받고, 설명 문장은 문서 이름이 아니라 거절됩니다(not_a_name).
6. 구 CLI 와 새 CLI 가 섞여 있는 동안(v0.9.x)에는 서버가 구 CLI 의 요청을 받습니다. 요청의 location 을
   archived, imported 로 바꿔 읽고, 응답에는 계산한 location 을 넣습니다. 구 CLI 가 폴더를 옮긴 파일을 다시
   올려도 서버의 문서는 바뀌지 않습니다. 구 CLI 의 doc search --location 은 400 입니다. v0.9.x 뒤의 판에서 이
   호환을 없앨 수 있으므로 모든 호스트의 CLI 를 갱신합니다.
`
