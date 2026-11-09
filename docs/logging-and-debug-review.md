# ReadySpec 运行期日志输出与 Debug 诊断能力评审报告

> **评审日期**：2026-11-09
> **评审范围**：ReadySpec 控制面（CLI、Web/SSE Server、RunManager、RunStore、Workspace、NativeSSH Executor、Compiler、Report）与 Web 前端控制台（`web/src`）
> **参考规格**：[`SPEC.md`](../SPEC.md)、[`DESIGN.md`](../DESIGN.md)、[`GLOSSARY.md`](../GLOSSARY.md)

> **后续实施设计**：[诊断输出实施设计](debug-diagnostics-design.md)将本调查转为具体输出契约、秘密边界与验收批次；[Pi / Kimi 3.1 Pro 技术复核记录](debug-diagnostics-technical-review.md)记录技术意见及处理。下文保留调查原貌，其中直接保留底层 `err/stderr`、输入不含秘密等建议不能直接作为实现准则，实施以安全投影后的设计为准。

---

## 1. 总体结论

**ReadySpec 在“业务审计证据与任务状态追踪”层面设计扎实，但在“系统运行期进程日志（Operational Logging）”与“故障现场排障深度（Debug Granularity）”上存在明显的“黑盒化”短板。**

### 1.1 现有优势（做得好的地方）

1. **任务级持久化事件流与状态重建**：通过 [`internal/runstore/store.go`](../internal/runstore/store.go) 维护单调递增的 `events.ndjson` 事件日志，配合 [`internal/webui/runs.go`](../internal/webui/runs.go) 中的 `projectRun` 与 SSE 流（`Server.runEvents`），实现了各主机阶段（`CONNECTING`、`DISCOVERING`、`PREPARING`、`UPLOADING_*`、`RUNNING_*`、`COLLECTING_*`、`CLEANING`、`FINISHED`）的实时追踪与断线重连恢复。
2. **结构化检查证据与溯源**：[`internal/compiler/compiler.go`](../internal/compiler/compiler.go) 中的 `CompileExecutionModes` 与 `Normalize` 将检查项映射为带 `Origin`、参数来源（`ParameterSources`）、采集起止时间（`StartTime` / `EndTime` / `DurationNS`）及 `HighRiskRootAudit` 标记的 `Evidence`。
3. **多工具离线预检与仿真能力**：内置离线静态编译（`readyspec plan compile`）、无连接执行预览（`readyspec run preview`）、确定性模拟器（[`internal/executor/simulator.go`](../internal/executor/simulator.go)）以及自定义 Shell 断言自测接口（`POST /api/v1/shell-selftest`），支持在不触达生产主机的情况下调试方案逻辑。
4. **严格的秘密防泄漏边界**：凭据在内存中清零（`wipeCredentials`）、配置探针脱敏（`D05` `redactedEvidence`）、清理归属令牌 `OwnerToken` 在公开报告与 SSE 事件中自动剥离。

### 1.2 核心短板（亟待改进的问题）

1. **进程级运行时日志（Process Logging）近乎空白**：全库未引入 `log/slog` 等结构化日志组件，也没有 `--debug` / `--verbose` / `READYSPEC_LOG_LEVEL` 等诊断开关。
2. **错误根因在边界层被过度吞没（Error Swallowing）**：SSH 连接、Sudo 策略校验、远端临时目录探测、Goss 执行、Web API 和 CLI 文件加载在捕获底层 `error` 后，大多丢弃了底层错误详情并替换为固定静态字符串，导致现场排障“只知哪个阶段失败，不知为何失败”。
3. **CLI 同步执行过程完全静默**：`readyspec run submit` 在长耗时运行中既不输出阶段进展，也不在委托模式下消费 SSE 事件流，只做静默轮询。
4. **部分规格契约与诊断展示尚未对齐**：如 Journal 尾部截断恢复未输出诊断、编译器/求值器 `ERROR` 状态缺少具体错误原因说明、`VerdictReasons` 在常规执行路径下未填充。

---

## 2. 详细评审意见

### 2.1 严重影响排障效率的问题（High Priority）

#### P0-1：远端 SSH / Sudo / 工作区探测 / Goss 执行丢弃底层错误详情

