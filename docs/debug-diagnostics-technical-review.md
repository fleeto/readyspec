# 诊断输出设计技术复核记录

日期：2026-11-09。状态：三轮技术复核完成，当前无未关闭 P1/P2 设计意见，可进入分批实施。目标是完善实施计划；未声明全部诊断功能已经实现。

## 参与者与依据

- 主设计者：当前 OpenCode Codex 会话，负责代码核对、设计整合及取舍。
- 独立技术复核：Pi CLI，provider `magpie`，model `kimi-code-cn/k3.1-pro`，thinking `high`。
- `pi --list-models 'k3.1'` 确认该模型可用；两次执行都显式指定上述模型，未使用别名或其他模型替代。
- 前两轮 Pi 仅启用 `read,grep,find,ls`，范围限仓库，不读凭据、用户工作区或 test-results，不修改实现。第三轮使用 `--no-tools`，显式附上修订设计和第二轮反馈，仅做文档对照收敛。各轮使用 `--no-session --no-extensions --no-mcp --no-skills --no-context-files` 隔离额外工具与历史会话。
- 输入：[原调查](logging-and-debug-review.md)、[原文档 Review](logging-and-debug-review_review.md)、相关 Go/Web 实现；第二轮增加[修订设计](debug-diagnostics-design.md)。

## 第一轮：独立技术调查

Pi 的原始结论摘录：

> 当前首要工作不是“把底层 err 或 stderr 写进报告”，而是先建立全链路关联 ID、类型化错误分类、字段白名单和进程故障语义。

> 提交前失败没有 run_id 是正确语义，不是缺陷。缺少的是 request_id/submission_id。

> 目标 SSH 认证失败不应注销控制面会话。

| 编号 | 技术意见及证据 | 设计处理 |
|---|---|---|
| R1-01 | submitRun 的私钥解析/公钥身份/认证方式错误早退并不经过 safeRunError | D1 包含全部 handler 和 Manager 边界；固定原因码不依赖字符串截取 |
| R1-02 | runSSH 把原始 stderr 拼入 error，增加日志可能泄漏 | §6 明确先移除该包装；typed error + 允许字段投影；未知输出不透传 |
| R1-03 | AUTH_ERROR 同时包括本机凭据、Agent、远端认证，不代表密码错误 | §3–5 划分原因码和阶段，UI 不把“收到空字段”表述成“用户未填写” |
| R1-04 | 提交前无 Run ID；错误没有可关联 ID | §4 服务端生成 request/submission ID，每次尝试更新；幂等键不变，Run 只在接受后公开 |
| R1-05 | worker 错误只打印固定文本，HTTP 继续存活 | §7 supervisor 与接受提交临界区同步拒绝新任务，有界关闭，非零退出 |
| R1-06 | replay 忽略尾部截断标记、损坏记录可能永久无报告 | §7 保留有效前缀并 WARN；损坏显示 recovery_error，不无限返回报告未就绪 |
| R1-07 | 清理 SSH AUTH_ERROR 映射401，客户端把它当 Web 会话过期 | §4 分离认证域；远端错误422，不清空网页会话；保留 CLEANUP 错误码 |
| R1-08 | SSE sanitizeEvent 仅剥离 owner token，非通用投影；新增事件可能破坏重放 | §6/8 同步 validEvent、sanitizeEvent/projectRun 与 Schema，安全投影不依赖删单字段 |
| R1-09 | CLI 静默、VerdictReasons 空等问题与核心认证诊断不同 | D4/D5 独立交付，保留原状态/退出码/判定语义 |

主设计者另发现预览/提交的目标集合不一致：preview.Build 只返回绑定主机，网页只为它们附带凭据，Manager.Submit 却校验完整清单。设计新增“3 台清单、1 台绑定、非空密码”的回归，用来证明该错误路径，未声称拿到了用户真实输入。

取舍：Pi 建议的细分 `reason_code` 在日志层采用；API/报告的既有大类字段保持，细分放在新增 diagnostics.code，不同时引入多套原因字段。采用服务端按次生成 submission ID，不增加客户端稳定关联 ID；重试仍由既有幂等键负责。worker 故障明确关闭，不留下没有 worker 的长期只读服务模式。

## 第二轮：修订设计复核

Pi 结论为“设计整体可实施”，但指出2项P1和5项P2，应修订后再确认收敛。没有把第二轮误记为通过。

