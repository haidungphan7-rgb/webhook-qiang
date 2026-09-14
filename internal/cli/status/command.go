// Package status answers one question that every script and window kept asking in its own
// way: "what is this instance actually running with, right now?"
//
// The tray, the manager window, the service script, the graphical launcher and both
// uninstallers each had their own port probe, their own health check and their own idea of
// "running". They drifted. This command is the single answer; the PowerShell side is
// eventually supposed to render it rather than re-derive it.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/version"
	"github.com/yuandzhang/webhook-zq/web"
)

// Result is the machine readable answer. Kept flat and additive: new fields are fine,
// removing or renaming one breaks every window that renders it.
type Result struct {
	Running           bool   `json:"running"`
	Pid               int    `json:"pid"`
	Port              uint16 `json:"port"`
	Healthy           bool   `json:"healthy"`
	ExePath           string `json:"exe_path"`
	ConfigPath        string `json:"config_path"`
	DSNMasked         string `json:"dsn_masked"`
	DatabaseReachable bool   `json:"database_reachable"`
	FrontendEmbedded  bool   `json:"frontend_embedded"`
	Version           string `json:"version"`
	BuildTime         string `json:"build_time"`
	LogPath           string `json:"log_path"`
}

// NewCommand builds the `status` command.
func NewCommand(log *zap.Logger) *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Report what this instance is running with (use --json for scripts)",
		Flags: []cli.Flag{
			&cli.UintFlag{
				Name:    "port",
				Usage:   "Port to probe",
				Value:   8080,
				Sources: cli.EnvVars("HTTP_PORT"),
			},
			&cli.StringFlag{
				Name:    "database-url",
				Usage:   "Connection string, reported masked",
				Sources: cli.EnvVars("DATABASE_URL"),
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "Print one JSON object instead of a table",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Uint("port") > math.MaxUint16 {
				return fmt.Errorf("--port %d is out of range (0-65535)", c.Uint("port"))
			}

			// #nosec G115 -- bounded by the explicit check above.
			port := uint16(c.Uint("port"))

			// The config file is the layer between the environment and the defaults, so a
			// configured port wins over the flag default when the flag was not given.
			if !c.IsSet("port") {
				if path, err := config.DefaultPath(); err == nil {
					if f, _, err := config.Load(path); err == nil && f.Port > 0 {
						port = f.Port
					}
				}
			}

			dsn := c.String("database-url")

			res := Result{
				Port:              port,
				ConfigPath:        configPath(),
				DSNMasked:         config.MaskDSN(dsn),
				DatabaseReachable: databaseReachable(dsn),
				FrontendEmbedded:  frontendEmbedded(),
				Version:           version.Version(),
				BuildTime:         version.BuildTime(),
				LogPath:           filepath.Join("logs", "server.log"),
			}

			if pid := pidListeningOn(port); pid > 0 {
				res.Running = true
				res.Pid = pid
				res.ExePath = processPath(pid)
				res.Healthy = healthy(port)
			}

			if c.Bool("json") {
				enc := json.NewEncoder(c.Root().Writer)
				enc.SetIndent("", "  ")

				if err := enc.Encode(res); err != nil {
					return err
				}

				return exitCodeFor(&res)
			}

			printTable(c, &res)

			return exitCodeFor(&res)
		},
	}
}

// exitCodeFor is the script-facing half of status: the printed report is for
// humans, the exit code is for everything else (the tray's doctor menu item,
// the acceptance scripts, any wrapper). The mapping is a published contract:
//
//	0  running and healthy
//	3  running but /healthz failed (half-dead service)
//	4  not running
//
// Message stays empty on purpose: the report above is the message.
func exitCodeFor(r *Result) error {
	switch {
	case !r.Running:
		return cli.Exit("", 4)
	case !r.Healthy:
		return cli.Exit("", 3)
	default:
		return nil
	}
}

func printTable(c *cli.Command, r *Result) {
	w := c.Root().Writer

	row := func(name, value string) { fmt.Fprintf(w, "  %-14s %s\n", name, value) }

	fmt.Fprintln(w, "")
	row("版本", r.Version)
	row("构建时间", r.BuildTime)
	row("端口", strconv.Itoa(int(r.Port)))

	if r.Running {
		state := "运行中"
		if r.Healthy {
			state += "，健康检查通过"
		} else {
			state += "，但健康检查失败"
		}

		row("服务", fmt.Sprintf("%s（pid %d）", state, r.Pid))
	} else {
		row("服务", "未运行")
	}

	if r.ExePath != "" {
		row("程序", r.ExePath)
	}

	row("数据库", boolText(r.DatabaseReachable))
	row("前端", boolText(r.FrontendEmbedded))
	row("配置文件", r.ConfigPath)
	row("日志", r.LogPath)
	fmt.Fprintln(w, "")
}

