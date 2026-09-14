// Package start implements the `start` command: it opens the database, applies
// migrations and serves HTTP until the process is asked to stop.
//
// Dependency construction happens here and nowhere else, so the wiring of the whole
// application can be read top to bottom in one function.
package start

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/crypto"
	apphttp "github.com/yuandzhang/webhook-zq/internal/http"
	"github.com/yuandzhang/webhook-zq/internal/http/frontend"
	"github.com/yuandzhang/webhook-zq/internal/httpapi"
	"github.com/yuandzhang/webhook-zq/internal/migrate"
	"github.com/yuandzhang/webhook-zq/internal/notify"
	"github.com/yuandzhang/webhook-zq/internal/pubsub"
	"github.com/yuandzhang/webhook-zq/internal/replay"
	"github.com/yuandzhang/webhook-zq/internal/retention"
	"github.com/yuandzhang/webhook-zq/internal/storage/postgres"
	"github.com/yuandzhang/webhook-zq/internal/tray"
	"github.com/yuandzhang/webhook-zq/internal/version"
	"github.com/yuandzhang/webhook-zq/web"
)

type command struct {
	c *cli.Command

	options struct {
		addr string
		port uint16

		timeouts struct {
			httpRead, httpWrite, httpIdle, shutdown time.Duration
		}

		databaseURL string

		maxRequestBodySize uint32
		replayTimeout      time.Duration
		replayMaxPreview   int
		replayMaxRedirects int
		replayMaxRetries   int
		replayBackoff      time.Duration
		replayRateLimit    int
		replayAllowHosts   []string
		retentionMaxEvents int
		retentionMaxDays   int
		retentionInterval  time.Duration
		authToken          string
		encryptKey         string
		publicURLRoot      string
		useLiveFrontend    bool
		trustProxyHeader   bool
		replayAllowPrivate bool
		authKeys           []string
	}
}

