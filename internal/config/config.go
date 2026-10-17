// Package config reads server settings from AGB_* environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// Config is the complete server configuration.
type Config struct {
	Addr    string
	DataDir string
	Token   string
	Actors  []model.Actor
	WebDir  string // folder with the built web client; empty means it is not served
	Backup  Backup
}

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

// MinTokenLength is the shortest AGB_TOKEN the server accepts.
const MinTokenLength = 32

// Load reads the configuration through getenv (usually os.Getenv).
func Load(getenv func(string) string) (*Config, error) {
	cfg := &Config{
		Addr:    getenv("AGB_ADDR"),
		DataDir: getenv("AGB_DATA_DIR"),
		Token:   getenv("AGB_TOKEN"),
		WebDir:  getenv("AGB_WEB_DIR"),
		Backup: Backup{
			Bucket:          getenv("AGB_BACKUP_BUCKET"),
			Endpoint:        getenv("AGB_BACKUP_ENDPOINT"),
			AccessKeyID:     getenv("AGB_BACKUP_ACCESS_KEY_ID"),
			SecretAccessKey: getenv("AGB_BACKUP_SECRET_ACCESS_KEY"),
			Prefix:          getenv("AGB_BACKUP_PREFIX"),
			Interval:        time.Hour,
			Retention:       672 * time.Hour,
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

	if cfg.WebDir != "" {
		if info, err := os.Stat(cfg.WebDir); err != nil || !info.IsDir() {
			errs = append(errs, fmt.Errorf("AGB_WEB_DIR must be an existing directory, got %q", cfg.WebDir))
		}
	}

	if v := getenv("AGB_BACKUP_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("AGB_BACKUP_INTERVAL must be a positive duration, got %q", v))
		} else {
			cfg.Backup.Interval = d
		}
	}
	if v := getenv("AGB_BACKUP_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("AGB_BACKUP_RETENTION must be a positive duration, got %q", v))
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
			return nil, fmt.Errorf("AGB_ACTORS entry %q must be id:kind", item)
		}
		if !model.ValidActorID(id) {
			return nil, fmt.Errorf("AGB_ACTORS actor id %q must match ^[a-z0-9]{1,32}$", id)
		}
		k, ok := model.ParseActorKind(kind)
		if !ok {
			return nil, fmt.Errorf("AGB_ACTORS actor kind %q must be user or agent", kind)
		}
		if seen[id] {
			return nil, fmt.Errorf("AGB_ACTORS actor %q declared twice", id)
		}
		seen[id] = true
		out = append(out, model.Actor{ID: id, Kind: k})
	}
	return out, nil
}
