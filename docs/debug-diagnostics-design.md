# ReadySpec 诊断输出实施设计

日期：2026-11-09。状态：经 Pi / Kimi 3.1 Pro 三轮技术复核收敛，当前无未关闭 P1/P2 设计意见，可按 D1–D5 分批实施；本文描述目标行为，不代表功能已实现。结论与取舍见[技术复核记录](debug-diagnostics-technical-review.md)。

2026-11-10 用户明确变更主机密钥要求：默认 `record-only`，去除强制 `known_hosts` 依赖；原文中信任库拒绝行为仅适用于可选 `strict` 模式。执行重连和清理仍在握手阶段匹配本任务已记录的指纹，既有机器身份/OwnerToken/秘密边界不变。此后续变更不冒充上述历史 Pi 评审已审核的新结论。

问题依据：[诊断能力调查](logging-and-debug-review.md)。已有的[文档 Review](logging-and-debug-review_review.md)主要检查呈现形式，不替代本文的技术复核。现有 `safeRunError` 三条固定错误文本改动只改善局部提示，不等于完成此设计。

## 1. 目标、边界与优先级

使用户能从一次失败中回答：请求是否被接受、哪个主机/步骤失败、已确认的原因是什么、下一步应检查什么，以及用哪个 ID 关联服务日志。无需复制密码、原始请求或开启不安全的 SSH 选项。

本轮覆盖提交前校验、SSH 连接/身份/凭据/提权、远端目录/投递/执行/回收、报告归因、HTTP/CLI 输出、worker 和存储恢复。日志不代替权威 Run Journal/RunReport，不改变判定语义、退出码、重试幂等性或主机身份验证。

| 顺序 | 交付 | 完成标准 |
|---|---|---|
| D1 / P1 | 提交诊断、请求关联、构建身份 | 没有 Run ID 时仍可定位；明确拒绝、已接受、结果未知三种状态 |
| D2 / P1 | SSH 与执行步骤的结构化原因 | 已接受的任务能按主机追踪实际失败；未知原因不伪称密码错误 |
| D3 / P1 | 进程日志、worker 健康、恢复诊断 | worker 退出后停止接受任务；存储恢复异常可追踪 |
| D4 / P2 | Evidence 与 VerdictReasons | 取证异常与结论有确定原因，兼容旧报告 |
| D5 / P2 | CLI SSE 进度与 Web 历史视图 | 执行中、断线后、尚未归档时都有可见状态 |

原调查中的“P0”表示阻碍现场排障，尚无证据表明所有列举问题都是 P0 事故；实施按上表优先级。潜在秘密泄漏、损坏存储被当作有效或 worker 停止后继续接受任务是发布阻断项。

## 2. 对应现有代码的事实

- `internal/webui/runs.go:submitRun` 在 Manager 接受前可能拒绝方案、清单、凭据。`safeRunError` 的字符串映射丢失原因；此时没有 Run，不应在 Run 历史里伪造检查失败。
- `internal/runmanager/manager.go:Submit` 区分凭据缺失、方法不匹配、密码为空及本机 Agent 不可用；这些分支不说明远端 SSH 密码是否正确。
- 具体可触发路径：`internal/preview/preview.go:Build` 只把角色绑定中的主机加入 `Result.Hosts`；`web/src/RunPreview.tsx:submit` 只为这些主机附带凭据，但 Manager.Submit 遍历完整 Inventory 校验凭据。因此“3 台清单、1 台绑定、非空密码”仍可因未绑定主机返回 AUTH_ERROR。这是代码路径证据，不是已经取得用户实际表单或确认本次故障根因。
- `internal/executor/native_ssh.go:connect/Preflight/Execute` 默认记录指纹；可选 strict 使用管理端 `known_hosts`。密码方式使用 `ssh.Password`，不借用 OpenSSH 客户端配置、ControlMaster 或其他认证方式。
- `internal/webui/server.go:writeError/ServeHTTP` 没有关联 ID；`web/src/api.ts:ApiError` 尚不保存请求 ID 或提交接受状态。
- `internal/cli/serve.go` 在 worker 返回错误后只输出固定文本；`internal/runstore/store.go:replay` 忽略尾部恢复标记，且先读 Journal 后读报告，可能使损坏 Journal 遮蔽有效报告；实施须恢复 SPEC §8.3 的有效报告优先约束。
- `internal/webui/runs.go:resolveCleanup` 把目标 SSH 认证错误映射为 HTTP 401；`web/src/api.ts:request` 会把它当作 Web 会话过期。两种认证域必须分离，清理失败也不能丢成 `RUN_SUBMISSION_REJECTED`。
- `internal/cli/run_control.go` 直接编码私有 Run.Events，`internal/cli/report.go` 直接模式会编码完整 Run/Report；这些路径不经过 Web sanitizeEvent，可能输出 cleanup owner token 等私有字段。新增 debug 之前必须补齐共享公共投影，不能假定现有全部 CLI 输出已安全。
- `internal/compiler/diagnostic.go` 已有定位诊断；保留其编译用途，通过适配器映射运行诊断，避免 compiler、report、executor 循环依赖。

