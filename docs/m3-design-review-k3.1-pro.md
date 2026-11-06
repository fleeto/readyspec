# M3 设计评审与讨论记录

日期：2026-11-06。主设计者：Codex；独立评审者：通过 **pi** 调用的 **magpie / kimi-code-cn/k3.1-pro**（K3.1 Pro）。评审对象：[M3 实现设计与计划](m3-implementation-design.md)，实现基线 `0d2fc93`。

这是一份设计 Review，不是 M3 实现验收。评审仅读取仓库任务相关代码和文档，不修改业务代码、不运行目标 SSH、不读取凭据、不创建 AWS 实例。

## 1. 实际调用与评审范围

使用 `pi --provider magpie --model kimi-code-cn/k3.1-pro`，禁用扩展、MCP、自动技能及额外上下文发现。首轮允许 read/grep/find/ls，对照 SPEC、DESIGN、GLOSSARY、Go 及 Web 代码核查。首轮在连续读取代码约 20 分钟后仍未输出终审，主代理中断进一步读取，保留会话并以 `--no-tools` 让同一模型基于已读内容完成首轮结论；没有把中间进度当作最终通过。

随后主代理逐条回应、修订设计，并把首轮完整结论、回应和修订后全文交给同一模型做无工具定向复审，避免继续扩大检索范围。

可复用调用形式（会话/输出文件在任务临时目录，不在仓库保存模型配置或凭据）：

```sh
pi --provider magpie --model kimi-code-cn/k3.1-pro --thinking medium \
  --no-extensions --no-mcp --no-skills --no-prompt-templates \
  --no-themes --no-context-files --no-tools --print \
  @review-response.md @round1-review.txt @docs/m3-implementation-design.md
```

## 2. 首轮结论与主代理回应

首轮结论原文：**“需要修订后复审。总体范围与 SPEC 四视图及 M3 合流目标一致，无需等待实现完成；但上述 P1 属于会导致错误持久化、重复执行或错误展示的契约缺口，应先关闭再进入对应实施阶段。”**

| 编号 | K3.1 Pro 首轮 P1 | 主代理讨论与修订 |
|---|---|---|
| R1-1 | 草稿缺 revision；发布后旧草稿可能继续编辑并重复发布 | §4 为全部更新规定 expected_revision；旧无修订更新返回409；发布 envelope 权威消费 source_draft_key/source_revision，旧索引补写失败也不重新开放编辑 |
| R1-2 | archive-only 导入形状及 release manifest 核验未定义 | §4 明确 released/archive_only 变体、单向停用、后导入 RELEASED 不重新激活；定义清单字段、计算顺序、校验和旧清单无法核验时的明确诊断；不修改 PlanDigest 排除规则 |
| R1-3 | 基础断言组合/边界/错误分类和单次 Goss 采集尚未闭合 | §6 固定唯一求值函数、AND/RE2/有限单值/range/字节上限和错误优先级；主代理实际完成原 Goss 单次采集实验；复审后保留0–255并加入确定性启动帧区分受控入口状态，将原工件回归列为实施验收 |
| R1-4 | “秘密变化不改语义”忽略私钥/Agent 公钥指纹；Gate 限制只写在UI | §5.2 改成同身份/同非秘密语义复用原键；换钥不得绕过冲突；Go共享提交入口同样校验方案状态与清单，处理停用/接受竞争 |
| R1-5 | 九阶段投影缺实际起止事件，不能凭投影补历史 | §5.3 补九阶段词表、StageTransition、Preflight/Execute StageRecorder、空模式omitted和旧记录未记录；不替代持久化执行标记 |
| R1-6 | after_seq/header 和增量/snapshot 一致性未定义 | §5.3 固定游标冲突/非法值400、reset全替换；统一服务端reducer并要求同序号前缀的snapshot与增量投影相等 |
| R1-7 | 重跑丢 simulator_profiles/budgets；PartialRun 保留无效覆盖 | §5.4/§7 补全非秘密allowlist、可选预算提交、后端投影主机/组/角色/覆盖和对应画像，保留冻结 Plan 不裁剪 |
| R1-8 | “兼容旧报告”可能被用作新报告缺证据的理由 | §5.4 分开旧报告读取与M3新生产者约束；新证据必须有真实来源，缺失不能继续判完整PASS，不改旧归档 |

P2 讨论：

