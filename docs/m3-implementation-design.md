# M3 四视图控制台与单二进制合流：实现设计与计划

状态：**最终设计，已完成 pi / K3.1 Pro 独立评审及讨论，可进入实施**。基线为 `0d2fc93`（M2 文档提交）；本文只规划 M3，不声称下述能力已经实现或通过实现验收。评审及验证依据见 [评审讨论记录](m3-design-review-k3.1-pro.md)。

依据：[SPEC.md](../SPEC.md) §§7、9–12、14–17、[DESIGN.md](../DESIGN.md) 的 ReadySpec 适用声明及 [GLOSSARY.md](../GLOSSARY.md)。M2 的执行、取消、恢复、凭据和 Verdict 契约继续有效。

## 1. 目标与交付边界

交付可独立使用的四视图业务闭环：作者编排并发布方案，工程师导入方案、配置执行、追踪任务、阅读证据和重新检查，操作者处置远端残留。React 静态产物和 Go 后端继续组成单个可执行文件，不引入前端运行时服务。

M3 包括为上述流程补齐必要的方案生命周期、公共模板、断言自测、历史重跑数据和任务读模型。不是把 M2 的简易表单包装成“四步向导”后即宣告完成。

不纳入：M4 六个 Linux 目标组合认证、Windows 11 管理端整体验证、20 主机 × 100 检查项正式压测报告；自动修复、常驻 Agent、多工作区/用户管理；未经 TODO-02 验证的 `readyspec.custom_result/v1`。本阶段不创建 AWS VM；Go 回环 SSH、离线模拟器和真实嵌入二进制足以支撑开发验收，正式平台矩阵保留在 M4。

## 2. 当前实现与差距（代码核对）

| 范围 | 已有实现 | M3 要完成的差距 |
|---|---|---|
| 壳层/工作台 | `web/src/App.tsx`、`Workbench.tsx`；四导航、主题、响应式导航、草稿保存、类型参数、批量操作/删除撤销、严格校验/导出 | 拆出业务视图；发布/停用/新版本草稿；错误定位到参数；完整交互验收 |
| 方案持久化 | `internal/webui/plans.go` 的 `plans/drafts/web-index.json`，仅保存 DRAFT；`internal/plan` 严格加载及摘要 | 生命周期权威记录、不可变发布、导入冲突及已有草稿兼容；CLI 复用 |
| 模板/自测 | 后端返回 36 个家族、37 个版本快照；局部 Shell 编辑 | 公共模板提升/查询；完整基础断言 Schema、编译归一化和纯函数自测；现 Schema 只有 `expect_exit_code` |
| 运行页 | `RunPreview.tsx` 能绑定清单、预览、提交、简单 SSE/结果表格 | 四步向导、角色建议、类型化现场参数、稳定幂等键、历史、取消、重连、阶段看板和报告 |
| Run 后端 | `internal/webui/runs.go` 已有 list/detail/report/SSE/cleanup；`GET run` 返回摘要及事件，SSE snapshot/reset 只有摘要 | 安全重跑配置、可重建主机阶段投影；当前摘要缺方案名称/版本；补正式端点别名 |
| 报告 | `internal/report` 权威归档和脱敏；前端本地简化类型 | 双视角表格、拓扑/失败/清理、完整证据、范围提示、JSON 下载；不能虚构历史报告没有的字段 |
| 清理 | `Templates.tsx` 行内凭据和确认勾选；后端重核验并处理 | 详情抽屉、活动进程冲突后明确二次确认、凭据所有出口清除、操作结果和审计反馈 |
| 单二进制 | Vite 输出至 `internal/webui/dist`，Go embed，Vitest/Playwright/双平台 CI | 防止陈旧 dist、离线启动验收、许可证保留；通过替代门槛后精确删除遗留物 |

## 3. 模块职责与状态所有权

沿用 React、TypeScript、Tailwind、Lucide、Vitest、Playwright，不为本计划引入 router、状态管理、UI 套件或数据库。

- `App.tsx` 留下认证、壳层、导航、当前编辑文档与未保存保护。现有 `Workbench` 行为增量修改，保留编辑期间请求竞态保护。
- `Overview.tsx` 显示近期任务和双角色快捷入口；`runs/` 内的向导、历史、任务详情、报告各自组织界面。
- `runs/useRunSession.ts` 是有深度的 Module：Interface 为选择 Run、订阅连接状态/投影、刷新和取消。Implementation 负责快照/增量排序、重连、401、终态读报告、AbortController 和过期响应隔离；视图不各写一套重连循环。
- `runs/wizardState.ts` 的 reducer 管理步骤、非秘密配置、预览修订号和一次提交意图；`reportModel.ts` 只做分组、筛选、排序，不推导 Verdict。
- `internal/planstore` 作为 Module，以工作区 Store 为依赖，集中草稿/发布/停用/导入冲突与模板提升。HTTP 和 CLI 仅为 Adapter；不要让 `plansMu` 和 Web handler 成为业务事实源。
- `internal/shellassert` 提供唯一基础断言求值 Interface，自测和真实 Goss Normalize 走同一 Implementation；不能出现“自测通过，真实执行按另一套规则判定”。
- 主机事实、RunStatus、Verdict、已接受取消及清理状态属于 Go；URL 只放视图、Run ID、非秘密过滤条件；草稿编辑和当前向导属于页面内存；localStorage 仅保存主题及“仅看异常”等显示偏好。