CLI SSH 登录成功只是对该执行环境、端点、用户名、认证方式的证据，不能直接推导服务端使用的环境相同。诊断应展示真实失败步骤，不根据经验猜测用户输入错误。

## 3. 统一诊断契约

新增无业务依赖的 `internal/diagnostic`：固定原因码注册表、强类型允许字段、到 API/报告/日志的安全投影。底层保留 typed error / `%w` 供 `errors.Is/As` 判断，出边界时转换为诊断。禁止以 `err.Error()` 子串作为新分类的主要依据。

```json
{
  "code": "CREDENTIAL_HOST_MISSING",
  "category": "AUTH_ERROR",
  "message": "该主机没有随请求收到对应的认证凭据。",
  "phase": "SUBMISSION",
  "host_id": "node-a",
  "field": "/credentials/node-a",
  "retryable": false,
  "action": "CHECK_HOST_CREDENTIAL_BINDING"
}
```

`category` 复用既有业务错误分类；`code` 提供稳定细分，不改变 CLI 退出码。`message`、`action` 来自注册表，不拼接任意错误文本；前端按既有中文策略展示，Go 日志事件和原因码使用英文。`phase` 使用既有阶段，补 `SUBMISSION`、`STORAGE_RECOVERY`。可选定位为 `host_id/role_id/item_id/field`，其中 `field` 使用合法 JSON Pointer 转义，不含字段原值。

每条诊断至多 1 KiB 消息，单响应最多 32 条；被省略数量另行记录。字符串按 UTF-8 边界截断、去除控制字符，UI 始终按文本渲染。字段上限、枚举及允许组合在 Go 类型与 Schema 同时验证；不增加任意 `details: map[string]any` 的后门。

字段映射固定为：既有 `Failure.Code` 和 API `error` 继续保留错误大类；新 `diagnostics[].category` 对应大类，`diagnostics[].code` 对应细分原因，日志 `reason_code` 等于这个细分码。不把现有 Failure.Code 替换为细分码，不让前端解析 message 判断错误。`compiler.Diagnostic.Code` 保留已有含义，使用适配器转换。

清单主机数、绑定目标数、凭据关联成功数允许以数值输出，用来发现集合不一致；不输出密码长度或原值。已授权 UI 可按 host_id 展示未绑定主机。预览、凭据校验、vault 安装、preflight/execute 与 `projectRun` 的阶段投影统一复用已有绑定目标集合计算，完整清单仍冻结保存；不为不执行的主机要求密码、获取 Agent、连接 SSH 或生成伪造的 OMITTED 阶段。空绑定/数量不符的最终拓扑继续按 SPEC §6.2/§7.1 和既有报告判定，不因诊断改造提前改变角色数量校验语义。

`retryable` 只表示原因可能短暂，不代表产品会自动重试。已提交任务、执行过的远端脚本及接受状态未知的请求都不得据此自动重跑。

## 4. 请求与任务关联、接受状态

- 在 `ServeHTTP` 最外层、Host/Origin/认证检查之前生成服务端随机 128-bit `request_id`；HTTP 响应头始终返回 `X-Request-ID`，JSON 错误同时返回 `request_id`。不信任或原样记录客户端传入的 ID。
- 服务端在进入提交 handler、读取 body 前生成随机 128-bit `submission_id`，标识一次提交尝试；直接 CLI 由命令入口生成。每次 HTTP 重试生成新的 request/submission ID，不使用客户端任意 ID，不对密码计算摘要。委托 CLI 使用服务端返回的 submission ID。
- `request_id` 标识一次 HTTP 请求；`submission_id` 标识一次提交尝试；`run_id` 仅在持久化接受成功后对外公开。CLI 直接模式使用同样的诊断格式，但没有 HTTP request ID，不伪造一条 HTTP 请求。
- idempotency key 仍负责去重，不与上述 ID 混用，不写日志；关联 ID 没有查询授权能力。
- Run 接受记录新增可选 `submission_id` 和初次接受的 `request_id`，不重写旧接受记录。重试取得既有 Run 时同时记录本次 request/submission ID 和既有 Run ID，原接受元数据保持不变。
- 拒绝提交只产生限量操作日志及 HTTP 诊断，不创建 Run、RunReport 或带凭据的失败快照。
- 本地拒绝或服务端接受前拒绝标记 `acceptance="rejected"`；持久化接受成功返回 `202`、Run ID、`acceptance="accepted"`；连接断开、响应无法解析、提交路径内部 5xx 可能发生在提交后，客户端标记 `acceptance="unknown"`，保留原幂等键，走现有不确定提交恢复路径。不能把没有响应理解为未接受。
- 关联 ID 本身不参与 PlanDigest、模板摘要、发布清单或 Run 幂等输入语义摘要；放在接受记录的可选元数据，不放入当前被 `semantic.Digest` 整体计算的 RunConfig。相同输入的新 request ID 不得导致 `RUN_IDEMPOTENCY_CONFLICT`。

