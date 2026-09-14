// Package uninstallcmd is the interactive removal counterpart of `install`.
//
// It scans for everything `install` (and the autostart/service wiring) put on
// the machine, shows one list, asks at most two questions, and removes the
// rest in dependency order: stop processes before deleting files, drop the
// database only after the services using it are gone. The database is never
// removed by default - it is the user's captured data, and "uninstall the
// program" must not mean "delete my data".
package uninstallcmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/urfave/cli/v3"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/config"
)

// defaultDBName is the database name this app is documented to use.
const defaultDBName = "webhook_rd"

// item is one thing the uninstaller can remove.
type item struct {
	label  string
	detail string
	remove func() error
}

// NewCommand builds the `uninstall` command.
func NewCommand(log *zap.Logger) *cli.Command {
	return &cli.Command{
		Name:  "uninstall",
		Usage: "交互式卸载：扫描并移除程序文件、服务、自启与注册项（数据库默认保留）",
		Description: "移除 webhook-zq 的全部本机痕迹。\n" +
			"默认只删程序本身；数据库包含已捕获的数据，需要单独确认才会删除。\n" +
			"非交互场景（脚本）用 --yes；此时数据库仅在显式给 --drop-database 时才会删除。",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "yes",
				Usage: "跳过所有确认，按默认执行（不删数据库）",
			},
			&cli.BoolFlag{
				Name:  "drop-database",
				Usage: "允许删除数据库（交互模式仍需输入 yes 二次确认）",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return run(ctx, c.Bool("yes"), c.Bool("drop-database"))
		},
	}
}

func run(ctx context.Context, yes, allowDrop bool) error {
	items := platformItems()

	dbItem := scanDatabase(ctx)
	if dbItem != nil {
		items = append(items, *dbItem)
	}

	fileItems := scanFiles()
	items = append(items, fileItems...)

	if len(items) == 0 {
		fmt.Println("未发现任何已安装内容，无需卸载。")

		return nil
	}

	fmt.Println("webhook-zq 卸载")
	fmt.Println("")
	fmt.Println("发现以下内容：")

	for i, it := range items {
		fmt.Printf("  %d. %s  %s\n", i+1, it.label, it.detail)
	}

	fmt.Println("")

	// One buffered reader for the whole run - see prompts for why a fresh
	// reader per question breaks piped input.
	p := newPrompts(os.Stdin, os.Stdout)

	if !yes {
		ok, err := p.confirm("以上内容将被移除，继续？", true)
		if err != nil {
			return err
		}

		if !ok {
			fmt.Println("已取消，未做任何修改。")

			return nil
		}
	}

	// The database rides in `items` for display purposes but is gated by its
	// own confirmation below, so its removal decision is tracked separately.
	dropDB := false
	if dbItem != nil {
		var err error

		dropDB, err = confirmDropDatabase(p, dbItem, yes, allowDrop)
		if err != nil {
			return err
		}
	}

	var failed int

	for _, it := range items {
		if it.label == "数据库" && !dropDB {
			fmt.Println("  跳过  数据库（已保留）")

			continue
		}

		if err := it.remove(); err != nil {
			failed++

			fmt.Printf("  失败  %s: %v\n", it.label, err)
		} else {
			fmt.Printf("  完成  %s\n", it.label)
		}
	}

	fmt.Println("")

	if failed > 0 {
		fmt.Printf("%d 项失败，其余已完成。\n", failed)

		return fmt.Errorf("%d removal step(s) failed", failed)
	}

	fmt.Println("卸载完成。")

	if dbItem != nil && !dropDB {
		fmt.Println("数据库已保留（如需删除：webhook-zq uninstall --drop-database）。")
	}

	return nil
}

