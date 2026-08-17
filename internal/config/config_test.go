package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	// Covers: $ preservation (the whole point — make mangles these), quote
	// stripping, comments, blanks, values containing '='.
	if err := os.WriteFile(path, []byte(""+
		"# comment line\n"+
		"SMTP_PASSWORD='ab$9kC$d2Qz'\n"+
		"SMTP_HOST=pro2.mail.ovh.net\n"+
		"\n"+
		"PLAIN_DOLLAR=pa$$word\n"+
		"QUOTED=\"hello world\"\n"+
		"EQUALS=a=b=c\n"+
		"NO_VALUE=\n"+
		"NOT_A_LINE_WITHOUT_EQUALS\n"+
		"CRLF_VALUE=ok\r\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	// Set-but-empty is DEFINED — loadDotEnv must leave it alone (the real
	// environment, even empty, wins over the file).
	t.Setenv("NO_VALUE", "")

	// The rest must be ABSENT, not set-but-empty: loadDotEnv only fills
	// undefined keys, and t.Setenv(k, "") would define them.
	withEnvAbsent(t, "SMTP_PASSWORD", "SMTP_HOST", "PLAIN_DOLLAR", "QUOTED", "EQUALS", "CRLF_VALUE")

	loadDotEnv(path)

	for k, want := range map[string]string{
		"SMTP_PASSWORD": "ab$9kC$d2Qz", // dollars intact — make would return "ab"
		"SMTP_HOST":     "pro2.mail.ovh.net",
		"PLAIN_DOLLAR":  "pa$$word",
		"QUOTED":        "hello world",
		"EQUALS":        "a=b=c", // split on FIRST '=' only
		"CRLF_VALUE":    "ok",    // \r trimmed
	} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if got := os.Getenv("NO_VALUE"); got != "" {
		t.Errorf("NO_VALUE = %q, want empty (environment wins)", got)
	}
}

func TestLoadDotEnv_RealEnvironmentWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("SMTP_PASSWORD=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SMTP_PASSWORD", "from-env")

	loadDotEnv(path)

	if got := os.Getenv("SMTP_PASSWORD"); got != "from-env" {
		t.Fatalf("environment must win over .env, got %q", got)
	}
}

func TestLoadDotEnv_MissingFileIsNoOp(t *testing.T) {
	loadDotEnv(filepath.Join(t.TempDir(), "does-not-exist.env"))
}

func TestLoad_IntegratesDotEnv(t *testing.T) {
	dir := t.TempDir()
	backup, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(backup) })
	if err := os.WriteFile(".env", []byte("SMTP_TLS='implicit'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withEnvAbsent(t, "SMTP_TLS")

	cfg := Load()
	if cfg.SMTPTLSMode != "implicit" {
		t.Fatalf("SMTP_TLS from .env = %q, want implicit", cfg.SMTPTLSMode)
	}
	if strings.Contains(cfg.SMTPTLSMode, "'") {
		t.Fatalf("quotes must be stripped, got %q", cfg.SMTPTLSMode)
	}
}

// withEnvAbsent removes keys for the test's duration, restoring whatever was
// there before. The inverse of t.Setenv: loadDotEnv only fills UNDEFINED
// keys, so tests must guarantee absence, not emptiness.
func withEnvAbsent(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		orig, had := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() {
			if had {
				os.Setenv(k, orig)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}