兼容错误响应保留现有 `error` 字符串，新增 `request_id`、可选 `submission_id`、`acceptance` 和 `diagnostics`。原编译 `diagnostics` 字段继续可读；客户端同时兼容旧错误响应。非 JSON/SSE 握手失败也从响应头恢复 request ID。不得为“定位方便”把原始请求存入日志。

进入提交 handler 的所有响应均携带本次 submission ID，包括 202 和 `created=false` 的幂等命中。Host/Origin/session/CSRF 等前置拒绝至少有 request ID，不伪造尚未生成的 submission ID；连接完全中断时 UI 如实显示“未取得服务端诊断 ID”。

| `/runs` 返回情况 | 客户端接受状态与后续动作 |
|---|---|
| 合法 202，含合法 Run ID | accepted，订阅该 Run；不再次提交 |
| 控制面明确的 401/403/409/413/422/429 或 `503/RUN_WORKER_STOPPED`，具有拒绝契约 | rejected；修正输入后由用户重新操作；409 冲突只拒绝本次请求，不否认原请求已有 Run |
| 网络断开、非 JSON、伪202缺 Run ID、通用5xx且没有可信明确状态 | unknown；即使封装为 ApiError 也不能推断 rejected；零自动重提 |
| 有效拒绝响应但代理移除/破坏响应契约 | 保守 unknown；不得单凭状态码推断接受前拒绝 |

Web unknown 时保留既有幂等键、冻结本次输入，先恢复查询再决定下一步。新增只读的 `POST /api/v1/runs/submission-status`：密钥仅在 body 传递、正常会话与 CSRF 鉴权、不写日志；在接受临界区内查询同一键的有效接受记录，返回 accepted + 原 Run ID、确认未接受的 rejected 或无法确认的 unknown，不创建/排队 Run。查不到内存索引不等于未接受，需核对权威接受记录；事务进行中、记录损坏或存储不可读保持 unknown。若 accepted.json 写入/目录同步后返回错误，立即关闭接受门并核对磁盘或重启恢复，不能让同键重试绕过未更新的内存索引创建第二个 Run。

CLI 增加显式 `--idempotency-key` 供操作者跨进程安全复用，不能打印原键到 stderr/日志。自动随机键仅在当前进程保留；unknown 后若键已丢失，提示先查历史，不宣称换个随机键可以安全重试。显式键可在恢复查询确认后由用户手工重试，仍不得自动重新执行。新查询的安全边界、接受临界区与故障注入属于 D1，不留到后续“体验优化”。

HTTP 401 仅用于控制面会话/访问令牌认证，远端 SSH/提权/清理认证失败使用 422 并保留 AUTH_ERROR/细分码；Web 客户端只在明确的控制面 `SESSION_REQUIRED/SESSION_EXPIRED`（及现有登录流程）上进入重新登录，不把任何远端 AUTH_ERROR 清空 Web 会话。清理诊断沿用同一投影器，但保留 CLEANUP 错误域，不借用提交拒绝兜底码。

示例（计划中的响应，非当前故障已确认原因）：

```json
{
  "error": "AUTH_ERROR",
  "request_id": "3a091f235aff4a28a207ff6e1049c137",
  "submission_id": "422eedf3b90243fbbca5e7cc7224fdf2",
  "acceptance": "rejected",
  "diagnostics": [{
    "category": "AUTH_ERROR",
    "code": "CREDENTIAL_PASSWORD_EMPTY",
    "phase": "SUBMISSION",
    "host_id": "node-a",
    "message": "该主机随请求提交的 SSH 密码为空。",
    "retryable": false,
    "action": "ENTER_SSH_PASSWORD"
  }]
}
```

UI 显示“任务未接受；node-a 的凭据为空；诊断 ID …”。这不等于“用户没有填写密码”，应进一步检查客户端状态、传输及服务端绑定，不责怪用户或断言远端密码错误。

## 5. 原因码与输出责任

