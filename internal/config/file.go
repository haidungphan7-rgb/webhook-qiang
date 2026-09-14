package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// SchemaVersion is written into the file so a future reader can migrate deliberately
// instead of guessing what a missing field meant.
const SchemaVersion = 1

// File is the on-disk configuration.
//
// It deliberately has no field for the encryption key or the auth token/keys: those are
// deployment secrets and a file next to the source tree is one `git add .` away from
// being published. They are read from the environment only, and `explain` reports
// whether they are set without ever printing them.
type File struct {
	Schema int `json:"schema"`

	Addr               string `json:"addr,omitempty"`
	Port               uint16 `json:"port,omitempty"`
	DatabaseURL        string `json:"database_url,omitempty"`
	PublicURLRoot      string `json:"public_url_root,omitempty"`
	MaxRequestBodySize uint32 `json:"max_request_body_size,omitempty"`

	ReplayTimeout      string   `json:"replay_timeout,omitempty"`
	ReplayMaxPreview   int      `json:"replay_max_preview,omitempty"`
	ReplayMaxRedirects int      `json:"replay_max_redirects,omitempty"`
	ReplayMaxRetries   int      `json:"replay_max_retries,omitempty"`
	ReplayBackoff      string   `json:"replay_backoff,omitempty"`
	ReplayRateLimit    int      `json:"replay_rate_limit,omitempty"`
	ReplayAllowHosts   []string `json:"replay_allow_hosts,omitempty"`
	ReplayAllowPrivate bool     `json:"replay_allow_private,omitempty"`

	RetentionMaxEvents int    `json:"retention_max_events,omitempty"`
	RetentionMaxDays   int    `json:"retention_max_days,omitempty"`
	RetentionInterval  string `json:"retention_interval,omitempty"`

	TrustProxy bool   `json:"trust_proxy_headers,omitempty"`
	LogLevel   string `json:"log_level,omitempty"`
}

// legacyFile is the shape server-manager.ps1 used to write (PascalCase, numbers as
// strings). Reading it keeps every existing installation working without a manual step.
type legacyFile struct {
	Port          float64  `json:"Port"`
	Addr          string   `json:"Addr"`
	DatabaseUrl   string   `json:"DatabaseUrl"`
	PublicUrlRoot string   `json:"PublicUrlRoot"`
	Timeout       string   `json:"Timeout"`
	RateLimit     string   `json:"RateLimit"`
	Retries       string   `json:"Retries"`
	MaxPreview    string   `json:"MaxPreview"`
	KeepEvents    string   `json:"KeepEvents"`
	KeepDays      string   `json:"KeepDays"`
	MaxBody       string   `json:"MaxBody"`
	LogLevel      string   `json:"LogLevel"`
	AllowHosts    []string `json:"AllowHosts"`
	AllowPrivate  bool     `json:"AllowPrivate"`
	TrustProxy    bool     `json:"TrustProxy"`
}

// DefaultPath is where the configuration file lives.
//
// It is outside the repository on purpose: the file can hold a database connection
// string, and a file next to the source tree is one `git add .` away from being
// published. WEBHOOK_ZQ_CONFIG wins over everything, so scripts and CI can point
// somewhere else without knowing any of the platform rules below.
func DefaultPath() (string, error) {
	return defaultPath(runtime.GOOS, os.Getenv)
}

// AppDir is the per-application directory the config file lives in. `install`
// uses it to place the binary next to the config (bin\ on Windows) so both the
// program and its settings are managed as one unit when uninstalling.
func AppDir() (string, error) {
	base, err := userConfigDir(runtime.GOOS, os.Getenv)
	if err != nil {
		return "", fmt.Errorf("cannot resolve the user config directory: %w", err)
	}

	return filepath.Join(base, "webhook-zq"), nil
}