建议新增文件是职责位置而非强制文件数量；禁止为只有单个直通调用的包装器机械新增 Interface。

## 4. 方案生命周期与工作区兼容

### 4.1 操作规则

1. 草稿允许未完成内容。保存调用 `plan.LoadDraft`，不声称有效摘要。严格校验/发布调用 `plan.Load`、模板信任校验及逐角色 `CompilePlan`；导入不使用 `normalize_draft` 重签外部内容。
2. 发布以当前已保存草稿修订为前提，用户看到版本、模板锁及完整摘要。保存、更新、删除、发布和停用均在服务端锁内重读并校验 expected_revision，防止旧标签页覆盖/发布新内容；修订标识为服务端生成的记录内容指纹，不信任客户端时间。已有草稿的 revision 从旧记录内容确定性生成，不需要先改写旧索引。创建新草稿不传 revision；更新旧草稿的旧客户端未带 revision 时返回 409 和重新打开提示，禁止退回 last-write-wins。DRAFT → RELEASED 时重新计算包含 `state` 的 PlanDigest 并写 release manifest；客户端不能自行把 state 改成 RELEASED 后保存。
3. 发布版本内容不可覆盖。编辑已发布/停用版本先创建新的 DRAFT，要求同 plan_id 的版本递增；采用现有 `CompareNumericVersions` 支持的数字版本，不能用字典序比较，也不悄悄扩大为完整 SemVer。
4. 停用只改变生命周期，不改变历史 Run 或已经冻结的发布快照；新正式执行不允许从方案库选择已停用记录。外部文件执行的既有 M2 规则不追加全局撤销机制。
5. 相同 ID+版本、相同内容导入幂等；不同语义内容返回 409。草稿的稳定 `draft_key` 与发布身份分开，前端不得以名称或可编辑 ID 猜测存储路径。

### 4.2 权威数据与摘要

新生命周期记录采用单个原子 JSON envelope（内部格式，不冒充 Plan Schema）：稳定 record key、revision、原始 RELEASED 文档及摘要、可选 ARCHIVED 文档及状态变迁。通过 `workspace.Store.WriteJSON` 和已有 lease 写入，进程内串行化同一记录。

`semantic.PlanDigest` 当前包含 state；不得通过排除 state 改掉既有摘要协议。停用生成状态为 ARCHIVED 的新快照并重新计算摘要，保留原 RELEASED 快照，二者由同一个权威 envelope 原子关联。历史 Run 仍指向原始冻结摘要。外部 ARCHIVED 导入与已有版本冲突检查时，仅允许把 state 恢复为 RELEASED 后计算的内容指纹与原发布摘要一致的单向停用；不能以状态差异为由覆盖角色、参数或模板。没有已有记录的严格合法 ARCHIVED 文件可作为只读归档记录导入，不重新激活。

envelope 有两种合法形状：`released` 记录必须含 release_snapshot（RELEASED），停用后另含 archive_snapshot（ARCHIVED）；`archive_only` 记录仅含 archive_snapshot 和经校验的 release_fingerprint，release_snapshot 为 null。后者不虚构本工作区曾发生发布，之后导入同内容 RELEASED 也保持已停用、返回现有记录；异内容仍 409，不能通过导入恢复活跃状态。同一身份的状态推进仅允许严格内容相同的 RELEASED → ARCHIVED；此受控转换不被实现成通用“同版异摘要可覆盖”。

新增发布清单格式 `readyspec.release-manifest/v1`：plan_id、version、released_plan_digest、template_lock、goss_version、compiler_version。生成顺序为：草稿完整校验/编译 → 克隆为 RELEASED → 计算 PlanDigest → 生成 manifest → 再做严格验证 → 原子写 envelope。manifest 被 PlanDigest 排除，避免循环，但导入须逐字段验证：Plan ID/版本相符、模板锁与嵌入快照一致、工具版本被本程序支持，released_plan_digest 等于文档置为 RELEASED 后的 PlanDigest；停用时沿用此清单，不用 ARCHIVED 摘要冒充原发布摘要。此机制是内容一致性校验，不是数字签名或发布者身份认证。

