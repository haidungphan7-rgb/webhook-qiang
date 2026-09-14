// Package installcmd turns the downloaded exe into an installed program.
//
// The release download is a self-contained binary, so installing is not about
// copying files the app needs - it is about making the app manageable like any
// other Windows program: an entry in "Apps & features" (which is what makes
// uninstalling feel native), and optionally a logon task for autostart.
// Everything lands under the per-user config directory, so no elevation is
// ever required.
package installcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/urfave/cli/v3"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/version"
)

// NewCommand builds the `install` command.
func NewCommand(log *zap.Logger) *cli.Command {
	return &cli.Command{
		Name:  "install",
		Usage: "安装到当前用户（应用列表、可选开机自启），无需管理员权限",
		Description: "把当前运行的二进制复制到用户目录并注册到 Windows \"应用和功能\"。\n" +
			"之后可在 \"设置 → 应用\" 中像普通程序一样卸载，或运行 webhook-zq uninstall。",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "autostart",
				Usage: "同时注册登录时自启（用户级计划任务，运行 start）",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return run(c.Bool("autostart"))
		},
	}
}

func run(autostart bool) error {
	src, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate the running binary: %w", err)
	}

	dst, err := installTarget()
	if err != nil {
		return err
	}

	if samePath(src, dst) {
		fmt.Println("当前已在安装位置运行，跳过复制。")
	} else if err := copyExecutable(src, dst); err != nil {
		return err
	}

	fmt.Printf("已安装到 %s\n", dst)

	if err := registerAppList(dst, version.Version()); err != nil {
		return err
	}

	fmt.Println(`已注册到 "设置 → 应用 → 安装的应用"（搜索 webhook-zq 可见）。`)

	// The logon task runs `start` with no environment of its own, and `start`
	// reads the config file as the layer between the environment and the
	// defaults - so persisting the DSN now is what makes autostart work.
	captureDatabaseURL()

	if autostart {
		if err := enableAutostart(dst); err != nil {
			return err
		}
	}

	fmt.Println("")
	fmt.Println("接下来：")

	if !databaseURLConfigured() {
		fmt.Println("  配置库  webhook-zq config set database_url=postgres://user:pwd@127.0.0.1:5432/webhook_rd")
		fmt.Println(`          （start 需要一个 PostgreSQL；未配置时启动会给出三种安装方式）`)
	}

	fmt.Println("  启动    webhook-zq start     然后打开 http://localhost:8080")
	fmt.Println("  卸载    webhook-zq uninstall（或从 \"设置 → 应用\" 里卸载）")

	return nil
}

// copyExecutable duplicates the binary, preserving the executable bit.
func copyExecutable(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("cannot create the install directory: %w", err)
	}

	in, err := os.Open(src) // #nosec G304 -- the path is the binary we are running from.
	if err != nil {
		return fmt.Errorf("cannot open the running binary: %w", err)
	}

	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("cannot create the installed binary: %w", err)
	}

	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("cannot copy the binary: %w", err)
	}

	return nil
}

// samePath reports whether two paths point at the same file. os.SameFile needs
// both to exist, so on a fresh install the fallback compares absolute paths
// (case-insensitively on Windows, where the same file can be reached as
// C:\a\b and c:\A\B).
func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)

	if errA != nil || errB != nil {
		return false
	}

	if runtime.GOOS == "windows" {
		return strings.EqualFold(absA, absB)
	}

	return absA == absB
}

// captureDatabaseURL persists the DATABASE_URL of the installing shell into
// the config file, so the autostart task (and any future bare `start`) has a
// database url even though the scheduled environment is empty. An existing,
// different value in the file is never overwritten: the file is the more
// deliberate choice, this is only a convenience capture.
func captureDatabaseURL() {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return
	}

	path, err := config.DefaultPath()
	if err != nil {
		return
	}

	f, _, err := config.Load(path)
	if err != nil || f == nil {
		return
	}

	if f.DatabaseURL != "" {
		if f.DatabaseURL != dsn {
			fmt.Println("注意：配置文件里已有另一个 database_url，未用当前环境变量覆盖。")
		}

		return
	}

	f.DatabaseURL = dsn

	if err := config.Save(path, f); err != nil {
		fmt.Printf("注意：无法写入 %s（%v），开机自启可能起不来。\n", path, err)
		return
	}

	fmt.Printf("已把当前 DATABASE_URL 记入 %s（自启任务会从这里读取）。\n", path)
}

// databaseURLConfigured reports whether `start` would find a database url in
// the environment or the config file, for the post-install hints.
func databaseURLConfigured() bool {
	if strings.TrimSpace(os.Getenv("DATABASE_URL")) != "" {
		return true
	}

	path, err := config.DefaultPath()
	if err != nil {
		return false
	}

	f, _, err := config.Load(path)

	return err == nil && f != nil && f.DatabaseURL != ""
}