// defaultPath is the injectable half of DefaultPath. Taking the platform and the
// environment lookup as arguments is what lets the tests cover all three platforms on
// any host - runtime.GOOS cannot be changed with t.Setenv.
func defaultPath(goos string, getenv func(string) string) (string, error) {
	if p := strings.TrimSpace(getenv("WEBHOOK_ZQ_CONFIG")); p != "" {
		return p, nil
	}

	base, err := userConfigDir(goos, getenv)
	if err != nil {
		return "", fmt.Errorf("cannot resolve the user config directory: %w", err)
	}

	return filepath.Join(base, "webhook-zq", "config.json"), nil
}

// userConfigDir resolves the per-user configuration directory for one platform.
//
// It mirrors os.UserConfigDir() with one deliberate difference: on Windows that function
// returns %AppData%, which is the ROAMING profile. This file can hold a database
// password, and a roaming profile is the wrong place for a machine-local secret, so
// %LOCALAPPDATA% is preferred when it is set.
//
// Expected results:
//
//	linux   $XDG_CONFIG_HOME  or  ~/.config
//	darwin  ~/Library/Application Support
//	windows %LOCALAPPDATA%    or  %AppData%
func userConfigDir(goos string, getenv func(string) string) (string, error) {
	switch goos {
	case "windows":
		if dir := getenv("LOCALAPPDATA"); dir != "" {
			return dir, nil
		}

		if dir := getenv("APPDATA"); dir != "" {
			return dir, nil
		}

		return "", errors.New("neither %LOCALAPPDATA% nor %APPDATA% is defined")

	case "darwin":
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("$HOME is not defined")
		}

		return filepath.Join(home, "Library", "Application Support"), nil

	default: // unix
		if dir := getenv("XDG_CONFIG_HOME"); dir != "" {
			// Deliberately not filepath.IsAbs: that applies the rules of the HOST
			// platform, so a POSIX path reads as relative when the check runs on Windows
			// - which is exactly what the table test does. The XDG spec defines an
			// absolute path as one starting with "/", so ask that instead.
			if !strings.HasPrefix(dir, "/") {
				return "", errors.New("path in $XDG_CONFIG_HOME is relative")
			}

			return dir, nil
		}

		home := getenv("HOME")
		if home == "" {
			return "", errors.New("neither $XDG_CONFIG_HOME nor $HOME are defined")
		}

		return filepath.Join(home, ".config"), nil
	}
}

// Load reads the file. A missing file is not an error: it means "everything default".
//
// The second result reports whether the file was in the legacy PascalCase shape and has
// been translated, so the caller can tell the user to re-save it.
func Load(path string) (*File, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &File{}, false, nil
		}

		return nil, false, err
	}

	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, false, fmt.Errorf("cannot parse %s: %w", path, err)
	}

	// A file with no schema field predates this format.
	if f.Schema == 0 && looksLegacy(data) {
		legacy, lerr := parseLegacy(data)
		if lerr != nil {
			return nil, false, lerr
		}

		return legacy, true, nil
	}

	return &f, false, nil
}

func looksLegacy(data []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}

	for _, k := range []string{"Port", "DatabaseUrl", "AllowHosts", "KeepEvents"} {
		if _, ok := probe[k]; ok {
			return true
		}
	}

	return false
}

func parseLegacy(data []byte) (*File, error) {
	var old legacyFile
	if err := json.Unmarshal(data, &old); err != nil {
		return nil, fmt.Errorf("cannot parse the legacy config: %w", err)
	}

	f := &File{
		Addr:               old.Addr,
		DatabaseURL:        old.DatabaseUrl,
		PublicURLRoot:      old.PublicUrlRoot,
		ReplayTimeout:      old.Timeout,
		ReplayBackoff:      "",
		ReplayAllowHosts:   old.AllowHosts,
		ReplayAllowPrivate: old.AllowPrivate,
		TrustProxy:         old.TrustProxy,
		LogLevel:           old.LogLevel,
	}

	if old.Port > 0 {
		f.Port = uint16(old.Port)
	}

	for _, pair := range []struct {
		raw string
		out *int
	}{
		{old.RateLimit, &f.ReplayRateLimit},
		{old.Retries, &f.ReplayMaxRetries},
		{old.MaxPreview, &f.ReplayMaxPreview},
		{old.KeepEvents, &f.RetentionMaxEvents},
		{old.KeepDays, &f.RetentionMaxDays},
	} {
		if strings.TrimSpace(pair.raw) == "" {
			continue
		}

		v, err := strconv.Atoi(strings.TrimSpace(pair.raw))
		if err != nil {
			return nil, fmt.Errorf("legacy value %q is not a number", pair.raw)
		}

		*pair.out = v
	}

	if strings.TrimSpace(old.MaxBody) != "" {
		v, err := strconv.ParseUint(strings.TrimSpace(old.MaxBody), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("legacy MaxBody %q is not a number", old.MaxBody)
		}

		f.MaxRequestBodySize = uint32(v)
	}

	return f, nil
}