func boolText(ok bool) string {
	if ok {
		return "可用"
	}

	return "不可用"
}

func healthy(port uint16) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil)
	if err != nil {
		return false
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}

	defer res.Body.Close()

	return res.StatusCode == http.StatusOK
}

// databaseReachable dials the host in the DSN rather than opening a real connection: a
// status probe must not need correct credentials, only to know whether something is
// listening there.
func databaseReachable(dsn string) bool {
	if dsn == "" {
		return false
	}

	host, port := dsnHostPort(dsn)
	if host == "" {
		return false
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 2*time.Second)
	if err != nil {
		return false
	}

	_ = conn.Close()

	return true
}

func dsnHostPort(dsn string) (string, string) {
	host, port := "127.0.0.1", "5432"

	u, err := url.Parse(dsn)
	if err == nil && u.Host != "" {
		host = u.Hostname()
		if p := u.Port(); p != "" {
			port = p
		}

		return host, port
	}

	// Not a URL (libpq keywords such as "host=... port=...").
	for _, field := range strings.Fields(dsn) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}

		switch strings.TrimSpace(k) {
		case "host":
			host = strings.TrimSpace(v)
		case "port":
			port = strings.TrimSpace(v)
		}
	}

	return host, port
}

func frontendEmbedded() bool {
	// The UI is compiled into the binary; if it is still the generated placeholder the
	// answer for "does the interface work" is no.
	return !web.Stubbed(web.Dist(false))
}

func configPath() string {
	p, err := config.DefaultPath()
	if err != nil {
		return ""
	}

	return p
}

// pidListeningOn finds the process listening on a port. There is no portable way to do
// this in the standard library, so each platform gets its own shell out. A failure is not
// an error: callers fall back to "not running".
func pidListeningOn(port uint16) int {
	var (
		out []byte
		err error
	)

	switch runtime.GOOS {
	case "windows":
		// #nosec G204 -- fixed command, no interpolated arguments.
		out, err = exec.Command("netstat", "-ano", "-p", "TCP").Output()
	case "darwin":
		// #nosec G204 -- fixed command; the only variable is the validated port number.
		out, err = exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(int(port)), "-sTCP:LISTEN", "-t").Output()
	case "linux":
		// #nosec G204 -- fixed command; the only variable is the validated port number.
		out, err = exec.Command("ss", "-ltnpH",
			fmt.Sprintf("sport = :%d", port)).Output()
	default:
		return 0
	}

	if err != nil {
		return 0
	}

	return parsePid(runtime.GOOS, string(out), port)
}

func parsePid(goos, out string, port uint16) int {
	want := fmt.Sprintf(":%d", port)

	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		switch goos {
		case "windows":
			// Proto LocalAddress ForeignAddress State PID
			if len(fields) < 5 {
				continue
			}

			if !strings.EqualFold(fields[3], "LISTENING") {
				continue
			}

			if !strings.HasSuffix(fields[1], want) {
				continue
			}

			if pid, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
				return pid
			}

		case "darwin":
			// -t prints just the pid.
			if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
				return pid
			}

		case "linux":
			// ss -ltnpH: State Recv-Q Send-Q Local:Port Peer:Port Process
			if !strings.Contains(line, want) {
				continue
			}

			if i := strings.Index(line, "pid="); i >= 0 {
				rest := line[i+4:]
				end := strings.IndexAny(rest, ",)")

				if end > 0 {
					rest = rest[:end]
				}

				if pid, err := strconv.Atoi(rest); err == nil {
					return pid
				}
			}
		}
	}

	return 0
}

func processPath(pid int) string {
	if runtime.GOOS == "windows" {
		// #nosec G204 -- fixed command; the pid comes from the local port table.
		out, err := exec.Command("wmic", "process", "where",
			fmt.Sprintf("ProcessId=%d", pid), "get", "ExecutablePath", "/value").Output()
		if err != nil {
			return ""
		}

		for _, line := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecutablePath="); ok {
				return strings.TrimSpace(v)
			}
		}
	}

	// Linux and macOS: /proc or lsof, neither of which is worth the extra code here -
	// the path is only used to prove the process is ours.
	if p, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		return p
	}

	return ""
}