| 步骤 | 最低原因码集合 | 分类与行动 |
|---|---|---|
| 提交凭据 | `CREDENTIAL_HOST_MISSING`、`CREDENTIAL_METHOD_MISMATCH`、`CREDENTIAL_PASSWORD_EMPTY`、`PRIVATE_KEY_PARSE_FAILED`、`AGENT_ENDPOINT_UNAVAILABLE` | 保持 AUTH_ERROR/15；只输出已验证 host_id、认证方式和字段定位，不输出密码长度/片段 |
| 方案/清单 | `PLAN_SCHEMA_INVALID`、`INVENTORY_HOST_INVALID`、`INVENTORY_VARIABLE_UNSUPPORTED`、`RELEASE_MANIFEST_MISSING`、`RELEASE_MANIFEST_DIGEST_MISMATCH`、`RELEASE_COMPILER_INCOMPATIBLE` | 保留既有分类/退出码；给出行列、字段、固定原因，不回显清单整行或脚本 |
| 连接 | `DNS_LOOKUP_FAILED`、`TCP_CONNECT_REFUSED`、`TCP_CONNECT_TIMEOUT`、`SSH_NEGOTIATION_FAILED` | 用 `net.DNSError/net.OpError/context` 等类型分类；未能可靠细分则使用固定未知原因 |
| 主机身份 | `KNOWN_HOSTS_UNREADABLE`、`HOST_KEY_UNKNOWN`、`HOST_KEY_CHANGED`、`HOST_KEY_REVOKED` | strict 模式核验信任库；默认 record-only 不依赖该文件，重连/清理仍匹配任务指纹。保持身份/网络归类和安全诊断，不自动改写信任记录 |
| SSH 认证 | `SSH_AUTH_REJECTED`、`AGENT_SELECTED_KEY_MISSING` | AUTH_ERROR；认证拒绝可能由服务端方法/账户策略造成，不命名为 PASSWORD_WRONG，不把 keyboard-interactive 当 password 自动回退 |
| 发现/提权 | `FACT_MACHINE_ID_MISSING`、`FACT_BOOT_ID_INVALID`、`CHECKSUM_TOOL_UNAVAILABLE`、`SUDO_CREDENTIAL_MISSING`、`SUDO_POLICY_DENIED`、`SUDO_RESULT_UNVERIFIABLE` | 只在明确 sudo 策略拒绝时归 AUTH_ERROR；解析异常不能伪称认证拒绝 |
| 目录/投递 | `REMOTE_TEMP_CANDIDATES_EXHAUSTED`、`UPLOAD_FAILED`、`PAYLOAD_DIGEST_MISMATCH` | 允许枚举候选索引及 `missing/readonly/noexec/symlink/permission/unverified` 原因；不输出任意路径/目录列表 |
| 执行/规范化 | `REMOTE_COMMAND_TIMEOUT`、`REMOTE_COMMAND_EXIT_UNEXPECTED`、`OUTPUT_LIMIT_EXCEEDED`、`RESULT_JSON_INVALID`、`RESULT_RESOURCE_MISSING`、`OBSERVATION_UNVERIFIABLE` | 输出执行方式、退出码、字节计数与原因码，不输出命令/原始 stderr；正常 FAIL 不变成 ERROR |
| 回收 | `CLEANUP_IDENTITY_CHANGED`、`CLEANUP_PROCESS_CHECK_FAILED`、`CLEANUP_ACTIVE_PROCESSES`、`CLEANUP_DELETE_UNCONFIRMED` | 清理归属及活动进程规则保持，不输出 owner token，不自动强杀 |
| 存储/worker | `JOURNAL_TAIL_RECOVERED`、`JOURNAL_RECORD_CORRUPT`、`REPORT_COMMIT_FAILED`、`RUN_WORKER_STOPPED` | 尾部恢复 WARN；中间损坏不忽略，发布可定位的存储故障；worker 停止后不再接收任务 |

需要底层库没有公开 typed error 的分支，应在调用边界包装自有 sentinel/typed cause；无法可靠识别认证拒绝、协商或流读取错误时使用保守未知码，不基于 `unable to authenticate` 等文案猜测新语义。

兼容迁移期间可隔离保留现有“大类分类适配器”，维持 AUTH_ERROR/CLI 15 等已验证语义；没有公开 typed cause 时新细分码使用 `SSH_HANDSHAKE_UNVERIFIABLE`，不能借机改成 NETWORK_ERROR 或声称已证实密码错误。该适配器不用于生成新的精确原因码；D2 真实回环错误密码用例同时校验原大类、退出码和细分原因的保守性。

## 6. 秘密、隐私与安全投影

原调查中“方案、主机清单、Override 不涉及秘密”的前提不成立。它们可能含 URL 凭据、私有地址、Shell 文本、敏感参数；通用字符串替换不是脱敏保证。

| 数据 | 操作日志（含 DEBUG） | 授权 UI/报告 |
|---|---|---|
| 密码、sudo 密码、私钥/口令、cookie、CSRF、访问/委托 token、owner token | 永不输出；不输出长度、哈希、前后缀 | 永不进入诊断；既有 OwnerToken 内部恢复材料与公开报告隔离 |
| 原始 HTTP body/header、查询字符串、Origin/Host、幂等键、环境变量全集 | 永不输出；路由用注册模板和固定拒绝码 | 只显示用户可行动的固定提示 |
| SSH/远端错误、命令、stdout/stderr、Goss 原始 JSON、脚本/Override | 只输出 typed cause、固定原因、允许的数值统计 | 诊断同样不原样输出；已有业务证据按模板的既有授权与脱敏契约处理，不因 debug 复制一份 |
| 主机 ID、role/item ID | 授权业务上下文中的有限长转义文本，最大 128 UTF-8 字节；不记录网络地址/用户名 | 授权 UI 可查看原本已有的主机配置；公开诊断示例只用占位数据 |
| 文件/工作目录 | 静态逻辑部件名、相对记录类型、行号、errno 枚举；不输出用户主目录/任意路径 | 允许已授权输入的字段定位，失败的任意字符串路径不回显 |
| SSH 指纹 | 默认只输出 unknown/changed/revoked 和 host_id | 授权身份详情可显示 observed/expected 指纹；仅作核验信息，不能自动信任 |