在 [`internal/executor/native_ssh.go`](../internal/executor/native_ssh.go) 中，`runSSH`（第 916–989 行）已经收集了远端命令的 `stderr` 并包装进 `error`，但在上层调用点几乎全部被丢弃，仅写入硬编码的笼统提示：

- **SSH 连接与认证**（`NativeSSH.Preflight` 第 341–345 行、`NativeSSH.Execute` 第 409–416 行）：当 `NativeSSH.connect` 失败时，无论是本地 `~/.ssh/known_hosts` 文件不存在、目标主机指纹不匹配（`knownhosts.KeyError`）、TCP 超时、连接被拒绝（`ECONNREFUSED`），还是公钥指纹不匹配，最终写入报告的 `Failure.Message` 全部是固定的 `"SSH connection or host key verification failed"`（或重连时的 `"target reconnect or host identity verification failed"`）。运维人员无法区分是“需要更新 `known_hosts`”、“防火墙不通”还是“认证凭据错误”。
- **远端身份与事实探测**（`discover` 第 385–403 行 -> `NativeSSH.Preflight` 第 352–355 行）：当缺少 `/etc/machine-id`、`/proc/sys/kernel/random/boot_id` 或缺少 `sha256sum`/`openssl` 时，统一返回 `"target identity facts could not be verified"`，不指出缺失的是哪一项系统特征或命令。
- **远端临时目录探测**（[`internal/executor/workspace_discovery.go`](../internal/executor/workspace_discovery.go) `discoverWorkspacePaths` 第 27–89 行 -> `NativeSSH.Preflight` 第 359–362 行）：`discoverWorkspacePaths` 内部区分了候选目录是否存在、`ro`、`noexec`、symlink 或权限不足，但返回给 `NativeSSH.Preflight` 后，`pathErr` 被直接丢弃，仅保留 `"a safe remote execution directory is unavailable"`。
- **Goss 投递与执行**（`NativeSSH.Execute` 第 531–585 行）：上传失败（如磁盘满 `ENOSPC` / 权限不足）、SHA256 校验失败、Goss 执行非预期退出或 `compiler.Normalize` 失败时，底层 `err`（含 `stderr`）均被丢弃，只返回 `"embedded Goss upload failed"`、`"Goss execution failed or returned invalid output"` 或 `"Goss output did not satisfy the result contract"`。
- **远端清理失败**（`NativeSSH.Execute` 第 493–499 行、`NativeSSH.ResolveCleanup` 第 639–733 行）：自动清理或手动清理失败时，底层 `cleanErr` / `err` 全部被抹去，仅返回固定错误码（如 `CLEANUP_PROCESS_CHECK_FAILED`、`CLEANUP_DELETE_UNCONFIRMED`）。

**改进建议**：
构建统一的错误脱敏与摘要函数（确保绝不回显密码或敏感输入，并限制单条诊断文本长度），将安全的底层原因（如 TCP 错误类型、`known_hosts` 具体错误、缺失的事实字段、被拒绝的候选目录列表、远端命令退出码及截断后的 `stderr`）保留到 `report.Failure.Message` 或扩展的诊断字段中。

#### P1-1：守护进程与后台组件缺乏运行时日志体系（Operational Logging）

整个 Go 后端没有使用标准库 `log` 或 `log/slog`，仅在 [`internal/cli/app.go`](../internal/cli/app.go) 和 [`internal/cli/serve.go`](../internal/cli/serve.go) 中有两处简单的 `fmt.Fprintln(os.Stderr, ...)`：

- **后台异常吞没真实错误**：[`internal/cli/serve.go`](../internal/cli/serve.go) 第 116–121 行中，当 `manager.Run(ctx)` 发生存储或执行致命错误退出时，仅向 `os.Stderr` 打印固定字符串 `"Run manager stopped after a storage or execution error"`，**把返回的 `err` 本身直接丢弃了**；且此时 HTTP Server 仍在继续监听并接受请求，形成后台 worker 停止但服务仍在响应的半死状态。
- **持久化恢复与损坏无诊断输出**：
  - [`SPEC.md`](../SPEC.md) §3.3 明确要求：*“事件流尾部若因断电/崩溃出现半行损坏，启动加载时保留完整有效前缀并记录诊断”*。但 [`internal/runstore/store.go`](../internal/runstore/store.go) 第 704 行 `Store.replay` 调用 `s.workspace.RecoverJSONLines(...)` 时，直接用 `_` 忽略了代表发生尾部截断恢复的 `truncated` 布尔返回值，没有任何日志或事件记录。
  - `runstore.Open`（第 178–181 行）在 `s.replay(run)` 失败时，仅将 `run.RecoveryError` 置为 `"RUN_STORAGE_ERROR"`，丢弃了具体是哪一行 Journal 损坏或哪个字段校验失败的 `err`。
