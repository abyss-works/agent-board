package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var docCmd = &command{name: "doc", summary: "기록 문서 등록·동기화·검색·조회", subs: []*command{
	docPutCmd, docSyncCmd, docSearchCmd, docGetCmd,
}}

// errNotUTF8 reports a record document file that is not text the server could
// keep as it is.
var errNotUTF8 = errors.New("UTF-8이 아닌 파일입니다.")

// readDocContent reads a record document file. The server stores text, so the
// bytes must be UTF-8; they are sent as they are.
func readDocContent(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(raw) {
		return "", errNotUTF8
	}
	return string(raw), nil
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func (a *App) renderDocument(w io.Writer, resp *client.Response) error {
	d, err := decode[model.Document](resp)
	if err != nil {
		return err
	}
	fields(w, [][2]string{
		{"project", d.Project},
		{"name", d.Name},
		{"location", string(d.Location)},
		{"kind", string(d.Kind)},
		{"date", deref(d.Date, "-")},
		{"title", deref(d.Title, "-")},
		{"tags", joinOr(d.Tags, "-")},
		{"sha256", d.SHA256},
		{"size", strconv.Itoa(d.Size)},
		{"created", formatTime(d.CreatedAt, a.Location) + " (" + d.CreatedBy + ")"},
		{"updated", formatTime(d.UpdatedAt, a.Location) + " (" + d.UpdatedBy + ")"},
	})
	return nil
}

var docPutCmd = &command{
	name: "put", summary: "기록 문서 파일 하나를 서버에 등록",
	usage: `사용법: agb doc put <파일> --project <KEY>

관리 저장소의 history/ 나 archive/ 에 있는 기록 문서 파일 하나를 서버에 등록합니다. 파일이 있는 폴더의
이름이 위치(history, archive)가 되고 파일 이름이 문서 이름이 됩니다. 파일은 UTF-8 이어야 하고 읽은
바이트 그대로 올라갑니다.

- 처음 올리면 등록됩니다.
- 같은 내용을 같은 위치로 다시 올리면 바뀌는 것이 없습니다.
- 같은 내용을 다른 폴더의 파일로 올리면(history 에서 archive 로 옮긴 기록) 위치만 바뀝니다.
- 같은 이름에 다른 내용이면 서버가 409(immutable)로 거절합니다. 기록 문서는 고칠 수 없습니다.

옵션:
  --project, -p <KEY>     프로젝트 키 (필수)
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc put", g)
		var project string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		path, err := one(pos, "<파일>")
		if err != nil {
			return err
		}
		if project == "" {
			return usageErrorf("--project 가 필요합니다.")
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return usageErrorf("경로를 해석하지 못했습니다: %v", err)
		}
		folder := filepath.Base(filepath.Dir(abs))
		location, ok := model.ParseDocumentLocation(folder)
		if !ok {
			return usageErrorf("기록 문서 파일은 history 나 archive 폴더에 있어야 합니다. %s 의 폴더 이름은 %q 입니다.", path, folder)
		}
		content, err := readDocContent(path)
		if err != nil {
			return usageErrorf("파일을 읽지 못했습니다: %v", err)
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.PutDocument(ctx, project, filepath.Base(abs), string(location), content)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderDocument)
	},
}

// docSyncResult sorts the files of a sync by what happened to them.
type docSyncResult struct {
	Uploaded  []string      `json:"uploaded"`
	Moved     []string      `json:"moved"`
	Unchanged []string      `json:"unchanged"`
	Conflicts []docConflict `json:"conflicts"`
	Skipped   []string      `json:"skipped"`
	Rejected  []docRejected `json:"rejected"`
}

// docConflict is a file whose content differs from the document of the same
// name on the server. It is not uploaded.
type docConflict struct {
	Name         string `json:"name"`
	LocalSHA256  string `json:"local_sha256"`
	ServerSHA256 string `json:"server_sha256"`
}

// docRejected is a file the server, or the CLI for a file that is not UTF-8,
// refused with a reason.
type docRejected struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// docFile is a record document file of the management repository.
type docFile struct {
	name     string
	location model.DocumentLocation
	path     string
}

// docEntry is what the server holds of a document that a sync compares.
type docEntry struct {
	location string
	sha256   string
}

// listDocFiles lists the *.md files that lie directly in the history and
// archive folders of root, history first, each folder by name. Files named
// _* are templates and subfolders are not record documents; both are left out.
// Files whose names are not those of record documents come back as skipped.
func listDocFiles(root string) (files []docFile, skipped []string, err error) {
	found := false
	for _, location := range model.DocumentLocations {
		dir := filepath.Join(root, string(location))
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		found = true
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, "_") {
				continue
			}
			if _, ok := model.ParseDocumentName(name); !ok {
				skipped = append(skipped, name)
				continue
			}
			files = append(files, docFile{name: name, location: location, path: filepath.Join(dir, name)})
		}
	}
	if !found {
		return nil, nil, usageErrorf("%s 에 history/ 나 archive/ 폴더가 없습니다. --root 로 관리 저장소 경로를 주세요.", root)
	}
	return files, skipped, nil
}

// serverDocs lists every document of the project, page by page.
func serverDocs(ctx context.Context, c *client.Client, project string) (map[string]docEntry, error) {
	docs := map[string]docEntry{}
	for offset := 0; ; {
		resp, err := c.ListDocuments(ctx, project, client.Page{Limit: 500, Offset: offset})
		if err != nil {
			return nil, err
		}
		l, err := decode[listOf[model.Document]](resp)
		if err != nil {
			return nil, err
		}
		for _, d := range l.Items {
			docs[d.Name] = docEntry{location: string(d.Location), sha256: d.SHA256}
		}
		offset += len(l.Items)
		if len(l.Items) == 0 || offset >= l.Total {
			return docs, nil
		}
	}
}

// apiReason summarizes a refusal of the server in one line.
func apiReason(e *client.Error) string {
	var parts []string
	for _, d := range e.Details {
		if d.Field != "" {
			parts = append(parts, d.Field+": "+d.Message)
		} else if d.Message != "" {
			parts = append(parts, d.Message)
		}
	}
	if len(parts) == 0 {
		return e.Message
	}
	return strings.Join(parts, "; ")
}

// syncDocFiles brings the files to the server and sorts them into res. A
// refusal that is about one file puts that file aside; any other error ends
// the sync and is returned.
func syncDocFiles(ctx context.Context, c *client.Client, project string, files []docFile, server map[string]docEntry, res *docSyncResult) error {
	for _, f := range files {
		content, err := readDocContent(f.path)
		if errors.Is(err, errNotUTF8) {
			res.Rejected = append(res.Rejected, docRejected{Name: f.name, Reason: err.Error()})
			continue
		}
		if err != nil {
			return err
		}
		sha := sha256Hex(content)
		cur, onServer := server[f.name]
		switch {
		case onServer && cur.sha256 != sha:
			res.Conflicts = append(res.Conflicts, docConflict{Name: f.name, LocalSHA256: sha, ServerSHA256: cur.sha256})
			continue
		case onServer && cur.location == string(f.location):
			res.Unchanged = append(res.Unchanged, f.name)
			continue
		}
		if _, err := c.PutDocument(ctx, project, f.name, string(f.location), content); err != nil {
			var apiErr *client.Error
			if !errors.As(err, &apiErr) {
				return err
			}
			switch {
			case apiErr.Status == http.StatusConflict && hasReason(apiErr.Details, "immutable"):
				res.Conflicts = append(res.Conflicts, docConflict{Name: f.name, LocalSHA256: sha, ServerSHA256: immutableSHA(apiErr.Details)})
				continue
			case apiErr.Status == http.StatusUnprocessableEntity || apiErr.Status == http.StatusRequestEntityTooLarge:
				res.Rejected = append(res.Rejected, docRejected{Name: f.name, Reason: apiReason(apiErr)})
				continue
			}
			return err
		}
		server[f.name] = docEntry{location: string(f.location), sha256: sha}
		if onServer {
			res.Moved = append(res.Moved, f.name)
		} else {
			res.Uploaded = append(res.Uploaded, f.name)
		}
	}
	return nil
}

// immutableSHA is the hash of the stored content that a refusal reports.
func immutableSHA(details []client.Detail) string {
	for _, d := range details {
		if d.SHA256 != "" {
			return d.SHA256
		}
	}
	return ""
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// printDocSync writes the result of a sync: JSON in a pipe, otherwise the
// counts and the names of each category.
func (a *App) printDocSync(g *globals, r *docSyncResult) {
	if a.jsonOutput(g) {
		enc := json.NewEncoder(a.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(r)
		return
	}
	summary := fmt.Sprintf("uploaded %d, moved %d, unchanged %d, conflicts %d", len(r.Uploaded), len(r.Moved), len(r.Unchanged), len(r.Conflicts))
	if len(r.Skipped) > 0 {
		summary += fmt.Sprintf(", skipped %d", len(r.Skipped))
	}
	if len(r.Rejected) > 0 {
		summary += fmt.Sprintf(", rejected %d", len(r.Rejected))
	}
	fmt.Fprintln(a.Stdout, summary)
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(a.Stdout, "\n== %s (%d) ==\n", title, len(lines))
		for _, line := range lines {
			fmt.Fprintln(a.Stdout, line)
		}
	}
	section("uploaded", r.Uploaded)
	section("moved", r.Moved)
	section("unchanged", r.Unchanged)
	var conflicts, rejected []string
	for _, c := range r.Conflicts {
		conflicts = append(conflicts, fmt.Sprintf("%s  local %s  server %s", c.Name, shortSHA(c.LocalSHA256), shortSHA(c.ServerSHA256)))
	}
	for _, x := range r.Rejected {
		rejected = append(rejected, x.Name+"  "+x.Reason)
	}
	section("conflicts", conflicts)
	section("skipped", r.Skipped)
	section("rejected", rejected)
}

var docSyncCmd = &command{
	name: "sync", summary: "관리 저장소의 기록 문서를 서버와 맞춤",
	usage: `사용법: agb doc sync --project <KEY> [--root <관리 저장소 경로>]

관리 저장소의 history/ 와 archive/ 에 직접 놓인 *.md 파일을 서버와 맞춥니다. 이름이 _ 로 시작하는 파일(서식)과
하위 폴더는 보지 않습니다. 이름이 기록 문서 형식(yyyy-mm-dd-HHMM-<종류>-<slug>.md)이 아닌 파일은 올리지
않고 skipped 로 알립니다.

- 서버에 없는 파일은 올립니다(uploaded).
- 서버에 같은 내용이 있고 위치가 다르면 위치를 옮깁니다(moved).
- 내용도 위치도 같으면 그대로 둡니다(unchanged).
- 서버의 같은 이름 문서와 내용(sha256)이 다르면 올리지 않고 conflicts 로 알립니다. 덮어쓰지 않습니다.
- 서버가 규칙 위반으로 거절한 파일과 UTF-8 이 아닌 파일은 rejected 로 알립니다.

결과를 파이프에는 JSON(uploaded, moved, unchanged, conflicts, skipped, rejected)으로, 터미널에는 개수와
이름으로 출력합니다. conflicts 나 rejected 가 있으면 출력한 뒤 종료 코드 1로 끝납니다.

옵션:
  --project, -p <KEY>     프로젝트 키 (필수)
  --root <경로>           관리 저장소 경로. 기본 현재 폴더
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc sync", g)
		var project, root string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&root, "root", ".", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		if project == "" {
			return usageErrorf("--project 가 필요합니다.")
		}
		files, skipped, err := listDocFiles(root)
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		server, err := serverDocs(ctx, c, project)
		if err != nil {
			return err
		}
		res := &docSyncResult{Uploaded: []string{}, Moved: []string{}, Unchanged: []string{}, Conflicts: []docConflict{}, Skipped: []string{}, Rejected: []docRejected{}}
		res.Skipped = append(res.Skipped, skipped...)
		syncErr := syncDocFiles(ctx, c, project, files, server, res)
		a.printDocSync(g, res)
		if syncErr != nil {
			return syncErr
		}
		if len(res.Conflicts)+len(res.Rejected) > 0 {
			return fmt.Errorf("충돌 %d건, 거절 %d건: 서버의 기록 문서는 고칠 수 없어 올리지 않았습니다. 출력의 conflicts, rejected 를 확인하세요.", len(res.Conflicts), len(res.Rejected))
		}
		return nil
	},
}