实现集中采用白名单构造器；`slog.Any("error", err)`、`slog.String("error", err.Error())`、`fmt %+v` 输出请求/凭据对象均禁止。Unknown cause 输出固定消息与可关联 ID；“DEBUG 模式”也不越过此边界。

先改造 `runSSH`：当前它会把原始 stderr 拼入 error 字符串，必须先换成安全 `Error()` 的 typed command error，再引入任意错误日志。错误对象只暴露退出码、超时、信号、输出超量等已登记元数据；cause 不序列化。需要判定 sudo 策略的 stderr 只能在 executor 内有界解析为枚举，未识别内容归 unknown，不作为错误字符串包装到上层。日志、报告、SSE 均显式经过相同安全投影；不能假定现有 `sanitizeEvent` 剥离 OwnerToken 就能保护所有新增诊断。

新增共享公共视图模块（如 `internal/publicview`，依赖 runstore/report/diagnostic，反向不依赖 Web/CLI）。Web、直接/委托 CLI `run status`、`report list --json`、`report show/export`、SSE 和诊断复制都使用其 Run/Report/Event 显式输出类型；禁止直接编码私有 Run、Acceptance 或 Event.Data。只纳入注册字段和经过验证的事件类型；未知事件保留序号和安全类型提示，不透传 RawMessage。投影深拷贝，不清除内部恢复副本的 owner token。公共 Run 摘要不含幂等键、私有清理材料或冻结原始表单。现有 CLI JSON 顶层结构可保留，但移除泄漏内部字段是明确的安全兼容收紧，须在 README/SPEC 说明；不能以兼容为由保留令牌。

既有 serve 启动时向操作者交付登录链接/启动令牌属于鉴权材料交付，不属于诊断日志。本设计不把它复制到 slog、日志文件、诊断摘要或支持导出；也不承诺用户自行录制/重定向包含登录链接的整个终端会话没有秘密。泄漏门禁分别验证既有明确鉴权交付和诊断输出，禁止把此例外扩大到任务密码、owner token 等数据。

## 7. 结构化进程日志与生命周期

基于标准库 `log/slog`，增加 `--log-level debug|info|warn|error`、`--log-format text|json`、可选 `--log-file` 和对应 `READYSPEC_LOG_LEVEL/FORMAT/FILE`。优先级为显式 flag > 环境 > 默认；默认 info/text/stderr。无须另加含义重复的 `--debug`。日志开关与产品鉴权分离。

Logger 在 CLI 配置解析后、workspace/runstore 打开前初始化，通过 Options 注入 CLI、server、manager、store、executor，纯 compiler/evaluator 返回诊断而非自行输出日志。将非法日志参数作为配置错误退出；不输出秘密配置值。

复用已鉴权 `GET /api/v1/system/info`，新增当前进程真实的 build 信息与 `accepting_runs/worker_state`，不能再用 `s.runs != nil` 推断健康。build 通过 Go 构建信息读取 version/revision/dirty/Go 版本，缺失时显示 unknown，不以磁盘上可能已被替换的二进制或当前源码 HEAD 代替进程身份；不输出构建机路径。UI 诊断复制包含这些字段，避免旧/新二进制共享相同 dev 版本号时无法识别差别。

| 级别 | 事件示例 |
|---|---|
| INFO | `service.started`、`submission.accepted/rejected`、`run.finished`、`service.stopped`，含构建版本/commit/dirty、实例 ID、绑定端口、固定 backend |
| WARN | 尾部恢复、可恢复 I/O 问题、SSE 重连/游标重置、日志降级；常见认证/安全拒绝计数后限流输出 |
| ERROR | worker 停止、持久化失败、日志写入失败、无法恢复的 Journal 损坏 |
| DEBUG | 各阶段开始/结束、耗时、字节计数、固定诊断码；不逐包追踪或记录协议明文 |

日志字段为 `time/level/event/request_id/submission_id/run_id/host_id/phase/reason_code/duration_ms` 的允许子集及已登记的数值字段。一次错误由负责边界输出一次，底层只返回 typed cause，避免重复噪声。成功的健康查询及每个静态资源请求不逐条输出 INFO。

可选日志文件启动时安全创建：Unix 专用目录 0700、文件 0600；用 no-follow/目录句柄原语逐级拒绝符号链接，在已打开句柄上核对 regular file、当前用户 owner、单硬链接，再收紧现有文件权限（创建时0600不会修复已有0644）。不改变用户无关父目录权限，不接管他人日志；不符合要求时启动失败。Windows 对任意 `--log-file` 路径显式应用用户专属安全描述符，拒绝 reparse point，不能假设路径位于 workspace 就自动有正确 DACL。

