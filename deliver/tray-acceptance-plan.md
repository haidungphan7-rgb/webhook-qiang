# 托盘 v1.0.1 重打 — 验收计划定稿（tester，2026-09-14）

> 依据：`deliver/tray-plan-v1.0.1.md`（pm 修订 4 封版）+ `deliver/tray-arch-decisions.md`（architect2 v1.1，退出码契约以其附录总表为准）+ `deliver/assets/tray/`（lead 已验证收口）+ `deliver/baseline-final-v1.0.1.md`（115/0 基线）。
> 实现排期：PR-A（pause 先行）→ PR-B（托盘本体，可与 A 并行）→ PR-C（钩子+status 退出码+autostart）→ PR-D（收尾）。
> 通用纪律：①时间戳裁定法起手（preflight 强制从当前工作树刷新构建+打印构建时间）②产物身份裁定法分段（本地构建=时间戳；下载物=本地哈希=checksums=GitHub API digest 三重对照）③验证产物一律文本回显，不截图不读图片二进制。

## 脚本架构

- `scripts/acceptance/b4-install-uninstall.ps1`：**保持 115 断言基线不动**（可比较性），仅 PR-C 两处同步变更（见三.3）。
- 新增 `scripts/acceptance/b5-tray.ps1`：托盘专项，随 PR 分阶段生长（A→B→C→D 各节），节首检 PR 标志（无对应功能时整节 SKIP 并打印原因，非 FAIL）。
- 终局门槛 = **b4 + b5 双绿** + tag 重打后发布验证（见五）。

## 一、Phase 0 前置门（PR-A 合入即跑，不等托盘）

pause 修复的风险类别=误触发时管道挂起超时而非 FAIL（静默卡死，拖垮 CI 且报错位置误导）。两实测带显式超时上限（每条 120s 硬限），挂起即 FAIL 且 FAIL 信息直接报「**pause 误触发嫌疑**」（指向性报错，非笼统超时）：

| # | 用例 | 断言 |
|---|---|---|
| P0-1 | 交互式 uninstall 管道跑完即退 | `uninstall`（不带 --yes）经 `cmd /c < in-y.txt` 管道喂行，进程在超时上限内退出，exit 0，输出含「卸载完成」——不挂起 |
| P0-2 | `--yes` 模式不 pause | `uninstall --yes` 同管道形态，超时上限内 exit 0；输出**不含**「按回车关闭」字样 |

- **门语义**：两测全过才放行后续全部阶段；任一挂起/超时=停线排查，不烧 b4/b5 全量。
- 逻辑对照（architect2 决策五）：GetConsoleProcessList==1 才 pause——管道内 ≥2 进程（cmd+自身）命中「不 pause」分支，P0-1 正是防该分支失效。

## 二、PR-B 验收（托盘本体；钩子未到，断言=进程/文件级）

| # | 用例 | 断言（全部机器可测） |
|---|---|---|
| B-1 | 图标资产 embed | 构建产物含两图标资源（exestrings/go:embed 编译通过即证；资产本身 lead 已验证 SHAPE-IDENTICAL，不重复验） |
| B-2 | tray 启动存活 | `webhook-zq tray`（无 flag）spawn 后进程存活 ≥5s，退出码不在断言内（此时无钩子） |
| B-3 | adopt-or-start：服务未起 | 干净态 spawn tray → 隐藏拉起 start → healthz 绿（≤20s 轮询）+ `logs\server.log` 文件生成且非空（start 落日志缺陷修复实证） |
| B-4 | adopt-or-start：服务已起 | 先起 start（端口健康）再 spawn tray → 服务进程**不被重启**（pid 不变）+ tray 存活 |
| B-5 | 防回环 | tray 拉起的 start 带 `WEBHOOK_ZQ_FROM_TRAY=1` 时不再反 spawn tray（进程计数：服务 1+tray 1，无二托盘） |
| B-6 | Job Object 绑定 | kill tray 进程 → 服务子进程同死（≤3s，无孤儿）——「Exit=同死不留孤儿」的前置实证 |
| B-7 | tray.log 生成 | `logs\tray.log` 存在（蓝灰翻转行格式不断言——辅证不进自动化，防时序抖动） |
| B-8 | 菜单项存在性 | **人工观察项**（诚实标注脚本不可断言）：右键菜单序=打开界面(粗体)/启停/设置页/自检/日志/─/卸载…/─/退出；已停止态无「停止」「打开设置页」 |

时序坑预警（决策 4）：Shell_TrayWnd 就绪等待只 gate 图标注册不 gate 服务——B-3 服务健康窗口 20s 有效；实测紧张优先怀疑 PG 冷启动（重启 PG 复测一次再报 defect）。

## 三、PR-C 验收（钩子+status 退出码；b4/b5 断言全自动化解锁）

### 1. 退出码契约（断言基准=决策文档附录总表 v1.1）

