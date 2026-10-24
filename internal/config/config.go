// Package config loads settings from a YAML file and environment variables.
//
// Plain settings can come from either place, and the environment wins.
// Secrets (passwords, keys, tokens) are read from the environment only. The
// YAML decoder rejects unknown keys, so a "password:" line in the file is an
// error, and so is a password inside a URL in the file. A URL given through
// PGBACKUP_DATABASE_URL or PGBACKUP_VERIFY_URL may carry one.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

// Config is the whole configuration.
type Config struct {
	Database   Database   `yaml:"database"`
	Storage    Storage    `yaml:"storage"`
	Encryption Encryption `yaml:"encryption"`
	Retention  Retention  `yaml:"retention"`
	Verify     Verify     `yaml:"verify"`
	Serve      Serve      `yaml:"serve"`
	Notify     Notify     `yaml:"notify"`
	Postgres   Postgres   `yaml:"postgres"`

	// Secrets come from the environment only.
	Secrets Secrets `yaml:"-"`
}

// Database is the server to back up. Put the password in PGBACKUP_DB_PASSWORD.
type Database struct {
	URL string `yaml:"url"`
}

// Storage is an S3-compatible bucket.
type Storage struct {
	Endpoint string `yaml:"endpoint"`
	Bucket   string `yaml:"bucket"`
	Region   string `yaml:"region"`
	Prefix   string `yaml:"prefix"`
	UseSSL   bool   `yaml:"use_ssl"`
}

// Encryption holds the age public key. The private key is a secret.
type Encryption struct {
	Recipient string `yaml:"recipient"`
}

// Retention is the grandfather-father-son policy.
type Retention struct {
	Daily   int `yaml:"daily"`
	Weekly  int `yaml:"weekly"`
	Monthly int `yaml:"monthly"`
}

// Verify configures the restore drill.
type Verify struct {
	// ScratchURL is the server where scratch databases are created. It
	// defaults to the database URL, so the drill runs on the source server.
	ScratchURL string  `yaml:"scratch_url"`
	Checks     []Check `yaml:"checks"`
}

// Check is a user-defined SQL check. The query must return one boolean: true passes.
type Check struct {
	Name string `yaml:"name"`
	SQL  string `yaml:"sql"`
}

// Serve configures the scheduler.
type Serve struct {
	Schedule string `yaml:"schedule"`
	Listen   string `yaml:"listen"`
	Verify   bool   `yaml:"verify"`
	Prune    bool   `yaml:"prune"`
}

// Notify switches on failure notifications. Credentials come from the environment.
type Notify struct {
	Telegram bool `yaml:"telegram"`
}

// Postgres points at the client tools when they are not on PATH.
type Postgres struct {
	BinDir string `yaml:"bin_dir"`
}

// Secrets are never read from the YAML file.
type Secrets struct {
	DBPassword      string
	S3AccessKey     string
	S3SecretKey     string
	AgeIdentity     string
	AgeIdentityFile string
	TelegramToken   string
	TelegramChatID  string
}

// Defaults returns the built-in settings.
func Defaults() Config {
	return Config{
		Storage:   Storage{Region: "us-east-1", Prefix: "pg-backup"},
		Retention: Retention{Daily: 7, Weekly: 4, Monthly: 6},
		Serve:     Serve{Schedule: "0 3 * * *", Listen: "127.0.0.1:9187", Verify: true},
	}
}

type override struct {
	name string
	set  func(c *Config, v string) error
}

func str(dst func(c *Config) *string) func(*Config, string) error {
	return func(c *Config, v string) error { *dst(c) = v; return nil }
}

func boolean(dst func(c *Config) *bool) func(*Config, string) error {
	return func(c *Config, v string) error {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%q is not true or false", v)
		}
		*dst(c) = b
		return nil
	}
}

func integer(dst func(c *Config) *int) func(*Config, string) error {
	return func(c *Config, v string) error {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%q is not a whole number", v)
		}
		*dst(c) = n
		return nil
	}
}