旧草稿和已接受 Run 的读取不受新清单要求影响；旧外部 RELEASED/ARCHIVED 缺清单版本或内容无法核验时，新的导入/发布/提交入口返回明确 `RELEASE_MANIFEST_UNVERIFIABLE`，可查看诊断并由作者显式创建新草稿再发布，不静默重签/迁移为可信发布。此为 M3 的明确兼容收紧，实施时同步 Schema、SPEC §12 和 CLI/API 说明；不能继续接受空对象并宣称已验证发布清单。

旧 `web-index.json` 继续可读；不在启动时破坏性迁移，不删除旧草稿。发布与保留草稿不要求跨文件事务：发布 envelope 是唯一发布提交点，并记录 source_draft_key/source_revision；对应源草稿通过发布记录被逻辑消费，不再列为可编辑同版草稿。发布后补写草稿标识只是派生操作；即使此步失败，保存/删除/重复发布也先查权威发布记录，禁止继续编辑原版本。相同 source_revision 重试返回既有发布，旧标签页修改返回 409；创建新版本必须新 draft_key，不能复用被消费键。派生列表可从 envelope/旧草稿重建；损坏记录显式报错，不跳过后谎报成功。

### 4.3 公共模板

`POST /templates/promote` 只接受选定 PLAN_LOCAL 的 STANDARD CUSTOM_SHELL；验证参数化、断言和静态编译，生成 USER_PUBLIC 快照及新摘要，按 ID+版本幂等/冲突规则原子存储。不得覆盖 BUILTIN，不修改原方案引用，不提升权限。库列表合并内置与公共模板并保留版本与来源；批量添加复制完整快照和模板锁。

## 5. 四视图行为

### 5.1 总览与方案工作台

总览保留作者/工程师入口，工程师的“导入并执行”直接进入向导第 1 步，不先跳转工作台；增加新建执行、最近配置重跑与近期任务表。任务行展示方案、版本、Gate/Trial、Full/Partial、执行器、RunStatus、Verdict 和时间；无报告显示“尚无归档报告”，不能把完成状态当就绪。壳层常驻未结案清理数量及入口，页面切换后仍可见；读取失败显示“清理状态未知”，不把失败当零残留。

工作台补发布、停用、新版本、草稿试跑入口；已发布与停用只读。试跑把当前文档严格校验后的快照送入向导并锁定 TRIAL。保留类型参数、显式空值、现场可调开关、复制独立 ID、批量操作及删除撤销。静态诊断返回 role_id/item_id/parameter 或 JSON pointer，点击后切换角色、选中检查项并聚焦具体字段；Schema 错误不回显秘密值。

### 5.2 四步执行向导

1. **选方案**：从方案库选择或文件/粘贴严格导入。Gate 要求 RELEASED；DRAFT 只能 Trial；ARCHIVED 只读。显示来源、状态、版本、摘要和非门禁范围提示。
2. **清单和认证**：quick/INI/YAML 三种输入，调用现有解析器，不用前端复制解析算法。一个 Run 沿用单一 AuthIdentity：认证方式和选定私钥/Agent 公钥指纹共用；按主机覆盖只支持密码/sudo 密码，M3 不引入混合认证方式或不同主机不同公钥。私钥提供口令输入，Agent 明确指定服务所在机器的 endpoint 与指纹。非 TLS 的远程 HTTP 在秘密输入处持续显示风险提示。
3. **角色与现场参数**：同名 inventory group 与 role_id 产生可确认建议；无唯一匹配则保持未分配，不按角色顺序猜测。表格控件编辑绑定；参数只列 OverridableParam，复用参数编辑器、明确 default/group/host 来源及同级冲突。角色建议不替代后端校验。
4. **静态预览并提交**：显示主机/角色/项数、STANDARD/PRIVILEGED、自定义 Shell、主动探测、配置 root 与身份未验证提示。不加 root/sudo 授权勾选。后端返回规范化文档摘要，提交同一份合法快照，修复当前“预览 normalize，提交原文”的分离。

Plan、inventory、role map、overrides、mode、scope、backend、simulator_profiles、budgets 或认证身份改变会失效预览/提交意图。密码/私钥字节不持久化，但私钥及 Agent 的选定公钥指纹属于 RunConfig 的语义身份：同键换钥必须冲突，不能宣称修改任何秘密都不改变语义。同指纹重新输入凭据可以保持同一意图，不允许替换已接受任务的凭据。最后一步生成一次随机幂等键（复用支持普通 HTTP 的 getRandomValues 工具，不依赖 randomUUID 的安全上下文），同一意图重试复用键；202 后进入 Run 详情，不能“重试”再创建一个键。