| 命令 | 断言矩阵 |
|---|---|
| `status` | 0=运行且健康（蓝）/ 3=运行不健康（灰）/ 4=未运行（灰）——三态各造场景实测退出码 |
| `tray --exit` | 0=已退出（含 WAIT_ABANDONED 被杀形态）/ 1=托盘未运行 / 2=已通知 5s 未退 |
| `tray --status` | 0=运行中 / 1=未运行（并发跑两次均 0 不要求） |
| `tray --uninstall` | 同 uninstall：0=成功 / 1=失败或撞活托盘 |

### 2. b5 新用例（钩子解锁）

| # | 用例 | 断言 |
|---|---|---|
| C-1 | Exit 无孤儿（三连） | `tray --exit` exit 0 → `tray --status` exit 1 → `status` exit 4，且 Get-Process 无 appDir 前缀进程残留 |
| C-2 | Exit 未运行态 | 托盘不在时 `tray --exit` exit 1（OpenEvent 失败路径） |
| C-3 | 单实例双开 | 第二次 `webhook-zq tray` → 静默 exit 0（无报错输出）——「已拉起 UI」可断言信号 |
| C-4 | 变灰机器等价断言 | 杀服务进程 → 轮询 `status`（≤12s=10s 托盘轮询+2s 探测超时）出现 running:false/exit 4——「图标已变灰」纯函数断言，无像素 |
| C-5 | 托盘卸载端到端（主用例） | `tray --uninstall` 管道喂行：不带 --drop-database 喂 `y` 单行；带时 `y\nyes\nyes` 三行——**D3/D4 断言逻辑逐字复用**（清单顺序/库保留或删/注册表任务文件零残留），仅换命令入口；且无 MessageBox（CLI 路径） |
| C-6 | uninstall 撞活托盘指引 | 服务+托盘活时直跑 `uninstall`（非 tray 编排）→ 输出含「检测到托盘正在运行」指引文案 + exit 1（决策 3.4 稳定持有判定，3s 窗口） |
| C-7 | schtasks 自启端到端（I7 重测） | `install --autostart` → `schtasks /Run` → 服务 healthz 绿（20s 窗口，同 B-3 预警）+ 进程=安装位 exe + 真实 config 还原；**新增人工观察**：登录场景控制台=百毫秒级短闪后消失（FreeConsole），非常驻黑窗 |
| C-8 | install 输出三行 | install 输出含溢出区提示行（「^ 溢出区」字样）+ tray 启动行 + 托盘右键卸载行 |
| C-9 | README 存在性 | README 含「让图标常显在右下角」小节 + 二次启动句（存在性断言，用户手工操作不自动化） |

### 3. b4 同步变更（合入即红的协调点，A5 已入 PR-C 交付物）

| 变更 | 原 | 新 |
|---|---|---|
| A5 参数断言 | `<Arguments>start</Arguments>` | `<Arguments>tray</Arguments>` |
| A5 文案断言（新增） | — | install --autostart 成功输出含「登录时自动启动托盘」 |

PR-C 合入描述必须带此断言变更说明（architect2 v1.1 已承诺写入交付物）。

## 四、PR-D 验收（收尾）

| # | 用例 | 断言 |
|---|---|---|
| D-1 | 启动.bat | 内容指向 `bin\webhook-zq.exe tray` |
| D-2 | 删除项 | `托盘.bat` + `scripts/tray.ps1` + README 对应节全部不在；`server-manager.ps1`/`start-gui.ps1` **仍在**（不做清单） |
| D-3 | U11 语义=遗留 PS 托盘清理 | **断言零变更**（realTray/decoyTray 自包含命令行匹配负例，b4:536-539 已核实）；b4 注释「three desktop scripts」顺手更新为遗留清理口径（不强制）；语义标签已双侧定调（验收计划+决策文档 PR-D 段），防后人误读 |
| D-4 | uninstall trayScripts kill list 保留 | 升级防御实证：v1.0.1 旧 PS 托盘进程（命令行含子串）在卸载时仍被清——即 U11 现有断言本身，无需新增 |
| D-5 | CHANGELOG/README | CHANGELOG 含托盘条目+自启变化+闪退修复三条 |

## 五、终局门槛（全部 PR 合入后）

1. **b4 全量复跑**（115 基线，PR-C 两处变更后预期仍 115/0——A5 断言替换不增减数量）+ **b5 全量**（P0+B+C+D 节全激活）双绿。
2. **tag v1.0.1 重打方案 A**（lead 决策）：删 Release 388090869 → 删远端 tag → main 追加 → 重打同号 → release.yml 重建 → 核对 buildTime（无 force-push/amend）。
3. **发布后验证**（脚本已就绪）：`relverify-checksums.ps1` 三重对照 + 下载物跑 b4/b5（产物身份裁定法第二段：LastWriteTime=下载时刻不可用作判据）。

## 六、断言纪律（沿用+新增）

- 基线 PASS 只能来自实测，推断只能标「推断/待测」。
- status 3/4 为新增语义：b4 现有脚本 status 调用零处（已核查），无存量破坏面；b5 新用例引入时按上表三态矩阵逐一造场景。
- 人工观察项独立成列（B-8 菜单/C-7 控制台短闪/I7 登录无残留黑窗），脚本不可断言的边界诚实标注，不伪装成自动断言。
- tray.log 只作排障辅证，不进自动化断言（与不 grep 日志的既有口径一致）。