var overrides = []override{
	{"PGBACKUP_DATABASE_URL", str(func(c *Config) *string { return &c.Database.URL })},
	{"PGBACKUP_S3_ENDPOINT", str(func(c *Config) *string { return &c.Storage.Endpoint })},
	{"PGBACKUP_S3_BUCKET", str(func(c *Config) *string { return &c.Storage.Bucket })},
	{"PGBACKUP_S3_REGION", str(func(c *Config) *string { return &c.Storage.Region })},
	{"PGBACKUP_S3_PREFIX", str(func(c *Config) *string { return &c.Storage.Prefix })},
	{"PGBACKUP_S3_USE_SSL", boolean(func(c *Config) *bool { return &c.Storage.UseSSL })},
	{"PGBACKUP_AGE_RECIPIENT", str(func(c *Config) *string { return &c.Encryption.Recipient })},
	{"PGBACKUP_RETENTION_DAILY", integer(func(c *Config) *int { return &c.Retention.Daily })},
	{"PGBACKUP_RETENTION_WEEKLY", integer(func(c *Config) *int { return &c.Retention.Weekly })},
	{"PGBACKUP_RETENTION_MONTHLY", integer(func(c *Config) *int { return &c.Retention.Monthly })},
	{"PGBACKUP_VERIFY_URL", str(func(c *Config) *string { return &c.Verify.ScratchURL })},
	{"PGBACKUP_SCHEDULE", str(func(c *Config) *string { return &c.Serve.Schedule })},
	{"PGBACKUP_LISTEN", str(func(c *Config) *string { return &c.Serve.Listen })},
	{"PGBACKUP_SERVE_VERIFY", boolean(func(c *Config) *bool { return &c.Serve.Verify })},
	{"PGBACKUP_SERVE_PRUNE", boolean(func(c *Config) *bool { return &c.Serve.Prune })},
	{"PGBACKUP_TELEGRAM", boolean(func(c *Config) *bool { return &c.Notify.Telegram })},
	{"PGBACKUP_PG_BIN_DIR", str(func(c *Config) *string { return &c.Postgres.BinDir })},
}

// Load reads the YAML file at path (skipped when path is empty), then applies
// environment overrides using getenv. A missing file is an error only when
// path was asked for explicitly, which the caller decides by passing "" or not.
func Load(path string, getenv func(string) string) (*Config, error) {
	c := Defaults()
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open config: %w", err)
		}
		defer f.Close()
		dec := yaml.NewDecoder(f)
		dec.KnownFields(true)
		if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}
	// Secrets only come from the environment, so a password inside a URL in
	// the file is as wrong as a "password:" key.
	for name, raw := range map[string]string{"database.url": c.Database.URL, "verify.scratch_url": c.Verify.ScratchURL} {
		if u, err := url.Parse(raw); err == nil && u.User != nil {
			if _, has := u.User.Password(); has {
				return nil, fmt.Errorf("read %s: %s contains a password; set PGBACKUP_DB_PASSWORD instead", path, name)
			}
		}
	}
	for _, o := range overrides {
		if v := strings.TrimSpace(getenv(o.name)); v != "" {
			if err := o.set(&c, v); err != nil {
				return nil, fmt.Errorf("%s: %w", o.name, err)
			}
		}
	}
	c.Secrets = Secrets{
		DBPassword:      getenv("PGBACKUP_DB_PASSWORD"),
		S3AccessKey:     getenv("PGBACKUP_S3_ACCESS_KEY"),
		S3SecretKey:     getenv("PGBACKUP_S3_SECRET_KEY"),
		AgeIdentity:     getenv("PGBACKUP_AGE_IDENTITY"),
		AgeIdentityFile: getenv("PGBACKUP_AGE_IDENTITY_FILE"),
		TelegramToken:   getenv("PGBACKUP_TELEGRAM_TOKEN"),
		TelegramChatID:  getenv("PGBACKUP_TELEGRAM_CHAT_ID"),
	}
	c.Storage.Prefix = strings.Trim(c.Storage.Prefix, "/")
	return &c, nil
}