响应丢失属于“接受状态待确认”：保持原键，可在重新输入凭据后以同键重试或查看历史；禁止自动生成新任务或自动回放携带秘密的 POST。修改配置必须先明确放弃原意图。秘密仅留当前表单内存，请求完成（成功/失败/丢失）、换认证方式、离开向导、会话过期及清理操作结束时统一清空；不入 URL、storage、日志或测试 trace。

服务端提交入口与共享 RunManager 同样验证：GATE 仅接受清单核验通过的 RELEASED，DRAFT 仅 TRIAL，ARCHIVED 不得新执行；不可通过直接 POST 或 CLI 绕过。方案库提交携带 record key 和冻结摘要，工作区服务在同一接受序列内检查未停用再提交 Run，避免“校验后停用、仍接受新 Gate”的竞态；不把长时间 SSH 预检放进该序列。已经接受的 Run 不受后续停用影响。外部文件无所属库记录时按文件的状态/摘要/manifest 核验，不引入跨工作区撤销服务。

### 5.3 实时任务与事件恢复

URL 用 hash 编码页面、Run ID、报告视角和非秘密过滤条件；兼容现有 `#overview/#workbench/#runs/#templates` 和登录 token fragment 清除逻辑，不新增运行时 router。刷新/后退可定位任务，不保存密码和清单。

Run 详情显示三套正交状态、逐主机阶段、耗时、失败与取消动作。M2 只有部分 PHASE 埋点，projection 本身无法补出缺失历史；M3 同时补 NativeSSH/Simulator 与 RunManager 的实际阶段记录：CONNECTING、DISCOVERING、PREPARING、UPLOADING、RUNNING_STANDARD、RUNNING_PRIVILEGED、COLLECTING、CLEANING、FINISHED。新增结构化 StageTransition（host、mode、phase、attempt、start/end/omitted、服务端时间和脱敏原因），通过请求注入的 StageRecorder 覆盖 Preflight 及 Execute，保留已有 PHASE 事件的可读性。空执行方式明确 omitted/无启用检查项，屏障等待不计作执行，未走到的阶段是未开始，旧记录缺事件是未记录；不得将这些状态映射为 ItemResult 的 PASS/SKIP。耗时来自同一 attempt 的实际起止时间。取消调用正式 POST 别名，202 是已接受取消，不宣称远端进程已退出；409 刷新最终报告。阶段埋点不能替代或移动 M2 的持久化执行标记；记录存储故障停止新操作，进入原有故障归档路径。

后端增加一致的 Run detail projection：summary、sequence、各 host/mode 最新阶段与已记录时间、失败、cleanup 摘要。GET detail 和 SSE snapshot/reset 使用同一投影函数，以 Store 锁内一致读取的 Run+sequence 生成，保留原 events 字段兼容旧调用方。快照不回传 credentials、owner token 或完整非必要配置。

浏览器使用 fetch SSE 以保留 CSRF/Last-Event-ID。支持分块 UTF-8、LF/CRLF、多 data 行和注释 heartbeat；增量按 sequence 去重，reset 完全替换投影，不拼旧状态；断开显示 stale 并指数退避重连（1/2/4/8/15 秒，最多 5 次后人工重试），重连不提交任务。游标规则：均无时初始一致快照；仅 after_seq 或 Last-Event-ID 时使用该非负整数；两者相同则接受，不同则 400，非法值 400；游标超出有效前缀时 reset。GET detail/snapshot/reset/增量全部由同一服务端 reducer 定义：新增增量 payload 携带该序号的投影变更，客户端按 host/mode/attempt 替换，不重算领域状态；旧事件字段保留。测试要求任意前缀重放的投影与同序号 snapshot 深度相等，重复、断线和 reset 后也相等。401 停止重连并要求认证，认证后按 Run ID 重建订阅；卸载/换 Run 立即 abort。终态取权威 report，409 以 0.5/1/2 秒间隔重试 3 次后显示“归档暂不可读”并提供人工刷新，不能自行用事件拼出最终 Verdict。

### 5.4 报告、下载与重跑

报告直接展示归档 Verdict、reasons、RunStatus、termination_reason、topology_results、host failures、cleanup warnings。Trial/Partial/simulator 永久显著提示“非正式全量门禁”；用户关闭过滤不改变该提示。

支持主机/检查项视角、按状态/角色/域过滤、仅看异常（FAIL/ERROR/UNSUPPORTED）、稳定排序和清除过滤。抽屉显示 expected/actual、来源、EvidenceMode、角色/模板身份、参数生效来源、执行身份及采集时间。兼容和生产约束分开：新程序读取旧报告时，缺字段明确“未记录”；M3 新产生的证据必须满足 SPEC §11.2/REQ-RES-06，不能用可选字段绕过采集。参数来源在 CompileHost 固化，模板摘要/版本来自冻结模板锁，实际 UID/身份和 root 审计来自 HostFacts，采集时间来自本次 Goss 原始记录；应有而无法取得的证据必须诊断 ERROR，不能显示完整 PASS。Schema/Go 类型允许读取旧记录，不承诺旧版程序能读取新增字段，也不改写旧归档。大证据折叠且按文本渲染，不注入 HTML；表格按需分页，避免挂载全部证据 DOM；下载为脱敏 `readyspec.report/v1` 原始 JSON。