// NewCommand builds the `start` command.
func NewCommand(log *zap.Logger, defaultPort uint16) *cli.Command { //nolint:funlen
	var cmd command

	boolFlag := func(name, usage string, env ...string) cli.Flag {
		f := cli.BoolFlag{Name: name, Usage: usage, OnlyOnce: true}
		if len(env) > 0 {
			f.Sources = cli.EnvVars(env...)
		}

		return &f
	}

	intFlag := func(name, usage string, value int, env ...string) cli.Flag {
		f := cli.IntFlag{Name: name, Usage: usage, Value: value, OnlyOnce: true}
		if len(env) > 0 {
			f.Sources = cli.EnvVars(env...)
		}

		return &f
	}

	durFlag := func(name, usage string, value time.Duration, env ...string) cli.Flag {
		f := cli.DurationFlag{Name: name, Usage: usage, Value: value, OnlyOnce: true}
		if len(env) > 0 {
			f.Sources = cli.EnvVars(env...)
		}

		return &f
	}

	strFlag := func(name, usage string, env ...string) cli.Flag {
		f := cli.StringFlag{Name: name, Usage: usage, OnlyOnce: true, Config: cli.StringConfig{TrimSpace: true}}
		if len(env) > 0 {
			f.Sources = cli.EnvVars(env...)
		}

		return &f
	}

	cmd.c = &cli.Command{
		Name:    "start",
		Usage:   "Start the webhook replay platform",
		Aliases: []string{"s", "server", "serve"},
		Flags: []cli.Flag{
			strFlag("addr", "IP address to listen on (0.0.0.0 binds to all interfaces)", "SERVER_ADDR"),
			&cli.UintFlag{Name: "port", Usage: "HTTP port", Value: uint(defaultPort), OnlyOnce: true, Sources: cli.EnvVars("HTTP_PORT")},
			durFlag("read-timeout", "maximum duration for reading a request", 60*time.Second, "HTTP_READ_TIMEOUT"),
			durFlag("write-timeout", "maximum duration before timing out writes", 60*time.Second, "HTTP_WRITE_TIMEOUT"),
			durFlag("idle-timeout", "keep-alive idle timeout", 120*time.Second, "HTTP_IDLE_TIMEOUT"),
			durFlag("shutdown-timeout", "graceful shutdown timeout", 15*time.Second, "SHUTDOWN_TIMEOUT"),

			strFlag("database-url", "PostgreSQL DSN (postgres://user:pwd@host:5432/db)", "DATABASE_URL"),

			&cli.UintFlag{Name: "max-request-body-size", Usage: "maximum captured body size in bytes",
				Value: uint(config.DefaultMaxRequestBodySize), OnlyOnce: true, Sources: cli.EnvVars("MAX_REQUEST_BODY_SIZE")},
			durFlag("replay-timeout", "HTTP client timeout for replays", config.DefaultReplayTimeout, "REPLAY_TIMEOUT"),
			intFlag("replay-max-preview", "maximum bytes of the replay response stored", config.DefaultReplayMaxPreview, "REPLAY_MAX_PREVIEW"),
			intFlag("replay-max-redirects", "0 disables redirects entirely", config.DefaultReplayMaxRedirects, "REPLAY_MAX_REDIRECTS"),
			intFlag("replay-max-retries", "retry count for failed replays (0 disables)", config.DefaultReplayMaxRetries, "REPLAY_MAX_RETRIES"),
			durFlag("replay-backoff", "base backoff between retries", config.DefaultReplayBackoff, "REPLAY_BACKOFF"),
			&cli.StringSliceFlag{Name: "replay-allow-host", Usage: "explicitly allowed replay target host (repeatable)",
				Sources: cli.EnvVars("REPLAY_ALLOW_HOSTS")},
			boolFlag("replay-allow-private", "allow replay to reserved addresses of allow listed hosts (local demos only)", "REPLAY_ALLOW_PRIVATE"),
			intFlag("replay-rate-limit", "replays allowed per caller per minute (0 disables)", config.DefaultReplayRateLimit, "REPLAY_RATE_LIMIT"),

			intFlag("retention-max-events", "maximum events kept per inbox", config.DefaultRetentionMaxEvents, "RETENTION_MAX_EVENTS"),
			intFlag("retention-max-days", "maximum event age in days", config.DefaultRetentionMaxDays, "RETENTION_MAX_DAYS"),
			durFlag("retention-interval", "how often retention is enforced", config.DefaultRetentionInterval, "RETENTION_INTERVAL"),

			strFlag("auth-token", "when set, the UI and /api/v1 require this token", "AUTH_TOKEN"),
			&cli.StringSliceFlag{
				Name:    "auth-keys",
				Usage:   "per tenant api keys: <tenant>:<key> (repeatable); enables multi tenant separation",
				Sources: cli.EnvVars("AUTH_KEYS"),
			},
			strFlag("encrypt-key", "base64 32 byte master key for secrets at rest", "ENCRYPT_KEY"),
			strFlag("public-url-root", "public base URL used when rendering receive URLs", "PUBLIC_URL_ROOT"),
			boolFlag("use-live-frontend", "serve the frontend from ./web/dist instead of the embedded build"),
			boolFlag("trust-proxy-headers", "trust X-Forwarded-For & friends for the client IP", "TRUST_PROXY_HEADERS"),
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Uint("port") > math.MaxUint16 {
				return fmt.Errorf("--port %d is out of range (0-65535)", c.Uint("port"))
			}

			if c.Uint("max-request-body-size") > math.MaxUint32 {
				return fmt.Errorf("--max-request-body-size %d is out of range (0-4294967295)", c.Uint("max-request-body-size"))
			}

			cmd.options.addr = c.String("addr")
			// #nosec G115 -- bounded by the explicit check above.
			cmd.options.port = uint16(c.Uint("port"))
			cmd.options.timeouts.httpRead = c.Duration("read-timeout")
			cmd.options.timeouts.httpWrite = c.Duration("write-timeout")
			cmd.options.timeouts.httpIdle = c.Duration("idle-timeout")
			cmd.options.timeouts.shutdown = c.Duration("shutdown-timeout")
			cmd.options.databaseURL = c.String("database-url")
			// #nosec G115 -- bounded by the explicit check above.
			cmd.options.maxRequestBodySize = uint32(c.Uint("max-request-body-size"))
			cmd.options.replayTimeout = c.Duration("replay-timeout")
			cmd.options.replayMaxPreview = c.Int("replay-max-preview")
			cmd.options.replayMaxRedirects = c.Int("replay-max-redirects")
			cmd.options.replayMaxRetries = c.Int("replay-max-retries")
			cmd.options.replayBackoff = c.Duration("replay-backoff")
			cmd.options.replayRateLimit = c.Int("replay-rate-limit")
			cmd.options.replayAllowHosts = c.StringSlice("replay-allow-host")
			cmd.options.retentionMaxEvents = c.Int("retention-max-events")
			cmd.options.retentionMaxDays = c.Int("retention-max-days")
			cmd.options.retentionInterval = c.Duration("retention-interval")
			cmd.options.authToken = c.String("auth-token")
			cmd.options.encryptKey = c.String("encrypt-key")
			cmd.options.publicURLRoot = c.String("public-url-root")
			cmd.options.useLiveFrontend = c.Bool("use-live-frontend")
			cmd.options.trustProxyHeader = c.Bool("trust-proxy-headers")
			cmd.options.replayAllowPrivate = c.Bool("replay-allow-private")
			cmd.options.authKeys = c.StringSlice("auth-keys")

			if cmd.options.addr == "" {
				cmd.options.addr = "0.0.0.0"
			}

			// The config file is the layer between the environment and the defaults -
			// the same precedence `config explain` prints. The autostart logon task
			// runs `start` with no environment of its own, so without this layer it
			// has no database url at all.
			file, err := loadConfigFile()
			if err != nil {
				return err
			}

			cmd.options.databaseURL = resolveDatabaseURL(cmd.options.databaseURL, file)

			cmd.options.addr, cmd.options.port = config.ApplyListen(
				cmd.options.addr, cmd.options.port, file, c.IsSet)

			return cmd.Run(ctx, log, file, c.IsSet)
		},
	}

	return cmd.c
}

