# 托盘架构决策（architect2，2026-09-14）

> 对象：`webhook-zq tray`（Windows 托盘，单 exe + 隐藏子命令基线已定）。
> 输入：`deliver/tray-plan-v1.0.1.md`（pm 修订 4 终版，lead 已封版）+ 仓库现状核查（`internal/cli/{app,start,status,install,uninstall}`、`scripts/tray.ps1`、`go.mod`、`Makefile`、`cmd/webhook-tester/main.go`）。
> 四项决策：CLI 钩子形态 / 状态查询入口 / 卸载路径对账 / 自启目标确认。每项给到可实现层面（命令名、参数、退出码、内核对象命名、时序）。
> 实现顺序见第六节——pause 修复拆出先行，不依赖托盘本体。
> 修订注记（v1.1，tester 反馈后）：A5 基线断言变更并入 PR-C 交付物；决策 4 补 I7 时序澄清（服务启动不等 taskbar）；PR-D 明确 scripts/tray.ps1 处置与 uninstall kill list 保留。

---

## 决策 1：CLI 钩子形态 = `tray` 子命令的三个控制 flag + 命名内核对象 IPC

### 1.1 结论

`webhook-zq tray` 在「启动托盘」之外提供三个控制 flag，全部通过**命名内核对象**（`golang.org/x/sys/windows`，**零新依赖**——x/sys 已在 go.mod）与运行中的托盘实例通信：

| 命令 | 行为 | 退出码 |
|---|---|---|
| `webhook-zq tray`（无 flag） | 启动托盘（单实例；二次运行=开浏览器 UI 后静默退出，即 pm 方案四节） | 0 |
| `webhook-zq tray --exit` | 通知运行中的托盘执行「退出」语义（停服务+图标消失+进程退出） | 0=已退出 / 1=托盘未在运行 / 2=已通知但 5s 内未退出 |
| `webhook-zq tray --status [--json]` | 探测托盘是否存活（互斥体探测，不产生副作用） | 0=运行中 / 1=未运行 |
| `webhook-zq tray --uninstall` | ①通知托盘退出（尽力而为，见 3.4）②**在本进程内**直接跑交互式 uninstall（stdin/stdout 继承调用方） | 同 `uninstall`（0=成功 / 1=有步骤失败） |

不做 `--stop`/`--start`/`--open`：tester 阻塞用例只有退出/卸载两条；服务启停的 CLI 等价物后续按同一机制扩展（加事件即可），本期不扩面。

### 1.2 内核对象命名（Local\ 命名空间，每用户会话一份，无需管理员）

| 对象 | 类型 | 创建者 | 生命周期 |
|---|---|---|---|
| `Local\webhook-zq-tray-singleton` | Mutex（未持有即创建） | 托盘进程启动时 | 托盘进程退出（含被杀，句柄关闭→内核释放） |
| `Local\webhook-zq-tray-exit` | Event（**auto-reset**，未触发态） | 托盘进程启动时（早于 mutex 取锁） | 同上 |

选 `Local\`（会话命名空间）而非 `Global\`：install 是每用户免提权的，托盘属于登录会话；快速用户切换下每会话各一个托盘、各管各的图标，互不误伤（服务是端口全局的，第二会话托盘走 adopt-or-start 只出图标，与既有设计一致）。

**双职能**：mutex 兼任 uninstall 协调信号（pm 方案三节已预留此对接点）——CLI 直跑 `uninstall` 时若撞上**稳定持有**锁的活托盘，按方案文案输出指引退出，不静默删 exe（判定与探测时机见 3.4）。

### 1.3 时序（`tray --exit` 完整序列）

```
CLI 进程                                托盘进程
────────                                ────────
OpenEvent(Local\...tray-exit)
 ├─ 失败(对象不存在=无托盘) → 打印"托盘未在运行" → exit 1
 └─ 成功 → SetEvent
                                        WaitForSingleObject(exitEvent) 命中
                                        → systray.Quit()（消息循环返回，图标消失）
                                        → 停止服务子进程（Job Object 终止）
                                        → CloseHandle(mutex)   ← 释放=应答(ACK)
WaitForSingleObject(mutex, 5s)
 ├─ WAIT_OBJECT_0 / WAIT_ABANDONED（托盘已退净/被杀）
 │    → ReleaseMutex → 打印"托盘已退出" → exit 0
 └─ 超时 → 打印现状/原因/下一步(taskkill 按路径) → exit 2
