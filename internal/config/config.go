// Package config reads server settings from AGB_* environment variables.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// DefaultDBMaxConns is the default PostgreSQL connection pool size when
// AGB_DATABASE_URL is configured and AGB_DB_MAX_CONNS is unset.
const DefaultDBMaxConns = 16

// Config is the complete server configuration.
type Config struct {
	Addr        string
	DataDir     string
	DatabaseURL string // PostgreSQL connection URL; empty keeps SQLite mode
	DBMaxConns  int    // PostgreSQL connection pool limit
	Token       string
	Actors      []model.Actor
	WebDir      string // folder with the built web client; empty means it is not served
	Backup      Backup
	// Embedding configures the search by meaning; it is off without an API key.
	Embedding Embedding
	// SeedTokens are per-actor secrets a local server registers at startup,
	// so agent actors can authenticate without a browser login. Empty for a
	// remote server.
	SeedTokens map[string]string
}

// UsePostgres reports whether the server is configured to run on PostgreSQL.
func (c *Config) UsePostgres() bool { return c.DatabaseURL != "" }

// Backup configures the periodic snapshot upload. It is off until a bucket
// is named.
type Backup struct {
	Bucket          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Prefix          string
	Interval        time.Duration
	Retention       time.Duration
}

// Enabled reports whether backups should run.
func (b Backup) Enabled() bool { return b.Bucket != "" }

// Embedding configures the embeddings API behind the search by meaning. It is
// off until an API key is set; then URL and Model are required.
type Embedding struct {
	APIKey      string
	URL         string // full endpoint URL of an OpenAI compatible embeddings API
	Model       string
	QueryPrefix string // prepended to the query text only
	Interval    time.Duration
	// QueryTimeout bounds the retry attempt of the query embedding; the
	// first attempt is shorter and both share a total limit.
	QueryTimeout time.Duration
	// Provider is a JSON object sent as the "provider" field of each
	// embeddings request; nil leaves the field out.
	Provider json.RawMessage
}

// Enabled reports whether the search by meaning should run.
func (e Embedding) Enabled() bool { return e.APIKey != "" }

// MinTokenLength is the shortest AGB_TOKEN the server accepts.
const MinTokenLength = 32