// loadConfigFile reads the user's config file. A missing file is fine (Load
// returns an empty one); a broken file is not, because silently ignoring it
// would mean the instance runs without values the user believes are set.
func loadConfigFile() (*config.File, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return nil, fmt.Errorf("cannot resolve the config path: %w", err)
	}

	f, _, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the config file (%s): %w", path, err)
	}

	return f, nil
}

// resolveDatabaseURL layers the two sources a bare `start` has: an explicit
// flag/env value wins, then the config file. An empty result is handled by Run,
// which turns it into setup instructions.
func resolveDatabaseURL(flagValue string, file *config.File) string {
	if flagValue != "" {
		return flagValue
	}

	if file != nil {
		return file.DatabaseURL
	}

	return ""
}

// Run starts the application and blocks until the context is cancelled.
func (cmd *command) Run(parentCtx context.Context, log *zap.Logger, file *config.File, provided func(string) bool) error { //nolint:funlen
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	settings, err := cmd.buildSettings(file, provided)
	if err != nil {
		return err
	}

	if cmd.options.databaseURL == "" {
		// This is the very first thing most people see, and PostgreSQL is the only
		// external dependency, so the error has to double as setup instructions.
		// A one line message here means the user knows something is missing but not
		// what to install, and the session ends before it has started.
		return errors.New(`database url is required (--database-url, DATABASE_URL, or "config set database_url")

这个程序需要一个 PostgreSQL 数据库（唯一的外部依赖）。三种方式任选：

  1) 本机装 PostgreSQL（Windows）
       winget install -e --id PostgreSQL.PostgreSQL.16
       createdb -U postgres webhook_rd
       然后：
       $env:DATABASE_URL = 'postgres://postgres:<你的密码>@127.0.0.1:5432/webhook_rd?sslmode=disable'

  2) 用 Docker（不需要本机装数据库）
       docker compose up
       或：docker run -d --name whq-pg -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=webhook_rd -p 5432:5432 postgres:16-alpine

  3) 连已有的 PostgreSQL
       $env:DATABASE_URL = 'postgres://user:pass@host:5432/dbname?sslmode=require'

数据库要自己建（程序只建表、不建库），并且需要两个扩展：
       CREATE EXTENSION IF NOT EXISTS pg_trgm;
       CREATE EXTENSION IF NOT EXISTS pgcrypto;

也可以让脚本帮你做这些：pwsh ./scripts/setup.ps1`)
	}

	pool, err := pgxpool.New(ctx, cmd.options.databaseURL)
	if err != nil {
		return fmt.Errorf("cannot parse database url: %w", err)
	}

	defer pool.Close()

	if err = pool.Ping(ctx); err != nil {
		// Reachability failures are almost always "wrong password / wrong host / database
		// does not exist", none of which the driver error spells out.
		return fmt.Errorf(`cannot reach the database: %w

最常见的原因是这三类，请依次确认：
  · 数据库不存在 —— 程序只建表不建库，请先执行：createdb -U postgres <库名>
  · 密码或用户名不对 —— 检查 DATABASE_URL 里的 user:pass 段
  · 端口或主机不对 —— 本机默认是 127.0.0.1:5432；Docker 里要用服务名而不是 localhost`, err)
	}

	applied, err := migrate.Up(ctx, pool)
	if err != nil {
		return fmt.Errorf("cannot migrate the database: %w", err)
	}

	if len(applied) > 0 {
		log.Info("applied migrations", zap.Strings("versions", applied))
	}

	var cipher *crypto.Cipher

	if cmd.options.encryptKey != "" {
		raw, decErr := base64.StdEncoding.DecodeString(cmd.options.encryptKey)
		if decErr != nil {
			return fmt.Errorf("invalid encrypt key: %w", decErr)
		}

		if cipher, err = crypto.NewCipher(raw); err != nil {
			return fmt.Errorf("cannot create cipher: %w", err)
		}
	} else {
		log.Warn("no encryption key configured: sensitive headers will be masked, " +
			"and HMAC signing secrets cannot be stored")
	}

	store := postgres.New(pool, cipher)

	// Retention only runs when configured; the interval flag is what makes it live.
	retention.Start(ctx, log.Named("retention"), store, settings.RetentionInterval)

	// One bus instance shared by the capture middleware (publisher) and the API
	// (subscriber) - the realtime stream only works if both ends see the same object.
	bus := pubsub.NewInMemory[notify.Message]()

	replaySvc := replay.New(log.Named("replay"), store, replay.Policy{
		Timeout:      settings.ReplayTimeout,
		MaxPreview:   settings.ReplayMaxPreview,
		MaxRedirects: settings.ReplayMaxRedirects,
		MaxRetries:   settings.ReplayMaxRetries,
		Backoff:      settings.ReplayBackoff,
		AllowHosts:   settings.ReplayAllowHosts,
		AllowPrivate: settings.ReplayAllowPrivate,
	})

	api := httpapi.New(httpapi.Deps{
		Log:      log.Named("api"),
		Settings: settings,
		Store:    store,
		Replay:   replaySvc,
		Cipher:   cipher,
		PubSub:   bus,
	})

	dist := web.Dist(cmd.options.useLiveFrontend)
	if web.Stubbed(dist) {
		log.Warn("frontend is not built: the UI will only show a placeholder page (run `make frontend`, or ./dev.ps1 which builds it for you)")
	}

	spa := frontend.New(dist)

	server := apphttp.NewServer(ctx, log.Named("http"),
		apphttp.WithReadTimeout(cmd.options.timeouts.httpRead),
		apphttp.WithWriteTimeout(cmd.options.timeouts.httpWrite),
		apphttp.WithIdleTimeout(cmd.options.timeouts.httpIdle),
	).Register(ctx, apphttp.Deps{
		Log:      log.Named("http"),
		Settings: settings,
		Store:    store,
		PubSub:   bus,
		Cipher:   cipher,
		API:      api,
	}, spa)

	server.ShutdownTimeout = cmd.options.timeouts.shutdown

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cmd.options.addr, cmd.options.port))
	if err != nil {
		// A port clash is one of the first things a newcomer hits, and the raw error is
		// English and says nothing about what to do. Match the DSN failure above: name the
		// cause and hand over a command that works.
		if isAddrInUse(err) {
			// Suggesting the port that is taken would be worse than suggesting none, so
			// go and find one that is actually free.
			return fmt.Errorf(
				"端口 %d 已被占用（%s:%d 上已经有别的进程在监听）。\n"+
					"换一个端口重跑：webhook-zq start --port %d（这个端口现在空闲）\n"+
					"或者查是谁占用了它：netstat -ano | findstr :%d\n"+
					"（Linux / macOS 用：lsof -nP -iTCP:%d -sTCP:LISTEN）\n"+
					"原始错误：%w",
				cmd.options.port, cmd.options.addr, cmd.options.port,
				suggestFreePort(cmd.options.port), cmd.options.port, cmd.options.port, err)
		}

		return fmt.Errorf(
			"无法在 %s:%d 上监听。\n"+
				"可能原因：端口被占用、地址无效、或权限不足。\n"+
				"排查步骤：\n"+
				"  1. 检查端口占用：netstat -ano | findstr :%d\n"+
				"  2. 换一个端口重跑：webhook-zq start --port %d\n"+
				"  3. 检查防火墙是否放行该端口\n"+
				"原始错误：%w",
			cmd.options.addr, cmd.options.port,
			cmd.options.port, suggestFreePort(cmd.options.port), err)
	}

	log.Info("server starting",
		zap.String("version", version.Version()),
		zap.String("address", cmd.options.addr),
		zap.Uint16("port", cmd.options.port),
		zap.String("open", fmt.Sprintf("http://%s:%d", displayHost(cmd.options.addr), cmd.options.port)),
	)

	go func() {
		defer func() { _ = ln.Close() }()

		if serveErr := server.StartHTTP(ctx, ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("http server stopped with an error", zap.Error(serveErr))
		}
	}()

	// The tray icon is this process's face to a non-terminal user: bring it
	// up now that the port is real, unless the tray spawned us (tray ->
	// start -> tray is otherwise a loop with an icon as its only symptom).
	// Failure is silent by design - a missing tray (Linux, CI, a stripped
	// binary) must never take the server down with it.
	if !tray.SpawnedFromTray() {
		tray.SpawnFromServer(cmd.options.port)
	}

	<-ctx.Done()

	if err = ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}