活动文件和全部备份执行同样的权限与路径检查，单 writer 持有独占写入租约；同一路径已被其他进程写入则安全拒绝，CLI/serve 使用日志文件时应配置不同路径。达到 10 MiB、单记录上限4 KiB时轮转，保留3份备份，总上限约40 MiB加一条记录；单writer先结束当前写入、关闭句柄，再重命名并安全创建新文件。轮转失败按运行期日志故障降级，不删除未知文件。原生 Windows DACL/reparse/轮转是验收要求，交叉编译不算通过。不把日志放入业务 Journal，不做无限保留或自动上传。

日志 writer 写失败/磁盘满时仅做一次独立、限量的 stderr 降级尝试，后续聚合计数，避免递归写日志；stderr 同样可能阻塞，不能同步无限等待。运行已接受任务不因操作日志故障被重复执行。启动时明确指定的日志文件不可用则启动失败；运行期间日志故障可降级，不吞掉权威存储失败。默认 stderr 和文件 writer 都由单独有界队列保护：最多1024条、序列化记录总字节不超过4 MiB，满时不阻塞提交/执行，聚合 dropped 计数；每个 writer 最多一条在途写入，禁止每条日志新建 goroutine。记录超4 KiB仅保留固定字段、首个原因及省略数，不截出非法 JSON。Handler 自行统计写入错误，因为 slog.Logger 会忽略 Handler.Handle 返回值；不能假定上层 logger 调用会报告故障。

worker 非预期退出或 HTTP 监听故障都先关闭接受门；与接受提交的临界区同步，优雅关闭期间提交返回 `503/RUN_WORKER_STOPPED`、`acceptance="rejected"`。supervisor 取消共同 context，保留首个致命错误；正常 SIGINT/SIGTERM 不伪装成 worker 故障。不保留长期监听但没有 worker 的模式，不伪造在途终态或自动重跑。

关闭总预算5秒：HTTP grace至多3秒，worker停止至多4秒（两者并行），日志 flush至多1秒且不得超过总截止。超时强制关闭HTTP连接；worker未停止时不得关闭它仍可能使用的 store，不再无条件等 `<-workerDone`。由进程最外层 supervisor 以13结束进程，内部库不随意 os.Exit；进入强制退出的资源所有权须显式交给顶层，避免 defer 先关闭 store 与 worker 竞态。Go不能安全中断任意堵塞 io.Writer：flush超时后不关闭仍在写入的句柄，不等待该writer，最终交由进程退出关闭；独立降级writer也最多一个，不制造无界goroutine。强制退出可能留下远端残留，按已持久化清理记录恢复，不能宣称已清理。已归档报告在后续 CLI/重启可读；不重新调度旧任务。验收覆盖 HTTP/worker 先后失败、提交竞争、慢writer、store并发访问及顶层子进程退出预算。

恢复必须先校验已提交 report.json，按 SPEC §8.3 及 M2 设计保留“有效报告优先”，不重算 Verdict；再分别处理 Journal。不能固化当前 replay 先读 Journal 的偏差：

| 已提交报告与 Journal | 恢复/查询行为 |
|---|---|
| 有效报告，Journal正常 | 原报告为权威，按既有规则补终态事件，不重算结果 |
| 有效报告，Journal中段损坏 | 继续提供原有效报告，Run查询附 recovery_error/安全诊断；损坏Journal只读保留，不向 poisoned Journal 追加 ARCHIVE_RECOVERED，不重写报告 |
| 无有效报告，Journal中段损坏 | 保留文件和最后可验证状态；报告接口500/RUN_STORAGE_ERROR、retryable=false；不无限409，不生成中断报告掩盖损坏；CLI无法读出权威报告返回13 |
| 仅不完整尾部 | 保留有效完整前缀、记录WARN及有效事件数，再按有效报告/取消/中断规则恢复，绝不重新排队 |

recovery_error 是查询附加诊断，不新增违反 Schema 的业务 RunStatus。损坏行内容不输出；报告本身损坏也保留证据，不当作有效终态。幂等命中带 RecoveryError 的既有Run仍返回原Run ID与acceptance=accepted、created=false并附存储故障，不重新安装凭据或调度；有有效报告可直接读，没报告则显示明确故障，不进入无限SSE等待。修复/备份不是日志功能的自动动作。

## 8. 报告、CLI 与 UI

`report.Failure` 和 `compiler.Evidence` 新增可选 `diagnostics`；Go 类型、`report.v1.json`、API 投影、SSE、前端模型和测试夹具同批修改。读取旧报告缺少诊断时显示“旧报告未记录具体原因”，不重新生成证据或改写旧归档；旧程序可能拒绝扩展报告，不承诺双向兼容。既有 schema 的 `additionalProperties` 与 required 规则须逐项验证。

扩展现有事件载荷时同步 `runstore.validEvent` 与 `webui.sanitizeEvent/projectRun`；如需新增事件 Kind，必须先更新 Journal 重放及 SSE 游标兼容测试，不能用未知事件把旧 Run 恢复阻断。诊断事件不承接原始操作日志。

Verdict 的计算与原因使用同一纯函数，保留原有优先级与退出码。输出稳定、去重的原因码：如 `REQUIRED_CHECK_FAILED`、`TOPOLOGY_FAILED`、`REQUIRED_EVIDENCE_ERROR`、`TRIAL_NOT_GATE`、`PARTIAL_SCOPE`、`SIMULATOR_BACKEND`。多因素同时记录，不掩盖主原因；WARNING 检查不被升级为 REQUIRED。