// Load reads the configuration through getenv (usually os.Getenv).
func Load(getenv func(string) string) (*Config, error) {
	cfg := &Config{
		Addr:        getenv("AGB_ADDR"),
		DataDir:     getenv("AGB_DATA_DIR"),
		DatabaseURL: strings.TrimSpace(getenv("AGB_DATABASE_URL")),
		DBMaxConns:  DefaultDBMaxConns,
		Token:       getenv("AGB_TOKEN"),
		WebDir:      getenv("AGB_WEB_DIR"),
		Backup: Backup{
			Bucket:          getenv("AGB_BACKUP_BUCKET"),
			Endpoint:        getenv("AGB_BACKUP_ENDPOINT"),
			AccessKeyID:     getenv("AGB_BACKUP_ACCESS_KEY_ID"),
			SecretAccessKey: getenv("AGB_BACKUP_SECRET_ACCESS_KEY"),
			Prefix:          getenv("AGB_BACKUP_PREFIX"),
			Interval:        time.Hour,
			Retention:       672 * time.Hour,
		},
		Embedding: Embedding{
			APIKey:       getenv("AGB_EMBEDDING_API_KEY"),
			URL:          getenv("AGB_EMBEDDING_URL"),
			Model:        getenv("AGB_EMBEDDING_MODEL"),
			QueryPrefix:  strings.ReplaceAll(getenv("AGB_EMBEDDING_QUERY_PREFIX"), `\n`, "\n"),
			Interval:     time.Minute,
			QueryTimeout: 10 * time.Second,
		},
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8420"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.Backup.Prefix == "" {
		cfg.Backup.Prefix = "agb"
	}

	var errs []error
	if cfg.Token == "" {
		errs = append(errs, errors.New("AGB_TOKEN is required"))
	} else if len(cfg.Token) < MinTokenLength {
		errs = append(errs, fmt.Errorf("AGB_TOKEN must be at least %d characters", MinTokenLength))
	}

	actors, err := parseActors(getenv("AGB_ACTORS"))
	if err != nil {
		errs = append(errs, err)
	}
	cfg.Actors = actors

	if cfg.DatabaseURL != "" {
		if err := validateDatabaseURL(cfg.DatabaseURL); err != nil {
			errs = append(errs, err)
		}
	}
	if v := strings.TrimSpace(getenv("AGB_DB_MAX_CONNS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			errs = append(errs, fmt.Errorf("AGB_DB_MAX_CONNS must be a positive integer, got %q", previewValue(v)))
		} else {
			cfg.DBMaxConns = n
		}
	}

	if cfg.WebDir != "" {
		if info, err := os.Stat(cfg.WebDir); err != nil || !info.IsDir() {
			errs = append(errs, fmt.Errorf("AGB_WEB_DIR must be an existing directory, got %q", previewValue(cfg.WebDir)))
		}
	}

	if v := getenv("AGB_BACKUP_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("AGB_BACKUP_INTERVAL must be a positive duration, got %q", previewValue(v)))
		} else {
			cfg.Backup.Interval = d
		}
	}
	if v := getenv("AGB_BACKUP_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("AGB_BACKUP_RETENTION must be a positive duration, got %q", previewValue(v)))
		} else {
			cfg.Backup.Retention = d
		}
	}
	if cfg.Backup.Enabled() {
		if cfg.Backup.Endpoint == "" {
			errs = append(errs, errors.New("AGB_BACKUP_ENDPOINT is required when AGB_BACKUP_BUCKET is set"))
		}
		if cfg.Backup.AccessKeyID == "" || cfg.Backup.SecretAccessKey == "" {
			errs = append(errs, errors.New("AGB_BACKUP_ACCESS_KEY_ID and AGB_BACKUP_SECRET_ACCESS_KEY are required when AGB_BACKUP_BUCKET is set"))
		}
	}
	if v := getenv("AGB_EMBEDDING_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("AGB_EMBEDDING_INTERVAL must be a positive duration, got %q", previewValue(v)))
		} else {
			cfg.Embedding.Interval = d
		}
	}
	if v := getenv("AGB_EMBEDDING_QUERY_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("AGB_EMBEDDING_QUERY_TIMEOUT must be a positive duration, got %q", previewValue(v)))
		} else {
			cfg.Embedding.QueryTimeout = d
		}
	}
	if v := getenv("AGB_EMBEDDING_PROVIDER"); v != "" {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(v), &obj); err != nil || obj == nil {
			errs = append(errs, fmt.Errorf("AGB_EMBEDDING_PROVIDER must be a JSON object, got %q", previewValue(v)))
		} else {
			cfg.Embedding.Provider = json.RawMessage(v)
		}
	}
	if cfg.Embedding.Enabled() {
		if cfg.Embedding.URL == "" {
			errs = append(errs, errors.New("AGB_EMBEDDING_URL is required when AGB_EMBEDDING_API_KEY is set"))
		}
		if cfg.Embedding.Model == "" {
			errs = append(errs, errors.New("AGB_EMBEDDING_MODEL is required when AGB_EMBEDDING_API_KEY is set"))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

// parseActors parses "id:kind,id:kind".
func parseActors(v string) ([]model.Actor, error) {
	if strings.TrimSpace(v) == "" {
		return nil, errors.New("AGB_ACTORS is required (example: owner:user,claude:agent)")
	}
	var out []model.Actor
	seen := map[string]bool{}
	for _, item := range strings.Split(v, ",") {
		item = strings.TrimSpace(item)
		id, kind, ok := strings.Cut(item, ":")
		if !ok {
			return nil, fmt.Errorf("AGB_ACTORS entry %q must be id:kind", previewValue(item))
		}
		if !model.ValidActorID(id) {
			return nil, fmt.Errorf("AGB_ACTORS actor id %q must match ^[a-z0-9]{1,32}$", previewValue(id))
		}
		k, ok := model.ParseActorKind(kind)
		if !ok {
			return nil, fmt.Errorf("AGB_ACTORS actor kind %q must be user or agent", previewValue(kind))
		}
		if seen[id] {
			return nil, fmt.Errorf("AGB_ACTORS actor %q declared twice", id)
		}
		seen[id] = true
		out = append(out, model.Actor{ID: id, Kind: k})
	}
	return out, nil
}

const maxErrValueRunes = 120

// previewValue caps an invalid environment value echoed in an error message at
// maxErrValueRunes runes on a UTF-8 boundary so a large broken value does not
// produce an unbounded error string.
func previewValue(s string) string {
	n := 0
	for i := range s {
		if n == maxErrValueRunes {
			return strings.ToValidUTF8(s[:i], "�") + "...(truncated)"
		}
		n++
	}
	return strings.ToValidUTF8(s, "�")
}

// validateDatabaseURL checks that raw uses the postgres:// or postgresql://
// scheme with a host. Error messages never echo raw values or credentials.
func validateDatabaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("AGB_DATABASE_URL must be a valid postgres:// or postgresql:// URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "postgres", "postgresql":
	default:
		return errors.New("AGB_DATABASE_URL must use the postgres:// or postgresql:// scheme")
	}
	if u.Host == "" && u.Query().Get("host") == "" {
		return errors.New("AGB_DATABASE_URL must include a database host")
	}
	return nil
}
