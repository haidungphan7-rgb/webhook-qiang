// Package traycmd is the hidden `tray` subcommand: the entry point of the
// Windows tray icon, plus the control flags scripts use to talk to a running
// one. Hidden because humans get the icon; this command exists for Task
// Scheduler, the service's spawn hook and the uninstaller.
//
// Exit codes are a contract (the acceptance plan asserts on them):
//
//	tray --exit        0 exited, 1 no tray running, 2 acknowledged but stuck
//	tray --status      0 running, 1 not running
package traycmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v3"

	uninstallcmd "github.com/yuandzhang/webhook-zq/internal/cli/uninstall"
	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/tray"
)

// ackTimeout is how long --exit and --uninstall wait for the tray's exit
// acknowledgement (it releases the single-instance mutex last, after the
// service is gone). Five seconds is generous for a clean shutdown and short
// enough that a stuck tray fails fast.
const ackTimeout = 5 * time.Second

// NewCommand builds the hidden `tray` command.
func NewCommand() *cli.Command {
	return &cli.Command{
		Name:   "tray",
		Hidden: true,
		Usage:  "Windows 托盘图标（内部命令，由计划任务与服务自动调用）",
		Description: "无参数时启动托盘图标并接管服务。" +
			"--exit / --status / --uninstall 用于脚本与卸载流程控制一个正在运行的托盘。",
		Flags: []cli.Flag{
			&cli.UintFlag{
				Name:    "port",
				Usage:   "服务端口",
				Value:   8080,
				Sources: cli.EnvVars("HTTP_PORT"),
			},
			&cli.BoolFlag{
				Name:  "exit",
				Usage: "通知正在运行的托盘退出（0 已退出 / 1 未在运行 / 2 未在限期内退出）",
			},
			&cli.BoolFlag{
				Name:  "status",
				Usage: "查询托盘是否在运行（0 运行中 / 1 未运行）",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "配合 --status，输出 JSON（tray_running 字段）",
			},
			&cli.BoolFlag{
				Name:  "uninstall",
				Usage: "让托盘退出后在本进程内运行交互式卸载",
			},
		},
		Action: run,
	}
}

func run(ctx context.Context, c *cli.Command) error {
	n := 0

	for _, b := range []bool{c.Bool("exit"), c.Bool("status"), c.Bool("uninstall")} {
		if b {
			n++
		}
	}

	if n > 1 {
		return errors.New("--exit、--status、--uninstall 一次只能给一个")
	}

	switch {
	case c.Bool("exit"):
		return requestExit(c)
	case c.Bool("status"):
		return printStatus(c)
	case c.Bool("uninstall"):
		return runUninstall(ctx, c)
	default:
		return startTray(c)
	}
}

// startTray is the Task Scheduler path: run the icon until it quits.
func startTray(c *cli.Command) error {
	port, err := resolvePort(c)
	if err != nil {
		return err
	}

	err = tray.Run(tray.Options{Port: port})
	if errors.Is(err, tray.ErrAlreadyRunning) {
		// The surviving instance already opened the interface; this one's
		// job is done. Silent success, never a second icon.
		return nil
	}

	return err
}

// requestExit signals, then waits for the mutex release that only happens
// once the tray's shutdown sequence is complete.
func requestExit(c *cli.Command) error {
	switch err := tray.RequestExit(); {
	case errors.Is(err, tray.ErrNoTray):
		return cli.Exit(errors.New("托盘未在运行"), 1)
	case err != nil:
		return fmt.Errorf("通知托盘退出失败：%w", err)
	}

	if !tray.WaitExited(ackTimeout) {
		// 现状 -> 原因 -> 下一步, in the format scripts can print verbatim.
		return cli.Exit(fmt.Errorf(
			"已通知托盘退出，但 %s 内未完成。\n可能原因：托盘进程卡死。\n下一步：%s",
			ackTimeout, forceKillHint()), 2)
	}

	fmt.Fprintln(c.Root().Writer, "托盘已退出")

	return nil
}

// forceKillHint names the executable the user is actually running, not the
// one the build scripts produce.
func forceKillHint() string {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}

	return fmt.Sprintf("taskkill /IM %s /F（强制结束全部相关进程）", filepath.Base(exe))
}

func printStatus(c *cli.Command) error {
	running := tray.IsRunning()

	if c.Bool("json") {
		return json.NewEncoder(c.Root().Writer).Encode(map[string]bool{
			"tray_running": running,
		})
	}

	if running {
		fmt.Fprintln(c.Root().Writer, "托盘运行中")

		return nil
	}

	fmt.Fprintln(c.Root().Writer, "托盘未运行")

	return cli.Exit("", 1)
}

// runUninstall is the menu's uninstall path: ask the tray to leave (its
// post-Run cleanup stops the service), tolerate a stuck one - the uninstall
// sweep is the backstop - then run the real interactive uninstall here.
func runUninstall(ctx context.Context, c *cli.Command) error {
	if err := tray.RequestExit(); err == nil {
		_ = tray.WaitExited(ackTimeout) // a stuck tray does not block the sweep
	}

	return uninstallcmd.Run(ctx, false, false)
}

// resolvePort follows the same precedence as `start`: flag, then env (via
// the flag's Sources), then the config file, then the 8080 default.
func resolvePort(c *cli.Command) (uint16, error) {
	if c.Uint("port") > math.MaxUint16 {
		return 0, fmt.Errorf("--port %d 超出范围（0-65535）", c.Uint("port"))
	}

	// #nosec G115 -- bounded by the explicit check above.
	port := uint16(c.Uint("port"))

	if !c.IsSet("port") {
		if path, err := config.DefaultPath(); err == nil {
			if f, _, err := config.Load(path); err == nil && f.Port > 0 {
				port = f.Port
			}
		}
	}

	return port, nil
}