CLI `run submit`：接受后立即向 stderr 输出 Run ID；JSON 报告只输出 stdout；直接模式消费现有阶段事件，委托模式使用 SSE。重连携带 Last-Event-ID，按序号去重；游标 reset 拉快照，流中断只重连/读状态，不再次提交。终端退化为固定文本，转义终端控制符；进度按主机阶段聚合，非 TTY 不输出动画。`--quiet` 仅关闭进度/INFO，不隐藏 WARN/ERROR；退出码和最终 JSON 保持。

UI：`ApiError` 保存 request ID、acceptance 和诊断；RunPreview 展示提交拒绝的步骤、host_id、修复动作和复制诊断摘要按钮。复制只包含允许字段及构建身份，不包含密码/整份表单。接受状态未知保留“不确定提交”保护。历史详情先取 Run 状态和阶段，报告 409 不再等价于“完全无状态”；显示每主机阶段起止和未知时长，报告终态再加载权威报告。多行证据文本保留换行，复制业务证据与复制诊断是两个明确操作。

## 9. 实施批次与验收

| 批次 | 改动位置 | 必需验收证据 |
|---|---|---|
| D1a | diagnostic 注册表；webui/server、runs/plans；Manager.Submit、preview/projectRun目标集合；公共视图基线 | 缺失一台执行目标凭据、空密码、方法不符返回不同诊断；有 request/submission ID、无 Run；3 台清单仅 1 台绑定时不误要求其他两台凭据，不获取Agent/SSH或生成未绑定阶段；公共CLI输出无owner token和幂等键 |
| D1b | web/api、RunPreview、CLI入口/稳定幂等键、恢复查询；system/info构建身份 | 202及幂等命中返回本次关联ID；重试摘要不变；显式拒绝和unknown矩阵可验证；accepted.json提交后丢响应/同步错误恢复不建第二Run；旧API可读；stdout JSON干净 |
| D2 | executor/connect、workspace discovery、上传/执行/清理边界 | 真实回环 SSH 夹具覆盖正确/错误/禁止 password、未知/变更/吊销主机键、缺 known_hosts、Agent；逐分支诊断码与阶段稳定 |
| D3 | cli/serve、runstore/replay、workspace、HTTP middleware、logging | 有效报告+中段损坏Journal仍提供原报告且不追加坏Journal；无有效报告损坏才返回存储错误；尾部恢复WARN；RecoveryError幂等不重跑；worker/HTTP失败竞争及总关闭预算；日志权限、已有0644、链接、磁盘满、慢写、原生Windows轮转/DACL证据 |
| D4 | compiler/Evidence、report/EvaluateVerdict、schema、RunHistory | timeout、超量/损坏输出、缺资源有原因；FAIL 保持 FAIL；多因素 Verdict 与旧报告兼容 |
| D5 | cli/submit 委托 SSE、web/RunHistory | 断线重连/序号重复/reset/无归档报告仍有进度；不重复远端执行；文本/JSON/非 TTY 行为一致 |

D2 前置门禁是先移除 `runSSH` 的原始 stderr 错误包装再接入日志。D1 必须覆盖不经过 `safeRunError` 的私钥解析、公钥指纹、未知认证方式等 handler 早退分支。清理 AUTH_ERROR 不清除 Web 会话，以及全部 CLEANUP_* 原因保留，也列入 D2 回归。

全批次共用的秘密泄漏门禁：使用不同的密码、私钥、口令、cookie/token、owner token 哨兵，以及包含它们的远端 stderr、错误链、脚本/URL、非法清单输入。捕获 text/JSON 日志、stdout/stderr、HTTP 错误、SSE、公开报告、诊断复制内容和轮转文件，验证不出现哨兵、片段或其摘要。必须用真实 CLEANUP_REGISTERED/CLEANUP_UPDATED 夹具验证直接CLI（run status、report list/show/export）、委托CLI、HTTP、SSE的公共投影相同且内部恢复仍持有正确owner token；不是只对空报告测试。测试应注入真实错误路径，不只是直接测试一个 redact 函数。哨兵若恰好等于受允许的业务字段值，须使用不同的无碰撞夹具，而不是承诺任意自然语言字串都能识别为秘密。

代码实施采用项目现有 Go/Web 测试框架，优先复现行为再修复，不接触用户真实主机、凭据或生产数据；原执行契约与 SSH 身份校验回归必须保留。设计文档本次只做内容、链接、结构校验，不把文档检查称为实现测试。

每批交付列出具体用例结果和未覆盖平台；最终同跑 `go test ./...`、`go test -race ./...`（支持平台）、`go vet ./...`、Web 单元测试/构建及直接相关浏览器回归。命令前按现有隔离条件检查环境，不能用跳过代替通过。同步 README 的日志 flag/env 示例、SPEC 的新增字段/接受状态契约、适用 Schema 与 DESIGN 现有诊断交互；仅新增领域术语时修改 GLOSSARY。