- **HTTP 安全拦截与 500 错误无服务端日志**：[`internal/webui/server.go`](../internal/webui/server.go) 第 167–185 行中的 `HOST_REJECTED`、`CROSS_ORIGIN_REJECTED`、`ORIGIN_REJECTED`、`Server.authenticate`（第 238–245 行）的鉴权失败/会话满，以及 [`internal/webui/plans.go`](../internal/webui/plans.go) 中的 `PLAN_STORE_ERROR`（500），服务端标准错误流没有任何记录。当内网用户配置反向代理、域名访问或排障工作区文件损坏问题时，管理员在 `readyspec serve` 终端看不到任何拦截原因与底层 I/O 错误。

**改进建议**：
引入 Go 标准库 `log/slog`（支持文本与 JSON 格式，默认输出 `WARN`/`ERROR` 及关键生命周期 `INFO`，支持通过 `--log-level` 或 `READYSPEC_LOG_LEVEL` 开启 `DEBUG`），为 `serve`、`runmanager`、`runstore` 和 `webui` 注入带 `run_id` / `host_id` 上下文的 Logger；并在 `RecoverJSONLines` 发生 `truncated == true` 时记录明确的结构化警告日志。

#### P1-2：Web API 与 CLI 错误映射过度裁剪，掩盖方案与输入错误细节

- **Web API 裁剪**（[`internal/webui/plans.go`](../internal/webui/plans.go) `safePlanError` 第 523–534 行、[`internal/webui/runs.go`](../internal/webui/runs.go) `safeRunError` 第 272–289 行）：
  - `safePlanError` 使用字符串截取只保留全大写前缀（例如将 `"PLAN_ERROR: item X references unknown template Y"` 截断成仅剩 `"PLAN_ERROR"`，若无匹配前缀则统一变成 `"PLAN_OPERATION_REJECTED"`）。
  - `safeRunError` 和 `Server.submitRun`（第 88–122 行）同样将 `plan.Load`、`inventory.Parse`、`preview.Build` 的具体校验错误抹平成 `"PLAN_ERROR: invalid plan"`、`"PLAN_ERROR: invalid inventory"` 或 `"PLAN_ERROR"`。
- **CLI 文件与参数加载丢弃 OS 错误**（[`internal/cli/app.go`](../internal/cli/app.go)、[`internal/cli/submit.go`](../internal/cli/submit.go)、[`internal/cli/preview.go`](../internal/cli/preview.go)）：
  - 如 `loadPlan`（`submit.go` 第 351–355 行）打开文件失败只返回 `"PLAN_ERROR: cannot open plan file"`（不打印路径与 `os.Open` 的 `ENOENT`/`EACCES`）；
  - 私钥解析失败（`submit.go` 第 138–140 行）只返回 `"AUTH_ERROR: private key cannot be parsed"`（不区分格式错误、口令错误还是不支持的加密算法）；
  - JSON Override 解析失败（`submit.go` 第 83–86 行）只返回 `"PLAN_ERROR: invalid field override JSON"`（不输出未知字段名或语法错误位置）。

**改进建议**：
方案、主机清单和 Override 文件的语法/语义校验错误不涉及服务端内部秘密，应保留结构化错误码 + 具体可读原因（例如复用 `compiler.Diagnostic` 结构），并在 CLI 中附带文件路径与底层非敏感错误描述。

---

### 2.2 中等优先级问题（Medium Priority）

#### P2-1：CLI `run submit` 执行期缺乏实时进度反馈与委托流式订阅

