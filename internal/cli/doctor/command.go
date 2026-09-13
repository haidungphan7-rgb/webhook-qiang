// Package doctor answers "what is missing and what do I do next".
//
// It exists because the platform's only external dependency is PostgreSQL, and every
// setup problem therefore shows up as a connection error that does not say which of the
// half dozen possible causes it was. doctor reports and points; it never changes
// anything - creating a database or installing an extension is the operator's decision.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/migrate"
	"github.com/yuandzhang/webhook-zq/web"
)

// problem carries one failed check together with the command that fixes it.
type problem struct {
	title string
	fix   []string
}

func NewCommand(log *zap.Logger) *cli.Command {
	var (
		dsn  string
		port uint
	)

	return &cli.Command{
		Name:  "doctor",
		Usage: "Check the environment and print the next command",
		Description: "Checks the database connection, the required extensions, the schema " +
			"version, the embedded frontend and the HTTP port. It changes nothing - it only " +
			"reports what is missing and what to run next.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "database-url",
				Usage:       "PostgreSQL DSN (postgres://user:pwd@host:5432/db)",
				Sources:     cli.EnvVars("DATABASE_URL"),
				OnlyOnce:    true,
				Config:      cli.StringConfig{TrimSpace: true},
				Destination: &dsn,
			},
			&cli.UintFlag{
				Name:        "port",
				Usage:       "port the server would listen on",
				Value:       8080,
				Sources:     cli.EnvVars("HTTP_PORT"),
				OnlyOnce:    true,
				Destination: &port,
			},
		},
		Action: func(ctx context.Context, _ *cli.Command) error {
			return run(ctx, log, dsn, port)
		},
	}
}

func run(ctx context.Context, log *zap.Logger, dsn string, port uint) error {
	var problems []problem

	ok := func(title string) { fmt.Printf("  ok    %s\n", title) }
	bad := func(p problem) {
		fmt.Printf("  FAIL  %s\n", p.title)
		for _, line := range p.fix {
			fmt.Printf("        %s\n", line)
		}

		problems = append(problems, p)
	}

	fmt.Println("webhook-zq doctor")
	fmt.Println()

	// ── 1. is a database configured at all ──────────────────────────────────
	fmt.Println("[1/6] 数据库配置")

	if dsn == "" {
		bad(problem{
			title: "DATABASE_URL 未设置",
			fix: []string{
				"最快的路（有 Docker，不用装数据库）：",
				"    docker run -d --name whq-pg -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=webhook_rd -p 5432:5432 postgres:16-alpine",
				"    set DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable",
				"",
				"本机已装 PostgreSQL：",
				"    createdb webhook_rd",
				"    set DATABASE_URL=postgres://postgres:<你的密码>@127.0.0.1:5432/webhook_rd?sslmode=disable",
			},
		})

		fmt.Println()
		fmt.Println("数据库是唯一的外部依赖，没有它什么都做不了。先配好 DATABASE_URL 再跑一次 doctor。")

		return errors.New("DATABASE_URL is not set")
	}

	ok("DATABASE_URL 已设置")

	// ── 2. can we connect, and if not, why ──────────────────────────────────
	fmt.Println()
	fmt.Println("[2/6] 数据库连接")

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		bad(problem{
			title: "DATABASE_URL 无法解析：" + err.Error(),
			fix:   []string{"期望格式：postgres://user:password@host:5432/dbname?sslmode=disable"},
		})

		return errors.New("cannot parse DATABASE_URL")
	}

	defer pool.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err = pool.Ping(pingCtx); err != nil {
		bad(classifyConnectError(err))

		return errors.New("cannot reach the database")
	}

	ok("可以连上数据库")

	// ── 3. extensions ───────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("[3/6] 所需扩展")

	for _, ext := range []string{"pg_trgm", "pgcrypto"} {
		var installed bool

		err = pool.QueryRow(ctx,
			"select exists (select 1 from pg_extension where extname = $1)", ext).Scan(&installed)

		if err != nil || !installed {
			bad(problem{
				title: fmt.Sprintf("缺少扩展 %s", ext),
				fix: []string{
					fmt.Sprintf("    psql \"$DATABASE_URL\" -c \"CREATE EXTENSION IF NOT EXISTS %s;\"", ext),
					"  （非属主账号需要让 DBA 执行）",
				},
			})

			continue
		}

		ok("扩展 " + ext)
	}

	// ── 4. schema ───────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("[4/6] 表结构")

	if err = migrate.Check(ctx, pool); err != nil {
		bad(problem{
			title: "表结构不是最新版本：" + err.Error(),
			fix:   []string{"    webhook-zq migrate --database-url \"$DATABASE_URL\""},
		})
	} else {
		ok("表结构已是最新（启动也会自动迁移）")
	}

	// ── 5. frontend ─────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("[5/6] 前端资源")

	if web.Stubbed(web.Dist(false)) {
		bad(problem{
			title: "这个二进制里没有打包前端（打开浏览器会是提示页）",
			fix: []string{
				"    npm --prefix ./web ci && npm --prefix ./web run build",
				"    然后重新构建二进制：go build -o webhook-zq ./cmd/webhook-tester",
				"  （API 与 /hooks/<token> 不受影响，现在就能用）",
			},
		})
	} else {
		ok("前端已内嵌")
	}

	// ── 6. port ─────────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("[6/6] 端口")

	addr := fmt.Sprintf(":%d", port)

	if l, listenErr := net.Listen("tcp", addr); listenErr != nil {
		bad(problem{
			title: fmt.Sprintf("端口 %d 已被占用", port),
			fix: []string{
				fmt.Sprintf("    webhook-zq start --port %d", port+1),
				"  或先停掉占用该端口的程序",
			},
		})
	} else {
		_ = l.Close()
		ok(fmt.Sprintf("端口 %d 可用", port))
	}

	// ── summary ─────────────────────────────────────────────────────────────
	fmt.Println()

	if len(problems) == 0 {
		fmt.Println("一切就绪。下一步：")
		fmt.Printf("    webhook-zq start --port %d\n", port)
		fmt.Println()
		fmt.Println("然后打开 http://127.0.0.1:" + fmt.Sprint(port) + " 。想验证链路是否真的通：")
		fmt.Println("    make accept")

		return nil
	}

	fmt.Printf("有 %d 项需要处理，按上面的命令依次解决，然后再跑一次 doctor。\n", len(problems))

	// Non zero exit so scripts can act on it, but no error text: the report above is
	// the message.
	return cli.Exit("", 1)
}