// confirmDropDatabase owns the two-step gate around the database: asked only
// when the user allowed it, off by default, and additionally requiring the
// literal word yes so a stray Enter can never drop captured data.
func confirmDropDatabase(p *prompts, dbItem *item, yes, allowDrop bool) (bool, error) {
	if !allowDrop {
		return false, nil
	}

	if yes {
		return true, nil
	}

	ok, err := p.confirm("同时删除"+dbItem.detail+"（不可恢复）？", false)
	if err != nil {
		return false, err
	}

	if !ok {
		return false, nil
	}

	return p.askWord("请输入 yes 确认删除数据库", "yes")
}

// scanDatabase reports the app database when one is configured and reachable.
// An unreachable database is not an error worth blocking the uninstall over:
// it is reported as a note, and the files/services are still removed.
func scanDatabase(ctx context.Context) *item {
	dsn, ok := databaseURLFromConfig()
	if !ok {
		return nil
	}

	db, maint, err := maintenanceDSN(dsn)
	if err != nil {
		return nil
	}

	conn, err := pgx.Connect(ctx, maint)
	if err != nil {
		fmt.Printf("注意：数据库当前不可达（%v），未包含在卸载内容中。\n", err)

		return nil
	}

	defer func() { _ = conn.Close(ctx) }()

	var exists bool

	if err := conn.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, db,
	).Scan(&exists); err != nil || !exists {
		return nil
	}

	detail := fmt.Sprintf("数据库 %s", db)
	if counts, err := tableCounts(ctx, dsn); err == nil {
		detail = fmt.Sprintf("数据库 %s（%s）", db, counts)
	}

	return &item{
		label:  "数据库",
		detail: detail,
		remove: func() error { return dropDatabase(ctx, maint, db) },
	}
}

// tableCounts connects to the app database itself (cross-database queries are
// not a thing in PostgreSQL) and summarizes what would be lost.
func tableCounts(ctx context.Context, dsn string) (string, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", err
	}

	defer func() { _ = conn.Close(ctx) }()

	var inboxes, events, replays int

	err = conn.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM inbox),
		       (SELECT count(*) FROM event),
		       (SELECT count(*) FROM replay_attempt)`).
		Scan(&inboxes, &events, &replays)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%d 收件箱 / %d 事件 / %d 重放", inboxes, events, replays), nil
}

// dropDatabase terminates the app's connections first so the DROP cannot fail
// on an in-use database, then drops it. Identifiers cannot be parameterized,
// so the name is validated against a strict allow-list before interpolation.
func dropDatabase(ctx context.Context, maint, db string) error {
	if !validIdent(db) {
		return fmt.Errorf("database name %q is not a plain identifier; drop it manually", db)
	}

	conn, err := pgx.Connect(ctx, maint)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close(ctx) }()

	if _, err := conn.Exec(ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		 WHERE datname = $1 AND pid <> pg_backend_pid()`, db); err != nil {
		return err
	}

	if _, err := conn.Exec(ctx, "DROP DATABASE "+db); err != nil {
		return err
	}

	return nil
}

var identRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func validIdent(s string) bool { return identRe.MatchString(s) }

// databaseURLFromConfig reads the DSN the app actually runs with: the config
// file first, then the DATABASE_URL environment variable `start` also accepts.
func databaseURLFromConfig() (string, bool) {
	path, err := config.DefaultPath()
	if err == nil {
		if f, _, lerr := config.Load(path); lerr == nil && f != nil && f.DatabaseURL != "" {
			return f.DatabaseURL, true
		}
	}

	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn, true
	}

	return "", false
}

// maintenanceDSN rewrites a DSN to point at the maintenance database (postgres)
// and returns both the original database name and the rewritten DSN.
//
// Both spellings pgx accepts are handled: URL form
// (postgres://user:pwd@host:5432/webhook_rd) and keyword form
// (host=... dbname=webhook_rd ...).
func maintenanceDSN(dsn string) (db, maint string, err error) {
	if u, perr := url.Parse(dsn); perr == nil &&
		(u.Scheme == "postgres" || u.Scheme == "postgresql") {
		db = strings.TrimPrefix(u.Path, "/")
		if db == "" {
			db = defaultDBName
		}

		u.Path = "/postgres"

		return db, u.String(), nil
	}

	db = defaultDBName

	if m := dbnameRe.FindStringSubmatch(dsn); m != nil {
		db = m[2]
	}

	maint = dbnameRe.ReplaceAllString(dsn, "${1}dbname=postgres")
	if !dbnameRe.MatchString(dsn) {
		maint = dsn + " dbname=postgres"
	}

	return db, maint, nil
}