“全量重跑”和“异常主机局部复测”从原 Run 的冻结 Plan+非秘密 RunConfig 开新向导，始终新 Run、新幂等键、新凭据。重跑精确保留 simulator_profiles 和 budgets（API 提交新增可选 budgets 并校验，省略仍用 M2 默认值）；Agent endpoint 是新操作现场输入，不从旧 Run 恢复。局部复测选择存在异常 ItemResult 或 host failure 的主机，保留这些主机的角色/允许覆盖、预算和对应 simulator profile，标明缺少完整拓扑；不能把没有异常的主机凭空补齐。无异常主机时禁用并解释。全量重跑恢复原清单。旧 Run 来自已停用方案时仅允许新 Trial 草稿或重新选有效发布版本，不静默沿用为 Gate。

PartialRun 配置由后端投影：inventory 与 profiles 只留选中主机；role_map 取主机交集、删空绑定；host override 仅留选中目标，group override 仅留仍被选中主机命中的组，并过滤到有效角色关联检查项。Plan 本身不裁剪、不改 min/max_nodes 或原始摘要；预览/提交仍走同一权限与覆盖解析器，组冲突不因前端隐藏而消失。测试要求投影配置可合法提交、不会引用被移除主机/组，并保持选中主机原有有效参数值。

### 5.5 模板与清理

模板按八领域中英文、来源和版本检索，详情抽屉展示完整参数契约和 EvidenceMode；支持公共模板提升、批量加入当前可编辑方案。清理列表展示 Run、host、machine/boot identity、路径、状态/原因；owner token 始终由服务端保留。

正常 CLEAN 先不传 confirm_terminate。后端报告存活进程时展示专用确认对话框，说明对象、可能终止进程及删除目录，再用新操作凭据提交确认；取消对话框不会发送破坏性请求。IGNORE 必须填写原因，成功后保留可查审计且不叫 CLEAN。所有失败清除凭据但保留非秘密选择和审计原因；列表空态只说“暂无未结案清理记录”，不说已检查所有远端。

列表默认仅未结案，增加“已处置审计”过滤及 Run 内清理审计入口；新 `GET /cleanups?state=all` 显式返回包括 CLEAN/IGNORED 的最新清理记录及审计原因，默认不带 query 保持仅未结案。历史权威来源为 Run Journal，不改归档报告来伪造后来已完成的清理。

## 6. 基础断言自测的前置闭环

按 SPEC §7.2 补完整基础断言：退出码，stdout/stderr 包含/不包含/相等/RE2 正则，有限数值的单值提取与比较/range。Schema 增加可选 assertions，旧 expect_exit_code 继续有效；结构化 custom_result 保持禁用。求值函数输入模拟输出与退出事实（正常/命令缺失/超时/截断），返回 PASS/FAIL/ERROR 和脱敏解释。文本有界，正则不执行 shell，数值拒绝 NaN/Inf/多值/解析失败。

真实执行仍只运行一次 Goss 受控命令。设计期已用锁定 Goss v0.4.9（校验仓库 SHA-256）验证：同一 command 同时配置 exit-status、stdout/stderr 的 `match-regexp: (?s).*`，`matcher-result.actual` 分别原样回传整数和字符串；成功匹配与故意不匹配均只写一次执行计数。空串、中文/转义/CRLF/末尾换行、65536/65537 字节输出均原样回传。因此采用此 observation-only 路径，再调用唯一求值函数，不启用 custom_result 协议、不二次执行脚本。早期试验中的 `eq` matcher 被 Goss 拒绝，最终方案只用已实测的 match-regexp。

求值契约固定为 `Evaluate(assertions, observation) -> {status, diagnostics}`。observation 包括 exit_code、stdout/stderr、execution_error、timed_out、truncated；采集缺失/重复/错误、超时、截断/超限、已证实的命令启动不可用均先返回 ERROR。无采集错误再对全部断言 AND：任一不满足 FAIL，否则 PASS；不推导全局 Verdict。stdout/stderr 不 trim、不转换换行，按 UTF-8 字节各限 64 KiB，超过判 ERROR；正则 RE2，数值取一个捕获结果（不指定捕获时对全文），恰好一个有限值才能比较，range 包含上下界。期望退出码保留 0–255，已执行的脚本返回 126/127 仍作为普通退出码求值，不新增保留值或不兼容禁用。