- **表现与依据**：
  - 在 [`internal/cli/submit.go`](../internal/cli/submit.go) 第 310–324 行直接持有工作区锁执行时，`manager.ProcessNext` 同步阻塞直至所有主机预检、执行、归档全部完成，期间 `stderr` 没有任何阶段输出。
  - 在工作区锁被 `serve` 占用而走本地委托（`delegateRun` 第 575–600 行）时，虽然 [`SPEC.md`](../SPEC.md) §3.2 写明*“通过本地受控 API 提交并订阅 SSE 事件流直至完成”*，但实际代码实现是每 `250ms` 轮询一次 `GET /api/v1/runs/<id>/report`（最长等待 2 小时），既没有订阅 `/events` SSE 流，也没有向终端打印任何 `run_id` 或主机阶段变化。一旦某台主机卡在连接或超时等待，CLI 用户面对的是长达数十分钟的空白终端。
- **改进建议**：
  - 提交成功后立即在 `stderr` 输出 `Accepted Run <run-id>`（保持 `stdout` 纯净仅输出最终 JSON 报告，不影响管道消费）；
  - 在 `--verbose`（或默认 `stderr` 进度模式）下，实时输出各主机的阶段转换（如 `[host-a] CONNECTING -> DISCOVERING -> RUNNING_STANDARD`）；委托模式下改为消费 `/api/v1/runs/<id>/events` SSE 流。

#### P2-2：编译器与内置模板求值器（Evaluator）将取证异常笼统归为 `ERROR`，缺少诊断说明

- **表现与依据**：
  - 在 [`internal/compiler/compiler.go`](../internal/compiler/compiler.go) `Normalize`（第 577–789 行）中，当 Goss 某项观测返回 `observation.Err != null`、`observation.Skipped == true`、匹配数量不为 1（`matches != 1`），或者各模板求值器（如 `logEvaluation`、`serviceEvaluation`、`limitsEvaluation` 等）解析输出失败时，仅将 `ItemResult.Status` 置为 `"ERROR"`。
  - `Evidence` 结构体（第 48–60 行）只有一个布尔值 `Error bool`，**没有将 Goss 原生返回的 `observation.Err` 字符串保存下来**。当 Goss 因权限不足、命令超时或路径异常产生 `err` 时，用户在报告和 UI 中只能看到 `Error: true`（“取证错误”），却看不到 Goss 报出的具体错误文本。
  - 在复杂的日志检查 [`internal/compiler/logs.go`](../internal/compiler/logs.go) `logEvaluation`（`L02`/`L03`，第 44–60 行）中，若 `/var/log/journal` 权限不足触发 `exit 2`（无 `__RS_WINDOW__` 头），`logEvaluation` 直接返回 `"ERROR", {"source": "system journal", "redacted": true}`，不说明是因为“journal 目录不可读”还是“时间窗口解析失败”。
- **改进建议**：
  - 在 `compiler.Evidence` 中增加可选的诊断字段（或在 `Actual` / 扩展字段中携带错误文本），当 `observation.Err` 非空或求值器判定格式/权限异常时，写入明确的诊断原因（如 Goss 的错误消息、退出码或求值器失败分支标识）。

#### P2-3：`Manager.archive` 归档报告时 `VerdictReasons` 在常规路径下始终为空切片

- **表现与依据**：
  - [`internal/report/report.go`](../internal/report/report.go) 第 68 行 `RunReport` 定义了 `VerdictReasons []string`，且前端 [`web/src/RunHistory.tsx`](../web/src/RunHistory.tsx) 第 338–344 行专门渲染了 `report.verdict_reasons` 列表。
  - 但在 [`internal/runmanager/manager.go`](../internal/runmanager/manager.go) `Manager.archive`（第 574–581 行）构造报告时，硬编码了 `VerdictReasons: []string{}`（仅在取消竞争或崩溃恢复 `recoverAcceptedRuns` 时才填入原因）。当 `Verdict` 因为 `TRIAL` 模式、`PARTIAL` 范围、`mock_simulator` 执行器、拓扑不满足或某必检项 `ERROR`/`FAIL` 被判定为 `UNKNOWN` 或 `NOT_READY` 时，`verdict_reasons` 为空，用户需自行翻阅所有主机和拓扑表推导为什么结论是 `UNKNOWN`。
