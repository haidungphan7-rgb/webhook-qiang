## 改了什么

<!-- 一两句话说清这个 PR 做的事；有关联 issue 就写 Closes #N -->

## 为什么这么改

<!-- 动机与权衡。涉及安全边界（重放白名单、脱敏、鉴权、SSRF）的必须说明为什么不会放宽防护 -->

## 怎么验证的

<!-- 实际跑过的命令与结果，不要写「应该没问题」 -->

- [ ] `make test`（Windows：`./make.ps1 test`）全绿
- [ ] 改了前端：`npm --prefix ./web run build` 成功、`npm --prefix ./web run test` 全绿

## 检查清单

- [ ] 用户可见的行为变化已同步到 README / docs
- [ ] 有破坏性变更时已在 CHANGELOG.md 的 Unreleased 登记并写明迁移方式