`execution_error` 必须有执行/采集事实来源：Goss 属性 `err` 的实际错误、缺失/损坏的观察记录，或 NativeSSH/Executor 的类型化执行故障；不得由退出码126/127、本地化 stderr 文本或浏览器猜测生成。追加实验发现 Goss 自身通过 shell 启动 exec，入口不存在/不可执行时也可能只有126/127、`err=null`，所以不能仅依赖 Goss.err 区分启动失败。

为补齐可验证事实，编译器在受控 `/bin/sh -c` 的参数环境设置后、用户脚本正文前插入固定 `printf` 启动帧。帧为按冻结方案/角色/检查项身份确定性生成的专用行前缀（不是秘密，不引入随机编译差异）；它与用户 stdout 在同一次 command 中回传，Normalize 校验并剥离精确前缀后再将原样用户输出交给断言及证据处理。缺/损坏帧代表无法确认受控入口已启动，判 ERROR；不得据此自动重跑，M2 执行标记仍优先。帧缺失不等价于“证明脚本未发生效果”。内部启动帧不是用户 `custom_result/v1` 协议，不要求脚本输出业务JSON，不允许脚本声明身份、全局Verdict或跳过结果。

设计实验已验证四个阳性对照：入口缺失（127）和不可执行（126）均无帧、用户脚本执行计数0；正常受控 shell 内主动 exit126/127均有帧、计数1，用户stdout剥帧后逐字节不变。已启动脚本内部处理子命令失败属于脚本语义，只有最终退出码时按普通断言处理。M3-1 将这组对照、帧碰撞/缺失/损坏、空输出和上限计数固化为原工件测试；原始采集上限为用户64KiB加已知帧长，公开证据不得泄漏/计入内部帧。Schema 不缩小既有退出码范围；自测允许显式选择“模拟启动失败”事实，不执行脚本或模拟启动帧。旧 expect_exit_code 与新 assertions 重复定义退出断言时仅允许值一致，歧义拒绝。

M3-1 将设计实验固化为 Go 原 Goss 回归测试：执行计数、stdout/stderr 属性唯一映射、空/Unicode/边界、超时、缺命令及合法非零退出码；同时测试纯函数自测与真实 Normalize 对同一 observation 完全一致。运行自测绝不解析执行用户脚本；证据展示规则与输出采集分开，敏感数据脱敏后才公开。

## 7. HTTP/CLI 契约增量

所有端点沿用 `/api/v1`、Origin/Host、session/CSRF、请求体上限及脱敏规则。前端 `api.request` 增加显式 method/signal 和 JSON/download 两种读取，旧调用保持默认行为，不自动重试写请求。

| 端点/命令 | 契约与兼容 |
|---|---|
| `/plans`、`/plans/{key}` | 列表/详情覆盖草稿与生命周期记录；旧 `key/document/updated_at` 保留，新增 revision/可用动作；已有 POST 按 draft_key 更新以及新 PUT/DELETE 均要求 expected_revision；无修订的旧客户端更新返回409，创建新草稿不受影响；删除不碰已发布记录或历史 Run |
| `POST /plans/{key}/release`、`archive` | expected_revision；服务端静态校验/原子提交；409 修订或版本冲突；不连接 SSH |
| `POST /plans/import`、`GET /plans/{key}/export` | 严格导入并入库；同版本冲突，不重签；导出合法自包含 YAML，Content-Disposition 安全文件名 |
| `POST /plans/validate`、`/plans/compile` | 保持原路由；规范化只用于明确作者草稿；增加定位诊断；逐记录 validate 路由复用同一 Module |
| `/templates`、`POST /templates/promote`、`shell-selftest` | 公共模板+内置列表；提升只 STANDARD；自测只求值，不运行脚本 |
| `POST /inventory/parse` | 原解析器输出 hosts/groups，建议分配是非权威 UI 辅助，不读目标主机 |
| `POST /runs/preview` | 保留旧字段，新增 validated_plan/digest 与参数来源摘要；明确静态 UID/权限未验证 |
| `GET /runs/{id}`、`events` | 加投影字段，旧 summary/events 不删除；snapshot/reset 对齐；summary 可增加 plan_id/name/version |
| `GET /runs/{id}/rerun-config` | 从冻结输入显式 allowlist 返回 Plan、inventory、role map、overrides、mode/scope/backend、simulator_profiles、budgets、认证方法/指纹；不返回秘密、Agent endpoint、owner token |
| `POST /runs/{id}/cancel`、`GET /cleanups`、`POST /cleanups/{id}/resolve` | 实现 SPEC 正式端点；保留 M2 DELETE run、`/cleanup` 和 Run 内 cleanup 路由作别名；跨 Run cleanup_id 必须精确唯一定位，冲突不猜 |
| Go CLI | 同步补 plan list/release/archive/import/export、run status/cancel、report export；调用同一 planstore/RunManager，保持 M2 lease/活动服务委派与退出码优先级，全部非交互 |