```

设计要点：
- **先 OpenEvent 再 SetEvent，探测不用碰 mutex**——`--exit` 全程不短暂持有单实例锁，避免「探测瞬间恰好有真正的 tray 在启动→误判已有实例→开浏览器退出」的竞态。
- **WAIT_ABANDONED 也算 ACK**：托盘被强杀（uninstall 扫杀/taskkill）时同样释放锁，两种死亡形态统一退出码 0。
- 事件对象随托盘进程死亡被内核回收（无引用即销毁），`OpenEvent` 失败=「无托盘」判定可靠，无需 pid 文件。
- auto-reset 语义与「一次性命令」吻合；两个并发 `--exit` 都能等到 mutex（先到者释放后后者取得），均 exit 0。

### 1.4 托盘侧实现要点

- 托盘启动顺序：CreateEvent(exit) → CreateMutex(抢锁；抢不到=已有实例→开 UI→静默 exit 0) → 起监控 goroutine `WaitForSingleObject(exitEvent, INFINITE)` → systray.Run(...)。事件先于锁创建，保证任何时刻 `--exit` 的 OpenEvent 结果都有效。
- exitEvent 命中后：`systray.Quit()` → Run 返回后停服务子进程 → 关闭句柄 → `os.Exit(0)`。停服务在放锁**之前**完成（放锁=对外宣告"已退净"，宣告前把孤儿清干净）。
- 默认 DACL 下同用户不同完整性级别（提权/非提权 PowerShell）互开对方内核对象均允许，无提权坑。

### 1.5 代码落点

- 新增 `internal/cli/tray/`（command.go + platform_windows.go + platform_other.go，非 Windows 编译为「Windows-only」提示命令，与 install/uninstall 同构）。
- `internal/cli/app.go` 的 `Commands` 注册 `tray.NewCommand(log, defaultHttpPort)`。
- 新依赖：`fyne.io/systray`（架构基线已定）；内核对象用现有 `golang.org/x/sys/windows`。

---

## 决策 2：状态可观测信号 = `status` 退出码约定（主断言）+ tray.log（辅证）

### 2.1 结论

**「图标变灰」不必断言像素——图标状态是服务可达性的纯函数**（pm 方案二节：蓝=端口可达 / 灰=其余，10s 轮询），断言 `status` 即断言图标态。两级信号：

**主断言：`webhook-zq status [--json]` 增加退出码约定**

| 退出码 | 含义 | 对应图标态 |
|---|---|---|
| 0 | running=true 且 healthy=true | 蓝 |
| 3 | running=true 但健康检查失败 | 灰（端口无响应） |
| 4 | running=false（未运行/被杀） | 灰（已停止） |

选 3/4 避开 1（main.go 错误路径已占用）与 2（CLI 惯例 usage error）。实现=urfave/cli v3 的 `cli.Exit(err, code)`。`--json` 输出字段不变（Running/Healthy/Pid 已够）。
- 语义：脚本可写 `webhook-zq status || echo "icon should be grey"`；tester 杀服务后轮询 `status --json`，`running:false` 在 ≤10s+2s 内出现即「图标已变灰」的机器等价断言（10s 托盘轮询周期 + 2s status 探测超时）。
- 兼容性核查：仓库内 `status --json` 消费者仅 `scripts/start-gui.ps1`（解析 JSON，不检查 `$LASTEXITCODE`），退出码变更无破坏面；PR 内实测确认。

**辅证：托盘状态转写日志 `%LOCALAPPDATA%\webhook-zq\logs\tray.log`**

图标每次蓝↔灰翻转写一行（zap，与 server.log 同目录，pm 方案「打开日志」菜单指向 server.log，tray.log 是排障追加物）：

```
2026-09-14T10:00:05 icon grey: service unreachable (port 8080)
2026-09-14T10:03:11 icon blue: service reachable (pid 4211)
```

供人工排障与验收失败时取证；自动化断言一律走 status 退出码（不 grep 日志，防时序抖动）。

**托盘自身存活：`tray --status [--json]`**（决策 1 表）——互斥体 200ms 试取：取到=无托盘（立即 ReleaseMutex 后报告，exit 1）；WAIT_TIMEOUT=有托盘（exit 0）。JSON `{"tray_running":true}`。tester「Exit 无孤儿」用例：`tray --exit` 退出码 0 + `tray --status` 退出码 1 + `status` 退出码 4 三连断言，全机器可测。

### 2.2 图标层两项库能力核查（2026-09-14 验证，触发=lead 六条业界惯例逐条对照）

**Explorer 崩溃重启后图标自动重注册：fyne.io/systray 内置，零自研代码。** 库 Windows 实现（systray_windows.go）三处闭环：`RegisterWindowMessageW("TaskbarCreated")` 注册广播消息 → 隐藏窗口 `wndProc` 收到该消息 → `t.nid.add()` 以持久化的 notifyIconData 重新 `NIM_ADD`（图标句柄+Tooltip 状态完整恢复）。机制依赖 GetMessage 消息循环存活，即 systray.Run 期间全程有效——「图标色=真实健康」原则的 Explorer 重启子项由此闭环，PR-B 直接受益。

**Tooltip 长度红线：库 `setTooltip` 无截断保护。** 其 Tip 字段为 `[128]uint16`（127 码元+NUL）；超长时 `copy` 静默截断且**丢弃 NUL 终止符**，数组成为非零结尾字符串，Shell 读取越界可能显示乱码（源码核查 2026-09-14）。我们的 Tooltip 只有固定三态模板（方案二节，最长 33 字符），距限极远，安全。红线：Tooltip 文案只允许三态模板+端口号，禁止拼接动态长文本（事件计数、错误详情等）——瞬时信息本属 Toast 通道，而 Toast 本期不做（方案二节/七节不做清单）。

**残余披露（不进本期重打，v1.0.2 候选）**：`start` 直跑撞已运行服务=WSAEADDRINUSE(10048) 报错退出（`internal/cli/start/command.go` 386-397 行，文案已是现状→原因→下一步形态：空闲端口建议+netstat 定位），**非**业界四步的「激活+静默退出」语义。托盘方案主路径（自启改跑 tray + 手动 tray 二次启动开浏览器退出 0，决策 1）已根治撞车场景；start 残余属低频路径（开发者/老用户），若升级为「探测到 healthy 自有服务→打印已在运行+开界面」，与 F1（install 不写 PATH）/F5（二次 install 提示）并列进 v1.0.2 候选，不扩本期 v1.0.1 重打范围。

---

## 决策 3：对账项——**无硬冲突**：封版文本（修订 3/4）已收敛于交互式向导，与架构裁定一致

### 3.1 事实核查（引文）

lead 交办描述的分歧「pm=spawn detached `uninstall --yes` vs architect=MessageBox gate→交互式不带 --yes」对应的是 pm **修订 2**；封版文本 `tray-plan-v1.0.1.md` 130 行已明确记录修订 3 起采纳 architect 方案：

> 「修订 2 设计=确认窗替代第一道 gate + spawn `uninstall --yes`；architect 裁定=GUI 零替代，只做引导，真确认=现有 CLI 向导。后者更优：①…②tester D3 管道喂行测试 100% 原样复用…③…」

128 行（终版执行序）：「点 [继续] → CREATE_NEW_CONSOLE spawn `webhook-zq uninstall`（**完整交互式向导，三道 gate 全走，不带 `--yes`**）→ 托盘立即退出」。

**结论：封版方案与架构裁定本就是同一方案，无技术阻塞，架构侧照封版文本执行，无需调整任何一方。** `--yes` 形态在封版文本中已被明确废弃。

### 3.2 tester D3 能否管道复用——能，但要看清两条路径的 stdio 差异（这是本决策唯一需要 tester 知道的增量）

托盘「卸载」在产品里有**两个等价触发面**，共享同一份删除代码（`internal/cli/uninstall` 的 `run()`，单一真相源不破）：

| 触发面 | 编排方 | uninstall 进程形态 | 可自动化性 |
|---|---|---|---|
| 菜单点「卸载 webhook-zq…」 | 托盘进程 | `CREATE_NEW_CONSOLE` spawn 新 exe，新控制台自有 stdin | 人工路径（人类向导可见性是产品要求） |
| CLI `webhook-zq tray --uninstall` | 调用方进程 | **本进程内直接调 `run()`**，stdin/stdout 继承管道 | **管道喂三行全自动**（三道 gate 原样走） |

`tray --uninstall` 不需要 MessageBox gate：显式敲命令=第一道确认（与用户直敲 `webhook-zq uninstall` 同级意图强度），三道 gate 全在 CLI 向导内，GUI 零替代原则不破。tester b4 端到端用例（pm 方案三节「验收复用」）**改走 `tray --uninstall` 路径**：喂 `y\n`（继续）→ 不带 `--drop-database` 时数据库 gate 直接跳过，或带时再喂 `y\n`+`yes\n`，与现行 D3 断言逻辑逐字相同。

### 3.3 时序与单 exe 文件锁（确认封版执行序成立）

`tray --uninstall` 序列：OpenEvent→SetEvent→等 mutex（5s）→ 本进程调 uninstall `run()`：
- 托盘已退净+服务已停（Exit 语义停了服务）→ `findOurProcesses` 只剩自身（已排除自身 pid，`platform_windows.go` ourProcess 现有逻辑）→ 干净删除。
- 托盘 5s 未退（异常）：**容忍并继续**——uninstall 的 ourProcess 扫杀按 appDir 路径前缀命中卡死托盘，自愈，不中断。
- 双保险不变：毫秒级退出窗口的极小竞态由「排除自身 pid」兜底（现状已有）。

### 3.4 「稳定持有 vs 退出中」判定（pm 方案三节点名归架构定）

CLI 直跑 `uninstall`（非 tray 编排）撞活托盘的判定：`OpenEvent(Local\...tray-exit)` 成功且 SetEvent 后 **3s** 内 mutex 未释放=稳定持有的活托盘→输出方案 3.3 的指引文案退出（非 0 退出码 1）；事件不存在=无托盘，直接继续。**不做毫秒级重试探测**——3s 窗口足够覆盖托盘正常退出路径（停服务 Job Object 终止 <1s），期间锁未放即按活托盘处理，宁可保守不删 exe。托盘自卸载（`tray --uninstall`）不走此判定（它自己就是通知方）。

---

## 决策 4：schtasks 自启目标改 `tray`——**确认可行，两个已识别坑及缓解**（无阻塞级坑）

install 写任务从 `/TR '"exePath" start'` 改 `/TR '"exePath" tray'`（`internal/cli/install/install_windows.go` enableAutostart，一行改动+文案）。核查结论：

**坑 1（已识别，缓解后可接受）：exe 是 console 子系统，Task Scheduler 拉起必配控制台窗口。**
`tray` 入口第一件事 `FreeConsole()`：最后一个附着进程脱离后 conhost 关窗。效果=**登录瞬间短闪（百毫秒级）后消失**，对比现状（跑 `start`=黑窗常驻不退）是数量级改善。这是「根治常驻黑窗」的准确表述：窗不驻留，但有闪。pm 方案七节「FreeConsole 消控制台（实现期实测）」即此。若实测闪感不可接受，备选=任务包一层 `conhost.exe --headless "exe" tray`（Win10 1809+，零窗口），本期不做、记录备选。
**坑 2（已识别，必须处理）：登录竞态——explorer/taskbar 未就绪时注册托盘图标会静默丢失。**
schtasks ONLOGON 触发早于 taskbar 创建完成是常态。缓解：tray 启动后先 `FindWindow("Shell_TrayWnd")` 就绪等待（500ms 轮询，上限 60s），就绪后再 systray.Run；期间不报错不出窗。这与「登录即启动是常态路径非边界」（pm 方案七节）对齐。

**服务启动不等 taskbar（I7 时序澄清，v1.1）**：adopt-or-start 在 mutex 取锁后立即发起，与 Shell_TrayWnd 等待**并行**——等待只 gate 图标注册（systray.Run），不 gate 服务。登录→服务健康时间线 ≈ 现行直跑 start 的全部耗时 +1–2s（exe spawn+端口探测+再 spawn start 一跳）；I7 的 20s 健康轮询窗口保持有效但余量收窄，实测紧张时优先怀疑 PG 冷启动而非 tray 链路。图标就绪最坏晚至 60s，属可见性问题不进 I7 断言（「无残留控制台」人工观察项可多等）。

其余核查均通过：ONLOGON 交互任务在用户会话内运行、可访问托盘（Shell_TrayWnd）；per-user Local\ 命名空间与 schtasks 提权模型无冲突；Job Object 绑定下注销杀托盘即杀服务，无孤儿；第二会话登录→第二托盘→adopt-or-start 出图标不抢服务，行为正确。`enableAutostart` 成功文案「登录时自动 start」改「登录时自动启动托盘」；install「接下来」清单按 pm 方案五节替换（含溢出区提示）。

---

## 五、顺手修两缺陷之 pause 修复（设计定稿，可先行合入）

`uninstall` 末尾 pause 条件（`internal/cli/uninstall`，Windows 文件）：

```
if !yes && GetConsoleProcessList(&count, 2) == 1 {
    fmt.Print("按回车关闭…")
    bufio 读一行
}
```

- `GetConsoleProcessList==1` =「这个控制台窗是为自己开的」：系统应用列表（UninstallString 直跑）/双击/托盘新控制台三条路径全命中 ✓
- 管道喂行（pwsh 管道、`tray --uninstall` in-process）：控制台内 ≥2 进程（pwsh+自身）→ 不 pause，管道不被挂起 ✓
- `--yes` 跳过 ✓（CI/脚本绝不等待交互）
- 无控制台（DETACHED 的托盘 spawn 场景未来若复用）：GetConsoleProcessList 返回 0 → 不 pause ✓

## 六、实现顺序（含 pause 先行拆分）

| 阶段 | 内容 | 依赖 | 解锁 |
|---|---|---|---|
| **PR-A（先行，无托盘依赖）** | pause 修复（第五节）+ CHANGELOG 条目 | 无 | tester Phase 0 前置门（pause 两实测）立即可跑，不等托盘 |
| **PR-B 托盘本体** | `fyne.io/systray` 依赖；tray 子命令骨架；mutex+event；adopt-or-start（隐藏拉 start+重定向 logs/server.log+`WEBHOOK_ZQ_FROM_TRAY=1` 防回环）；Job Object；start 成功后 spawn tray（DETACHED+HideWindow）；Shell_TrayWnd 就绪等待+FreeConsole；图标蓝/灰+10s 轮询+tray.log；菜单全集含卸载编排（MessageBox gate→停服务→CREATE_NEW_CONSOLE spawn 交互式 uninstall→自退） | PR-A 可并行 | UI 全功能 |
| **PR-C 钩子+状态+自启** | `tray --exit/--status/--uninstall`；`status` 退出码 0/3/4；install autostart 目标 start→tray + install 输出三行替换 + README 联动；**b4 断言同步变更**（tester 发现：A5 schtasks XML `<Arguments>start`→`tray` + install 成功文案断言「登录时自动 start」→「登录时自动启动托盘」）——PR-C 描述必须带断言变更说明，避免合入即红 | PR-B | tester b4 托盘用例全自动化（决策 1/2 断言面） |
| **PR-D 收尾** | `Local\` 对象命名复核、`启动.bat`→`bin\webhook-zq.exe tray`、删 `托盘.bat`+`scripts/tray.ps1`+README 对应节（文件级引用仅此三处，已核查：全仓 grep）；**uninstall `trayScripts` kill list 保留不动**——升级防御：v1.0.1 用户已跑着的旧 PS 托盘进程凭命令行匹配在卸载时仍被清（kill 逻辑按命令行子串，不要求文件在新装中存在），`server-manager.ps1`/`start-gui.ps1` 按不做清单不动；CHANGELOG/文档。U11 的 realTray/decoyTray 是自包含命令行匹配负例，不依赖 tray.ps1 文件存在，PR-D 后断言依然有效；**U11 语义=「遗留 PS 托盘清理（升级防御）」而非新托盘识别测试**（PR-C 后真实托盘是 Go 进程，tester 验收计划已同步此标签，防后人从断言名误读） | PR-C | 发版联动（tag 重打方案 A 归 lead） |

PR-A 建议今天独立提：改动面=uninstall 包一个函数+测试，风险最小，tester 的 Phase 0 不再被托盘排期绑架。

## 附录：退出码总表（tester 断言契约）

| 命令 | 0 | 1 | 2 | 3 | 4 | 5 |
|---|---|---|---|---|---|---|
| `status` | 运行且健康 | （保留：错误） | （保留） | 运行但 unhealthy | 未运行 | — |
| `tray --exit` | 托盘已退出 | 托盘未在运行 | 5s 未退出 | — | — | — |
| `tray --status` | 托盘运行中 | 托盘未运行 | — | — | — | — |
| `tray --uninstall` | 卸载完成 | 有步骤失败/撞活托盘 | — | — | — | — |
