package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"

	"github.com/wsk0715/agent-board/internal/model"
)

// LocalOptions are the command-line overrides for the local server.
type LocalOptions struct {
	Addr    string // --addr, "" uses AGB_ADDR or the default
	DataDir string // --data-dir, "" uses AGB_LOCAL_DATA_DIR or the OS default
}

// SeedTokens are the per-actor token secrets the local server registers in its
// database. The CLI reads the same file to pick its token for the "local"
// context.
type SeedTokens map[string]string

// LoadLocal reads the configuration for "agb serve --local". Unlike Load it
// does not require AGB_TOKEN or AGB_ACTORS: credentials and actors are seeded
// from <data dir>/local-auth.json, so a local server is isolated from the
// remote credentials that may exist in the shell.
func LoadLocal(getenv func(string) string, opts LocalOptions) (*Config, error) {
	addr := "127.0.0.1:8420"
	if v := getenv("AGB_ADDR"); v != "" {
		addr = v
	}
	if opts.Addr != "" {
		addr = opts.Addr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("--local 주소 %q 형식이 올바르지 않습니다: %w", addr, err)
	}
	if host != "127.0.0.1" {
		return nil, fmt.Errorf("--local 서버는 127.0.0.1 에만 받을 수 있습니다 (주어진 값: %q)", addr)
	}

	dataDir := getenv("AGB_LOCAL_DATA_DIR")
	if opts.DataDir != "" {
		dataDir = opts.DataDir
	}
	if dataDir == "" {
		if dataDir, err = DefaultLocalDataDir(getenv); err != nil {
			return nil, fmt.Errorf("로컬 데이터 폴더를 정할 수 없습니다: %w", err)
		}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("로컬 데이터 폴더를 만들지 못했습니다 (%s): %w", dataDir, err)
	}
	_ = os.Chmod(dataDir, 0o700)

	auth, err := LoadLocalAuth(LocalAuthPath(dataDir))
	if err != nil {
		return nil, err
	}

	cfg, err := Load(func(k string) string {
		switch k {
		case "AGB_ADDR":
			return addr
		case "AGB_DATA_DIR":
			return dataDir
		case "AGB_TOKEN":
			return auth.Token
		case "AGB_ACTORS":
			return localActorsEnv(auth.Actors)
		default:
			return getenv(k)
		}
	})
	if err != nil {
		return nil, err
	}
	cfg.SeedTokens = map[string]string(auth.Tokens)
	return cfg, nil
}

// DefaultLocalDataDir is the OS-specific folder the local server and the CLI
// share when AGB_LOCAL_DATA_DIR is not set.
func DefaultLocalDataDir(getenv func(string) string) (string, error) {
	if v := getenv("AGB_LOCAL_DATA_DIR"); v != "" {
		return v, nil
	}
	switch runtime.GOOS {
	case "windows":
		local := getenv("LOCALAPPDATA")
		if local == "" {
			return "", errors.New("LOCALAPPDATA 환경변수가 없습니다")
		}
		return filepath.Join(local, "agb"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "agb"), nil
	default:
		xdg := getenv("XDG_DATA_HOME")
		if xdg != "" && filepath.IsAbs(xdg) {
			return filepath.Join(xdg, "agb"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", "agb"), nil
	}
}

// LocalAuthPath is the location of the local server's seed credentials file.
func LocalAuthPath(dataDir string) string { return filepath.Join(dataDir, "local-auth.json") }

// LocalAuth is the content of local-auth.json. Token is the shared admin
// token, Actors the declared actors, and Tokens the per-actor secrets.
type LocalAuth struct {
	Token  string            `json:"token"`
	Actors []model.Actor     `json:"actors"`
	Tokens map[string]string `json:"tokens,omitempty"`
}

var defaultLocalActors = []model.Actor{
	{ID: "owner", Kind: model.ActorUser},
	{ID: "claude", Kind: model.ActorAgent},
	{ID: "codex", Kind: model.ActorAgent},
	{ID: "gemini", Kind: model.ActorAgent},
	{ID: "system", Kind: model.ActorUser},
}

// LoadLocalAuth reads the seed file, or creates it with fresh random secrets
// when it does not exist. A corrupt file is never overwritten: LoadLocalAuth
// returns an error instead.
func LoadLocalAuth(path string) (*LocalAuth, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		auth, err := newLocalAuth()
		if err != nil {
			return nil, err
		}
		if err := writeLocalAuth(path, auth); err != nil {
			return nil, err
		}
		return auth, nil
	}
	if err != nil {
		return nil, err
	}
	var auth LocalAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		return nil, fmt.Errorf("local-auth.json 을 읽지 못했습니다: %w", err)
	}
	if auth.Token == "" {
		return nil, fmt.Errorf("local-auth.json 에 token 값이 없습니다")
	}
	return &auth, nil
}

func newLocalAuth() (*LocalAuth, error) {
	token, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	auth := &LocalAuth{
		Token:  token,
		Actors: defaultLocalActors,
		Tokens: map[string]string{},
	}
	for _, a := range defaultLocalActors {
		if a.Kind == model.ActorUser {
			continue
		}
		secret, err := randomHex(32)
		if err != nil {
			return nil, err
		}
		auth.Tokens[a.ID] = model.TokenPrefix + secret
	}
	return auth, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeLocalAuth(path string, auth *LocalAuth) error {
	raw, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(raw, '\n'), 0o600)
}

// writeFileAtomic writes data to path via a temp file in the same folder.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Chmod(path, perm)
}

func localActorsEnv(actors []model.Actor) string {
	s := ""
	for i, a := range actors {
		if i > 0 {
			s += ","
		}
		s += a.ID + ":" + string(a.Kind)
	}
	return s
}