func (a *App) renderDocumentSearch(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.Document]](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "검색 결과가 없습니다.")
		return nil
	}
	tb := &table{header: []string{"PROJECT", "NAME", "KIND", "DATE", "TITLE", "SNIPPET"}}
	for _, d := range l.Items {
		// A snippet cuts the original, so it carries its line breaks.
		snippet := strings.Join(strings.Fields(deref(d.Snippet, "")), " ")
		tb.add(d.Project, d.Name, string(d.Kind), deref(d.Date, "-"), deref(d.Title, "-"), snippet)
	}
	tb.write(w, a.TermWidth)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}

var docSearchCmd = &command{
	name: "search", summary: "기록 문서 전문 검색 (관련도 순)",
	usage: `사용법: agb doc search <검색어...> [옵션]

기록 문서의 이름, 제목, 태그, 원문을 검색합니다. 프로젝트를 주지 않으면 모든 프로젝트를 찾습니다.
여러 단어는 모두 포함(AND)이고, 전체를 큰따옴표로 감싸면 구(phrase) 하나로 찾습니다. 관련도 순이고
결과에 snippet 이 붙습니다. 원문은 agb doc get <KEY> <파일명> --content 로 읽습니다.

옵션:
  --project, -p <P>       프로젝트 키. 쉼표로 여러 개(OR). 없으면 모든 프로젝트
  --kind <K>              decision, incident, report, implementation. 쉼표 OR
  --limit <N>             최대 200. 기본 50
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc search", g)
		var f client.DocumentFilter
		fs.StringVar(&f.Project, "project", "", "")
		fs.StringVar(&f.Project, "p", "", "")
		fs.StringVar(&f.Kind, "kind", "", "")
		fs.IntVar(&f.Limit, "limit", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return usageErrorf("검색어가 필요합니다.")
		}
		f.Query = strings.Join(pos, " ")
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.SearchDocuments(ctx, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderDocumentSearch)
	},
}

var docGetCmd = &command{
	name: "get", summary: "기록 문서 단건 (--content 는 원문만)",
	usage: `사용법: agb doc get <KEY> <파일명> [--content]

기록 문서 하나의 속성을 보입니다. 터미널이 아니면 원문을 포함한 JSON입니다.

옵션:
  --content               원문만 그대로 출력합니다. --json, --table 과 무관합니다
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc get", g)
		var content bool
		fs.BoolVar(&content, "content", false, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		switch {
		case len(pos) < 2:
			return usageErrorf("<KEY>와 <파일명> 인자가 필요합니다.")
		case len(pos) > 2:
			return usageErrorf("인자가 너무 많습니다: %s", strings.Join(pos[2:], " "))
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.GetDocument(ctx, pos[0], pos[1])
		if err != nil {
			return err
		}
		if !content {
			return a.emit(g, resp, a.renderDocument)
		}
		d, err := decode[model.Document](resp)
		if err != nil {
			return err
		}
		if d.Content == nil {
			return errors.New("응답에 원문이 없습니다.")
		}
		_, err = io.WriteString(a.Stdout, *d.Content)
		return err
	},
}