// Validate checks what every command needs: a database, a bucket, S3 keys.
func (c *Config) Validate() error {
	var problems []string
	if c.Database.URL == "" {
		problems = append(problems, "database.url (or PGBACKUP_DATABASE_URL) is empty")
	} else if _, err := c.DatabaseName(); err != nil {
		problems = append(problems, err.Error())
	}
	if c.Storage.Endpoint == "" {
		problems = append(problems, "storage.endpoint is empty")
	}
	if c.Storage.Bucket == "" {
		problems = append(problems, "storage.bucket is empty")
	}
	if c.Secrets.S3AccessKey == "" || c.Secrets.S3SecretKey == "" {
		problems = append(problems, "PGBACKUP_S3_ACCESS_KEY and PGBACKUP_S3_SECRET_KEY must be set")
	}
	for i, ch := range c.Verify.Checks {
		if ch.Name == "" || strings.TrimSpace(ch.SQL) == "" {
			problems = append(problems, fmt.Sprintf("verify.checks[%d] needs a name and sql", i))
		}
	}
	if c.Notify.Telegram && (c.Secrets.TelegramToken == "" || c.Secrets.TelegramChatID == "") {
		problems = append(problems, "notify.telegram is on but PGBACKUP_TELEGRAM_TOKEN or PGBACKUP_TELEGRAM_CHAT_ID is not set")
	}
	if len(problems) > 0 {
		return errors.New("invalid config: " + strings.Join(problems, "; "))
	}
	return nil
}

// DatabaseName is the database named in the URL.
func (c *Config) DatabaseName() (string, error) {
	u, err := parseURL(c.Database.URL)
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return "", errors.New("database url has no database name")
	}
	return name, nil
}

// DatabaseURL is the source URL with PGBACKUP_DB_PASSWORD filled in when the URL has none.
func (c *Config) DatabaseURL() (string, error) {
	return WithPassword(c.Database.URL, c.Secrets.DBPassword)
}

// ScratchURL is the server used for restore drills.
func (c *Config) ScratchURL() (string, error) {
	if c.Verify.ScratchURL != "" {
		return WithPassword(c.Verify.ScratchURL, c.Secrets.DBPassword)
	}
	return c.DatabaseURL()
}

// Recipients parses the age public key.
func (c *Config) Recipients() ([]age.Recipient, error) {
	if c.Encryption.Recipient == "" {
		return nil, errors.New("no age recipient: set encryption.recipient or PGBACKUP_AGE_RECIPIENT (make a key with: pg-backup keygen)")
	}
	r, err := age.ParseX25519Recipient(strings.TrimSpace(c.Encryption.Recipient))
	if err != nil {
		return nil, fmt.Errorf("age recipient: %w", err)
	}
	return []age.Recipient{r}, nil
}

// Identities parses the age private key from PGBACKUP_AGE_IDENTITY or the
// file named by PGBACKUP_AGE_IDENTITY_FILE.
func (c *Config) Identities() ([]age.Identity, error) {
	var r io.Reader
	switch {
	case c.Secrets.AgeIdentity != "":
		r = strings.NewReader(c.Secrets.AgeIdentity)
	case c.Secrets.AgeIdentityFile != "":
		f, err := os.Open(c.Secrets.AgeIdentityFile)
		if err != nil {
			return nil, fmt.Errorf("open age identity file: %w", err)
		}
		defer f.Close()
		r = f
	default:
		return nil, errors.New("no age private key: set PGBACKUP_AGE_IDENTITY or PGBACKUP_AGE_IDENTITY_FILE")
	}
	ids, err := age.ParseIdentities(r)
	if err != nil {
		return nil, fmt.Errorf("age identity: %w", err)
	}
	return ids, nil
}

func parseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("bad database url: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, errors.New("database url must start with postgres:// or postgresql://")
	}
	return u, nil
}

// WithPassword fills password into a postgres URL that has a user but no password.
func WithPassword(raw, password string) (string, error) {
	u, err := parseURL(raw)
	if err != nil {
		return "", err
	}
	if password != "" && u.User != nil {
		if _, has := u.User.Password(); !has {
			u.User = url.UserPassword(u.User.Username(), password)
		}
	}
	return u.String(), nil
}