// Save writes the file with 0600 permissions and keeps one backup of the previous
// contents: a bad `config set` should never be unrecoverable.
func Save(path string, f *File) error {
	f.Schema = SchemaVersion

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	if old, err := os.ReadFile(path); err == nil {
		// #nosec G703 -- the path is the caller-supplied config path; the .bak
		// sibling next to it is exactly where the backup belongs.
		_ = os.WriteFile(path+".bak", old, 0o600)
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// dsnPassword matches the credentials part of a URL style connection string.
// It is applied to the raw string on purpose: rebuilding the URL from a parsed one
// escapes the replacement ("***" became "%2A%2A%2A"), which is both ugly and confusing
// in a diagnostic.
var dsnPassword = regexp.MustCompile(`^([a-zA-Z0-9+.\-]+://[^:/@]+:)([^@]*)(@)`)

// MaskDSN hides the password in a connection string. The host and database are useful
// when answering "which database is this instance talking to", the password is not.
func MaskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}

	masked := dsnPassword.ReplaceAllString(dsn, "${1}***${3}")

	// Keyword style: "host=... password=secret dbname=...".
	if masked == dsn {
		masked = dsnKeywordPassword.ReplaceAllString(dsn, "${1}***")
	}

	return masked
}

var dsnKeywordPassword = regexp.MustCompile(`(?i)(password\s*=\s*)([^\s']+)`)

// Field describes one configuration key: the file key, the environment variable that can
// override it, the equivalent `start` flag, how to read and write it, and whether the
// value may be printed.
//
// This table is the single definition shared by `config explain`, `config set` and
// `config expand` - without it every consumer grows its own idea of what the keys are.
type Field struct {
	Key    string
	Env    string
	Flag   string
	Secret bool
	Mask   func(string) string
	Get    func(*File) string
	Set    func(*File, string) error
}

func strField(key, env, flag string, get func(*File) *string) Field {
	return Field{
		Key: key, Env: env, Flag: flag,
		Get: func(f *File) string { return *get(f) },
		Set: func(f *File, v string) error { *get(f) = v; return nil },
	}
}

// scalarField is the shared wiring for the two scalar settings (int, bool): the
// config file, the environment and `config set` all go through the same parse
// and format seams, so one spelling per kind is enough.
func scalarField[T int | bool](key, env, flag, kind string, get func(*File) *T, parse func(string) (T, error), format func(T) string) Field {
	return Field{
		Key: key, Env: env, Flag: flag,
		Get: func(f *File) string { return format(*get(f)) },
		Set: func(f *File, v string) error {
			n, err := parse(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("%s must be %s, got %q", key, kind, v)
			}

			*get(f) = n

			return nil
		},
	}
}

func intField(key, env, flag string, get func(*File) *int) Field {
	return scalarField(key, env, flag, "a number", get, strconv.Atoi, strconv.Itoa)
}

func boolField(key, env, flag string, get func(*File) *bool) Field {
	return scalarField(key, env, flag, "true or false", get, strconv.ParseBool, strconv.FormatBool)
}

func durationField(key, env, flag string, get func(*File) *string) Field {
	f := strField(key, env, flag, get)
	f.Set = func(file *File, v string) error {
		if _, err := time.ParseDuration(strings.TrimSpace(v)); err != nil {
			return fmt.Errorf("%s must be a duration such as 10s or 1m, got %q", key, v)
		}

		*get(file) = strings.TrimSpace(v)

		return nil
	}

	return f
}