上述路径是新增设计，不是当前已实现端点。新增字段及版本行为实施时同步 SPEC/示例；禁止为了满足设计把客户端期望假设成现存返回值。

## 8. 单二进制交付与遗留清理

维持 `web → internal/webui/dist → go:embed`。构建顺序：许可证收集 → tsc/Vite → `verify-goss` → Go build。CI 在构建后检查 dist 与已提交文件一致；如内容受平台影响，先固定换行/构建元数据再比较，不允许靠忽略全部 dist 达到通过。

离线 smoke：只复制编译二进制到隔离临时目录，清空前端源码依赖，启动真实服务，从浏览器完成登录、四视图、导入/模拟器 Run/报告下载；运行时不需要 Node/Python/Docker/CDN/在线字体。目标 Linux 上的 Python 探针依赖与管理端零运行时依赖不同，不能删除目标能力声明。

离线测试主动拒绝并记录非回环网络请求，出现任一外网请求即失败；仅本机测试 API/静态资源允许访问。运行进程 PATH 不含 Node/Python/Docker 不影响测试驱动在外部启动 Playwright；不能把测试驱动的 Node 依赖误称为产品运行依赖。

M3 功能和替代测试通过后，再在独立变更中核对并删除精确 Git 跟踪清单：`src/readyspec/`、旧 `tests/unit/`、`tests/tui/`、`pyproject.toml`、仅服务旧 Python 原型的 Dockerfile/.dockerignore，以及实际存在的相关锁文件/入口。删除前用 `git ls-files` 和引用检索确认归属，不清理 `.venv`、用户工作区、缓存、未跟踪文件或外部环境。迁移必要历史用例到 Go/Web 后再删；不是把所有 Python 测试一删了之。

保留 `internal/plan/schemas`、Go 原工件测试、Goss payload、第三方许可证/NOTICE、DESIGN/GLOSSARY/规格和历史设计文档。README/帮助/CI/Makefile 不再指向 Python CLI、Docker 管理端或“远程执行暂不可用”；删除阶段不修改历史 Run/清理审计或做旧 Python 工作区自动迁移。

## 9. 实施顺序与每步验收

| 阶段 | 交付 | 必须通过的验收 |
|---|---|---|
| M3-1 契约与关键可行性 | 盘点路由/Schema/CLI；Goss 单次执行基础断言采集；冻结生命周期/修订、投影、重跑和公共模板契约 | 退出码/stdout/stderr 单次关联阳性/阴性；不执行命令的自测；归档摘要转换、导入冲突、旧草稿兼容测试设计 |
| M3-2 作者闭环 | planstore、发布/停用/导入导出、公共模板、基础断言、工作台入口和参数定位 | TDD：同版本异摘要拒绝、并发草稿修订冲突、发布不可改、停用不改历史、公共提升不改原引用/不提权；自测与真实 Normalize 同结果 |
| M3-3 运行向导 | 方案选择、清单解析、建议绑定、类型现场参数、冻结预览和一次提交意图 | 三清单格式；预览零 SSH；改配置失效；丢响应同键重试只接受一次；普通 HTTP 可生成键；私钥口令及所有秘密出口清空 |
| M3-4 历史/实时/报告 | 任务导航、九阶段起止事件及投影/重连、取消、完整新证据、JSON 下载及全量/局部重跑 | 重排/重复/reset/401/断线/终态409；同序号 snapshot/增量投影一致；空模式/重试/预检失败阶段；取消与归档竞争；旧报告缺字段、新报告字段必填；试跑/局部/模拟器不显示正式放行 |
| M3-5 清理与产品一致性 | 二次确认、审计、双主题、键盘与响应式、旧帮助清理 | 活进程无确认零终止/删除；IGNORE 原因；失败清凭据；空态/错误/stale/权限态；焦点与 Escape 返回 |
| M3-6 合流与移除原型 | 二进制离线 smoke、生成物一致性、遗留替代清单和精确删除 | 四视图真实浏览器流程全通；Go/Web 及三目标 build；所有旧入口引用已替换；删除审查；M4 待认证声明保留 |

不以工时估计代替退出条件。每阶段变更保持可独立审查，后续阶段不能绕过前置契约/安全验收。

## 10. 验证矩阵与完成条件

新增业务按 TDD，先复现行为缺失，再实现。复用现有 Go/Vitest/Playwright；文案和纯样式用内容/构建/浏览器验证。不得给测试连接生产目标，敏感测试使用专用虚构材料且不进入截图/trace。