## 10. 当前故障的完成判据

对用户三主机提交问题，先取得有 request ID 的具体提交诊断，再决定修复客户端凭据绑定、服务端凭据转换或实际 SSH 问题。不得从“无 Run ID”推导密码错误，也不得从单主机 CLI SSH 成功推导三主机都可登录。

成功判据：三台凭据均绑定且接受状态明确；若任务被接受，出现 Run ID；若连接/身份/认证失败，归档具体 host_id 和已确认的原因码。真实主机验证由用户操作或另行明确授权，不向对话索取密码。现有主机问题尚未因本文而解决。

## 11. 预期 debug 输出样例

以下均为虚构夹具输出，明确最终实现需要做到什么，不是本次服务已经输出的日志。没有端点地址、用户名、密码、stderr 或幂等键。

### 11.1 提交前拒绝：没有 Run ID

```text
time=2026-11-09T09:00:00Z level=INFO event=service.started version=0.2.0-dev commit=5173d4a dirty=false instance_id=service-demo port=18480
time=2026-11-09T09:00:02Z level=DEBUG event=submission.validation_started request_id=3a091f235aff4a28a207ff6e1049c137 submission_id=422eedf3b90243fbbca5e7cc7224fdf2 host_count=3 auth_method=password
time=2026-11-09T09:00:02Z level=INFO event=submission.rejected request_id=3a091f235aff4a28a207ff6e1049c137 submission_id=422eedf3b90243fbbca5e7cc7224fdf2 host_id=node-b phase=SUBMISSION reason_code=CREDENTIAL_HOST_MISSING acceptance=rejected
```

UI：“任务未接受。node-b 没有收到对应认证凭据。检查主机与凭据的绑定。诊断 ID：3a09…”。CLI stderr 输出同一原因；AUTH_ERROR 的退出码仍为 15，不能显示“SSH 密码错误”。如未开启文件日志，复制响应的诊断摘要也能定位原因。

### 11.2 接受后拒绝：主机身份变更

```json
{"time":"2026-11-09T09:01:00Z","level":"INFO","event":"submission.accepted","request_id":"e9e03de8d61c438f9d2c3a8ba151479a","submission_id":"81104d113ba445f9a1160e0775db3c4f","run_id":"run-AAAAAAAAAAAAAAAAAAAAAAAAAA","acceptance":"accepted"}
{"time":"2026-11-09T09:01:01Z","level":"DEBUG","event":"host.phase_started","run_id":"run-AAAAAAAAAAAAAAAAAAAAAAAAAA","host_id":"node-a","phase":"CONNECTING"}
{"time":"2026-11-09T09:01:01Z","level":"WARN","event":"host.preflight_rejected","run_id":"run-AAAAAAAAAAAAAAAAAAAAAAAAAA","host_id":"node-a","phase":"SSH_AUTHENTICATION","reason_code":"HOST_KEY_CHANGED","duration_ms":145}
```

UI：“任务已接受；node-a 的 SSH 主机密钥与受信任记录不一致，检查可信指纹来源。”附 Run ID 和既有业务分类；不自动更新 known_hosts。报告保留同一原因码，INFO 汇总终态、Verdict 和现有退出码推导。此例不意味着其他主机可以绕过全目标预检屏障继续执行。

### 11.3 CLI 委托与证据异常

```text
Accepted Run run-AAAAAAAAAAAAAAAAAAAAAAAAAA
[node-a] CONNECTING
[node-a] DISCOVERING
[node-a] RUNNING_STANDARD
[node-a] COLLECTING_STANDARD
[node-a / item-1] ERROR OBSERVATION_UNVERIFIABLE
[node-a] CLEANING
Run run-AAAAAAAAAAAAAAAAAAAAAAAAAA COMPLETED UNKNOWN: REQUIRED_EVIDENCE_ERROR
```

以上过程写 stderr，stdout 只有最终合法 JSON 报告。CLI 必须区分单个 Evidence 的错误与整个 Goss 结果无法解码：如果整体结果无效导致 Run 失败，不套用上例 COMPLETED。字段名或样例状态不能成为弱化真实执行语义的理由。

### 11.4 worker 故障与降级

```json
{"time":"2026-11-09T09:02:00Z","level":"ERROR","event":"worker.stopped","reason_code":"REPORT_COMMIT_FAILED","run_id":"run-AAAAAAAAAAAAAAAAAAAAAAAAAA"}
{"time":"2026-11-09T09:02:01Z","level":"WARN","event":"submission.rejected","request_id":"26e38d29ac554edbaa25fecdf2ffd83e","reason_code":"RUN_WORKER_STOPPED","acceptance":"rejected"}
```

关闭期间后续提交返回 503 和关联 ID，随后服务非零退出；旧报告文件仍保留，可用 CLI 或恢复后服务读取。不能只显示“HTTP 页面能打开”就认为 worker 健康。日志自身不可写时使用独立、限量的 stderr 降级提示，不把异常对象或错误文本整段打印。
