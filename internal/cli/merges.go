package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/abyss-works/agent-board/internal/client"
	"github.com/abyss-works/agent-board/internal/model"
)

// locatedRepo is one repository section of a location comment.
type locatedRepo struct {
	top, remote, branch, head string
}

var locatedBranch = regexp.MustCompile(`^- 브랜치: (\S+) \(HEAD (\S+)\)`)

// locatedRepos reads the repository sections of every location part in the
// comment bodies.
func locatedRepos(bodies []string) []locatedRepo {
	var out []locatedRepo
	for _, body := range bodies {
		i := strings.Index(body, locationHeading)
		if i < 0 {
			continue
		}
		var cur *locatedRepo
		for _, l := range strings.Split(body[i:], "\n") {
			l = strings.TrimRight(l, "\r")
			switch {
			case strings.HasPrefix(l, "### "):
				out = append(out, locatedRepo{top: strings.TrimSpace(l[4:])})
				cur = &out[len(out)-1]
			case cur == nil:
			case strings.HasPrefix(l, "- 원격: "):
				cur.remote = strings.TrimSpace(strings.TrimPrefix(l, "- 원격: "))
			default:
				if m := locatedBranch.FindStringSubmatch(l); m != nil {
					cur.branch, cur.head = m[1], m[2]
				}
			}
		}
	}
	return out
}

// remoteURL is the origin URL of a repository without credentials, or "".
func remoteURL(dir string) string {
	raw, err := git(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		u.User = nil
		return u.String()
	}
	return raw
}

// mergeLink is a merge commit of a work branch with the commits it brought.
type mergeLink struct {
	repo, sha, subject string
	commits            []string
}

// findMerge finds the merge commit on the first-parent history of the
// current branch of dir that brought in the work recorded in r. The recorded
// HEAD is used when it lies on a side branch; otherwise the work branch is
// looked up by name in the merge subjects after the recorded HEAD.
func findMerge(dir string, r locatedRepo) (sha string, ok bool) {
	head, err := git(dir, "rev-parse", "--verify", "--quiet", r.head+"^{commit}")
	if err != nil {
		head = ""
	}
	if head != "" {
		if _, err := git(dir, "merge-base", "--is-ancestor", head, "HEAD"); err != nil {
			return "", false
		}
		path, _ := git(dir, "rev-list", "--first-parent", "--ancestry-path", "--reverse", head+"..HEAD")
		if first, _, _ := strings.Cut(path, "\n"); first != "" {
			if parents, _ := git(dir, "rev-list", "--parents", "-n", "1", first); len(strings.Fields(parents)) > 2 {
				return first, true
			}
		}
	}
	current, _ := git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if r.branch == "" || r.branch == current || r.branch == "HEAD" {
		return "", false
	}
	rng := "HEAD"
	if head != "" {
		rng = head + "..HEAD"
	}
	merges, _ := git(dir, "log", "--first-parent", "--merges", "--reverse", "--format=%H %s", rng)
	name := regexp.MustCompile(`(^|[^\w/.-])` + regexp.QuoteMeta(r.branch) + `($|[^\w/.-])`)
	for _, l := range strings.Split(merges, "\n") {
		if h, subject, found := strings.Cut(l, " "); found && name.MatchString(subject) {
			return h, true
		}
	}
	return "", false
}

// mergeLinks finds, for each local repository, the merges of the work the
// location comments recorded for the same repository: the same origin URL,
// or the same path when the comment has no URL.
func mergeLinks(dirs []string, located []locatedRepo) (links []mergeLink, misses []string) {
	seen := map[string]bool{}
	for _, dir := range dirs {
		top, err := git(dir, "rev-parse", "--show-toplevel")
		if err != nil {
			continue
		}
		remote := remoteURL(dir)
		for _, r := range located {
			if (r.remote == "" || r.remote != remote) && !(r.remote == "" && r.top == top) {
				continue
			}
			sha, ok := findMerge(dir, r)
			if !ok {
				misses = append(misses, top+" "+r.branch)
				continue
			}
			if seen[sha] {
				continue
			}
			seen[sha] = true
			subject, _ := git(dir, "log", "-1", "--format=%s", sha)
			list, _ := git(dir, "log", "--format=%h %s", sha+"^1.."+sha+"^2")
			l := mergeLink{repo: top, sha: sha, subject: subject}
			if remote != "" {
				l.repo = remote
			}
			if list != "" {
				l.commits = strings.Split(list, "\n")
			}
			links = append(links, l)
		}
	}
	return links, misses
}

// writeMerges renders the merges part of a done comment.
func writeMerges(w io.Writer, links []mergeLink) {
	fmt.Fprint(w, "## 병합\n")
	for _, l := range links {
		fmt.Fprintf(w, "\n- %s: `%s` %s\n", l.repo, l.sha[:12], l.subject)
		for _, c := range l.commits {
			fmt.Fprintf(w, "  - `%s`\n", c)
		}
	}
}

// linkMerges adds the merges of the issue's work branches as commit refs and
// returns the merges part for the done comment. issue is the response of the
// status change. An issue without a location comment has no work branch to
// link, so it passes silently. Problems are warnings: the comment is still
// added.
func (a *App) linkMerges(ctx context.Context, c *client.Client, key string, issue *client.Response, dirs []string) string {
	warn := func(format string, args ...any) string {
		fmt.Fprintf(a.Stderr, "agb: 경고: "+format+"\n", args...)
		return ""
	}
	acts, err := c.IssueActivity(ctx, key, client.Page{Limit: 200})
	if err != nil {
		return warn("커밋을 잇지 못했습니다: %v", err)
	}
	l, err := decode[struct {
		Items []model.Activity `json:"items"`
	}](acts)
	if err != nil {
		return warn("커밋을 잇지 못했습니다: %v", err)
	}
	var bodies []string
	for _, it := range l.Items {
		if it.Kind == model.ActivityCommented && it.Body != nil {
			bodies = append(bodies, *it.Body)
		}
	}
	located := locatedRepos(bodies)
	if len(located) == 0 {
		return ""
	}
	links, misses := mergeLinks(dirs, located)
	for _, m := range misses {
		warn("병합 커밋을 찾지 못했습니다: %s", m)
	}
	if len(links) == 0 {
		return ""
	}
	cur, err := decode[model.Issue](issue)
	if err != nil {
		return warn("커밋을 잇지 못했습니다: %v", err)
	}
	refs := append([]string{}, cur.Refs...)
	for _, m := range links {
		if ref := "commit:" + m.sha; !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	if len(refs) > len(cur.Refs) {
		if _, err := c.PatchIssue(ctx, key, map[string]any{"refs": refs}, quoteETag(strconv.FormatInt(cur.Version, 10))); err != nil {
			warn("병합 커밋 참조를 붙이지 못했습니다: %v", err)
		}
	}
	var b strings.Builder
	writeMerges(&b, links)
	return b.String()
}
