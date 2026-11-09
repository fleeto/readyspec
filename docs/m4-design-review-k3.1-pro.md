# M4 验收计划评审与讨论记录

评审对象：[M4 验收矩阵与环境清单](m4-acceptance-plan.md)。实现基线：`main@d370b54`。主设计者：Codex；独立评审者：通过 pi 调用的 `magpie / kimi-code-cn/k3.1-pro`。

本记录只涉及计划评审，不代表 M4 实机测试、资源盘点或性能验收完成。两轮评审已完成，主设计者与独立评审者已对修订计划达成一致，状态为**评审通过，待实施**。

## 调用与证据

使用 `pi --provider magpie --model kimi-code-cn/k3.1-pro --thinking medium`，禁用工具、扩展、MCP、技能、模板、主题和额外上下文发现；显式提供任务相关文档和源码，不读取身份材料、不操作目标。

首轮输入：计划、SPEC、DESIGN、Makefile、M2/M3 实施记录、`internal/executor/native_ssh.go`、`internal/template/registry.go`。首轮计划 SHA-256：`cd4de98d6362317190acd56b3526e3988b0bd725f4ec2e4d2cf1c1760cf5d5a7`。

原始输出、输入摘要及定向复审资料保存在本任务临时目录 `/tmp/opencode/readyspec-m4-review-LagXZP/`。各轮结论、逐项回应及最终关闭情况记录如下，最终评审原文附在文末。

pi 版本：`1.5.0`。模型目录实际返回 `magpie kimi-code-cn/k3.1-pro`；未使用 highspeed 或其他模型替代。调用形式：

```sh
pi --provider magpie --model kimi-code-cn/k3.1-pro --thinking medium \
  --no-extensions --no-mcp --no-skills --no-prompt-templates \
  --no-themes --no-context-files --no-tools --no-session --print \
  @round1-prompt.md @docs/m4-acceptance-plan.md @SPEC.md @DESIGN.md \
  @Makefile @docs/m2-implementation-design.md @docs/m3-implementation-design.md \
  @internal/executor/native_ssh.go @internal/template/registry.go
```

## 首轮结论与修订

首轮结论原文：

> 暂不通过。存在 1 项 P0、1 项 P1 设计阻断；两项均须关闭并复审后，计划才能进入实施。
> 这只是计划评审，不代表 M4 实机验收完成，也不要求现在执行实机测试。

| 编号 | 模型意见 | 主设计者核对与修订 |
|---|---|---|
| P0-01 | MIN 无 Python 却以 S01 作为成功闭环；注册表为 S01 声明 python3 | 采纳。核对 requirements 与 H01 编译命令后，§4.3 成功 Run 改为 H01（uname）+ POSIX printf/exit 基础 Shell；S01 缺依赖单列 M4-MIN-03，ERROR/不得READY，不替代F03成功证据；FULL 六平台36模板覆盖不变。§4.1 18格明确使用FULL变体 |
| P1-01 | noexec/只读只笼统要求回退，可能仅测 STANDARD 就签署包含特权安全的 F10 | 采纳。§4.3 为两模式固定 SPEC 候选顺序，逐层限制前置候选、单独验证 /tmp 受限和显式路径、全部受限时该模式零执行；补普通/特权权限、root二次验真、env-i、token、清理、root/NOPASSWD sudo证据 |

首轮 P2：无。模型确认其余 F01–F14、18格、36模板、Windows11、身份屏障、五类E2E、故障清理、20×100/race/资源/守恒及版本证据已有覆盖。TODO-02 作为非阻断说明，继续冻结未验证的高级协议；若后续启用，仍须另行完成16KiB/转义/并发关联等规格验证，不把本次评审当作启用依据。

## 定向复审

将首轮原始输出、逐项回应、修订后完整计划及相关依据重新交给同一 provider/model，要求逐项确认关闭情况并检查修订是否引入新冲突。

第二轮计划 SHA-256：`1cf051593ae5f5d11669ca845df334d33a845e99d9e092089fadab890e0abde1`。本轮显式输入包括首轮原文、逐项回应、修订计划、SPEC、template/registry.go、compiler/remaining.go 和 Makefile；后者用于核查 H01 确实采用 uname 采集，不依赖 Python。

主代理静态检查通过：18格均保持 NOT_RUN；六目标、F01–F14、五个端到端场景完整；目录中的36模板与计划范围匹配；MIN成功/缺依赖反例分离，两模式候选顺序及零执行判据齐全；本地文档链接、空白与 `git diff --check` 无问题。本次未运行实机、Go/Web 回归或压测，因为变更仅为计划文档，不产生实现验收结论。

第二轮结论：P0-01、P1-01 均关闭；新增设计阻断、必须修订及新增非阻断建议均无。主设计者核对后接受，双方达成一致。复审后仅更新计划的状态和评审结果，不再修改验收规则。实施仍先处理 GAP-01/02，再按计划逐阶段执行；所有实机格子保持 NOT_RUN。

## 第二轮评审原文

> **评审通过，无未关闭的设计阻断，可进入实施；不代表M4实机验收完成。**

### 首轮阻断关闭情况

**P0-01：已关闭**

修订已消除 MIN 成功闭环与 S01 Python 依赖之间的冲突：

- §4.3 `M4-MIN-01` 改用仅依赖 `uname` 的 H01，以及仅使用 POSIX `printf` 常量并 `exit 0` 的基础 Shell，不再以 S01 充当无 Python 环境的成功证据。
- 成功闭环明确要求有效规范化 PASS、投递、验真、回收和清理证据，并将 `READY/0` 限定于 RELEASED、GateRun、FullRun、必检及拓扑全部满足的运行。
- §4.3 `M4-MIN-03` 将 S01 缺 Python3 单列为独立反例，预期 ERROR，不得 READY，也不作为 F03 成功证据。
- §3.2 的 MIN/FULL 边界、§4.2 六平台 36 模板六类用例以及 §4.1 十八格 FULL 要求均保持明确，没有借 MIN 调整减少完整认证范围。

**P1-01：已关闭**

§4.3 `M4-TMP-01` 已明确覆盖两种执行方式及各自候选顺序：

- STANDARD：`显式指定 → /tmp → /var/tmp → $HOME/.cache`。
- PRIVILEGED：`显式指定 → /run → /var/tmp → /tmp`。
- 要求逐层制造 noexec、只读限制并验证回退，同时覆盖显式路径优先、无效显式路径受控回退及全部适用候选受限。
- 全受限时要求该模式 ERROR 且 Goss 执行计数为零，并明确不得抹除混合 Run 中此前取得的 STANDARD 结果。
- `/tmp` 单独受限但 PRIVILEGED 已选择 `/run` 时，要求记录真实选择，不伪造回退。
- 证据涵盖实际目录、执行方式与计数、权限、归属令牌、普通/root 验真、特权二次验真、环境清洗和清理，并覆盖 root 直连及 NOPASSWD sudo、`sudo -k`。
- 显式路径配置入口未实现时纳入 GAP-01，不允许通过省略用例规避；F03/F10 的阻断关系仍然有效。

新增设计阻断：无。

必须修订：无。

非阻断建议：无新增非阻断建议。
