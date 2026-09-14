# Webhook 调试与重放平台

一个轻量的 Webhook 调试平台：创建收件箱 → 接收第三方请求 → 查看**原始**内容 → 把事件**重放**到指定测试地址。

技术栈：**Go 1.27 + PostgreSQL 16 + React 19 + Mantine 8 + Vite 7**。单个二进制（前端已 `go:embed` 打包），`git clone` 后无需任何代码生成步骤即可构建。

---

> **关于平台**：服务端本体是纯 Go，Linux / macOS / Windows 都能跑，也可以直接用 Docker。
> Windows 托盘图标已内置进二进制（`webhook-zq tray`），`scripts/` 下其余的控制面板、图形启动器
> 与卸载器仍是 **Windows 专用**的桌面便利工具（依赖 WinForms）；Linux / macOS 请用命令行、
> `systemd`/`launchd` 或 `docker compose`。下面每一节都同时给出 bash 与 PowerShell 两种写法。

## 1. 快速开始

### 下载即用（Windows，推荐）

不想装 Go / Node 工具链的话，直接用 Release 里编译好的单文件 exe（前端已内嵌，不需要 Node）：

```powershell
# 1) 从 GitHub Releases 下载（本页右侧 Releases，或命令行）：
curl.exe -LO https://github.com/haidungphan7-rgb/webhook-qiang/releases/latest/download/webhook-zq-windows-amd64.exe

# 2) 告诉它数据库在哪（库要自己建，迁移只建表不建库）：
$env:DATABASE_URL = 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'

# 3) 跑起来，打开 http://127.0.0.1:8080
.\webhook-zq-windows-amd64.exe start --port 8080
```

想让它像"正常安装的软件"一样可管理（出现在「设置 → 应用」里、可卸载、可自启）：

```powershell
.\webhook-zq-windows-amd64.exe install                # 安装到 %LOCALAPPDATA%\webhook-zq\bin
.\webhook-zq-windows-amd64.exe install --autostart    # 同上 + 登录时自动 start
```

