package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// Config is the per-machine configuration file. Empty fields are omitted
// from the file.
type Config struct {
	URL        string `json:"url,omitempty"`
	Token      string `json:"token,omitempty"`
	Actor      string `json:"actor,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	ClientHost string `json:"client_host,omitempty"`
}

// configKeys are the keys "agb config set" accepts. The token is set only
// through "agb config set-token".
var configKeys = []string{"url", "actor", "session_id", "client_host"}

// field returns a pointer to the field named by a config key.
func (c *Config) field(key string) (*string, bool) {
	switch key {
	case "url":
		return &c.URL, true
	case "actor":
		return &c.Actor, true
	case "session_id":
		return &c.SessionID, true
	case "client_host":
		return &c.ClientHost, true
	}
	return nil, false
}

// defaultConfigPath is <user config dir>/agb/config.json.
func defaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agb", "config.json"), nil
}

// loadConfig reads the file. A missing file is an empty configuration.
func loadConfig(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("설정 파일을 읽지 못했습니다 (%s): %w", path, err)
	}
	return cfg, nil
}

// saveConfig writes the file readable by the owner only. On Windows the
// file inherits the permissions of the user's profile directory.
func saveConfig(path string, cfg Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// Settings is what a command uses to reach the server.
type Settings struct {
	URL        string
	Token      string
	Actor      string
	SessionID  string
	ClientHost string
}

// resolveSettings applies the precedence flag > environment > file and
// fills the defaults for the session ID and the client host. Values that
// would be rejected by the server are refused here with a usage error.
func resolveSettings(g *globals, getenv func(string) string, file Config, now time.Time, hostname string) (Settings, error) {
	pick := func(flag, envName, fileValue string) string {
		if flag != "" {
			return flag
		}
		if v := getenv(envName); v != "" {
			return v
		}
		return fileValue
	}
	s := Settings{
		URL:        strings.TrimRight(pick(g.url, "AGB_URL", file.URL), "/"),
		Token:      pick("", "AGB_TOKEN", file.Token),
		Actor:      pick(g.actor, "AGB_ACTOR", file.Actor),
		SessionID:  pick(g.sessionID, "AGB_SESSION_ID", file.SessionID),
		ClientHost: pick(g.clientHost, "AGB_CLIENT_HOST", file.ClientHost),
	}
	if s.SessionID == "" {
		s.SessionID = defaultSessionID(now)
	}
	if s.ClientHost == "" {
		s.ClientHost = sanitizeHost(hostname)
	}
	if s.URL == "" {
		return s, usageErrorf("서버 주소가 없습니다. --url, AGB_URL, 또는 `agb config set url <주소>`로 설정하세요.")
	}
	if s.Actor != "" && !model.ValidActorID(s.Actor) {
		return s, usageErrorf("행위자 ID 형식이 아닙니다 (소문자와 숫자 1~32자).")
	}
	if !model.ValidSessionID(s.SessionID) {
		return s, usageErrorf("세션 ID 형식이 아닙니다 (영숫자로 시작하는 영숫자, '.', '_', '-' 1~128자).")
	}
	if !model.ValidClientHost(s.ClientHost) {
		return s, usageErrorf("클라이언트 호스트 형식이 아닙니다 (점으로 나뉜 영숫자·하이픈 라벨, 64자 이내).")
	}
	return s, nil
}

// requireAuth checks that the settings can make an authenticated request.
func (s Settings) requireAuth() error {
	if s.Token == "" {
		return usageErrorf("토큰이 없습니다. AGB_TOKEN 환경변수를 두거나 `agb config set-token`으로 저장하세요.")
	}
	if s.Actor == "" {
		return usageErrorf("행위자가 없습니다. --actor, AGB_ACTOR, 또는 `agb config set actor <ID>`로 설정하세요.")
	}
	return nil
}

// defaultSessionID is cli-YYYYMMDD in the local date of now.
func defaultSessionID(now time.Time) string {
	return "cli-" + now.Format("20060102")
}

// sanitizeHost turns a machine name into a valid X-Client-Host value:
// dot separated labels of letters, digits and hyphens, 64 bytes at most.
func sanitizeHost(h string) string {
	var labels []string
	for _, raw := range strings.Split(h, ".") {
		var b strings.Builder
		for _, r := range raw {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
				b.WriteRune(r)
			default:
				b.WriteByte('-')
			}
		}
		label := strings.Trim(b.String(), "-")
		if len(label) > 63 {
			label = strings.TrimRight(label[:63], "-")
		}
		if label != "" {
			labels = append(labels, label)
		}
	}
	out := strings.Join(labels, ".")
	if len(out) > 64 {
		out = out[:64]
		if i := strings.LastIndex(out, "."); i > 0 && !model.ValidClientHost(out) {
			out = out[:i]
		}
		out = strings.TrimRight(out, ".-")
	}
	if out == "" {
		return "unknown-host"
	}
	return out
}
