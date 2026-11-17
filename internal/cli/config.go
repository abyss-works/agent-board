package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	configPkg "github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/model"
)

var configFileKeys = map[string]bool{
	"current_context": true, "contexts": true, "url": true, "token": true, "tokens": true, "actor": true,
	"hook_actors": true, "session_id": true, "client_host": true,
}

// localAuthTokenFor returns the per-actor secret for agent actors and the
// shared admin token for user actors (and unknown actor names).
func localAuthTokenFor(actor string, auth *configPkg.LocalAuth) string {
	if t := auth.Tokens[actor]; t != "" {
		return t
	}
	return auth.Token
}

const (
	defaultContextName = "default"
	localContextName   = "local"
	defaultLocalURL    = "http://127.0.0.1:8420"
)

// ContextConfig holds the connection settings of one named server target.
// Token is the shared token, which only a user actor may use. Tokens holds the
// actor tokens that "agb login" received, by actor ID.
type ContextConfig struct {
	URL    string            `json:"url,omitempty"`
	Token  string            `json:"token,omitempty"`
	Tokens map[string]string `json:"tokens,omitempty"`
	Actor  string            `json:"actor,omitempty"`
}

func (cc ContextConfig) isEmpty() bool {
	return cc.URL == "" && cc.Token == "" && len(cc.Tokens) == 0 && cc.Actor == ""
}

func (cc ContextConfig) clone() ContextConfig {
	out := cc
	if len(cc.Tokens) > 0 {
		out.Tokens = make(map[string]string, len(cc.Tokens))
		for k, v := range cc.Tokens {
			out.Tokens[k] = v
		}
	} else {
		out.Tokens = nil
	}
	return out
}