// Fields is every key the config file understands, in the order `explain` prints them.
func Fields() []Field {
	return []Field{
		strField("addr", "SERVER_ADDR", "--addr", func(f *File) *string { return &f.Addr }),
		{
			Key:  "port",
			Env:  "HTTP_PORT",
			Flag: "--port",
			Get:  func(f *File) string { return strconv.Itoa(int(f.Port)) },
			Set: func(f *File, v string) error {
				n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 16)
				if err != nil {
					return fmt.Errorf("port must be a number between 1 and 65535, got %q", v)
				}

				f.Port = uint16(n)

				return nil
			},
		},
		{
			Key:  "database_url",
			Env:  "DATABASE_URL",
			Flag: "--database-url",
			Mask: MaskDSN,
			Get:  func(f *File) string { return f.DatabaseURL },
			Set:  func(f *File, v string) error { f.DatabaseURL = v; return nil },
			// Not secret: knowing which database an instance talks to is often the whole
			// point of asking. Only the password is masked.
		},
		strField("public_url_root", "PUBLIC_URL_ROOT", "--public-url-root", func(f *File) *string { return &f.PublicURLRoot }),
		{
			Key:  "max_request_body_size",
			Env:  "MAX_REQUEST_BODY_SIZE",
			Flag: "--max-request-body-size",
			Get:  func(f *File) string { return strconv.FormatUint(uint64(f.MaxRequestBodySize), 10) },
			Set: func(f *File, v string) error {
				n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32)
				if err != nil {
					return fmt.Errorf("max_request_body_size must be a number, got %q", v)
				}

				f.MaxRequestBodySize = uint32(n)

				return nil
			},
		},
		durationField("replay_timeout", "REPLAY_TIMEOUT", "--replay-timeout", func(f *File) *string { return &f.ReplayTimeout }),
		intField("replay_max_preview", "REPLAY_MAX_PREVIEW", "--replay-max-preview", func(f *File) *int { return &f.ReplayMaxPreview }),
		intField("replay_max_redirects", "REPLAY_MAX_REDIRECTS", "--replay-max-redirects", func(f *File) *int { return &f.ReplayMaxRedirects }),
		intField("replay_max_retries", "REPLAY_MAX_RETRIES", "--replay-max-retries", func(f *File) *int { return &f.ReplayMaxRetries }),
		durationField("replay_backoff", "REPLAY_BACKOFF", "--replay-backoff", func(f *File) *string { return &f.ReplayBackoff }),
		intField("replay_rate_limit", "REPLAY_RATE_LIMIT", "--replay-rate-limit", func(f *File) *int { return &f.ReplayRateLimit }),
		{
			Key:  "replay_allow_hosts",
			Env:  "REPLAY_ALLOW_HOSTS",
			Flag: "--replay-allow-host",
			Get:  func(f *File) string { return strings.Join(f.ReplayAllowHosts, ",") },
			Set: func(f *File, v string) error {
				parts := strings.Split(v, ",")
				out := make([]string, 0, len(parts))

				for _, p := range parts {
					if p = strings.TrimSpace(p); p != "" {
						out = append(out, p)
					}
				}

				f.ReplayAllowHosts = out

				return nil
			},
		},
		boolField("replay_allow_private", "REPLAY_ALLOW_PRIVATE", "--replay-allow-private", func(f *File) *bool { return &f.ReplayAllowPrivate }),
		intField("retention_max_events", "RETENTION_MAX_EVENTS", "--retention-max-events", func(f *File) *int { return &f.RetentionMaxEvents }),
		intField("retention_max_days", "RETENTION_MAX_DAYS", "--retention-max-days", func(f *File) *int { return &f.RetentionMaxDays }),
		durationField("retention_interval", "RETENTION_INTERVAL", "--retention-interval", func(f *File) *string { return &f.RetentionInterval }),
		boolField("trust_proxy_headers", "TRUST_PROXY_HEADERS", "--trust-proxy-headers", func(f *File) *bool { return &f.TrustProxy }),
		strField("log_level", "LOG_LEVEL", "--log-level", func(f *File) *string { return &f.LogLevel }),
	}
}