之后从任意目录运行 `%LOCALAPPDATA%\webhook-zq\bin\webhook-zq.exe`（该目录不在 PATH 上，需要的话可自行加进用户 PATH），卸载走「设置 → 应用」或 `uninstall` 子命令（见下文[停止与卸载](#停止与卸载)）。Linux / macOS 用户下载对应平台的二进制后放到 `~/.local/bin` 即可（`install` 命令也会帮你放；Linux 上该目录通常已在 PATH 中，macOS 若不在，`install` 会提示）。

### 依赖（源码构建需要）

| 组件 | 版本 | 用途 |
|---|---|---|
| Go | ≥ 1.26（实测 1.27） | 后端 |
| PostgreSQL | ≥ 14（实测 16） | 唯一存储，核心数据全部落库 |
| Node.js | ≥ 20（实测 26） | 仅构建前端；运行不需要 Node |
| PostgreSQL 扩展 | `pg_trgm`、`pgcrypto` | 前者是关键字搜索的 GIN 索引，后者提供 `gen_random_uuid()` |

> 两个扩展都不能由非属主账号创建（PG 13+ 起它们不是 trusted 扩展）。若应用账号不是数据库属主，
> 请让 DBA 预先执行：
>
> ```sql
> CREATE EXTENSION IF NOT EXISTS pg_trgm;
> CREATE EXTENSION IF NOT EXISTS pgcrypto;
> ```
>
> 否则首次启动的迁移会直接失败。

### 启动

```powershell
# 1) 库要自己建：迁移只建表，不建库
createdb webhook_rd

# 2) 前端要先构建一次。仓库里的 web/dist 只有一个占位页，
#    跳过这步打开浏览器会是空白的（./dev.ps1 会自动做）
npm --prefix ./web ci
npm --prefix ./web run build

# 3) 启动（默认连 postgres://postgres:postgres@127.0.0.1:5432/webhook_rd）
$env:DATABASE_URL = 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'
go run ./cmd/webhook-tester start --port 8080
```

> 想用统一入口：`./make.ps1 build`（等价于下面的 `make build`）。

**Linux / macOS**

```bash
createdb webhook_rd
export DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'
make frontend            # 构建前端（会内嵌进二进制；不做这步打开浏览器是空白的）
make build
./bin/webhook-zq start --port 8080
```

> **Windows 上没有 `make`**（系统不自带，也不是 Git for Windows 的一部分）。仓库因此同时提供
> **`make.ps1`** —— 与 Makefile 同名目标、同样的版本注入、同样不依赖 Docker：
>
> | 你要做的事 | Windows | Linux / macOS |
> |---|---|---|
> | 构建二进制 | `./make.ps1 build` | `make build` |
> | 构建前端 | `./make.ps1 frontend` | `make frontend` |
> | 跑全部测试 | `./make.ps1 test` | `make test` |
> | 端到端验收 | `./make.ps1 accept` | `make accept` |
> | 看全部目标 | `./make.ps1 help` | `make help` |
>
> 两个入口必须同步维护：加目标时两边都要加。

另有两条一键命令（PowerShell 7）：

```powershell
./dev.ps1              # 自动构建前端并启动
./dev.ps1 -Dev         # 额外起 Vite 开发服务器（8081，前端热更新）
```

首次启动会自动建表（内嵌迁移，事务 + 幂等）。打开 <http://127.0.0.1:8080>。

**有 Docker 的话一条命令**（含 PostgreSQL，无需本地安装）：

```bash
docker compose up          # http://127.0.0.1:8080
```

> 容器里**默认不放行本地重放**：`docker-compose.yml` 中的 `REPLAY_ALLOW_HOSTS` / `REPLAY_ALLOW_PRIVATE`
> 是注释掉的。要跑演示第 4 步（重放到本地接收端），先取消那两行注释再 `docker compose up`。

### 预置启动档位（不想记参数时用这个）

| 脚本 | 用途 | 与默认值的差异 |
|---|---|---|
| `./scripts/start-local.ps1` | 本机演示 | 放行 `127.0.0.1` 作为重放目标（配合 `node scripts/test-receiver.mjs 9099`），超时 3s、失败重试 2 次 |
| `./scripts/start-secure.ps1` | 有多人访问时 | 自动生成 32 字节加密密钥与访问凭据（**只打印一次**）、敏感头加密落库，**私网与链路本地目标仍然拦截** |

```powershell
./scripts/start-local.ps1 -Port 8080
./scripts/start-secure.ps1                    # 单一共享 token
./scripts/start-secure.ps1 -Tenants alice,bob # 多租户，每个租户一把 key
```

> 加固档位把凭据打在启动日志里：**加密密钥丢了就无法解密已存的敏感头**，这是刻意的——没有后门。用浏览器登录后走「帮助 → 访问密钥」，填冒号后面那一段（存进 `whq_token` Cookie）。

### 环境变量

每个启动参数都有**同名的环境变量**作为兜底（**没有前缀**）。优先级是 `命令行 flag > 环境变量 > config.json > 默认值`（config.json 由 `install` 或 `webhook-zq config set` 写入，`config explain` 打印的正是这张优先级表；`ENCRYPT_KEY` / `AUTH_TOKEN` / `AUTH_KEYS` 例外——部署密钥只从环境变量读，绝不落文件）：

```bash
DATABASE_URL HTTP_PORT SERVER_ADDR MAX_REQUEST_BODY_SIZE
REPLAY_TIMEOUT REPLAY_MAX_PREVIEW REPLAY_MAX_REDIRECTS REPLAY_MAX_RETRIES
REPLAY_ALLOW_HOSTS REPLAY_ALLOW_PRIVATE REPLAY_RATE_LIMIT
RETENTION_MAX_EVENTS RETENTION_MAX_DAYS RETENTION_INTERVAL
AUTH_TOKEN AUTH_KEYS ENCRYPT_KEY PUBLIC_URL_ROOT TRUST_PROXY_HEADERS
LOG_LEVEL LOG_FORMAT
```

所以 `docker-compose.yml`、systemd、K8s 里都用环境变量配，命令行只在临时起服务时用。

### 新电脑一键部署（启动器）

`scripts/setup.ps1` 会把一台**什么都没装的机器**带到跑起来的状态：检查依赖 → 缺什么给出 winget 安装命令 → 建库 → 构建 → 启动。双击 **`启动.bat`** 即可。

```powershell
pwsh ./scripts/setup.ps1                        # 交互式（本机 / Docker / 只检查）
pwsh ./scripts/setup.ps1 -Mode Local -Demo      # 本机 + 放行本地重放（演示用）
pwsh ./scripts/setup.ps1 -Mode Docker           # 有 Docker 时最省事
pwsh ./scripts/setup.ps1 -Mode Local -NoStart   # 只准备环境，不启动
```

它**不会**问你要不要打开 `--replay-allow-private` 这类开关——那些是安全边界，留在启动命令里（见下文速查表）。启动器只管环境。
每步都会写进 `logs/setup.log`，双击启动窗口关了也能回看。

### 不知道缺什么？跑 doctor

```bash
webhook-zq doctor
```

一条命令检查 6 项，每项都给**可直接复制的修复命令**，并且**不会自动修改任何东西**：

```
[1/6] 数据库配置     DATABASE_URL 是否设置
[2/6] 数据库连接     连不上时区分：库不存在 / 密码不对 / 权限不足 / 超时
[3/6] 所需扩展       pg_trgm、pgcrypto
[4/6] 表结构         是否已是最新（缺则给 migrate 命令）
[5/6] 前端资源       这个二进制里有没有打包前端
[6/6] 端口           默认 8080 是否被占
```

全绿时会打印下一步：`webhook-zq start --port 8080`。
连接错误按 **SQLSTATE** 判定（`3D000` 库不存在、`28P01` 密码错误），不靠错误文案猜。

### 托盘图标（推荐）

双击 **`启动.bat`**，或用命令：

```powershell
webhook-zq tray
```

右下角出现图标后（Windows 11 新图标默认在 `^` 溢出区）：

- **打开界面** → 浏览器打开 webhook-zq
- **启动服务 / 停止服务** → 一键启停后台服务
- **打开设置页** → 浏览器打开设置
- **自检** → 新窗口运行 doctor
- **打开日志** → 资源管理器定位服务日志
- **卸载 webhook-zq…** → 停止服务并打开卸载向导
- **退出** → 退出托盘并停止服务

图标颜色反映服务状态：**蓝色**=端口可达，**灰色**=已停止。
**退出图标时会把服务一起停掉**——托盘就是开关，不会留下你看不见的后台进程。
启动时若端口上已经有实例在跑，托盘会**接管**它而不是再起一个（再起只会端口冲突）。

### 后台常驻（开机自启）

不想每次开终端的话，用 `install --autostart` 一步到位（注册计划任务，登录时自动启动托盘）：

```powershell
webhook-zq install --autostart       # 安装 + 登录自启（跑的是 tray 子命令，无黑窗）
webhook-zq status                    # 看状态与健康检查
webhook-zq tray --status             # 查询托盘是否在运行
webhook-zq uninstall                 # 卸载（含取消自启 + 停服务 + 删文件）
```

> **为什么不是 Windows 服务**：`sc.exe` 只能管实现了服务控制接口的程序，普通控制台程序启动必然失败（1053 超时）。
> 计划任务是 Windows 内置能力，程序无需改造，效果一样：登录后自动运行、不显示窗口、关掉终端也不停。
> Linux 上直接用 systemd（见 `docs/部署指南.md` §3B），systemd 管理普通前台进程本来就没问题。

### 停止与卸载

**用下载的 exe 安装的**（`webhook-zq install`），一条命令交互式卸载（手里还留着下载的 exe 的话，在下载目录直接跑 `.\webhook-zq-windows-amd64.exe uninstall` 也一样）：

```powershell
$whq = "$env:LOCALAPPDATA\webhook-zq\bin\webhook-zq.exe"
& $whq uninstall
```

它会先扫描再确认：进程 / 开机自启 / 「应用和功能」条目 / 程序文件（含 config.json 与加密密钥）默认移除；**数据库默认保留**——那是你抓到的数据，卸载程序不该替你决定删不删。真要连库一起删：

```powershell
& $whq uninstall --drop-database   # 会显示库里的收件箱/事件数量，并要求输入 yes 二次确认
& $whq uninstall --yes             # 脚本场景：跳过交互，按默认执行（不删库）
```

**源码仓库用户**（有 `scripts/` 目录的）继续用脚本版，它能额外清理构建产物：

```powershell
# 停掉（前台运行时直接按 Ctrl+C 最干净，程序会优雅退出）
pwsh ./scripts/uninstall.ps1                  # 只停止服务，数据全部保留

# 卸载
pwsh ./scripts/uninstall.ps1 -RemoveService   # + 注销 Windows 服务
pwsh ./scripts/uninstall.ps1 -DropDatabase    # + 删除数据库（会再确认一次）
pwsh ./scripts/uninstall.ps1 -RemoveBuild     # + 删除 bin\、web\dist、logs\、根目录 wh.exe
pwsh ./scripts/uninstall.ps1 -All             # 以上全部
```

默认行为是**只停进程、不删任何数据**；删库需要手工输入 `yes` 确认。
> 注意：PostgreSQL **本体不会**被卸载命令移除（它是独立安装的软件），需要的话在「应用和功能」里单独卸载。

### 常见组合速查表

| 我想干什么 | 命令 |
|---|---|
| 一条命令跑起来（建库 + 构建前端 + 起服务 + 开浏览器） | `pwsh ./scripts/start-gui.ps1`（图形）或 `pwsh ./scripts/setup.ps1 -Mode Local` |
| 起一个放行了本机地址的实例（要把事件重放到本机服务时） | `pwsh ./scripts/start-local.ps1` |
| 启用鉴权与加密（多人访问时） | `pwsh ./scripts/start-secure.ps1 -Tenants alice,bob` |
| 有 Docker，只想看界面 | `docker compose up` |
| 只跑迁移不启服务 / 检查 schema 版本 | `make migrate` / `make migrate-check` |
| 灌示例数据 | `make seed`（需要 `psql` 在 PATH 上） |
| 验证链路是否真的通 | `make accept` |

> 没有"启动时交互式选参数"的向导，这是**有意的**：这批参数是安全边界而不是偏好设置。
> 让它们留在命令行里，才能被看见、被 review、被版本控制。`--replay-allow-private`
> 尤其不能变成运行期可开的开关——那是这系统唯一的 SSRF 闸。

**常用 make 目标**（都不依赖 Docker）：`make build` / `make test` / `make frontend` / `make dev` / `make migrate` / `make migrate-check`。

可选示例数据（不自动执行）。三条事件覆盖 JSON / 表单 / 二进制三种形态，其中二进制那条用来验证
「二进制 body 不参与搜索」这条规则：

```bash
psql "$DATABASE_URL" -f scripts/sample-data.sql
```

### 冒烟自检（`./scripts/smoke.ps1`）

新机器上最快确认「收得进来、看得见、放得出去」的办法：建箱 → 发一条 → 看事件 → 重放。
它会在重放被拒绝时说明这是防护在生效，而不是报错。

更严格的验证走 `make accept`（`scripts/acceptance/` 下的断言脚本）。

### 四步走通（`./scripts/smoke.ps1`）

```bash
# 1) 创建收件箱
curl -XPOST localhost:8080/api/v1/inboxes -H 'content-type: application/json' -d '{"name":"demo"}'
#    -> {"id":"…","token":"gktvqivv…","receive_url":"http://localhost:8080/hooks/gktvqivv…"}

# 2) 发送一条 webhook（带敏感头，用来验证打码与不转发）
curl -XPOST 'localhost:8080/hooks/<token>/orders?src=github' \
  -H 'content-type: application/json' -H 'authorization: Bearer top-secret' \
  -d '{"event":"order.paid","amount":42.5}'
#    -> {"id":"<event_id>","ok":true}

# 3) 在页面查看：http://127.0.0.1:8080/inboxes/<id>/events/<event_id>

# 4) 重放到本地测试接收端
node scripts/test-receiver.mjs 9099            # 另开终端
curl -XPOST localhost:8080/api/v1/events/<event_id>/replay \
  -H 'content-type: application/json' -d '{"target_url":"http://127.0.0.1:9099/ok"}'
```

> 第 4 步默认会返回 **403 target_blocked**——这正是防护在生效（内网地址被拒绝）。
> 要演示本地重放，显式开启演示开关（生产禁止）：
> `start --replay-allow-host 127.0.0.1 --replay-allow-private`

---

## 2. 架构

```
浏览器 (React 19 + Mantine 8)
   │  /api/v1/*  REST            WS  /api/v1/inboxes/{id}/events/subscribe
   ▼
Go net/http（ServeMux）
   ├── /hooks/{token}   ← 捕获中间件（先于路由执行，1 MiB 流式上限）
   ├── /api/v1/*        ← 手写 JSON API（internal/httpapi）
   ├── /healthz         ← 探针（含数据库连通性）
   └── /                ← 内嵌 SPA
   ▼
internal/replay（目标校验 → 拨号复检 → 头剥离 → 超时 → 截断 → 落库）
   ▼
PostgreSQL：inbox / event / replay_attempt
```

**为什么这样选**

| 决策 | 理由 |
|---|---|
| 不用 `oapi-codegen` 生成前后端代码 | 上游项目生成文件不入库，`git clone` 后必须先 `go generate` + `npm run generate` 才能编译。改成手写 `/api/v1/*` + 手写 TS client，实现「克隆即构建」。契约只有一份：`internal/httpapi` 的 DTO ↔ `web/src/api/v1.ts`。 |
| 不用 ORM | 只有 3 张表、20 个查询；手写 SQL 让分页/筛选/聚合（LATERAL）可控可讲，避免隐式 N+1。 |
| 自研 40 行迁移器 | 内嵌 SQL + 事务 + `schema_migrations`，二进制自包含，启动即迁移，无版本漂移。 |
| 捕获是**中间件**不是路由 | 命中即返回，不进路由表；1 MiB 限制在读取阶段生效，超大 body 不会被完整缓冲。 |
| 前端不用 IndexedDB | 上游把浏览器本地库当第一数据源，换台电脑收件箱就没了。核心数据必须持久化到 PostgreSQL，故彻底删除 Dexie，服务端是唯一真源。 |

目录：`internal/{config,crypto,capture,notify,migrate,storage(+postgres,mem),replay,httpapi,http,pubsub,retention,cli}`，`web/src`，`migrations`（内嵌于 `internal/migrate/migrations`），`scripts`（演示与测试接收端）。

### 刻意不做的选型（有意识地不做，而非没想到）

| 不引入 | 理由 |
|---|---|
| ORM / sqlc | 3 张表、约 15 条 SQL；手写 scan 已完成且已测。换掉会毁掉 `ListEvents` 的 `LEFT JOIN LATERAL`（一次查询带出重放次数与最近结果，避免 N+1），任何 ORM 都会把它变成 N+1 或逼你写 Raw |
| ULID / UUIDv7 | 每箱硬顶 500 条，UUIDv4 的 B-tree 页分裂代价低于测量噪声；改动面覆盖 storage / crypto（AAD 绑定）/ DTO / 全部测试。若数据量上千万再评估 |
| keyset 分页 | 最深 25 页；keyset 会砍掉"直接跳第 N 页"的交互，且 `count(*)` 在箱内走索引无压力 |
| TanStack Query / Zustand | 4 个页面、无跨页共享状态；且"WS 推元数据 → 重新 load 当前页"已天然解决缓存失效，引入反而会造成"缓存旧页 vs 新事件"不一致 |
| react-hook-form + zod | 全项目仅 1 个必填字段 + 1 个 URL，两个纯函数足够 |
| viper / 配置文件 | 安全相关配置（EncryptKey / AuthToken / AllowHosts）走多源优先级会让"哪个值生效了"变模糊 |
| Redis / 消息队列 / K8s / Helm | 单实例部署；pub/sub 已抽象为 `pubsub.PubSub[T]`，多副本时替换 `internal/pubsub` 一处即可 |
| testcontainers / Playwright | 贡献者环境不一定有 Docker / 浏览器；`mem` 驱动 + `TEST_PG_DSN` 已覆盖需求 |
| Prometheus / OpenTelemetry | 当前规模用不上；日志 + 请求 ID 足以串起"接收 → 重放"全链路 |
| JWT / 注册登录 | 不要求登录体系；API Key → 租户映射是最小可用形态 |

---

## 3. 数据模型

| 表 | 关键列 | 说明 |
|---|---|---|
| `inbox` | `id, owner_key, name, token UNIQUE, enabled, response_*, signing_secret_enc, require_signature, retention_max_events/days` | 收件箱；`token` 唯一索引，轮换后旧地址立即失效 |
| `event` | `inbox_id, method, path, query, content_type, headers jsonb, body bytea, body_text text, body_size, client_ip, signature_valid` | `body` 存原始字节；`body_text` 是可搜索镜像 |
| `replay_attempt` | `event_id, attempt_no, retry_of, target_url, edited_input, started_at, finished_at, duration_ms, status_code, response_preview, preview_truncated, outcome, error` | 每次尝试一条记录，含被策略拒绝的 |

索引：`(inbox_id, created_at DESC)`、`(inbox_id, content_type)`、`body_text` 的 pg_trgm GIN。

---

## 4. 关键设计说明

### 4.1 Token 生成方式

`crypto/rand` 取 **32 字节（256 bit）**，映射到 **31 字符无歧义字母表**（`abcdefghijkmnpqrstuvwxyz23456789`，去掉 `0/O/1/l/I`），得到 51 字符 token。URL 安全、可手抄、不可枚举；`inbox.token` 上有唯一约束，轮换时若（极小概率）冲突会自动重生成。接收入口 `POST /hooks/{token}` **不需要登录**。

### 4.2 原始数据保存

请求体以 `bytea` **逐字节**入库，全链路不解码、不重编码、不裁剪。详情页的「JSON 美化」是纯前端展示行为（`prettyPrint` 只影响渲染），保存与重放读的都是同一份原始字节。测试用例 `TestCapture_BodyIsPreservedByteForByte` 用含 `NUL`、非法 UTF-8、CRLF 的字节序列做逐字节断言。

### 4.3 敏感请求头策略

- **存储**：`authorization / proxy-authorization / cookie / set-cookie / x-api-key / x-auth-token / x-csrf-token` 在入库前就处理——配置了 `--encrypt-key` 时用 **AES-256-GCM** 加密（HKDF-SHA256 按 inbox 派生密钥，inbox id 作为 AAD，密文挪到别的行解不开）；未配置时替换为 `***redacted***`。**任一种情况都不会有明文进库**，并置 `sensitive=true` 标记。
- **展示**：默认显示掩码；只有在服务端启用了访问控制（`--auth-token`）时才允许 `?reveal=1` 解密查看。
- **重放**：这些头一律不转发（见 4.5）。
- **取舍**：没有加密密钥时，原始值确实不可恢复——这是有意选择（宁可丢失也不明文落库），此处明确说明。

### 4.4 请求体上限

`--max-request-body-size`，默认 **1 MiB（1048576）**。实现用 `http.MaxBytesReader(w, body, max+1)`：预算是 `max+1` 是因为「正好 1 MiB 通过」与「超过 1 MiB 拒绝」必须能区分，多读的那一字节就是判据；超限立即 **413 且不落库**（另有 `Content-Length` 预检做快速失败）。测试覆盖「正好 1 MiB 通过」「+1 字节 413」「10 MiB 不被完整缓冲」。

### 4.5 重放：目标地址限制（SSRF）

三层防护，全部默认开启：

1. **提交时校验**：scheme 只允许 `http/https`；URL 不允许 userinfo；对主机名做 DNS 解析，**任一**解析结果是保留地址即拒绝。
2. **拨号时复检**（`transport.DialContext` 包装）：每建一条连接都重新解析并判定目标地址。既防 DNS rebinding，又因为「每连接都跑」而天然覆盖重定向的每一跳。
   > 为什么不是 `net.Dialer.Control`：两个钩子拿到的地址都是 `host:port`，解析发生在 `net.Dialer` 内部，**都看不到解析后的 IP**。早期版本误以为 `Control` 能拿到 IP，于是把"主机名"一律判为不可校验，结果**所有公网域名的重放都失败**。现在由包装器自己 `SplitHostPort` → 解析（5s 超时）→ 对每个解析结果套用同一套 `blockedIP()` 规则。
3. **重定向**：默认**不跟随**（`MaxRedirects=0`）；若显式配置 >0，则每一跳重新执行完整校验（含 DNS 与拨号复检）。

被拒绝的地址：loopback、`0.0.0.0/8`、RFC1918、CGNAT `100.64/10`、链路本地 `169.254/16`（含云元数据）、`192.0.0/24`、benchmark `198.18/15`、组播与广播、IPv6 ULA `fc00::/7`、链路本地 `fe80::/10`、6to4 `2002::/16`、NAT64 `64:ff9b::/96`、IPv4-mapped。出站 `Transport.Proxy` 恒为 `nil`，不读代理环境变量。

**可验证**：`replay_attempt` 会为**被拒绝的**尝试也落一条 `outcome=blocked` 记录；49 条 URL 用例在 `TestValidateTarget_Table` 里逐条断言（含 `http://0.1.2.3/`、`http://[2002:7f00:1::]/`、十进制/十六进制/短写 IP、DNS 解析到私网与公私混合）。

**Allow list 的语义**：`--replay-allow-host` 只能**收窄**、不能放宽 IP 策略；要允许保留地址必须**同时**显式传 `--replay-allow-private`（启动时会校验这种组合，避免"配了却不生效"）。默认两者都关，本地演示才需要开。

#### 防护边界（明确不做什么）

本方案不宣称是"完美的生产级 SSRF 防护"，以下场景明确不覆盖：

1. **不限制端口**：`https://example.com:22/` 会被放行（只限制地址，不限制端口）。
2. **不防"公网主机被用作跳板"**：目标主机自身发起的内网访问不在防护范围（需 egress 代理或网络隔离）。
3. **不校验响应内容**：只保存前 4096 字节预览，不解析、不渲染。
4. **DNS 结果在校验后到连接前的窗口内理论上仍可变**：连接在建立时会复查真实 IP（`DialContext` 中的复检），已把风险降到最低，但**不做"解析即锁定 IP"的 pinning**。
5. **不做 URL 维度的速率/并发限制**。
6. **不做 IPv6 保留段穷举**（已覆盖 ULA/链路本地/6to4/NAT64/IPv4-mapped，未覆盖全部 IANA 保留段）。

### 4.6 重放：超时分层与结果分类

| 层 | 值 |
|---|---|
| DNS 解析 | 5 s（`resolveTimeout`，校验与连接复检各一次） |
| 拨号 / TLS 握手 / 响应头 | `--replay-timeout`，默认 **10 s** |
| 单次尝试总时长 | 同上（`http.Client.Timeout`） |
| 重试次数 | `--replay-max-retries`，默认 **0**（上限 5） |
| 退避 | `--replay-backoff`，默认 500 ms，指数增长 + ±20% 抖动 |
| **最坏总时长** | `timeout × (1 + retries)` + 退避累计（默认即 10 s） |
| 捕获侧 | read 60 s / write 60 s / idle 120 s / 优雅关停 15 s |

结果分类：`success`（2xx）、`http_error`（收到响应但非 2xx，仍存状态码与预览）、`timeout`（客户端超时）、`network_error`（DNS/连接/TLS）、`blocked`（**被策略拒绝，请求从未发出**）。

### 4.7 重放：其它规则

| 规则 | 实现 |
|---|---|
| 保留原始 body 与 Content-Type | 直接取 `event.body`；`Content-Type` 始终带上（编辑时以编辑值为准） |
| 不转发 Host / Content-Length / Authorization / Cookie / X-API-Key | `neverForward` + `capture.IsSensitive` 双表过滤；`Content-Length` 由 Go 按实际发送字节重算 |
| 客户端超时 | `--replay-timeout`（默认 10s），覆盖拨号、TLS、响应头与整体 |
| 响应只存有限预览 | `io.LimitReader(max+1)`，默认 4 KB，`preview_truncated` 标记是否截断 |
| 2xx=成功，其它=已响应但业务失败 | `outcome = success \| http_error`，非 2xx 仍保存状态码与预览 |
| 每次尝试都记录 | 目标 URL、开始/完成时间、耗时、状态码、预览、outcome、错误；重试链用 `attempt_no` + `retry_of` 关联 |

### 4.8 访问控制与多租户

两种模式，都**不影响**公开接收入口（第三方永远可以无凭据投递）：

| 模式 | 用法 | 效果 |
|---|---|---|
| 单令牌 | `--auth-token <key>` | UI 与 `/api/v1` 需要该令牌；所有数据属于默认租户 |
| 多租户 | `--auth-keys alice:<kA>,bob:<kB>` | 每个密钥对应一个租户；租户即 `inbox.owner_key` |

多租户下**列表过滤**与**按 ID 归属校验**都生效：alice 看不到 bob 的收件箱；即使 alice 拿到了 bob 的收件箱 UUID，直接按 ID 访问也返回 **404**（不是 403——"它存在"本身也是信息）。事件、重放、导出、WebSocket 订阅同样校验归属。

未配置任何密钥时 API 完全开放（本地开发默认），此时所有数据属于默认租户。前端的「显示敏感头原始值」需要 `--auth-token` 才可用。

### 4.9 其它边界行为

- Token 不存在 → **404**（不泄漏 token 是否存在）；收件箱停用 → **403**；body 超限 → **413**；强制签名且签名无效 → **401**（**fail closed**：开了 `require_signature` 却没配密钥时一律拒绝）。
- 来源 IP 默认取 socket 地址；只有在 `--trust-proxy-headers` 且部署于会重写 `X-Forwarded-For` 的反代之后才信任该头（**不接受用请求头来决定是否信任请求头**）。
- CORS 预检（`OPTIONS`）直接 204，不产生事件。
- 实时推送的 WebSocket 校验 `Origin`：跨站页面无法订阅事件流（无 `Origin` 的脚本客户端仍可用，由 `--auth-token` 保护）。

---

## 5. API 一览（`internal/httpapi/api.go`）

```
GET    /api/v1/settings                      服务端限制（UI 直接展示，不用猜）
GET    /api/v1/inboxes?q=&enabled=&limit=&offset=
POST   /api/v1/inboxes                       {"name":"…"} -> 201
GET    /api/v1/inboxes/{id}
PATCH  /api/v1/inboxes/{id}                  改名/启停/签名配置/保留策略
DELETE /api/v1/inboxes/{id}
POST   /api/v1/inboxes/{id}/token/rotate     旧地址立即失效
GET    /api/v1/inboxes/{id}/events?from=&to=&content_type=&method=&q=&limit=&offset=
DELETE /api/v1/inboxes/{id}/events           清空
GET    /api/v1/inboxes/{id}/events/subscribe WebSocket 实时推送
GET    /api/v1/events/{id}?reveal=1
DELETE /api/v1/events/{id}
GET    /api/v1/events/{id}/export?format=curl|json&reveal=1
POST   /api/v1/events/{id}/replay           {"target_url":…,"body_base64":…,"sign":true}
GET    /api/v1/events/{id}/replays
```

统一错误体：`{"error":{"code":"…","message":"…"}}`；`/replay` 返回 `ok` 字段（HTTP 200 仅表示「尝试已执行并已记录」，不代表目标成功）。

### 5.1 `webhook-zq status --json`：机器可读的状态契约

```console
$ webhook-zq status --port 8080 --json
{
  "running": true, "pid": 12345, "port": 8080, "healthy": true,
  "exe_path": "D:\\webhook-zq\\bin\\webhook-zq.exe",
  "config_path": "C:\\Users\\me\\AppData\\Local\\webhook-zq\\config.json",
  "dsn_masked": "postgres://postgres:***@127.0.0.1:5432/webhook_rd",
  "database_reachable": true, "frontend_embedded": true,
  "version": "1.4.0", "build_time": "2026-09-13T10:00:00Z",
  "log_path": "logs/server.log"
}
```

**这是对外承诺的稳定契约**，不只给自家脚本用。约定：

- 只**新增**字段，不改名、不改语义、不删除；破坏性变更只在主版本号提升时发生。
- 所有字段都是值类型，**JSON 里不会出现 `null`**；取不到值时数字为 `0`、字符串为 `""`、布尔为 `false`。不要写 `if (x === null)`。
- 密码**只在服务端脱敏**（`dsn_masked`），消费方不要再自己实现一遍掩码。

| 字段 | 类型 | 含义 | 取不到值时 |
|---|---|---|---|
| `running` | bool | 端口上有进程在监听 | `false` |
| `pid` | int | 该进程 PID | `0` |
| `port` | uint16 | 查询的端口 | 回显入参 |
| `healthy` | bool | `GET /healthz` 返回 200（含数据库连通性） | `false` |
| `exe_path` | string | 该进程的可执行文件完整路径 | `""` |
| `config_path` | string | 解析出的配置文件路径（Windows `%LOCALAPPDATA%`、macOS `~/Library/Application Support`、Linux `$XDG_CONFIG_HOME` 或 `~/.config`） | `""` |
| `dsn_masked` | string | 已脱敏的连接串——主机与库名保留，密码替换为 `***` | `""` |
| `database_reachable` | bool | 二进制侧直连数据库是否成功 | `false` |
| `frontend_embedded` | bool | 二进制里打包的是**真实前端**还是「前端尚未构建」占位页 | `false` = 占位页，界面打不开 |
| `version` | string | 构建时由 `git describe --tags --always --dirty` 注入 | `0.0.0@undefined`（`go run` 未注入时） |
| `build_time` | string | UTC 构建时间 | `unknown` |
| `log_path` | string | 日志文件的**相对**路径（相对进程工作目录） | **始终有值**——它是拼接出来的，不代表文件已存在，也不是绝对路径 |

**已知的消费方**（改字段前请一并检查）：

| 消费方 | 位置 | 用到的字段 | 降级行为 |
|---|---|---|---|
| `scripts/start-gui.ps1` | 84-100 | `port` `running` `pid` `healthy` `exe_path` `frontend_embedded` | 拿不到 `status --json`（旧二进制 / 报错）时回退到自己按端口探测，并在 `Source` 标注 |

用法：

```bash
webhook-zq status --port 8080 --json | jq -r '.healthy'
```

```powershell
$s = webhook-zq status --port 8080 --json | ConvertFrom-Json
if (-not $s.healthy) { Write-Warning "端口 $($s.port) 上不健康（pid $($s.pid)）" }
```

---

## 6. 测试

```bash
go test ./...                      # 单元/组件测试（无需数据库、无需网络）
pwsh -File _base/e2e.ps1           # 端到端（真实 PostgreSQL）
```

已覆盖（Go 侧 13 个包，前端 148 个用例 / 15 个文件，纯函数 + 组件 + 页面三层）。
用例数会随提交增长，以 `./make.ps1 test`（或 `make test`）的实际输出为准：

| 包 | 内容 |
|---|---|
| `internal/replay` | **49 条 URL 策略用例**（含 `0.1.2.3`、6to4、NAT64、v4-mapped、十进制/十六进制/短写 IP、DNS seam 的私网与公私混合）、`blockedIP` 表、`checkDialAddress` 表、客户端加固（超时/Proxy 恒 nil）、**重定向 4 条**（默认不跟随 / 公网链跟随 / 私网跳被拦 / 次数上限）；success / 500 仍存预览 / 超时 / 网络错误 / blocked 落库 / 头剥离 / 签名基于原始字节 / 退避 |
| `internal/http/middleware/webhook` | 1 MiB 通过、+1 字节 413 且不落库、10 MiB 不缓冲、404/403、body 逐字节保真、强制签名无密钥 401、签名有效/无效、来源 IP 不可伪造、OPTIONS 不落库、敏感头打码 |
| `internal/capture` / `internal/crypto` | 掩码与加密两条路径、排序、Reveal 跨 inbox 失败关闭、HKDF/AES-GCM 往返与篡改、HMAC 常量时间比较 |
| `internal/storage/mem` | 二进制 body 不可搜索（与 PostgreSQL 同规则，防止驱动分叉导致"测试假绿"）、文本 body 可搜索、按条数保留 |
| `internal/retention` | 按每收件箱限额清理（不同限额的收件箱互不干扰）、空库无操作、**遍历所有租户**（回归：曾因 `owner_key` 默认为 `default` 而一次都没执行过） |
| `internal/httpapi` | handler 级：建箱校验、列表筛选与分页夹取、**租户归属（他人资源返回 404）**、PATCH 部分更新与非法值拒绝（204/304、未知签名方案）、**重放被拦截也要落库**、**限流 429 + `Retry-After`**、敏感头默认掩码、cURL 导出不含明文、未知路由 404 |
| `internal/ratelimit` | 限额边界、窗口重置、按 key 独立、0 = 不限、并发下计数精确 |
| `internal/config` | 13 组非法配置必须被拒（含"allow host 是私网地址但未开 AllowPrivate"这种配了不生效的组合）、合法默认值通过 |
| `web`（vitest） | 148 个用例：纯函数（base64 往返含多字节、`prettyPrint` 非法 JSON 返回 null 且不抛、字节格式化、筛选参数）+ `errors`（按 code→status→兜底，**服务端英文绝不外泄**）+ `clipboard`（三级回退、`execCommand` 也抛时不崩、不留 textarea）+ `OutcomeBadge`（七种状态都有标签/图标/下一步）+ **页面级**（收件箱列表空状态三分支与 token 打码；事件详情敏感头掩码→reveal；**429 时事件正文必须仍在**；倒计时递减；从文案兜底解析 `Retry-After`；**被拦截只走结果卡**） |
| `internal/storage/mem` | 内存驱动，让 handler 级测试不依赖数据库 |

**测试不放松运行时限制**：DNS 与拨号注入（`Policy.lookupIP`、`Policy.dialer`）都是**未导出字段**，只有包内 `_test.go` 能设置，生产配置无法关闭任何一条防护；测试里 `ValidateTarget` 仍然全量执行。

---

## 7. 已知边界与未完成项（如实说明）

1. **不是完整生产级 SSRF 方案**：不做出站代理白名单、不防 DNS 慢速重绑定以外的攻击面、未穷举全部 IPv6 保留段。定位是「不追求生产级完整，但绝不能无约束」。
2. ~~多租户未完成~~ 已落地（见 4.8）。剩余限制：租户信息只来自 API 密钥，没有用户/角色体系，密钥轮换需重启或重新下发配置。
3. **实时推送只在单实例有效**：in-memory pub/sub，多副本需 Redis（已删除 Redis 驱动）或改用 PG `LISTEN/NOTIFY`。
4. **重放重试对非幂等请求有副作用**：5xx 会自动重试，第三方多为非幂等 POST。默认 `--replay-max-retries=0`（关闭），开启需自行评估。
5. **关键字搜索只覆盖可搜索镜像**：`body_text` 仅在 body 是合法 UTF-8 且不含 `NUL` 时写入，**且截断到 64 KiB**（避免 1 MiB 正文让 trigram 索引爆炸）；二进制 payload 不参与搜索（仍可查看与重放）。0002 之前写入的历史行由 0003 回填，含 NUL 或非法编码的行保持不可搜索。
   - 索引说明：`body_text` 建了 GIN trigram 索引；中英文短词（1–2 字符）基本用不上该索引会退化为顺序扫描，演示数据量下有无索引都够用——**没有做过大规模 `EXPLAIN` 验证，不建议宣称"搜索很快"**。
6. **分页为 LIMIT/OFFSET + count(*)**，深翻页会变慢；排序用 `(created_at, id)` 消除同毫秒下的重复/漏行。
7. **迁移无 advisory lock**，多副本同时冷启动理论上可能重复应用同一迁移。
8. **速率限制只覆盖重放**：`POST /api/v1/events/{id}/replay` 有按调用方（优先租户、回退 peer IP）的限额，默认 30 次/分钟（`--replay-rate-limit`，0 = 关闭），超限返回 429 + `Retry-After`；**`/hooks/{token}` 收件入口未限流**，生产应前置反代。
9. **`reveal` 需要访问控制**：只有配置了 `--auth-token` 或 `--auth-keys` 时 `?reveal=1`（也接受 `reveal=true`）才生效（未启用鉴权时 API 是开放的，解密敏感头会失去意义），每次解密会记审计日志（事件 ID + 来源地址）。
10. **`--replay-allow-private` 是按主机豁免，不是全局关闭防护**：只有同时出现在 `--replay-allow-host` 里的主机才允许解析到保留地址，其它主机的拨号复检照常执行。它仍然要求「白名单主机 + 显式开关」两个条件同时满足，请勿在公网部署开启。
11. **自定义响应码不能是 204/304/1xx**：这些状态码按规范不带响应体，无法回传事件 ID，PATCH 时会直接拒绝并说明原因。
12. **保留策略是后台周期任务**（默认每 10 分钟，`--retention-interval`），不是写入时即时裁剪：刚超过限额的几分钟内仍可能查到多余事件。
13. **未做**：HMAC 之外的签名算法、请求编辑的历史对比、`/hooks/{token}` 收件入口的限流。

---
## 10. 常用参数

```
--database-url            PostgreSQL DSN（必填；flag > DATABASE_URL > config.json）
--port / --addr           监听地址（默认 8080 / 0.0.0.0）
--max-request-body-size   1 MiB
--replay-timeout          10s
--replay-max-preview      4096
--replay-max-redirects    0（禁用重定向）
--replay-max-retries      0
--replay-allow-host       可重复，仅用于收窄
--replay-allow-private    演示用：允许白名单主机指向保留地址
--retention-max-events    500（每箱）
--retention-max-days      30
--retention-interval      10m
--auth-token              设置后 UI 与 /api/v1 需要鉴权（/hooks 仍公开）
--encrypt-key             base64 32 字节主密钥（敏感头加密与签名密钥存储）
--trust-proxy-headers     仅在可信反代之后开启
--use-live-frontend       开发时读取 web/dist 而不是内嵌资源
```