- **按主机认证覆盖：采纳澄清。** 一个 Run 仍只有一种认证方式/公钥身份；按主机覆盖限密码/sudo，不新增混合身份 Schema。
- **CLEAN/IGNORED 审计可查：采纳。** 增加已处置过滤、Run审计入口、常驻未结案提醒，不改原报告来伪造后续清理状态。
- **原始报告下载：保留权威报告与脱敏优先。** 不为了保留尚不支持的未来字段跳过Schema/owner-token清理；下载内容来自归档报告，不由当前页面重新拼接业务结果。
- **离线和遗留删除：采纳。** 离线 smoke 主动拒绝所有非回环请求；旧测试与 Go/Web 替代用例映射作为删除前置条件。

## 3. 主代理独立验证

从仓库读取并校验 `goss-linux-amd64` 的锁定 SHA-256，仅在任务临时目录运行受控 Shell 探针；每个探针先追加一次计数，再输出 stdout/stderr 并退出。配置同一 Goss command 的 exit-status 及 stdout/stderr `match-regexp`，按 JSON 的 resource-id/property 检查原始 actual。

实际输出摘要：

```text
observation: executions=1, goss_exit=0, exit-status=3,
             stdout="line-one\nline-two\n", stderr="error-one\nerror-two\n"
negative:    executions=1, goss_exit=1, 三项 actual 与 observation 完全一致
empty:          executions=1, exact_output_round_trip=true
unicode-escape: executions=1, exact_output_round_trip=true (含 CRLF/末尾换行)
64KiB:          executions=1, exact_output_round_trip=true (65536 bytes)
64KiB+1:        executions=1, exact_output_round_trip=true (65537 bytes)
```

这些结果只证明原 Goss 的单次三属性采集可行；65537 字节可回收不意味着 M3 应接受超限证据。M3 的断言求值器、超时/缺命令分类、Schema 与UI仍需实施并按计划验收，未把此实验称为完整断言实现或M4平台认证。试用 `eq` matcher 时 Goss 返回 Unknown matcher；最终设计采用已有编译器和本轮实验都证实的 `match-regexp` observation-only 路径。

## 4. 定向复审与最终结论

第二轮逐项结论：R1-1/2/4/5/6/7/8 均已关闭；R1-3 的 Goss 采集及断言主体关闭，但主代理新增的“126/127一律为ERROR”会破坏既有期望退出码语义。K3.1 Pro 要求只用可证实的启动/采集事实分类，不以数字推断命令未执行。主代理接受此反对意见，删除新增保留值，保留0–255。

为核查事实来源，主代理追加原 Goss 实验：缺失入口与不可执行入口均可能只有126/127且 `err=null`，所以不能承诺从 Goss.err 自动识别全部启动失败。最终设计采用受控 shell 内、用户正文前的确定性启动帧：一次执行采集，Normalize校验剥帧，再对原样用户输出求值；无/坏帧为无法确认启动的ERROR，不证明无副作用、不允许自动重跑。不是启用用户结构化结果协议，不使用随机数破坏编译确定性。

原 Goss 对照实测：

| 场景 | 退出码 | Goss.err | 启动帧 | 用户正文执行计数 | 分类依据 |
|---|---|---|---|---|---|
| 入口缺失 | 127 | null | 无 | 0 | 无法确认受控入口启动，ERROR |
| 入口不可执行 | 126 | null | 无 | 0 | 无法确认受控入口启动，ERROR |
| 正常 shell 主动 exit126 | 126 | null | 有 | 1 | 普通退出码断言，不能因数值误判ERROR |
| 正常 shell 主动 exit127 | 127 | null | 有 | 1 | 普通退出码断言，不能因数值误判ERROR |

有帧两例均验证剥帧后的用户 stdout 与原文完全一致。上述结果不是尚未实现的 Normalize 测试，正式Go回归仍列入 M3-1。

最后一次定向确认原文：

> 不影响已通过结论，且无剩余设计阻断。固定启动帧为 `/bin/sh` 已启动提供了可验证事实来源，避免依赖 Goss `err` 或退出码猜测；同次采集、精确校验剥帧、原样保留用户输出及 64 KiB 用户证据边界均已明确。无/坏帧归 ERROR 且不自动重跑，也未缩小 0–255 退出码语义。四组原 Goss 对照支持该设计；Go 原工件回归仍属既定实施验收。**设计评审通过，可进入实施；不代表实现验收。**

最终无未关闭设计阻断，后续按设计中的 M3-1 至 M3-6 实施。评审没有宣称新的生命周期、断言模块、阶段事件或UI已经实现，也没有将M4平台/性能认证提前标为通过。