// classifyConnectError turns a driver error into the cause and the command that fixes
// it. It matches on SQLSTATE rather than on message text, which changes between
// versions and languages.
func classifyConnectError(err error) problem {
	var pgErr *pgconn.PgError

	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "3D000": // invalid_catalog_name
			return problem{
				title: "数据库不存在（程序只建表，不建库）",
				fix: []string{
					"    createdb -U postgres <库名>",
					"  或在已有的库里执行：psql \"$DATABASE_URL\" -c 'select 1'",
				},
			}
		case "28P01", "28000": // invalid_password / invalid_authorization_specification
			return problem{
				title: "用户名或密码不对",
				fix: []string{
					"  检查 DATABASE_URL 里的 user:password 段",
					"  重设密码：psql -U postgres -c \"ALTER USER postgres PASSWORD 'newpass'\"",
				},
			}
		case "42501": // insufficient_privilege
			return problem{
				title: "账号权限不足（建表和装扩展都需要属主或显式授权）",
				fix: []string{
					"  让 DBA 预先执行：CREATE EXTENSION IF NOT EXISTS pg_trgm; CREATE EXTENSION IF NOT EXISTS pgcrypto;",
				},
			}
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return problem{
			title: "连接超时——主机或端口不对，或者被防火墙挡了",
			fix: []string{
				"  本机 PostgreSQL 默认是 127.0.0.1:5432",
				"  在 Docker 里运行时要用服务名，不能用 localhost",
			},
		}
	}

	return problem{
		title: "无法连接：" + err.Error(),
		fix: []string{
			"  常见原因：库不存在 / 密码不对 / 主机端口不对",
			"  确认 PostgreSQL 正在运行，然后重跑：webhook-zq doctor",
		},
	}
}

var _ = os.Stdout