// Config is the per-machine configuration file. Empty fields are omitted from
// the file. CurrentContext and Contexts hold the named connection targets.
// Legacy top-level URL, Token, Tokens and Actor fields are kept on the struct
// for in-memory compatibility and migrated into Contexts["default"] when read
// or written. HookActors holds the actor that "agb hook" acts as, by the name
// of the agent tool that "agb setup" connected. Extra holds the keys of the
// file that this struct does not know, so a rewrite keeps them unchanged.
type Config struct {
	CurrentContext string                   `json:"current_context,omitempty"`
	Contexts       map[string]ContextConfig `json:"contexts,omitempty"`

	URL    string            `json:"-"`
	Token  string            `json:"-"`
	Tokens map[string]string `json:"-"`
	Actor  string            `json:"-"`

	HookActors map[string]string `json:"hook_actors,omitempty"`
	SessionID  string            `json:"session_id,omitempty"`
	ClientHost string            `json:"client_host,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// diskConfig is the JSON shape on disk, accepting both the current contexts
// layout and the legacy top-level target fields.
type diskConfig struct {
	CurrentContext string                   `json:"current_context,omitempty"`
	Contexts       map[string]ContextConfig `json:"contexts,omitempty"`
	URL            string                   `json:"url,omitempty"`
	Token          string                   `json:"token,omitempty"`
	Tokens         map[string]string        `json:"tokens,omitempty"`
	Actor          string                   `json:"actor,omitempty"`
	HookActors     map[string]string        `json:"hook_actors,omitempty"`
	SessionID      string                   `json:"session_id,omitempty"`
	ClientHost     string                   `json:"client_host,omitempty"`
}

// mergeLegacy folds the legacy top-level target fields into Contexts["default"].
// Existing fields of that context are kept; a field the context already has
// with a different non-empty value is a conflict and is reported as an error
// so the caller can leave the file untouched. Actor tokens merge by actor.
func (c *Config) mergeLegacy() (migrated bool, err error) {
	hasLegacy := c.URL != "" || c.Token != "" || len(c.Tokens) > 0 || c.Actor != ""
	if !hasLegacy {
		return false, nil
	}
	cc := c.Contexts[defaultContextName].clone()
	mergeField := func(legacy, existing, name string) error {
		if legacy == "" {
			return nil
		}
		if existing != "" && existing != legacy {
			return legacyFieldConflict(name, name)
		}
		return nil
	}
	if err := mergeField(c.URL, cc.URL, "url"); err != nil {
		return false, err
	}
	if err := mergeField(c.Token, cc.Token, "token"); err != nil {
		return false, err
	}
	if err := mergeField(c.Actor, cc.Actor, "actor"); err != nil {
		return false, err
	}
	for actor, tok := range c.Tokens {
		if existing, ok := cc.Tokens[actor]; ok && existing != "" && existing != tok && tok != "" {
			p := "tokens." + actor
			return false, legacyFieldConflict(p, p)
		}
	}
	if cc.URL == "" {
		cc.URL = c.URL
	}
	if cc.Actor == "" {
		cc.Actor = c.Actor
	}
	if cc.Token == "" {
		cc.Token = c.Token
	}
	if len(c.Tokens) > 0 {
		if cc.Tokens == nil {
			cc.Tokens = make(map[string]string, len(c.Tokens))
		}
		for actor, tok := range c.Tokens {
			if tok != "" {
				cc.Tokens[actor] = tok
			}
		}
	}
	if c.Contexts == nil {
		c.Contexts = map[string]ContextConfig{}
	}
	c.Contexts[defaultContextName] = cc
	if c.CurrentContext == "" {
		c.CurrentContext = defaultContextName
	}
	return true, nil
}

// legacyFieldConflict reports the error for a field that the legacy top-level
// shape and the default context both set to different values. topPath and
// ctxPath name the same field in each shape.
func legacyFieldConflict(topPath, ctxPath string) error {
	return fmt.Errorf("설정 파일의 최상위 %s 값과 contexts.%s 의 %s 값이 서로 다릅니다. 어느 값을 쓸지 자동으로 정하지 않습니다. 설정 파일에서 한쪽을 지운 뒤 다시 실행하세요", topPath, defaultContextName, ctxPath)
}

// normalize folds any legacy top-level target fields into Contexts["default"].
// A conflict between the legacy values and an existing default context leaves
// the values as they are; loadConfig reports it as an error before that.
func (c *Config) normalize() (migrated bool) {
	migrated, _ = c.mergeLegacy()
	c.URL = ""
	c.Token = ""
	c.Tokens = nil
	c.Actor = ""
	for name, cc := range c.Contexts {
		if len(cc.Tokens) == 0 && cc.Tokens != nil {
			cc.Tokens = nil
			c.Contexts[name] = cc
		}
	}
	return migrated
}

// configKeys are the keys "agb config set" accepts. The token is set only
// through "agb config set-token".
var configKeys = []string{"url", "actor", "session_id", "client_host"}

// validContextName reports whether name is a valid context identifier:
// non-empty and free of whitespace or control characters.
func validContextName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// resolveContext chooses the active context name and its ContextConfig.
// Precedence: --context flag > AGB_CONTEXT env > file.CurrentContext > "default" (if present).
func resolveContext(g *globals, getenv func(string) string, file Config) (string, ContextConfig, error) {
	file.normalize()
	explicit := ""
	if g != nil && g.context != "" {
		explicit = g.context
	} else if getenv != nil {
		if v := getenv("AGB_CONTEXT"); v != "" {
			explicit = v
		}
	}
	if explicit != "" {
		if !validContextName(explicit) {
			return "", ContextConfig{}, usageErrorf("접속 대상 이름 %q 형식이 올바르지 않습니다 (공백과 제어문자를 쓸 수 없습니다).", explicit)
		}
		if cc, ok := file.Contexts[explicit]; ok {
			if explicit == localContextName && cc.URL == "" {
				cc.URL = defaultLocalURL
			}
			return explicit, cc, nil
		}
		if explicit == localContextName {
			return localContextName, ContextConfig{URL: defaultLocalURL}, nil
		}
		return "", ContextConfig{}, usageErrorf("접속 대상 %q 이 등록되어 있지 않습니다. `agb config list-contexts`로 목록을 확인하거나 `agb config set-context %s --url <주소>`로 등록하세요.", explicit, explicit)
	}

	name := file.CurrentContext
	if name == "" {
		if _, ok := file.Contexts[defaultContextName]; ok {
			name = defaultContextName
		}
	}
	if name == "" {
		return "", ContextConfig{}, nil
	}
	if cc, ok := file.Contexts[name]; ok {
		if name == localContextName && cc.URL == "" {
			cc.URL = defaultLocalURL
		}
		return name, cc, nil
	}
	if name == localContextName {
		return localContextName, ContextConfig{URL: defaultLocalURL}, nil
	}
	return name, ContextConfig{}, nil
}

// writableContextName returns the context name that config write commands
// (set, unset, set-token, login, logout) mutate.
func writableContextName(g *globals, getenv func(string) string, c *Config) (string, error) {
	c.normalize()
	explicit := ""
	if g != nil && g.context != "" {
		explicit = g.context
	} else if getenv != nil {
		if v := getenv("AGB_CONTEXT"); v != "" {
			explicit = v
		}
	}
	if explicit != "" {
		if !validContextName(explicit) {
			return "", usageErrorf("접속 대상 이름 %q 형식이 올바르지 않습니다 (공백과 제어문자를 쓸 수 없습니다).", explicit)
		}
		if _, ok := c.Contexts[explicit]; !ok {
			if explicit == localContextName {
				if c.Contexts == nil {
					c.Contexts = map[string]ContextConfig{}
				}
				c.Contexts[localContextName] = ContextConfig{URL: defaultLocalURL}
				return localContextName, nil
			}
			return "", usageErrorf("접속 대상 %q 이 등록되어 있지 않습니다. `agb config set-context %s --url <주소>`로 먼저 등록하세요.", explicit, explicit)
		}
		return explicit, nil
	}
	if c.CurrentContext != "" {
		return c.CurrentContext, nil
	}
	c.CurrentContext = defaultContextName
	return defaultContextName, nil
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
// If the file uses the legacy top-level target schema (no "contexts" key and at
// least one non-empty target field), loadConfig backs up the raw bytes to
// config.json.bak (preserving any existing .bak) and atomically writes the
// migrated schema back to path.
func loadConfig(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	var rawKeys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawKeys); err != nil {
		return Config{}, fmt.Errorf("설정 파일을 읽지 못했습니다 (%s): %w", path, err)
	}
	var disk diskConfig
	if err := json.Unmarshal(raw, &disk); err != nil {
		return Config{}, fmt.Errorf("설정 파일을 읽지 못했습니다 (%s): %w", path, err)
	}
	cfg = Config{
		CurrentContext: disk.CurrentContext,
		Contexts:       disk.Contexts,
		URL:            disk.URL,
		Token:          disk.Token,
		Tokens:         disk.Tokens,
		Actor:          disk.Actor,
		HookActors:     disk.HookActors,
		SessionID:      disk.SessionID,
		ClientHost:     disk.ClientHost,
	}
	for key, value := range rawKeys {
		if !configFileKeys[key] {
			if cfg.Extra == nil {
				cfg.Extra = map[string]json.RawMessage{}
			}
			cfg.Extra[key] = append(json.RawMessage(nil), value...)
		}
	}
	hasLegacy := disk.URL != "" || disk.Token != "" || len(disk.Tokens) > 0 || disk.Actor != ""
	if _, err := cfg.mergeLegacy(); err != nil {
		return Config{}, fmt.Errorf("설정 파일을 읽지 못했습니다 (%s): %w", path, err)
	}
	cfg.URL = ""
	cfg.Token = ""
	cfg.Tokens = nil
	cfg.Actor = ""
	if hasLegacy {
		bakPath := path + ".bak"
		if _, statErr := os.Stat(bakPath); errors.Is(statErr, os.ErrNotExist) {
			if err := writeFileAtomic(bakPath, raw, 0o600); err != nil {
				return Config{}, fmt.Errorf("설정 백업 파일을 쓰지 못했습니다 (%s): %w", bakPath, err)
			}
		}
		if err := saveConfig(path, cfg); err != nil {
			return Config{}, fmt.Errorf("이전된 설정 파일을 쓰지 못했습니다 (%s): %w", path, err)
		}
	}
	for name, cc := range cfg.Contexts {
		if len(cc.Tokens) == 0 && cc.Tokens != nil {
			cc.Tokens = nil
			cfg.Contexts[name] = cc
		}
	}
	return cfg, nil
}

// writeFileAtomic writes data to path via a temporary file in the same
// directory with mode perm.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Chmod(path, perm)
}

// saveConfig writes the file readable by the owner only. On Windows the
// file inherits the permissions of the user's profile directory.
func saveConfig(path string, cfg Config) error {
	if _, err := cfg.mergeLegacy(); err != nil {
		return fmt.Errorf("설정 파일을 쓰지 못했습니다 (%s): %w", path, err)
	}
	cfg.normalize()
	out := diskConfig{
		CurrentContext: cfg.CurrentContext,
		HookActors:     cfg.HookActors,
		SessionID:      cfg.SessionID,
		ClientHost:     cfg.ClientHost,
	}
	if len(cfg.Contexts) > 0 {
		out.Contexts = make(map[string]ContextConfig, len(cfg.Contexts))
		for name, cc := range cfg.Contexts {
			cleaned := cc.clone()
			if len(cleaned.Tokens) == 0 {
				cleaned.Tokens = nil
			}
			if cleaned.isEmpty() && name != cfg.CurrentContext {
				continue
			}
			out.Contexts[name] = cleaned
		}
		if len(out.Contexts) == 0 {
			out.Contexts = nil
		}
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	keys := make([]string, 0, len(object)+len(cfg.Extra))
	for key := range object {
		keys = append(keys, key)
	}
	for key := range cfg.Extra {
		if _, exists := object[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var merged, pretty bytes.Buffer
	merged.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			merged.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		merged.Write(name)
		merged.WriteByte(':')
		if value, ok := object[key]; ok {
			merged.Write(value)
		} else {
			merged.Write(cfg.Extra[key])
		}
	}
	merged.WriteByte('}')
	if err := json.Indent(&pretty, merged.Bytes(), "", "  "); err != nil {
		return err
	}
	pretty.WriteByte('\n')
	return writeFileAtomic(path, pretty.Bytes(), 0o600)
}

// Settings is what a command uses to reach the server.
type Settings struct {
	Context    string
	URL        string
	Token      string
	Actor      string
	SessionID  string
	ClientHost string
}

// resolveSettings selects the context (--context > AGB_CONTEXT > current_context),
// applies flag > environment > context precedence, and fills the defaults for
// the session ID and the client host.
func resolveSettings(g *globals, getenv func(string) string, file Config, now time.Time, hostname string) (Settings, error) {
	ctxName, cc, err := resolveContext(g, getenv, file)
	if err != nil {
		return Settings{}, err
	}
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
		Context:    ctxName,
		URL:        strings.TrimRight(pick(g.url, "AGB_URL", cc.URL), "/"),
		Actor:      actorOfContext(g, getenv, cc),
		SessionID:  pick(g.sessionID, "AGB_SESSION_ID", file.SessionID),
		ClientHost: pick(g.clientHost, "AGB_CLIENT_HOST", file.ClientHost),
	}
	s.Token = tokenOfContext(s.Actor, getenv, cc, ctxName == localContextName)
	if ctxName == localContextName && s.Actor == "" {
		s.Actor = "owner"
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

func actorOfContext(g *globals, getenv func(string) string, cc ContextConfig) string {
	if g != nil && g.actor != "" {
		return g.actor
	}
	if getenv != nil {
		if v := getenv("AGB_ACTOR"); v != "" {
			return v
		}
	}
	return cc.Actor
}

// actorOf is the actor a run acts as: the flag, else AGB_ACTOR, else the
// selected context's actor in the file.
func actorOf(g *globals, getenv func(string) string, file Config) string {
	_, cc, err := resolveContext(g, getenv, file)
	if err != nil {
		return actorOfContext(g, getenv, ContextConfig{})
	}
	return actorOfContext(g, getenv, cc)
}

func tokenOfContext(actor string, getenv func(string) string, cc ContextConfig, isLocal bool) string {
	if t := cc.Tokens[actor]; t != "" {
		return t
	}
	if isLocal {
		if dir, err := configPkg.DefaultLocalDataDir(getenv); err == nil {
			if auth, err := configPkg.LoadLocalAuth(configPkg.LocalAuthPath(dir)); err == nil {
				return localAuthTokenFor(actor, auth)
			}
		}
		if cc.Token != "" {
			return cc.Token
		}
	} else if getenv != nil {
		if t := getenv("AGB_TOKEN"); t != "" {
			return t
		}
		if cc.Token != "" {
			return cc.Token
		}
		return ""
	}
	if getenv != nil {
		if t := getenv("AGB_TOKEN"); t != "" {
			return t
		}
	}
	return cc.Token
}

// tokenOf is the token a request of actor carries in the selected context:
// the actor's token in the context, else AGB_TOKEN for a non-local target,
// else the shared token in the context. For local, the seed token comes before
// the shared token and AGB_TOKEN stays last.
func tokenOf(actor string, getenv func(string) string, file Config) string {
	return tokenOfWithGlobals(nil, actor, getenv, file)
}

func tokenOfWithGlobals(g *globals, actor string, getenv func(string) string, file Config) string {
	name, cc, err := resolveContext(g, getenv, file)
	if err != nil {
		return ""
	}
	return tokenOfContext(actor, getenv, cc, name == localContextName)
}

// requireAuth checks that the settings can make an authenticated request.
func (s Settings) requireAuth() error {
	if s.Token == "" {
		return usageErrorf("토큰이 없습니다. `agb login --actor %s`로 이 행위자의 토큰을 받으세요. (AGB_TOKEN 환경변수와 `agb config set-token`의 공용 토큰은 사용자 행위자 전용입니다.)", orPlaceholder(s.Actor))
	}
	return s.requireActor()
}

// requireActor checks that the settings name an actor.
func (s Settings) requireActor() error {
	if s.Actor == "" {
		return usageErrorf("행위자가 없습니다. --actor, AGB_ACTOR, 또는 `agb config set actor <ID>`로 설정하세요.")
	}
	return nil
}

// orPlaceholder is the actor to write into a command shown to the user, or
// <ID> when the actor is not known.
func orPlaceholder(actor string) string {
	if actor == "" {
		return "<ID>"
	}
	return actor
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
