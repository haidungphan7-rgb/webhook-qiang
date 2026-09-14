# Changelog

本项目的所有显著变更都记录在这个文件里。
格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。

## [1.0.1] - 2026-09-14

### 内置托盘图标（Windows）

- `webhook-zq tray`：单 exe 内置的系统托盘图标，替代旧版 PowerShell 脚本方案。启动时若端口上已有实例则**接管**而非重启；退出时停止服务，不留后台孤儿进程。
- `tray --exit` / `tray --status` / `tray --uninstall`：通过命名内核对象（Mutex + Event）与运行中的托盘通信，供脚本和卸载流程使用。退出码作为稳定契约（0=成功，1=未运行，2=超时）。
- 图标颜色反映服务状态（蓝色=端口可达 / 灰色=已停止），10s 轮询；状态判定走 `status` 退出码（0=运行且健康，3=运行但不健康，4=未运行）。
- `install --autostart` 的计划任务目标从 `start` 改为 `tray`：登录后自动出图标、后台运行无黑窗。
- `start` 成功后自动拉起托盘（DETACHED + HideWindow），开发者直跑 `start` 也能得到图标。
- 删除 `托盘.bat` 与 `scripts/tray.ps1`（功能已内置）；`启动.bat` 改为直接调用 `bin\webhook-zq.exe tray`。
- 卸载时先协调托盘退出（通知 + 等 mutex 释放），再执行删除；`trayScripts` kill list 保留不动（升级防御：清理旧版 PS 托盘进程）。

### 安装与卸载（内置子命令）

- `webhook-zq install`：把下载的单文件 exe 变成"正常安装的软件"——Windows 上注册进「设置 → 应用」、可卸载，`--autostart` 附带登录自启（二进制落在 `%LOCALAPPDATA%\webhook-zq\bin`，Linux / macOS 落 `~/.local/bin`）。环境变量里的 `DATABASE_URL` 自动捕获写入 config.json（文件里已有不同值时不覆盖，只提示）。
- `webhook-zq uninstall`：交互式卸载，先扫描列出**进程（含托盘 / 管理窗口）/ 开机自启 / 「应用和功能」条目 / 程序文件（含 config.json 与加密密钥）**再确认；**数据库默认保留**——卸载程序不替你决定删不删你抓到的数据。`--drop-database` 才删库（先显示收件箱 / 事件数量，要求输入 `yes` 二次确认）；`--yes` 供脚本场景跳过交互。
- 卸载顺序保证先停托盘 / 管理窗口、再停服务进程；进程按命令行识别（脚本名 + 项目路径双条件），不会误杀其它项目的同名脚本。

### 启动

- `start` 读取 config.json：`--database-url` 与监听地址 / 端口支持 `命令行 flag > 环境变量 > config.json` 三级来源（`config explain` 打印的正是这张优先级表）；config.json 损坏时启动报错并给出文件路径，不再静默忽略。登录自启任务因此不再需要自带环境变量。

### 文档

- README 新增「下载即用」小节（install / uninstall）；环境变量优先级说明补上 config.json 一级。

## [1.0.0] - 2026-09-13

首个公开版本。

### 收件箱与捕获

- 多收件箱，每箱一个唯一的接收入口 `POST /hooks/{token}`：token 由 `crypto/rand` 32 字节熵映射到 51 字符无歧义字母表，URL 安全、可手抄、不可枚举。
- 请求体**逐字节**入库（`bytea`），全链路不解码、不重编码、不裁剪；详情页的 JSON 美化只是展示行为，保存与重放读的是同一份原始字节。
- 自定义响应码与响应体；token 可轮换（旧地址立即失效）；收件箱可启停（停用后投递返回 403）。
- 1 MiB 流式请求体上限（`--max-request-body-size` 可调），超限 413 且不落库。
- 每箱独立保留策略（条数 / 天数），后台周期裁剪。

### 查看与搜索

- 事件列表按时间、方法、Content-Type、关键字筛选；关键字搜索走 `pg_trgm` GIN 索引；二进制 body 不参与搜索（仍可查看与重放）。
- 事件详情展示原始 headers 与 body；敏感头默认掩码。
- WebSocket 实时推送（校验 `Origin`，防跨站页面订阅）。
- 一键导出 cURL 命令或 JSON（导出永远不含敏感头明文）。

### 重放

- 把任意一条事件原样重放到指定 URL：保留原始 body 与 Content-Type。
- SSRF 三层防护，默认开启：提交时校验（scheme / userinfo / 保留地址 DNS 判定）、拨号时复检（防 DNS rebinding，覆盖重定向每一跳）、重定向默认不跟随。
- 被拒绝的尝试也落库（`outcome=blocked`）；49 条 URL 策略用例逐条断言。
- 敏感头（Authorization / Cookie / X-API-Key 等）一律不转发；`Host` / `Content-Length` 按实际发送重算。
- 响应只存有限预览（默认 4 KiB，带截断标记）。
- 超时分层（DNS 5s，拨号 / TLS / 整体默认 10s）；可选重试（默认关闭）与指数退避。
- 重放限流默认 30 次/分钟（`--replay-rate-limit`，0 = 关闭），超限 429 + `Retry-After`。

### 安全

- 敏感头加密落库：AES-256-GCM，HKDF-SHA256 按收件箱派生密钥（inbox id 作为 AAD，密文挪到别的行解不开）；未配置密钥时替换为掩码——任一情况**明文都不进库**。
- 访问控制：`--auth-token` 单令牌或 `--auth-keys` 多租户；租户数据完全隔离（访问他人资源返回 404 而非 403）；`/hooks/{token}` 投递入口永远公开。
- HMAC 请求签名校验，fail closed：开了 `require_signature` 却没配密钥时一律拒绝。
- 解密查看敏感头（`?reveal=1`）仅在启用访问控制时可用，每次解密记审计日志。
- `--trust-proxy-headers` 显式信任反代；CORS 预检直接 204 且不落库。

### 部署与运维

- 单二进制：前端 `go:embed` 进二进制，Linux / macOS / Windows 开箱即用，运行时不需要 Node。
- `docker compose up` 一条命令起全套（含 PostgreSQL）。
- CLI 子命令：`start`、`migrate`（内嵌迁移，事务 + 幂等）、`doctor`（6 项检查，每项给可复制的修复命令）、`status --json`（12 字段机器可读状态契约，只增不改）。
- 全部参数支持同名环境变量兜底：命令行 flag > 环境变量 > 默认值。
- Windows 桌面便利工具（仅 Windows）：托盘、控制面板、图形启动器与卸载器、开机自启（计划任务）。
- 附带工具：示例数据、测试接收端、冒烟自检脚本。

### 开发者

- `make` 与 `make.ps1` 双入口（Windows 系统不自带 make），同目标、同版本注入、都不依赖 Docker。
- 测试：Go 13 个包（含 49 条 SSRF URL 策略用例、请求体逐字节保真断言）+ 前端 vitest 三层（纯函数 / 组件 / 页面）。
- CI：go / web / lint / smoke（真实 PostgreSQL）四个 job。
