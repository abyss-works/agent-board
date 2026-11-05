package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

// locationHeading starts the part of a comment that records where the work
// on an issue stands on one host. issue get finds the latest one by it.
const locationHeading = "## 작업 위치"

// maxLocationFiles caps the uncommitted file names listed per repository.
const maxLocationFiles = 50

// defaultLocationRepos are the repositories recorded when --repo is not
// given: the one holding the current folder, and project/ under it when that
// is a repository of its own (the management repository layout).
func defaultLocationRepos() []string {
	repos := []string{"."}
	if _, err := os.Stat(filepath.Join("project", ".git")); err == nil {
		repos = append(repos, "project")
	}
	return repos
}

// workLocation renders the location part of a comment: the host and, per
// repository, the branch, HEAD, the distance to its upstream, the
// uncommitted file names and the extra worktrees. File contents are never
// read.
func workLocation(host string, repos []string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n- 호스트: %s\n", locationHeading, host)
	for _, dir := range repos {
		top, err := git(dir, "rev-parse", "--show-toplevel")
		if err != nil {
			return "", fmt.Errorf("%s는 git 저장소가 아닙니다: %w", dir, err)
		}
		branch, _ := git(dir, "rev-parse", "--abbrev-ref", "HEAD")
		head, _ := git(dir, "rev-parse", "--short", "HEAD")
		fmt.Fprintf(&b, "\n### %s\n\n- 브랜치: %s (HEAD %s)\n", top, branch, head)
		if counts, err := git(dir, "rev-list", "--left-right", "--count", "@{u}...HEAD"); err == nil {
			if f := strings.Fields(counts); len(f) == 2 {
				fmt.Fprintf(&b, "- 원격 추적 브랜치와 차이: 앞 %s, 뒤 %s\n", f[1], f[0])
			}
		} else {
			b.WriteString("- 원격 추적 브랜치: 없음\n")
		}
		status, err := git(dir, "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil {
			return "", err
		}
		var files []string
		if status != "" {
			files = strings.Split(status, "\n")
		}
		fmt.Fprintf(&b, "- 커밋하지 않은 파일: %d개\n", len(files))
		for n, f := range files {
			if n == maxLocationFiles {
				fmt.Fprintf(&b, "  - 그 밖에 %d개\n", len(files)-n)
				break
			}
			fmt.Fprintf(&b, "  - `%s`\n", f)
		}
		list, err := git(dir, "worktree", "list")
		if err != nil {
			return "", err
		}
		if lines := strings.Split(list, "\n"); len(lines) > 1 {
			b.WriteString("- 워크트리:\n")
			for _, l := range lines[1:] {
				fmt.Fprintf(&b, "  - `%s`\n", strings.Join(strings.Fields(l), " "))
			}
		}
	}
	return b.String(), nil
}

// git runs git in dir and returns its trimmed output.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// latestLocation returns the newest location part in activity bodies listed
// newest first, with the index of its record, or -1 when there is none.
func latestLocation(bodies []string) (string, int) {
	for n, body := range bodies {
		if i := strings.Index(body, locationHeading); i >= 0 {
			return body[i:], n
		}
	}
	return "", -1
}

// locationHost reads the host line of a location part.
func locationHost(loc string) string {
	for _, l := range strings.Split(loc, "\n") {
		if h, ok := strings.CutPrefix(l, "- 호스트: "); ok {
			return strings.TrimSpace(h)
		}
	}
	return ""
}

// printLatestLocation prints, under a doing issue, the newest location part
// left in its comments, and says when another host left it.
func (a *App) printLatestLocation(ctx context.Context, c *client.Client, resp *client.Response, key string) error {
	i, err := decode[model.Issue](resp)
	if err != nil || i.Status != model.StatusDoing {
		return err
	}
	acts, err := c.IssueActivity(ctx, key, client.Page{Limit: 50})
	if err != nil {
		return err
	}
	l, err := decode[struct {
		Items []model.Activity `json:"items"`
	}](acts)
	if err != nil {
		return err
	}
	bodies := make([]string, len(l.Items))
	for n, it := range l.Items {
		if it.Kind == model.ActivityCommented && it.Body != nil {
			bodies[n] = *it.Body
		}
	}
	loc, n := latestLocation(bodies)
	if n < 0 {
		return nil
	}
	it := l.Items[n]
	fmt.Fprintf(a.Stdout, "\n== 마지막 작업 위치 (%s, %s) ==\n", formatTime(it.At, a.Location), it.Actor)
	if host := locationHost(loc); host != "" && host != c.ClientHost {
		fmt.Fprintf(a.Stdout, "다른 호스트(%s)에서 남긴 기록입니다. 아래 상태는 그 호스트에만 있습니다.\n", host)
	}
	fmt.Fprintln(a.Stdout, strings.TrimPrefix(loc, locationHeading+"\n"))
	return nil
}