func (cmd *command) buildSettings(file *config.File, provided func(string) bool) (*config.AppSettings, error) {
	s := config.AppSettings{
		PublicURLRoot:      strings.TrimRight(cmd.options.publicURLRoot, "/"),
		MaxRequestBodySize: cmd.options.maxRequestBodySize,
		ReplayTimeout:      cmd.options.replayTimeout,
		ReplayMaxPreview:   cmd.options.replayMaxPreview,
		ReplayMaxRedirects: cmd.options.replayMaxRedirects,
		ReplayMaxRetries:   cmd.options.replayMaxRetries,
		ReplayBackoff:      cmd.options.replayBackoff,
		ReplayAllowHosts:   cmd.options.replayAllowHosts,
		ReplayAllowPrivate: cmd.options.replayAllowPrivate,
		ReplayRateLimit:    cmd.options.replayRateLimit,
		RetentionMaxEvents: cmd.options.retentionMaxEvents,
		RetentionMaxDays:   cmd.options.retentionMaxDays,
		RetentionInterval:  cmd.options.retentionInterval,
		AuthToken:          cmd.options.authToken,
		EncryptKey:         cmd.options.encryptKey,
		TrustProxy:         cmd.options.trustProxyHeader,
		MaxPageSize:        config.DefaultMaxPageSize,
	}

	// The config file fills every field the user did not set explicitly.
	// `provided` is true for both flags and environment variables, so the file
	// never overrides anything the user asked for on this run.
	config.Apply(&s, file, provided)

	if s.MaxRequestBodySize == 0 {
		s.MaxRequestBodySize = config.DefaultMaxRequestBodySize
	}

	if len(cmd.options.authKeys) > 0 {
		s.AuthKeys = make(map[string]string, len(cmd.options.authKeys))

		for _, raw := range cmd.options.authKeys {
			name, key, found := strings.Cut(raw, ":")
			if !found {
				return nil, fmt.Errorf("invalid auth key %q: expected <tenant>:<key>", raw)
			}

			s.AuthKeys[strings.TrimSpace(name)] = strings.TrimSpace(key)
		}
	}

	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	if s.PublicURLRoot != "" {
		u, err := url.Parse(s.PublicURLRoot)
		if err != nil {
			return nil, fmt.Errorf("invalid public url root: %w", err)
		}

		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, errors.New("public url root must use http or https")
		}
	}

	// (allow host / reserved address validation lives in config.Validate so it is covered
	// by the config unit tests as well)
	return &s, nil
}

func displayHost(addr string) string {
	if addr == "0.0.0.0" || addr == "::" || strings.HasPrefix(addr, "127.") {
		return "127.0.0.1"
	}

	return addr
}

// suggestFreePort returns a port near `from` that is actually free, or `from+1` when
// nothing in the small probe range opens. It is only ever used to build a hint, so a
// race - someone taking the port between this call and the user's next command - is
// harmless.
func suggestFreePort(from uint16) uint16 {
	for candidate := int(from) + 1; candidate <= int(from)+20 && candidate <= 65535; candidate++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", candidate))
		if err != nil {
			continue
		}

		_ = ln.Close()

		return uint16(candidate)
	}

	return from + 1
}

// isAddrInUse reports whether a listen failure is "the port is taken" as opposed to
// anything else (a bad address, no permission). Only the first case has a next step worth
// printing, so the two must not be conflated.
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}

	// Windows reports WSAEADDRINUSE (10048) instead of the POSIX EADDRINUSE, so the
	// errors.Is above never matches there. 10048 is stable across every Windows version.
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.Errno(10048)
	}

	return false
}