- **改进建议**：
  - 在 `report.EvaluateVerdict`（或 `topology.Verdict`）计算结论的同时，生成对应的原因说明列表并填入 `VerdictReasons`。

---

### 2.3 低优先级与体验优化项（Low Priority / Polish）

#### P3-1：Web UI 报告与诊断视图的交互细节可进一步贴合 `DESIGN.md`

- **执行期/历史事件流（Events）未在历史详情页展示**：`GET /api/v1/runs/{id}` 返回了完整的 `stage_transitions` 和脱敏后的 `events`，但 [`web/src/RunHistory.tsx`](../web/src/RunHistory.tsx)（第 106–132 行）只请求了 `/report`，没有展示各主机的阶段耗时（`stage_transitions`）。当用户查看仍在 `RUNNING`/`PENDING` 尚未生成报告的任务时，`/report` 返回 409，UI 仅提示“归档报告暂不可读，请稍后刷新”，无法在历史页实时观察或回看各主机阶段耗时。
- **证据与日志代码块辅助能力**：[`DESIGN.md`](../DESIGN.md) §7 `Logs, terminal and code`（第 200–206 行）提出提供复制、自动换行切换等能力；目前 `RunHistory.tsx`（第 518–536 行）使用 `<pre>{JSON.stringify(...)}</pre>`，当 `Evidence.actual` 是多行字符串（如自定义 Shell 的 `stdout`/`stderr`）时，`JSON.stringify` 会将换行符转义为字面量 `\n` 挤在一行显示，阅读体验欠佳。

#### P3-2：缺少面向排障的 CLI 调试辅助过滤能力

- **表现与依据**：当线上某个内置检查项在特定发行版上表现异常时，研发/交付工程师需要检视 ReadySpec 最终为某台主机或某个检查项生成的 Goss YAML 配置与探测脚本。虽然有 `readyspec plan compile` 和 `readyspec run preview --json`，但输出的 JSON 中 `config` 是嵌套且经过转义的字节流，缺少按单主机/单角色直接导出可读 Goss YAML 配置的快捷参数。

---

## 3. 优先级改进路线图汇总

| 优先级 | 改进方向 | 涉及核心文件 | 预期收益 |
| :--- | :--- | :--- | :--- |
| **P0** | **保留并脱敏远端执行底层错误**：在 SSH 连接、工作区探测、Sudo 校验、Goss 执行及 Normalize 失败时，将脱敏后的底层 `err` / `stderr` 摘要写入 `Failure.Message` 和 `Evidence` | `internal/executor/native_ssh.go`、`internal/executor/workspace_discovery.go`、`internal/compiler/compiler.go` | 解决现场排障“只知失败阶段、不知具体原因”的核心痛点 |
| **P1** | **引入进程级 `slog` 日志与 `--debug` 开关**：补齐 `serve` 守护进程、Journal 截断恢复（对齐 `SPEC.md` §3.3）、存储异常及 HTTP 安全拦截日志 | `internal/cli/app.go`、`internal/cli/serve.go`、`internal/runstore/store.go`、`internal/webui/server.go` | 守护进程可观测，满足 `SPEC.md` 恢复诊断要求，消除后台静默故障 |
| **P1** | **细化 CLI / Web API 校验错误反馈**：改进 `safePlanError` / `safeRunError` 与 CLI 文件读取错误，保留具体校验失败原因与字段定位 | `internal/webui/plans.go`、`internal/webui/runs.go`、`internal/cli/submit.go` | 方案编写与 CLI 调用出错时能一次定位问题字段或文件权限 |
| **P2** | **补齐 `VerdictReasons` 生成与 CLI 进度流**：在归档报告中自动填充结论推导原因；CLI `submit` 支持向 `stderr` 输出实时阶段进展并在委托模式下订阅 SSE | `internal/runmanager/manager.go`、`internal/report/report.go`、`internal/cli/submit.go` | 提升长任务 CLI 交互体验与报告结论的自解释能力 |
| **P3** | **优化 Web UI 历史阶段展示与多行证据渲染**：在 `RunHistory` 中支持查看任务的阶段进度（`/runs/{id}`），对多行字符串证据按原始换行展示并支持复制 | `web/src/RunHistory.tsx` | 改善自定义 Shell 输出阅读体验与运行中任务的可观测性 |
