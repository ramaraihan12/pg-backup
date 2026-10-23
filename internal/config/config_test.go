package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const sampleYAML = `
database:
  url: postgres://backup@db.internal:5432/toko_maju
storage:
  endpoint: s3.example.test:9000
  bucket: backups
  prefix: /nightly/
  use_ssl: true
encryption:
  recipient: age1example
retention:
  daily: 3
  weekly: 2
  monthly: 1
verify:
  checks:
    - name: no orphan orders
      sql: SELECT count(*) = 0 FROM orders WHERE customer_id IS NULL
serve:
  schedule: "*/5 * * * *"
`

func TestDefaults(t *testing.T) {
	c, err := Load("", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Retention.Daily != 7 || c.Retention.Weekly != 4 || c.Retention.Monthly != 6 {
		t.Errorf("retention defaults: %+v", c.Retention)
	}
	if c.Serve.Schedule != "0 3 * * *" || c.Serve.Listen != "127.0.0.1:9187" || !c.Serve.Verify {
		t.Errorf("serve defaults: %+v", c.Serve)
	}
	if c.Notify.Telegram {
		t.Error("telegram must be off by default")
	}
}

func TestLoadYAML(t *testing.T) {
	c, err := Load(writeFile(t, "c.yaml", sampleYAML), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Storage.Bucket != "backups" || !c.Storage.UseSSL || c.Storage.Region != "us-east-1" {
		t.Errorf("storage: %+v", c.Storage)
	}
	if c.Storage.Prefix != "nightly" {
		t.Errorf("prefix should be trimmed of slashes, got %q", c.Storage.Prefix)
	}
	if c.Retention.Daily != 3 || len(c.Verify.Checks) != 1 {
		t.Errorf("retention/checks: %+v %+v", c.Retention, c.Verify.Checks)
	}
	if name, err := c.DatabaseName(); err != nil || name != "toko_maju" {
		t.Errorf("DatabaseName = %q, %v", name, err)
	}
}

func TestEnvOverridesYAML(t *testing.T) {
	c, err := Load(writeFile(t, "c.yaml", sampleYAML), env(map[string]string{
		"PGBACKUP_S3_BUCKET":        "other",
		"PGBACKUP_RETENTION_DAILY":  "14",
		"PGBACKUP_S3_USE_SSL":       "false",
		"PGBACKUP_SCHEDULE":         "0 4 * * *",
		"PGBACKUP_DATABASE_URL":     "postgres://x@h/db2",
		"PGBACKUP_TELEGRAM":         "true",
		"PGBACKUP_RETENTION_WEEKLY": " 5 ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Storage.Bucket != "other" || c.Retention.Daily != 14 || c.Retention.Weekly != 5 {
		t.Errorf("overrides not applied: %+v %+v", c.Storage, c.Retention)
	}
	if c.Storage.UseSSL || c.Serve.Schedule != "0 4 * * *" || !c.Notify.Telegram {
		t.Errorf("overrides not applied: %+v %+v", c.Storage, c.Serve)
	}
	if name, _ := c.DatabaseName(); name != "db2" {
		t.Errorf("database name = %q", name)
	}
}

func TestBadEnvValues(t *testing.T) {
	for k, v := range map[string]string{
		"PGBACKUP_RETENTION_DAILY": "many",
		"PGBACKUP_S3_USE_SSL":      "maybe",
	} {
		if _, err := Load("", env(map[string]string{k: v})); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%s: error should name the variable, got %v", k, v, err)
		}
	}
}

func TestSecretsCannotBeInYAML(t *testing.T) {
	for _, body := range []string{
		"storage:\n  secret_key: hunter2\n",
		"database:\n  password: hunter2\n",
		"nonsense: true\n",
	} {
		if _, err := Load(writeFile(t, "c.yaml", body), env(nil)); err == nil {
			t.Errorf("config %q should be rejected", body)
		}
	}
}

func TestSecretsFromEnv(t *testing.T) {
	c, err := Load("", env(map[string]string{
		"PGBACKUP_S3_ACCESS_KEY": "ak", "PGBACKUP_S3_SECRET_KEY": "sk", "PGBACKUP_DB_PASSWORD": "pw",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Secrets.S3AccessKey != "ak" || c.Secrets.S3SecretKey != "sk" || c.Secrets.DBPassword != "pw" {
		t.Errorf("secrets: %+v", c.Secrets)
	}
}

func TestMissingFileIsAnError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), env(nil)); err == nil {
		t.Fatal("expected an error")
	}
}

func TestPasswordInjection(t *testing.T) {
	c := Defaults()
	c.Secrets.DBPassword = "s3cret/pw"

	c.Database.URL = "postgres://backup@h:5432/d"
	got, err := c.DatabaseURL()
	if err != nil || got != "postgres://backup:s3cret%2Fpw@h:5432/d" {
		t.Errorf("DatabaseURL = %q, %v", got, err)
	}

	c.Database.URL = "postgres://backup:inline@h:5432/d"
	got, _ = c.DatabaseURL()
	if !strings.Contains(got, "inline") {
		t.Errorf("a password already in the URL must win, got %q", got)
	}

	c.Database.URL = "postgres://backup@h:5432/d"
	c.Verify.ScratchURL = "postgres://admin@other:5432/postgres"
	got, _ = c.ScratchURL()
	if !strings.HasPrefix(got, "postgres://admin:s3cret%2Fpw@other") {
		t.Errorf("ScratchURL = %q", got)
	}
	c.Verify.ScratchURL = ""
	got, _ = c.ScratchURL()
	if !strings.HasPrefix(got, "postgres://backup:") {
		t.Errorf("ScratchURL should default to the database URL, got %q", got)
	}
}

func TestBadDatabaseURL(t *testing.T) {
	for _, u := range []string{"mysql://h/d", "host=h dbname=d", "postgres://h:5432", "postgres://h:5432/"} {
		c := Defaults()
		c.Database.URL = u
		if _, err := c.DatabaseName(); err == nil {
			t.Errorf("%q should be rejected", u)
		}
	}
}

func validConfig() *Config {
	c := Defaults()
	c.Database.URL = "postgres://u@h/d"
	c.Storage.Endpoint = "s3:9000"
	c.Storage.Bucket = "b"
	c.Secrets.S3AccessKey = "a"
	c.Secrets.S3SecretKey = "s"
	return &c
}

func TestValidate(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(c *Config){
		"database":             func(c *Config) { c.Database.URL = "" },
		"endpoint":             func(c *Config) { c.Storage.Endpoint = "" },
		"bucket":               func(c *Config) { c.Storage.Bucket = "" },
		"access key":           func(c *Config) { c.Secrets.S3AccessKey = "" },
		"secret key":           func(c *Config) { c.Secrets.S3SecretKey = "" },
		"unnamed check":        func(c *Config) { c.Verify.Checks = []Check{{SQL: "select true"}} },
		"telegram without env": func(c *Config) { c.Notify.Telegram = true },
	}
	for name, mutate := range cases {
		c := validConfig()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	c := validConfig()
	c.Notify.Telegram = true
	c.Secrets.TelegramToken, c.Secrets.TelegramChatID = "t", "1"
	if err := c.Validate(); err != nil {
		t.Errorf("telegram with env set: %v", err)
	}
}

func TestAgeKeys(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	c := Defaults()
	if _, err := c.Recipients(); err == nil {
		t.Error("expected an error without recipient")
	}
	if _, err := c.Identities(); err == nil {
		t.Error("expected an error without identity")
	}

	c.Encryption.Recipient = id.Recipient().String()
	if rs, err := c.Recipients(); err != nil || len(rs) != 1 {
		t.Errorf("Recipients: %v, %v", rs, err)
	}
	c.Encryption.Recipient = "not-a-key"
	if _, err := c.Recipients(); err == nil {
		t.Error("expected an error for a bad recipient")
	}

	c.Secrets.AgeIdentity = id.String()
	if ids, err := c.Identities(); err != nil || len(ids) != 1 {
		t.Errorf("Identities from env: %v, %v", ids, err)
	}

	c.Secrets.AgeIdentity = ""
	c.Secrets.AgeIdentityFile = writeFile(t, "key.txt", "# created: today\n"+id.String()+"\n")
	if ids, err := c.Identities(); err != nil || len(ids) != 1 {
		t.Errorf("Identities from file: %v, %v", ids, err)
	}
	c.Secrets.AgeIdentityFile = filepath.Join(t.TempDir(), "missing")
	if _, err := c.Identities(); err == nil {
		t.Error("expected an error for a missing identity file")
	}
}