| 层级 | 命令/场景 |
|---|---|
| Go | `go test ./...`、`go vet ./...`；Linux `go test -race ./...`；新 planstore/selftest/投影/兼容和安全拒绝用例 |
| Web | `npm --prefix web run format:check`、`npm --prefix web test`、`npm --prefix web run build`；组件验证向导、生命周期、报告、清理、键盘焦点 |
| 浏览器 | `npm --prefix web run test:e2e`：真实 Go 临时工作区；作者发布→导出→严格导入→模拟器任务→报告/重跑→清理审计；Playwright HTTP fake 仅用于断线/乱序/错误注入，不冒充 SSH 实测 |
| 打包 | `make build-all`（先确保最新 web）；单二进制离线 smoke；Windows/Linux CI；三架构构建不冒充六平台认证 |
| 视觉/可用性 | 1440/1024/390 CSS px，深浅主题、200% zoom、reduced motion；状态不只用颜色，表格/抽屉/弹窗键盘可用，无秘密截图 |
| 遗留清理 | Git 精确删除清单、引用扫描和替代用例映射；不读取/删除用户历史数据 |

完成条件：SPEC §10.1 四视图及所依赖后端逐项有行为证据；历史、刷新、重连、重复提交、取消和清理关键路径闭环；无浏览器 Verdict 重算；所有新增接口有兼容测试；离线单二进制可运行；旧原型仅在替代验收后删除；M4/TODO-02 的限制仍显式可见。不得因为已有 UI、CI 绿色或仅模拟器路径通过就跳过原 Goss 与权限/摘要约束。

需求追踪：REQ-WEB-01 → 壳层/四视图/常驻清理提醒；REQ-WEB-02 → 总览直接导入执行；REQ-WEB-03 → 工作台三栏/参数定位；REQ-WEB-04 → 文件/粘贴及三清单格式；REQ-WEB-05 → 报告双视角/证据/复测；REQ-WEB-06 → 非 TLS 警示和秘密清除。REQ-PLAN-02/05/06/07/09 → §4 生命周期与离线跨工作区导入；REQ-TPL-12/13 → 公共提升和唯一断言求值器；REQ-RES-06/09 → 新证据完整性与权威归档；REQ-CLI-01 和 REQ-PLAT-01 → CLI 补齐与合流。F07/F08 是本阶段主验收门槛，F06/F09–F13 在改动范围内回归；F02/F14 正式矩阵与性能签署仍归 M4。

## 11. 设计评审记录

独立评审实际通过 pi 调用 `magpie / kimi-code-cn/k3.1-pro`。首轮核对仓库代码并提出 8 项 P1；主代理逐项回应和修改，定向复审关闭其中 7 项，要求修正 126/127 不能单凭数值推定命令不存在的规则。主代理采纳并补做原 Goss 对照，发现 `err=null` 也可能是启动失败，遂给出确定性启动帧及剥帧方案；同一评审模型再次确认无剩余设计阻断。

最终评审原文：**“设计评审通过，可进入实施；不代表实现验收。”** 各问题、回应、取舍、实际调用模型和本机验证证据保存在 [M3 设计评审与讨论记录](m3-design-review-k3.1-pro.md)。

## 12. M3 实现验收记录

2026-11-06：按 §9 的 M3-1 至 M3-6 及 §10 完成实现验收。方案作者与模板生命周期、断言自测、四步 Run 向导、冻结输入、历史/局部复测/全量重跑、九阶段投影、SSE 游标恢复、取消、报告证据、清理确认和审计均有 Go/Web 行为回归。冻结方案以原始规范化文档持久化并在恢复时重新校验，避免空可选字段丢失造成摘要漂移。

验证记录：`go test -race ./...`、`go vet ./...`、Web 23 项 Vitest、Prettier、Playwright 7/7、`make build-all` 均通过。Playwright 阻断所有非回环浏览器请求；清理闭环通过真实 Go 服务和真实 Run Journal 验收：E2E fixture 预置待处置记录，浏览器仅注入 CLEAN 返回的“关联进程仍活动”状态以触发确认框，取消后断言 CLEAN 请求没有再次发出；IGNORE 请求、返回的 `IGNORED` 与审计原因、切换“全部审计记录”后的读取均经过真实服务端 API/Journal。另将 Linux amd64 二进制单独置于临时目录，以空 `PATH` 启动真实服务并执行 7 项浏览器流程，验证了登录、四视图、严格导入、模拟器 Run、报告下载、重跑及清理审计。检查了 `git diff --check` 和旧入口引用；按 §8 精确 Git 跟踪清单移除 `src/readyspec/`、`tests/unit/`、`tests/tui/`、`pyproject.toml`、旧 Dockerfile 与 `.dockerignore`，未清理未跟踪缓存。

M3 完成不代表 M4 的六个 Linux 目标组合认证、Windows 11 管理端整体验证或正式性能报告已完成；真实 SSH/目标平台认证和 TODO-02 自定义结果协议仍按 SPEC 保留原有边界。