var dbnameRe = regexp.MustCompile(`(^|\s)dbname=([^\s]+)`)

// prompts owns the input stream for one interactive run.
//
// A fresh bufio.Reader per line silently swallows input when stdin is
// redirected: the first ReadString buffers ahead everything the pipe
// currently holds, that reader (and its buffer) is then discarded, and the
// next question reads from whatever follows the discarded buffer - for
// `echo yes | webhook-zq uninstall --drop-database` that means the second
// gate reads EOF and aborts. A terminal is unaffected either way (a tty
// delivers one line at a time), which is exactly why the bug lived unnoticed.
type prompts struct {
	in  *bufio.Reader
	out io.Writer
}

func newPrompts(in io.Reader, out io.Writer) *prompts {
	return &prompts{in: bufio.NewReader(in), out: out}
}

// confirm asks a yes/no question. An empty line selects the default; anything
// unreadable (EOF, closed pipe) is an error rather than a silent default, so
// piping a script into the interactive flow cannot accidentally delete
// anything.
func (p *prompts) confirm(question string, defYes bool) (bool, error) {
	hint := "[y/N]"
	if defYes {
		hint = "[Y/n]"
	}

	fmt.Fprintf(p.out, "%s %s: ", question, hint)

	line, err := p.readLine()
	if err != nil {
		return false, fmt.Errorf("无法读取确认输入（脚本场景请加 --yes）: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return defYes, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// askWord requires the user to type an exact word - the second gate in front
// of irreversible actions.
func (p *prompts) askWord(question, want string) (bool, error) {
	fmt.Fprintf(p.out, "%s: ", question)

	line, err := p.readLine()
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(line) == want, nil
}

func (p *prompts) readLine() (string, error) {
	line, err := p.in.ReadString('\n')

	return strings.TrimRight(line, "\r\n"), err
}

// trayScripts are the PowerShell entry points that keep a desktop presence
// alive. tray.ps1 polls the server and restarts it within 500ms, so an
// uninstaller that only kills webhook-zq.exe reports success while the service
// immediately comes back to life.
var trayScripts = []string{"tray.ps1", "server-manager.ps1", "start-gui.ps1"}

// psProcessRow mirrors one object of the PowerShell JSON produced by
// `Select-Object ProcessId,CommandLine | ConvertTo-Json`.
type psProcessRow struct {
	ProcessID   uint32  `json:"ProcessId"`
	CommandLine *string `json:"CommandLine"`
}

// parsePSProcessList accepts the JSON ConvertTo-Json emits: an array for
// several rows, a bare object for exactly one (Windows PowerShell 5.1 has no
// -AsArray switch), or "null" for none.
func parsePSProcessList(data []byte) []psProcessRow {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	if trimmed[0] == '{' {
		trimmed = append([]byte{'['}, append(trimmed, ']')...)
	}

	var rows []psProcessRow
	if err := json.Unmarshal(trimmed, &rows); err != nil {
		return nil
	}

	return rows
}

// isTrayCommandLine reports whether a PowerShell command line runs one of our
// desktop scripts. The "webhook-zq" substring is required as well: tray.ps1
// alone is a plausible file name in any other project, and killing some other
// project's window during an uninstall would be worse than missing ours.
func isTrayCommandLine(cmdline string) bool {
	s := strings.ToLower(cmdline)
	if !strings.Contains(s, "webhook-zq") {
		return false
	}

	for _, script := range trayScripts {
		if strings.Contains(s, script) {
			return true
		}
	}

	return false
}
