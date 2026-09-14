# 贡献指南

感谢你愿意花时间在这个项目上。目标很简单：**让一个陌生人在 30 分钟内把项目跑起来，并有机会提交第一个 PR。**

## 1. 把项目跑起来

需要三样东西：Go 1.27+、Node.js 20+、PostgreSQL 16+（含 `pg_trgm` 与 `pgcrypto` 两个扩展）。

```powershell
createdb webhook_rd
$env:DATABASE_URL = 'postgres://postgres:postgres@127.0.0.1:5432/webhook_rd?sslmode=disable'
./make.ps1 frontend           # 构建前端（前端会内嵌进二进制）
./make.ps1 build
./bin/webhook-zq.exe start --port 8080
```

> **Windows 没有 `make`**，所以用 `./make.ps1`（与 Makefile 同名目标）。Linux / macOS 用 `make`。

或者用 Docker（不需要本地 PostgreSQL）：

```bash
docker compose up
```

Windows 上也可以直接双击 `启动.bat`。

## 2. 跑测试

```powershell
./make.ps1 test               # Go 单测 + 前端单测
./make.ps1 accept             # 端到端断言（需要 PostgreSQL 与已构建的二进制）
# Linux / macOS：make test / make accept
```

`make accept` 会跑 `scripts/acceptance/` 下的脚本：它们是真的起服务、真连数据库，用来覆盖 Go 单测覆盖不到的部分（例如"重放到本机成功"与"重放到云元数据地址被拒"必须同时成立）。

## 3. 代码约定

- **注释一律用英文，面向用户的文案一律用中文。** （代码是给所有开发者看的，UI 是给中文用户看的。）
- Go：`gofmt`，并且 `go build ./...` 必须通过。新增逻辑请配一个测试。
- 前端：改完跑 `npm run build && npm run test`。
- **配置相关的改动请特别小心**：`--replay-allow-private`、`--encrypt-key`、`--auth-*` 是安全边界，涉及它们的 PR 请在描述里说明攻击面变化。



## 4. 提交 PR

- 一个 PR 做一件事，描述里说清"改了什么 / 为什么 / 怎么验证"。
- 如果是修 bug，请附复现步骤。
- 涉及安全边界的改动，请说明它是否放宽了限制。

## 5. 报告安全问题

请不要在公开 Issue 里贴你的实例地址、凭据或内部网络结构。涉及 SSRF、密钥泄漏等问题，请私下联系维护者。