// SecretFields are never written to the file and never printed. They are listed so that
// `explain` can still answer the question that actually gets asked: "is it on?".
func SecretFields() []Field {
	return []Field{
		{Key: "encrypt_key", Env: "ENCRYPT_KEY", Flag: "--encrypt-key", Secret: true},
		{Key: "auth_token", Env: "AUTH_TOKEN", Flag: "--auth-token", Secret: true},
		{Key: "auth_keys", Env: "AUTH_KEYS", Flag: "--auth-keys", Secret: true},
	}
}

// Lookup finds a field by its file key.
func Lookup(key string) (Field, bool) {
	for _, f := range Fields() {
		if f.Key == key {
			return f, true
		}
	}

	return Field{}, false
}

// Apply copies the file values that are actually set into the settings. Values that the
// caller already provided through a flag or an environment variable win, so the file
// behaves as the layer between the environment and the defaults.
//
// provided reports whether a key was set elsewhere; it is the CLI's IsSet.
func Apply(s *AppSettings, f *File, provided func(key string) bool) {
	if f == nil {
		return
	}

	// addr and port are not part of AppSettings: they decide what the process listens on
	// and are handled by the start command itself (see ApplyListen).

	// Replay
	if f.ReplayTimeout != "" && !provided("replay-timeout") {
		if d, err := time.ParseDuration(f.ReplayTimeout); err == nil {
			s.ReplayTimeout = d
		}
	}

	if f.ReplayBackoff != "" && !provided("replay-backoff") {
		if d, err := time.ParseDuration(f.ReplayBackoff); err == nil {
			s.ReplayBackoff = d
		}
	}

	if f.ReplayMaxPreview > 0 && !provided("replay-max-preview") {
		s.ReplayMaxPreview = f.ReplayMaxPreview
	}

	if f.ReplayMaxRedirects > 0 && !provided("replay-max-redirects") {
		s.ReplayMaxRedirects = f.ReplayMaxRedirects
	}

	if f.ReplayMaxRetries > 0 && !provided("replay-max-retries") {
		s.ReplayMaxRetries = f.ReplayMaxRetries
	}

	if f.ReplayRateLimit > 0 && !provided("replay-rate-limit") {
		s.ReplayRateLimit = f.ReplayRateLimit
	}

	if len(f.ReplayAllowHosts) > 0 && !provided("replay-allow-host") {
		s.ReplayAllowHosts = f.ReplayAllowHosts
	}

	if f.ReplayAllowPrivate && !provided("replay-allow-private") {
		s.ReplayAllowPrivate = true
	}

	// Retention
	if f.RetentionMaxEvents > 0 && !provided("retention-max-events") {
		s.RetentionMaxEvents = f.RetentionMaxEvents
	}

	if f.RetentionMaxDays > 0 && !provided("retention-max-days") {
		s.RetentionMaxDays = f.RetentionMaxDays
	}

	if f.RetentionInterval != "" && !provided("retention-interval") {
		if d, err := time.ParseDuration(f.RetentionInterval); err == nil {
			s.RetentionInterval = d
		}
	}

	// Capture
	if f.MaxRequestBodySize > 0 && !provided("max-request-body-size") {
		s.MaxRequestBodySize = f.MaxRequestBodySize
	}

	// Misc
	if f.PublicURLRoot != "" && !provided("public-url-root") {
		s.PublicURLRoot = f.PublicURLRoot
	}

	if f.TrustProxy && !provided("trust-proxy-headers") {
		s.TrustProxy = true
	}
}

// ApplyListen fills in the listen address and port, which live outside AppSettings.
func ApplyListen(addr string, port uint16, f *File, provided func(key string) bool) (string, uint16) {
	if f == nil {
		return addr, port
	}

	if f.Addr != "" && !provided("addr") {
		addr = f.Addr
	}

	if f.Port > 0 && !provided("port") {
		port = f.Port
	}

	return addr, port
}
