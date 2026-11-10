# 用户试用反馈改进计划评审记录

日期：2026-11-10。评审对象：[改进计划](usability-improvement-plan.md)，实现基线 `027cf8c`。

主设计者 Codex；独立评审者为 Pi 1.5.0 显式调用的 `magpie / kimi-code-cn/k3.1-pro`。`pi --list-models 'k3.1'` 已确认该精确模型存在，没有使用 highspeed 或其他模型替代。

## 调用和范围

使用 `--thinking medium --no-extensions --no-mcp --no-skills --no-prompt-templates --no-themes --no-context-files --no-tools --no-session --print`，仅附上计划、相关 Go/Web 代码与设计规范。不提供现场报告、凭据或用户主机数据，不允许模型操作目标或修改文件。

首轮原始输出：`/tmp/opencode/readyspec-usability-review-round1.txt`；提示文件：`/tmp/opencode/readyspec-usability-review-prompt.md`。第二轮对应 `readyspec-usability-review-round2.txt` 和 `readyspec-usability-review-round2-prompt.md`，均在同一临时目录。以下结论已与实际输出核对。

## 首轮结论与主动修订

Pi 首轮原文：**“必须修改的 P0：无”“必须修改的 P1：无”“必要 P2：无”“通过，可以定稿。”“尚未关闭的问题：无。”**

评审确认：容量精确值与参数三态、草稿保存、前后端门禁和异步失效、字段诊断、冻结名称、报告兼容、安全和无障碍均已闭合。主设计者接受该结论，并主动收紧三处边界后进行第二轮定向确认：

1. U2：零目标门禁仅作用新接受；先在原子接受临界区完成幂等/冲突处理，保留历史 Run 查询和同键重试，不误报 rejected；补凭据清理与并发验收。
2. U3：普通元数据不可用可回退 ID，401/403 则继续触发既有会话/权限错误流程，不静默吞错或绕过鉴权。
3. U1：仅展示解析后的用户名，未设置则明示；不为文案新增用户名来源追踪。U2 校验路径已与实际注册路由核对。

## 第二轮确认与定稿

第二轮实际命令：

```sh
pi --provider magpie --model kimi-code-cn/k3.1-pro --thinking medium \
  --no-extensions --no-mcp --no-skills --no-prompt-templates \
  --no-themes --no-context-files --no-tools --no-session --print \
  @/tmp/opencode/readyspec-usability-review-round2-prompt.md \
  @/tmp/opencode/readyspec-usability-review-round1.txt \
  @docs/usability-improvement-plan.md @internal/runmanager/manager.go \
  @internal/runstore/store.go @web/src/api.ts @internal/plan/types.go
```

第二轮完整结论开头原文：

> **通过，可以定稿；无未关闭设计问题。** 未发现新的 P0/P1 阻断或必要 P2 修改。此次修订已把关键顺序落实到现有代码 seam，不只是重复首轮原则。

Pi 逐项确认：

- **接受顺序**：`Store.AcceptWithCommit` 在锁内做幂等比较；零目标独立校验应位于幂等处理后、`beforeCommit` 前。现有 `validateSubmission` 在幂等比较前，不能直接放进去。原请求返回、冲突、并发和凭据清理的验收充分。主设计者据此把具体落点明确写入计划 §4.2。
- **鉴权分流**：`api.ts` 的401清理CSRF并通知会话失效；403抛出权限错误。计划将它们与普通名称获取失败分流，没有增加匿名下载；冻结名称、联合键和历史报告兼容仍成立。
- **必填与草稿**：原生 required 服务于推进/发布/执行，草稿保存须走不触发原生约束校验的路径；用户名不新增来源追踪，不推断连接成功。计划的允许保存未完成值覆盖此要求。
- **其余范围**：容量精确边界、可选诊断字段、不重算 Verdict、按钮键盘与响应式、保留主机身份校验均无新增设计缺口。

主设计者逐条对照代码后接受第二轮结论，将计划标记为定稿。首轮和第二轮均无未关闭 P0/P1/P2；没有用其他模型代替指定评审，也没有把通过结论扩大为实现完成。

## 文档验证

设计评审阶段只新增计划和评审记录。已检查两份文档的 Prettier 格式、空白和相对链接，并核对六条反馈到 U1–U5 的覆盖。该阶段没有业务代码变动，未宣称实现测试或 SSH 实机验收已经通过。随后用户授权实施，实际交付和验证见计划 §9；本文的 Pi 结论仍仅适用于设计。

本次仅评审计划；未实施 U1–U5，也不替代后续实现和平台验收。

后续用户明确要求去除强制 known_hosts 依赖，当前计划 U5 据此改为默认 record-only / 可选 strict。本文保留原设计评审事实，不能把此前“保留强制核验”的评审结论用来阻止用户的新要求，也不声称 Pi 已评审该后续变更。