| 编号 | 问题 | 修订与定位 |
|---|---|---|
| R2-P1-1 | Journal损坏一律500会遮蔽有效报告，违反SPEC有效报告优先 | §7恢复矩阵：有效报告照常提供；坏Journal不追加；无有效报告才500；尾部恢复独立；幂等命中RecoveryError不重排队 |
| R2-P1-2 | 直接CLI status/list编码私有Run/Event，可输出owner token | §6共享公共视图显式覆盖直接/委托CLI status/list/show/export、HTTP/SSE；深拷贝，不破坏内部恢复；D1/D2真实清理Journal哨兵门禁 |
| R2-P2-1 | 202未要求submission ID；ApiError不等于明确拒绝；CLI随机键无法跨进程保留 | §4提交响应/unknown矩阵、只读恢复查询、显式CLI幂等键；接受记录同步不确定即关闭接受门/恢复核对，防内存索引未更新造成重复 |
| R2-P2-2 | 已有0644、外部日志路径、Windows reparse/轮转权限不具体 | §7句柄上owner/regular/nlink检查与收紧；显式Windows安全描述符；所有备份同边界；独占writer，原生Windows验收 |
| R2-P2-3 | 只给HTTP5秒不能限制worker/慢writer，提前关闭store有竞态 | §7总预算5秒，HTTP/worker并行预算+1秒日志flush；超时顶层退出13，不关仍使用的store/句柄；有界queue，不承诺中断任意阻塞writer |
| R2-P2-4 | 禁用字符串分类可能意外改变AUTH_ERROR/15大类 | §5仅允许旧大类兼容适配，细分保守unknown；真实回环同时验证错误码、退出码、细分证据 |
| R2-P2-5 | projectRun全Inventory会为未绑定主机伪造OMITTED阶段 | §3统一preview/凭据/vault/preflight/execute/projectRun目标集合；完整Inventory仍冻结 |

主设计者追加：诊断复制从当前进程system/info取得真实build身份；serve登录令牌交付不混入debug通道；CLI样例采用单项OBSERVATION_UNVERIFIABLE，整体Goss JSON损坏不伪称单项COMPLETED。

## 第三轮：关闭项对照

Pi 原文结论：

> R2-P1-1、R2-P1-2 已关闭，当前无未关闭 P1 设计阻断；本轮也未发现剩余或新增的 P2 设计问题。该设计可进入分批实施，但此结论仅表示计划闭合，不代表功能或验收测试已经完成。

| 项目 | Pi第三轮状态 | 最终设计位置 |
|---|---|---|
| R2-P1-1 有效报告优先 | 已关闭 | §7恢复矩阵与D3验收 |
| R2-P1-2 直接CLI公共输出隔离 | 已关闭 | §6公共视图、§9真实CLEANUP哨兵门禁 |
| R2-P2-1 接受状态与幂等恢复 | 已关闭 | §4响应矩阵、恢复查询与CLI稳定键 |
| R2-P2-2 日志文件安全 | 已关闭 | §7权限、链接、轮转、Windows原生证据 |
| R2-P2-3 总关闭预算 | 已关闭 | §7资源所有权和总截止 |
| R2-P2-4 AUTH_ERROR/15兼容 | 已关闭 | §5保守细分与旧大类适配 |
| R2-P2-5 阶段投影目标集合 | 已关闭 | §3公共目标集合、§9 D1a |

Pi还强调两项实施验收边界：尾部恢复必须实际处理不完整尾部，不能忽略后继续原文件追加；所有公共出口必须强制经过公共投影，不能回退为各CLI分支自行修补。主设计者接受该约束。

三轮原始输出分别保存在本任务临时目录的 `readyspec-debug-pi-round1.md`、`readyspec-debug-pi-round2.md`、`readyspec-debug-pi-round3.md`（`/tmp/opencode/`）；本记录保留可随仓库传递的结论、问题与关闭依据。设计文档样例中的关联ID已按32位十六进制校验，修正了两处样例长度，不影响上述设计取舍。

实现阶段的测试义务仍保留。当前CLI公共输出、worker生命周期和主机故障等实现风险不会因为文档意见关闭而自动消失。

## 验证边界

本次交付为设计与技术复核，不以 Markdown 检查冒充功能回归。代码实施应按设计 D1–D5 提交行为测试证据。现有未提交的 safeRunError 局部改动保留，不在此任务中扩写为完整日志实现，也不宣称已经解决用户主机 AUTH_ERROR。

已执行的静态预览（不访问网络、不接受任务）：

```bash
./dist/readyspec run preview --plan examples/minimal-plan.yaml \
  --inventory 'node-a=192.0.2.1,node-b=192.0.2.2,node-c=192.0.2.3' \
  --inventory-format quick --role-map app=node-a --json
```

实际返回 `host_count: 1`、仅有 `hosts[0].host.id: node-a`、`network_accessed: false`。结合网页只遍历 `result.hosts` 绑定凭据与 Manager 遍历完整 Inventory 的源代码，证实该集合差异的触发条件；本次没有提交一份假任务来制造报告，也没有连接用户主机。

最终文档验证实际输出：

```text
PASS: 3 documents; local links/fences; 4 JSON/NDJSON blocks, 7 records, 5 JSON correlation IDs; text IDs valid
```

另执行 `git diff --check`，成功且无输出。本次未新增或运行实现测试：交付仅为计划和技术复核；以上内容/格式验证不等于未来 Schema/实现已经通过测试。源码中此前会话的 safeRunError 改动及原有 test-results 均保留。
