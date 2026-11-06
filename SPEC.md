# ReadySpec 产品与技术规格

> **文档标识**：READYSPEC-SPEC-V2  
> **规格版本**：2.0（Windows/Linux 三架构单二进制 + Web UI + 标准领域词汇基线）  
> **修订日期**：2026-11-04  
> **关联权威词典**：[`GLOSSARY.md`](./GLOSSARY.md)  
> **关联界面设计规范**：[`DESIGN.md`](./DESIGN.md)  
> **文档类型**：PRD + SRS + 需求追踪矩阵（RTM）+ 验收基线  
> **状态**：架构与领域词汇已定稿；标注 `TODO` 的目标平台实测事项在取得测试报告前不得视为已认证

## 目录

1. [文档约定与决定状态](#1-文档约定与决定状态)
2. [产品目标、用户与范围](#2-产品目标用户与范围)
3. [总体架构与部署运行模式](#3-总体架构与部署运行模式)
4. [领域模型、文件结构与版本治理](#4-领域模型文件结构与版本治理)
5. [检查模板、现场参数值与编译契约](#5-检查模板现场参数值与编译契约)
6. [36 项预置检查模板与环境拓扑核对契约](#6-36-项预置检查模板与环境拓扑核对契约)
7. [自定义 Shell 与结果协议](#7-自定义-shell-与结果协议)
8. [Run Manager、远端执行、失败与远端残留清理](#8-run-manager远端执行失败与远端残留清理)
9. [安全与信任边界](#9-安全与信任边界)
10. [Web UI、CLI、CI/CD 与受控本地 API](#10-web-uiclicicd-与受控本地-api)
11. [检查任务结果、观测证据与检查报告](#11-检查任务结果观测证据与检查报告)
12. [检查方案发布、导入导出与兼容性](#12-检查方案发布导入导出与兼容性)
13. [非功能需求与运维规则](#13-非功能需求与运维规则)
14. [需求追踪矩阵 RTM](#14-需求追踪矩阵-rtm)
15. [测试与发布验收基线](#15-测试与发布验收基线)
16. [M0–M4 实施路线与里程碑](#16-m0m4-实施路线与里程碑)
17. [TODO、技术风险与变更控制](#17-todo技术风险与变更控制)
18. [参考资料与领域词汇对照](#18-参考资料与领域词汇对照)

---

## 1. 文档约定与决定状态

### 1.1 规范性用词与统一词汇约束

- **MUST / 必须**：正式发布的强制要求，违反即不满足对应验收。
- **MUST NOT / 禁止**：明确不允许的行为。
- **SHOULD / 应当**：默认设计；例外需要记录理由和风险。
- **MAY / 可以**：不改变核心语义的可选实现。
- **P0**：正式发布阻断范围；**P1/P2**：后续增强，不可伪装成已实现。
- **统一语言与仓库过渡期说明**：本规格及所有新编写的 Schema、Go 代码、API、CLI 与 Web UI 文案**必须严格遵循 [`GLOSSARY.md`](./GLOSSARY.md)** 中定义的标准名词，禁止使用 `_Avoid_` 所列旧词。M3 验收前保留的 Python 原型及旧测试现已由 Go/Web 实现和替代用例覆盖，并随 M3 验收从仓库移除。

### 1.2 已确认的产品与架构决策

| 主题 | 冻结决定 |
|---|---|
| 核心定位 | 通用 Linux 业务软件部署前环境**检查方案编写、目标主机检查、就绪结论门禁与检查报告**工具；不是持续服务器监控或 CMDB 资产平台 |
| 管理端交付形态 | **Windows 与 Linux 双操作系统、三架构零外部运行时依赖单二进制（`CGO_ENABLED=0`）**：交付 `windows/amd64`（`readyspec.exe`）、`linux/amd64`、`linux/arm64` 三个可执行产物 |
| 交互与入口 | **内嵌式 Web UI + 非交互 CI/CD CLI 统一单二进制双入口**（无参或 `readyspec serve` 启动 Web 服务；子命令执行 CLI，移除终端 TUI） |
| 协调与工作区锁 | **自适应双模协调器**：同工作区有活动 `readyspec serve` 时，CLI 自动通过本地受控 API 委托执行（Web UI 实时同步可见）；无活动服务时 CLI 直接持跨平台文件锁（Linux `flock` / Windows `LockFileEx`）在进程内独立执行 |
| 目标主机前提 | 纯 Linux（`x86_64` / `aarch64`），仅需 SSH + POSIX `/bin/sh` + 可用临时执行空间；**无需目标主机预装 Python、Goss、Docker 或常驻 Agent** |
| 检查与传输引擎 | 单二进制内嵌经 SHA-256 锁定的**未经修改官方 `Goss v0.4.9`（`linux-amd64` 与 `linux-arm64`）**；管理端采用纯 Go 原生 SSH（`golang.org/x/crypto/ssh`）+ 净室自研 Ansible INI/YAML 主机清单解析器；同时保留**内置仿真执行器**供离线预演与测试 |
| 检查覆盖 | 八大目标主机检查领域共 **36 项预置 P0 检查模板（`CheckTemplate`）** + 方案级 **环境与拓扑要求（`TopologyPolicy`）** 自动核对 |
| 方案与生命周期 | 自包含单 YAML 文件；`检查方案 (Plan) → 环境与拓扑要求 (TopologyPolicy) + 节点角色 (Role) → 检查项 (CheckItem)` 扁平结构；生命周期仅含 **草稿 (`DRAFT`) → 已发布 (`RELEASED`) → 已停用 (`ARCHIVED`)** 三态 |
| 自定义检查 | 方案内创建自定义 Shell 检查模板，可提升为公共模板；仅允许 **普通执行 (`STANDARD`)**（严禁经 `sudo` 提权；若 SSH 登录账号本身为 `root`，提示实际执行身份并记录审计，无须额外授权） |
| 检查任务管理 | 单活动检查任务（`Run`）、FIFO 队列最多 10、每任务默认最多 5 台目标主机并发；同主机先 `STANDARD` 后 `PRIVILEGED` 串行；支持 **正式门禁 (`GateRun`) / 草稿试跑 (`TrialRun`)** 与 **全量检查 (`FullRun`) / 局部复测 (`PartialRun`)** |
| 状态与门禁 | 任务状态（`RunStatus`）、单项结果（`ItemResult`）、就绪结论（`Verdict`）三套状态正交独立；**仅 `RELEASED` 方案 + `GateRun` + `FullRun` + 真实 SSH 执行正常完成且必检项全通过方可产出放行级 `READY`（退出码 `0`）** |
| 管理端技术栈与许可 | 后端 `Go` 原生（`Cobra` + `net/http` SSE + `x/crypto/ssh` + `x/sys` + `yaml.v3` + `jsonschema` + `RFC 8785 JCS`）+ 前端 `React + TypeScript + Vite + Tailwind CSS + Lucide`（构建期静态编译 `embed` 内嵌）；全栈严格保持 `Apache-2.0 / MIT / BSD / ISC` 宽松许可证（零 `GPL/LGPL/AGPL`） |
| 目标平台认证范围 | Ubuntu 24.04 / Debian 13 / Rocky Linux 9 × `x86_64` / `aarch64` 六个首批认证目标组合 |

### 1.3 条件技术能力与禁止变通

- **安全特权执行（`PRIVILEGED`）通道**：属于承诺的安全门槛，必须在 M0 证明防普通用户篡改（TOCTOU 防护与 `root` 态二次摘要验真）；失败时**不得冒险放行**。
- **自定义结构化结果协议**：高级条件能力。M0 若无法可靠从锁定的 Goss JSON 结果无二次执行地提取完整记录，仍必须交付基础断言模式，但**不得启用依赖未验证结构化协议的已发布方案**，严禁绕过 Goss 对同一命令做二次 SSH 执行。
- **取证方式保真**：每项预置检查模板都必须满足其声明的取证方式（`EvidenceMode`）；允许准确声明目标环境条件依赖，**不允许静默降级为浅层检查冒充 `PASS`**。

---

## 2. 产品目标、用户与范围

### 2.1 产品使命

软件研发/交付作者明确写出“软件部署前各节点角色需要什么样的系统环境”；ReadySpec 将检查方案编译为确定性的检查工件，在现场 Linux 目标主机上无代理执行验证，输出证据可审计的**就绪（`READY`）/ 未就绪（`NOT_READY`）/ 待确认（`UNKNOWN`）**就绪结论。它不推断业务要求，不替用户修改或安装目标环境，也不承担部署后的持续监控。

### 2.2 用户画像与主要工作流

| 使用者 | 核心工作流 | 不要求用户先具备 |
|---|---|---|
| 方案作者 | 新建检查方案草稿 → 配置环境与拓扑要求 → 定义节点角色 → 挑选检查模板/编写自定义 Shell 生成检查项 → 设定阈值并标记现场可调参数 → 草稿试跑或静态校验 → 发布并导出自包含检查方案 YAML | 客户现场的目标主机地址、SSH 凭据 |
| 部署工程师 | 启动单二进制打开 Web UI → 导入自包含已发布方案 → 录入主机清单（快速 IP 或 Ansible INI/YAML） → 分配节点角色与填写现场参数值 → 查看零 SSH 静态预览及特权/`root` 执行提示 → 发起检查任务 → 查看实时进度与检查报告 | 理解 Goss 语法、安装 Python/Node/Docker 运行时 |
| 运维排障人员 | 按目标主机或按检查项查看不满足项与异常证据 → 手工修复目标主机环境 → 一键发起异常主机局部复测（`PartialRun`）验证修复效果 → 最终发起全量检查（`FullRun`） | 自动环境修复工具 |
| CI/CD 流水线 | 调用同一 `readyspec` 单二进制 CLI 非交互提交正式门禁任务 → 按确定性退出码（`0` 放行，非 `0` 阻断）控制流水线 → 导出权威 JSON 检查报告归档 | 浏览器或图形界面 |

### 2.3 功能范围

1. **八大目标主机检查领域（36 项预置 P0 `CheckTemplate`）**：硬件与计算（4）、操作系统与内核（4）、存储与文件系统（5）、网络与通信（6）、系统资源与基础服务（5）、安全与访问（4）、软件依赖与运行时（5）、系统健康与生命周期（3）。
2. **方案级环境与拓扑要求（`TopologyPolicy`）**：声明允许的目标 CPU 架构、各节点角色的最小/最大机器数、是否允许同机混部，以及运行期的实机动态身份去重与检查项覆盖完整性核对。操作系统发行版与版本要求由具体的 `O01 发行版与版本` 检查项表达，不设置方案级 OS 白名单。
3. **按需选择**：由方案作者按节点角色挑选所需检查项并赋予重要性（`REQUIRED` 必检、`WARNING` 警告、`INFO` 信息）；空方案或无任何启用必检项的方案禁止产出 `READY`。

### 2.4 明确排除

- 目标主机自动安装依赖、自动修改配置、修改业务服务或修改 `sudoers`，以及破坏性/写入式磁盘压测、漏洞扫描或网段扫描。
- 目标主机常驻 Agent、要求目标主机预装 Python/Goss/Docker、检查 Windows 目标服务器（Windows 仅作为管理端运行操作系统）。
- 检查项之间的运行时有向无环图（DAG）、跨检查项条件分支、数据传递或循环编排。
- CMDB 常驻主机资产台账、部署后持续健康监控与指标时序趋势、CI/CD 流水线编排引擎、事故响应平台、多租户公网 SaaS 平台、在线模板市场或运行时联网拉取二进制。
- 宣称任意用户自定义 Shell 可被静态证明为绝对只读或受完整内核级沙箱隔离。

---

## 3. 总体架构与部署运行模式

### 3.1 单二进制逻辑组件

| 组件 | 职责 | 边界 |
|---|---|---|
| Embedded Web UI | 浏览器端 DevOps 控制台（总览、方案工作台、检查与报告、模板与清理） | 编译期内嵌静态资源，通过受控 REST + SSE 与后端交互 |
| CLI & Adaptive Coordinator | 解析命令行子命令；自动探测同工作区活动服务实现“本地 API 委托 / 进程内持锁直跑”无缝切换 | 与 Web UI 共享同一套控制面语义与状态规则 |
| HTTP + SSE Server | 默认绑定 `127.0.0.1` 提供本地 Web 与 API 服务；处理会话鉴权、`Origin`/`Host` 校验与 SSE 实时事件流 | 非回环监听时强制启用口令/Token 鉴权 |
| Run Manager & Ephemeral Vault | 检查任务幂等提交、FIFO 队列、主机并发调度、取消控制、短期纯内存凭据托管（终态即焚）与事件广播 | 单一工作区任一时刻仅允许一个活动 `RunManager` 持锁 |
| Plan & Template Registry | JSON Schema 校验、SafeYAML 解析、RFC 8785 摘要锁定、方案生命周期管理、自包含导入导出 | 纯本地静态处理，绝不发起 SSH 或执行命令 |
| Resolver & Goss Compiler | 三级参数线性解析、只读环境变量转义、零 SSH 静态预览、每主机每执行方式生成并智能合并 Goss YAML | 发现属性矛盾直接报错阻断，不改写方案原始断言 |
| Inventory & Native SSH Executor | 净室解析 Ansible INI/YAML 与快速主机表；基于 `golang.org/x/crypto/ssh` 连接 Linux 目标主机、投递内嵌 `Goss v0.4.9`、执行普通/特权检查与安全清理 | 零外部 `ssh`/`sshpass`/`ansible` 进程依赖 |
| Mock Simulator Executor | 解析真实编译出的 Goss 规则并按预设画像生成仿真结果 | 界面与检查报告强制显著标识 `mock_simulator`，严禁产出放行级门禁信号 |
| Verdict Normalizer & Store | 将 Goss JSON 还原为单项结果与观测证据，核对 `TopologyPolicy`，推导 `Verdict`，执行跨平台原子持久化 | 不因 Goss 进程非零退出码一概归为 `FAIL` |

### 3.2 跨平台进程协调与网络监听模型

- **默认本地模式（`readyspec` 或 `readyspec serve`）**：
  - 进程启动时获取工作区排他文件锁（`.lock`），在受保护目录写入运行态发现文件 `runtime/server.json`（含监听地址、实例随机标识与本地委托密钥），默认仅绑定 `127.0.0.1:18480`（端口占用可自动递增或显式指定）。
  - 桌面环境下自动调用系统浏览器打开 `http://127.0.0.1:<port>/#token=<bootstrap_secret>`（利用 URL Fragment 避免密钥出现在 HTTP Referer 或访问日志中），前端用该密钥换取 `HttpOnly; SameSite=Strict` 会话 Cookie 与 CSRF 请求头令牌；无图形环境（如 Linux SSH 跳板机终端）自动跳过拉起浏览器并打印访问说明。
  - 关闭浏览器标签页**不会**中断 `readyspec serve` 中正在执行的检查任务；重新打开页面即可通过快照 + SSE 游标恢复实时观察。
- **显式内网跳板机监听模式（`--listen 0.0.0.0:<port>` 或指定内网网卡 IP）**：
  - 绑定非 `127.0.0.1`/`::1` 回环地址时，**必须启用访问令牌鉴权**（读取 `READYSPEC_AUTH_TOKEN`、`--auth-token`，或自动生成高熵一次性访问口令输出到控制台）。
  - 产品部署范围为受信任内网，允许以纯 HTTP 监听非回环地址，不强制 TLS；此模式下访问令牌、会话及业务数据也经明文传输，属于已接受的部署取舍。支持通过 `--tls-cert` 与 `--tls-key` 可选启用 HTTPS；使用非回环 HTTP 时，Web UI 在输入 SSH/Sudo 密码处必须展示醒目的明文传输风险提示。
- **CLI 自适应双模执行**：
  - 当执行 `readyspec run/plan/report/cleanup ...` 时，协调器先尝试非阻塞获取工作区文件锁：
    1. **获取成功**（当前未运行 Web 服务）：CLI 直接在当前进程内持有文件锁启动 `RunManager` 完成操作并退出，无需事先启动后台守护进程。
    2. **锁已被占用**：读取 `runtime/server.json` 验证同工作区活动 `readyspec serve` 实例身份，通过本地受控 API 提交并订阅 SSE 事件流直至完成（若该服务的等待队列已满，返回退出码 `16 WORKSPACE_BUSY`）；若锁由另一短命 CLI 进程持有，在 `--no-wait` 下立即返回退出码 `16 WORKSPACE_BUSY`，否则按等待预算轮询等待锁释放。
  - **本机凭据委托**：私钥由 CLI 读取并解析，经已认证本地通道交接必要密钥材料至服务的任务内存保险箱；服务不得打开 CLI 提供的私钥路径。Agent 认证由服务直接连接用户明确指定、符合本地用户访问规则的同机 Unix socket 或 Windows 命名管道，固定选定公钥身份，不静默改用服务自身 Agent，也不引入任意网络转发或依赖 CLI 存活的签名代理。新任务接受前必须取得所需材料或可用 Agent 连接；接受后 CLI 退出不影响任务，但 Agent 自身锁定、退出、移除密钥或要求人工确认仍可导致认证失败，不自动替换凭据。

### 3.3 跨平台工作区持久化布局

- **工作区根目录解析优先级**：环境变量 `READYSPEC_WORKSPACE` → `--workspace` 参数 → 平台默认用户数据目录：
  - **Linux**：`$XDG_DATA_HOME/readyspec/workspace`（默认 `~/.local/share/readyspec/workspace`，目录权限 `0700`，文件权限 `0600`，排他锁采用 `unix.Flock`）。
  - **Windows**：`%LOCALAPPDATA%\ReadySpec\workspace`（创建时设置受保护 DACL，剥离非必要继承权限，仅允许当前登录用户、`SYSTEM` 与 `Administrators` 完全控制；排他锁采用 Win32 `LockFileEx`，进程异常终止由操作系统内核立即释放锁）。
- **逻辑子目录**：

| 目录 | 内容 | 生命周期策略 |
|---|---|---|
| `plans/drafts/`、`plans/released/` | 检查方案草稿、已发布与已停用方案文件 | 用户管理；绝不因历史检查任务过期自动清理 |
| `templates/` | 用户提升的公共检查模板（`USER_PUBLIC`） | 版本不可变 |
| `runs/<run-id>/` | 不含秘密的 Plan/RunConfig 冻结记录、原子接受记录 `accepted.json`、单调序号 Journal `events.ndjson`、可重建状态快照、最终 `report.json` | 已完成普通任务默认保留 30 天且最多 100 条 |
| `cleanups/` | 待清理残留记录（`CleanupItem`） | 处于 `PENDING / FAILED / UNKNOWN` 或审计保留态时永久受保护 |
| `exports/` | 用户主动导出的自包含检查方案与检查报告 | 不受自动历史清理影响 |
| `runtime/` | 排他锁 `.lock` 与活动服务发现文件 `server.json` | 瞬态运行文件，服务退出自动移除 |

冻结输入、接受记录、状态快照、报告和清理记录采用**同目录临时文件写入 + `fsync`/`FlushFileBuffers` + 原子重命名替换及目录同步**（Windows 下严格保证关闭句柄后原子替换）。事件 Journal 使用仍受工作区路径约束与排他租约保护的追加接口；每个 Run 串行分配序号、追加记录，关键转换同步成功后才确认接受或允许对应远端副作用。事件流尾部若因断电/崩溃出现半行损坏，启动加载时保留完整有效前缀并记录诊断；中间记录损坏不得跳过，须保留文件并报告存储故障。绝不将未完成原子写入的报告视为有效终态。

冻结输入先落盘，最后原子写入并同步的 `accepted.json` 才代表任务已接受；其内容包含 Run ID、工作区幂等键、语义摘要、初始状态、接受时间及冻结输入引用。未提交接受记录的输入不得进入队列。幂等索引和状态快照均可重建，分别以有效接受记录和串行 Journal 为依据，不要求跨文件事务；最终结果以有效 `report.json` 为准。完整恢复顺序与 M2-1 验证关口见 [`docs/m2-implementation-design.md`](docs/m2-implementation-design.md) §§4、9、12。

---

## 4. 领域模型、文件结构与版本治理

### 4.1 核心领域对象与不可变约束

| 领域对象 | 核心职责与必需字段 | 关键约束 |
|---|---|---|
| `CheckTemplate` | 模板 ID、版本、来源（`BUILTIN / USER_PUBLIC / PLAN_LOCAL`）、领域分类、参数 Schema、执行方式（`STANDARD / PRIVILEGED`）、取证方式（`EvidenceMode`）、实现定义、语义摘要 | 已发布版本不可原地修改；非内置模板永远限定为 `STANDARD` |
| `CheckItem` | 稳定检查项 ID、名称、引用模板 ID+版本+摘要、重要性（`REQUIRED / WARNING / INFO`）、启停开关、设定参数值、显式空值列表、现场可调参数列表（`overridable_params`） | 严格隶属于单一节点角色；复制到同角色或其他角色必须生成全新的稳定检查项 ID |
| `Role` | 节点角色标识（如 `web`、`db`）、显示名、最小/最大节点数、是否允许同机混部（`allow_colocation`）、专属 `items` 列表 | 角色间检查项完全独立，互不共享引用指针 |
| `TopologyPolicy` | 允许的目标 CPU 架构列表（`allowed_arch`），配合各 `Role` 的节点数与混部约束 | 作为方案级固有属性直接核对，不混作目标主机 Goss 模板；OS 发行版与版本由具体检查项核对 |
| `Plan` | 方案 ID、名称、适用软件与版本、方案版本号、状态（`DRAFT / RELEASED / ARCHIVED`）、`topology_policy`、`roles`、内嵌模板快照、模板摘要锁、发布清单、RFC 8785 语义摘要 | `RELEASED` 单文件自包含，不可修改（修改须另存新版本；停用可标为 `ARCHIVED`），绝不含凭据 |
| `RunConfig` | 方案引用与期望摘要、任务类别（`GATE / TRIAL`）、任务范围（`FULL / PARTIAL`）、主机清单快照、角色到目标主机映射、现场参数值列表（`field_overrides`）、执行器类型、非秘密认证元数据、时间预算、幂等键 | 提交即冻结为不可变输入，绝不反写检查方案；磁盘快照不含密码、私钥、Agent 连接或可取回凭据的句柄，`credential_handle` 仅存在于任务内存上下文；不设特权模板批准列表或 `root` 直连授权开关 |
| `RunReport` | 报告 Schema 版本、任务元数据、执行器标识、运行状态（`RunStatus`）、就绪结论（`Verdict`）及原因、环境与拓扑核对结果、各目标主机阶段与单项结果（`ItemResult` + 观测证据）、待清理残留列表、安全审计摘要 | 原子写入完成后归档；前端与 CLI 直接展示报告结论，不二次重算另一套 `Verdict` |

### 4.2 检查方案单文件结构与校验基线

一份标准检查方案 YAML 文件顶层包含：`schema_version`（`readyspec.plan/v1`）、`plan_id`、`name`、`software_name`、`software_version`、`version`、`state`（`DRAFT | RELEASED | ARCHIVED`）、`description`、`topology_policy`、`roles`（每项含节点拓扑属性及 `items` 数组）、`embedded_templates`、`template_lock`、`release_manifest`、`digest`、`updated_at`。

- **草稿试跑与正式发布分离**：
  - `DRAFT` 允许保存未完成编辑；未通过静态校验的草稿禁止发起任何检查任务。
  - 通过静态校验的 `DRAFT` 可直接以 **`TrialRun`（草稿试跑）** 类别提交检查任务（内部在任务目录冻结当次草稿内容，**不在方案库生成额外的方案状态**）；`TrialRun` 的安全隔离与清理规则同正式任务，但无任何必检 `FAIL` 时其 `Verdict` 封顶为 `UNKNOWN`（严禁产出放行级 `READY`）。
  - 正式门禁检查（`GateRun`）仅接受状态为 `RELEASED` 的检查方案。
- **语义摘要（RFC 8785 JCS）**：排除 `digest`、`updated_at`、`release_manifest` 非语义字段，将 `embedded_templates` 按 `template_id@version` 稳定排序后，按 RFC 8785 规范化 JSON UTF-8 字节计算 `sha256:<64-hex>`。
- **SafeYAML 安全解析**：强制单文件大小上限 `10 MiB`；基于 YAML AST 严格拒绝重复映射键、未知非标标签、循环引用与超限锚点别名展开。

### 4.3 版本与供应链锁定

- `Plan`、`CheckTemplate`、`CustomResult`、`RunReport` 四套 JSON Schema（Draft 2020-12）独立演进并随单二进制内嵌。
- 内嵌的官方 `Goss` 二进制固定为 **`v0.4.9`**（`Apache-2.0`），编译构建期与程序启动时双重校验 `linux-amd64` 与 `linux-arm64` 镜像的锁定 SHA-256，严禁运行时联网拉取。
- 相同 `plan_id + version`（或 `template_id + version`）若语义摘要不同，导入或发布时必须明确报版本摘要冲突并拒绝静默覆盖。

---

## 5. 检查模板、现场参数值与编译契约

### 5.1 参数类型系统

支持：`string`、`boolean`、`integer`、`number`、`capacity`（明确区分十进制 `GB/MB` 与二进制 `GiB/MiB`）、`duration`、`path`、`version_constraint`、`ip_or_cidr`、`port`、`enum`、`list`（支持带 `enum_values` 的多选列表，如允许的 CPU 架构）、`map`、`regex`、`secret_ref`。参数严格区分**未设置（继承模板默认）**、**显式赋值**与**显式空值（`explicit_null_params`）**。

### 5.2 三级线性参数解析与现场参数值约束

参数生效优先级（确定性三级线性覆盖）：
**检查模板默认值 → 检查项设定值（`CheckItem.params`）→ 执行配置中的现场参数值（`FieldOverride`：主机组级 → 目标主机级）**。

- **现场可调边界**：部署人员在 `RunConfig` 中提交的 `FieldOverride` 仅允许修改方案作者在该检查项 `overridable_params` 中显式勾选开放的参数（例如数据盘挂载路径、业务监听网卡），严禁修改 Shell 脚本内容、检查模板类型、执行方式、取证方式或检查项启停/重要性。
- **同级多组覆盖冲突阻断**：若同一台目标主机同时归属多个主机组，且多个组对同一检查项同一参数给出了不同的组级 `FieldOverride`（又无单主机级覆盖消歧），解析器必须显式报错 `OVERRIDE_CONFLICT`，绝不依赖组列表遍历顺序。
- **来源可追溯**：每个解析后的参数值记录其生效来源（模板默认 / 检查项设定 / 组级现场值 / 主机级现场值）；敏感参数在报告与界面中仅展示来源与脱敏掩码。

### 5.3 编译阶段与原生 Goss 智能合并

1. **静态校验**：核对 JSON Schema、参数类型/范围/枚举约束、模板引用完整性与特权可信资格。
2. **零 SSH 静态预览**：基于 `Plan` 与 `RunConfig` 计算涉及目标主机数、候选检查项总数、所需特权执行模板、`root` 直连主机、自定义 Shell 项与主动网络探测项，不发起任何网络连接。
3. **目标主机能力发现**：SSH 连接目标主机采集 OS、内核、CPU 架构、机器指纹、登录身份、`sudo` 能力、校验工具与可执行临时目录。
4. **分执行方式独立编译**：按目标主机分别生成 `STANDARD` 与 `PRIVILEGED` 两份独立 Goss YAML 及属性溯源映射表（`PropertyOrigin`）：
   - 当同一目标主机绑定多个节点角色（同机混部）或多个检查项指向同一底层 Goss 资源键（如同一文件路径 `/data` 或同一端口 `tcp:8080`）时，编译器自动合并无矛盾属性断言，并在归一化时按属性映射精确还原回各自角色与检查项；若对同一属性提出矛盾断言（如一个要求存在、一个要求不存在），立即报 `RESOURCE_CONFLICT` 编译冲突错误，绝不依赖 YAML 键覆盖。
   - 自定义 Shell 参数统一经 POSIX 安全单引号转义后作为只读环境变量前缀 `RS_PARAM_<KEY>='...' readonly RS_PARAM_<KEY>;` 注入，严禁字符串拼接命令源码。

---

## 6. 36 项预置检查模板与环境拓扑核对契约

### 6.1 八大领域 36 项预置检查模板详细契约

每项预置 `CheckTemplate` 均具备稳定 ID、参数 Schema、执行方式、明确的取证方式（`EvidenceMode`）及六类必测场景（正常 `PASS`、正常 `FAIL`、阈值边界、环境/权限 `ERROR`、现场参数值覆盖、Goss 属性映射）。

#### 6.1.1 硬件与计算（4 项）

| ID / 名称 | 输入参数及 PASS/FAIL 断言 | 取证方式与关键观测证据 | 执行方式、平台依赖与必测异常 |
|---|---|---|---|
| **H01 CPU 架构** | 允许的规范化架构列表（`x86_64`、`aarch64` 多选）；实测属于集合判 `PASS`，否则 `FAIL` | `METADATA`：`uname -m` 原始值及规范化架构标识 | `STANDARD`；测试架构别名（`amd64`/`arm64`）、未知架构、命令输出异常；不推断微架构指令集 |
| **H02 逻辑 CPU 数** | 最少在线逻辑 CPU 核数；达到阈值判 `PASS`，否则 `FAIL` | `METADATA`：在线逻辑核数、读取接口（`/proc`/sysfs）与采集时间 | `STANDARD`；区分在线核数与配置核数；不混同 cgroup CPU 配额 |
| **H03 主机总内存** | 最低物理总内存容量；达到阈值判 `PASS`，否则 `FAIL` | `METADATA`：`/proc/meminfo` `MemTotal` 原始字节与单位换算值 | `STANDARD`；严格区分 `GB` 与 `GiB` 换算、临界值与解析异常；不混同 `MemAvailable` |
| **H04 Swap 策略** | 互斥模式（要求禁用 Swap / 要求启用 / 最低总 Swap 容量）；符合策略判 `PASS` | `EFFECTIVE_STATE`：Swap 激活状态、总容量及活动交换设备列表 | `STANDARD`；测试无 Swap、多设备累加、0 容量与读取异常；不把剩余 Swap 混作总量 |

#### 6.1.2 操作系统与内核（4 项）

| ID / 名称 | 输入参数及 PASS/FAIL 断言 | 取证方式与关键观测证据 | 执行方式、平台依赖与必测异常 |
|---|---|---|---|
| **O01 发行版与版本** | 允许的 OS ID 及版本范围；命中支持集合判 `PASS`，否则 `FAIL` | `METADATA`：`/etc/os-release` 中的 `ID`、`VERSION_ID` 与原始来源 | `STANDARD`；测试缺失字段、未知发行版与版本号前缀匹配边界 |
| **O02 内核版本** | 最低内核版本或允许区间；按数值分段比较满足判 `PASS` | `METADATA`：`uname -r` 原文、规范化主次修订号与保留的发行版后缀 | `STANDARD`；测试多位数版本号数值比较（非字典序）及非标后缀解析 |
| **O03 sysctl 内核参数** | 内核参数名及精确期望值或数值区间；不符判 `FAIL` | `EFFECTIVE_STATE`：`/proc/sys` 查询路径、期望值与运行态实测值 | `STANDARD`；区分参数不存在（`FAIL`）与权限拒绝无法读取（`ERROR`） |
| **O04 内核模块状态** | 模块名及期望加载/未加载状态；匹配判 `PASS` | `EFFECTIVE_STATE`：规范化模块名（`-`/`_` 归一）与 `/proc/modules` 状态 | `STANDARD`；测试模块未加载、名称下划线归一化，不将未出现在模块表直接臆断为内核不支持 |

#### 6.1.3 存储与文件系统（5 项）

| ID / 名称 | 输入参数及 PASS/FAIL 断言 | 取证方式与关键观测证据 | 执行方式、平台依赖与必测异常 |
|---|---|---|---|
| **S01 路径存在与类型** | 绝对路径、期望存在性、文件/目录类型与符号链接跟随策略；满足判 `PASS` | `EXISTENCE`：路径存在状态、实际节点类型与符号链接目标信息 | `STANDARD`；测试普通文件/目录/悬空符号链接/父目录无执行权限（`ERROR`） |
| **S02 路径权限与属主** | 路径、期望属主/属组及八进制权限位；匹配判 `PASS` | `METADATA`：Goss `file` 采集的实际 UID/GID、用户名/组名与模式位 | `STANDARD`；测试权限不符（`FAIL`）、路径不存在与访问被拒（`ERROR`） |
| **S03 挂载点属性** | 挂载路径、期望文件系统类型、必需/禁止挂载选项；满足判 `PASS` | `EFFECTIVE_STATE`：Goss `mount` 采集的源设备、文件系统类型与活动挂载参数 | `STANDARD`；测试 `noexec`/`ro` 检出、未独立挂载与 bind mount 场景 |
| **S04 目录可用空间** | 目标路径与最低可用容量；按非特权可用字节达到阈值判 `PASS` | `METADATA`：只读文件系统统计的实际可用字节、总容量与挂载点 | `STANDARD`；严格禁止写入测试文件，区分 `GB`/`GiB` 与保留块边界 |
| **S05 目录可用 inode** | 目标路径与最低可用 inode 数或空闲比例；达到阈值判 `PASS` | `METADATA`：可用 inode、总 inode、空闲百分比与文件系统类型 | `STANDARD`；对不报告固定 inode 总数的文件系统（总数为 0）判 `UNSUPPORTED`/`ERROR` 而非误判 `0` |

#### 6.1.4 网络与通信（6 项）

| ID / 名称 | 输入参数及 PASS/FAIL 断言 | 取证方式与关键观测证据 | 执行方式、平台依赖与必测异常 |
|---|---|---|---|
| **N01 网络接口与地址** | 网卡名、期望 UP 状态及 IPv4/IPv6 CIDR 要求；匹配判 `PASS` | `EFFECTIVE_STATE`：Goss `interface` 采集的接口状态与绑定地址集合 | `STANDARD`；测试多 IP 绑定、IPv6 地址与网卡不存在场景 |
| **N02 本地端口占用状态** | `tcp`/`udp` 协议、绑定地址、端口号与期望监听/空闲状态；匹配判 `PASS` | `EFFECTIVE_STATE`：Goss `port` 采集的协议、监听状态与绑定套接字信息 | `STANDARD`；测试双栈通配监听（`0.0.0.0`/`[::]`）及部署前端口冲突检出 |
| **N03 远端 TCP 连通性** | 目标 `host:port`、超时与期望可达性；符合判 `PASS` | `ACTIVE_PROBE`：Goss `addr` 探测连接结果、耗时与拒绝/超时原因 | `STANDARD`；显式声明主动网络探测，仅限 TCP，区分连接拒绝与 DNS 解析错误 |
| **N04 DNS 域名解析** | 域名、记录类型（`A`/`AAAA`/`CNAME` 等）与期望结果；匹配判 `PASS` | `ACTIVE_PROBE`：Goss `dns` 采集的解析记录集合、耗时与解析器视角 | `STANDARD`；区分 `NXDOMAIN`、记录不匹配（`FAIL`）与无解析器/超时异常 |
| **N05 HTTP(S) 端点探测** | URL、`GET`/`HEAD` 方法、期望状态码/头/正文片段及 TLS 校验策略；匹配判 `PASS` | `ACTIVE_PROBE`：Goss `http` 状态码、TLS 校验概要与受限响应摘要 | `STANDARD`；禁止修改类 HTTP 方法，默认校验证书链，响应读取硬上限 `1 MiB` |
| **N06 目标路由决策** | 目标 IP、可选源地址及期望出口网卡/下一跳；匹配判 `PASS` | `EFFECTIVE_STATE`：`ip route get` 实测选路出口接口与网关地址 | `STANDARD`；缺少 `ip` 命令时判 `ERROR` 而非业务 `FAIL`，支持策略路由视角 |

#### 6.1.5 系统资源与基础服务（5 项）

| ID / 名称 | 输入参数及 PASS/FAIL 断言 | 取证方式与关键观测证据 | 执行方式、平台依赖与必测异常 |
|---|---|---|---|
| **R01 系统服务状态** | 服务名及期望运行（`active`）/自启（`enabled`）要求；满足判 `PASS` | `EFFECTIVE_STATE`：systemd Unit 活动状态与启用状态独立观测值 | `STANDARD`；区分 `static`/`indirect` 与普通 `enabled`，测试服务未安装与停止态 |
| **R02 进程存在与数量** | 进程匹配名与期望存活数量范围；命中数在区间内判 `PASS` | `EFFECTIVE_STATE`：Goss `process` 或受控进程表匹配计数（脱敏命令行） | `STANDARD`；避免在报告中泄露完整命令行口令参数，测试 0 进程与超上限场景 |
| **R03 系统平均负载** | 1/5/15 分钟窗口选择与负载上限；不超阈值判 `PASS` | `METADATA`：`/proc/loadavg` 对应窗口实测值与阈值 | `STANDARD`；明确表达运行队列平均负载而非瞬时 CPU 使用率 |
| **R04 进程或服务资源限制** | 指定 PID 或 systemd Unit、限制项（如 `NOFILE`/`NPROC`）及最低阈值；满足判 `PASS` | `EFFECTIVE_STATE`（读 `/proc/<pid>/limits`）或 `CONFIG`（读 `systemctl show`） | `STANDARD`；**反向必测：严禁读取当前 SSH 会话 `ulimit` 冒充目标进程/服务限制** |
| **R05 时间同步状态** | 期望时钟已同步及允许的时间服务类型；同步标志就绪判 `PASS` | `EFFECTIVE_STATE`：`timedatectl show` / `chronyc` 同步状态与活动服务名 | `STANDARD`；适配 `chrony`、`systemd-timesyncd` 与 `ntpd`，缺查询工具报 `ERROR` |

#### 6.1.6 安全与访问（4 项）

| ID / 名称 | 输入参数及 PASS/FAIL 断言 | 取证方式与关键观测证据 | 执行方式、平台依赖与必测异常 |
|---|---|---|---|
| **A01 系统用户属性** | 用户名、存在性、UID/GID 与登录 Shell；匹配判 `PASS` | `METADATA`：Goss `user` 采集的用户条目字段 | `STANDARD`；区分用户不存在（按断言判 `PASS`/`FAIL`）与 NSS 查询故障（`ERROR`） |
| **A02 用户组与成员** | 组名、GID 及必需包含的成员列表；匹配判 `PASS` | `METADATA`：Goss `group` 与组成员查询结果 | `STANDARD`；同时核对主组与附加组成员关系 |
| **A03 SSH 服务端生效配置** | 配置项名、可选 `Match` 上下文及期望生效值；匹配判 `PASS` | `EFFECTIVE_STATE`：`/usr/sbin/sshd -T [-C ...]` 输出的解析后生效键值 | **内置 `PRIVILEGED`**；**反向必测：凭据或提权权限不足时前置拒绝；通过校验后实际执行仍因权限变化等原因失败时记 `ERROR`，严禁降级 `grep /etc/ssh/sshd_config` 静态文本** |
| **A04 SELinux / AppArmor 状态** | 安全框架类型与期望运行模式；匹配判 `PASS` | `EFFECTIVE_STATE`：安全模块存在性及当前有效模式（如 `enforcing`/`disabled`） | `STANDARD`；按发行版适配查询接口，区分安全模块未启用与查询工具缺失 |

#### 6.1.7 软件依赖与运行时（5 项）

| ID / 名称 | 输入参数及 PASS/FAIL 断言 | 取证方式与关键观测证据 | 执行方式、平台依赖与必测异常 |
|---|---|---|---|
| **D01 系统软件包** | 包名、安装存在性及可选版本区间；满足判 `PASS` | `METADATA`：`dpkg-query` / `rpm` 采集的包状态、完整版本（含 epoch/release） | `STANDARD`；适配 deb/rpm 体系，区分未安装（`FAIL`）与包数据库锁/损坏（`ERROR`） |
| **D02 可执行命令检查** | 命令名或绝对路径及可执行要求；存在且具执行位判 `PASS` | `EXISTENCE`：解析后的实际文件路径与权限模式位 | `STANDARD`；仅通过文件系统属性/`command -v` 检查，绝不盲目运行目标程序 |
| **D03 语言运行时版本** | 受支持运行时类型（Java/Python/Node 等）、路径与版本约束；满足判 `PASS` | `METADATA`：受控 `--version` 原文与提取的规范化版本号 | `STANDARD`；仅限预置白名单安全版本参数，区分未安装与非标版本串解析失败 |
| **D04 静态二进制依赖（ELF）** | 目标 ELF 路径、期望架构与必需 `NEEDED` 动态库列表；全部满足判 `PASS` | `STATIC_BINARY`：`readelf`/`objdump` 提取的 ELF 头架构、`NEEDED` 列表与 `ldconfig -p` 映射 | `STANDARD`；**反向必测：严禁调用 `ldd` 或直接运行目标二进制**；目标缺 `readelf`/`objdump` 时如实报 `ERROR` |
| **D05 环境与配置文件键值** | 指定配置文件路径/格式、键名与期望匹配规则；满足判 `PASS` | `CONFIG`：受控文件键路径与脱敏后的实测值摘要 | `STANDARD`；敏感键值强制掩码脱敏，不把当前 SSH 环境变量当成业务服务配置 |

#### 6.1.8 系统健康与生命周期（3 项）

| ID / 名称 | 输入参数及 PASS/FAIL 断言 | 取证方式与关键观测证据 | 执行方式、平台依赖与必测异常 |
|---|---|---|---|
| **L01 系统运行时长（Uptime）** | 最短/最长运行秒数或时长区间；落在区间内判 `PASS` | `METADATA`：`/proc/uptime` 实测秒数与采集时间戳 | `STANDARD`；测试刚重启未达最短稳定期与超长未重启阈值边界 |
| **L02 内核 OOM 与致命事件历史** | 回溯时间窗口、允许最大事件数（默认 0）与 `allow_since_boot` 选项；覆盖充分且未超限判 `PASS` | `LOG_HISTORY`：命中事件计数（单次 OOM 聚合去重）、日志源及**可读最早日志时间戳覆盖证明** | `STANDARD`（及可选受控 `PRIVILEGED` 读日志变体）；**反向必测：发现超限事件可直接判 `FAIL`，但若 0 事件且最早可读日志晚于要求窗口起点（且未开启 `allow_since_boot`）或无权读取，必须判 `ERROR`，严禁误判 `PASS`** |
| **L03 系统服务异常重启历史** | 服务名、回溯时间窗口与允许最大异常失败/重启次数；覆盖充分且未超限判 `PASS` | `LOG_HISTORY`：`journalctl` 采集的异常退出/自动重启计数与日志窗口覆盖区间 | `STANDARD`；与 `L02` 相同，日志轮转缺口或权限受限导致窗口覆盖不足时严禁判 `PASS` |

### 6.2 方案级环境与拓扑要求（`TopologyPolicy`）自动核对规则

每次检查任务在目标主机采集与执行结束后，由控制面自动执行以下三项方案级核对并记入 `RunReport.topology_results`：
1. **目标 CPU 架构核对**：核对每台目标主机实测的 `arch` 是否落在 `Plan.topology_policy.allowed_arch` 允许集合内；实测不符判 `FAIL`，未采集到架构信息判 `ERROR`。方案级策略不再核对操作系统发行版或版本；需要此类约束时使用 `O01 发行版与版本` 检查项。
2. **节点角色机器数、混部与实机唯一性核对**：
   - 在 `FullRun` 下核对各节点角色分配的目标主机数是否满足 `[min_nodes, max_nodes]`，以及同机承担多角色时是否违反任一角色的 `allow_colocation=false`（违反判 `FAIL`）；在 `PartialRun` 下固定标记为未覆盖完整拓扑（`ERROR`），从机制上阻止局部复测产生全局放行信号。
   - 比对各目标主机实测动态指纹 `(machine_id, boot_id, ssh_host_key_fp)`，若发现不同主机名指向同一物理/虚拟机，报实机身份重复冲突（`ERROR`）。
3. **必检项覆盖完整性核对**：确认绑定节点角色下全部启用的 `REQUIRED` 检查项均已产生对应的目标主机结果记录，防止漏跑静默放行。

---

## 7. 自定义 Shell 与结果协议

### 7.1 编排、提升与权限红线

- 方案作者可在方案工作台直接创建作用域限于当前方案的局部自定义 Shell 模板（`PLAN_LOCAL`），通过检查项引用；亦可经静态参数化与安全审查后将其提升为用户公共检查模板（`USER_PUBLIC`，不自动改动原方案引用）。
- **权限铁律**：所有用户自定义 Shell 模板永远限定为 `STANDARD` 普通执行，**严禁将非 `root` 连接通过 `sudo` 提权运行自定义 Shell**。当目标主机 SSH 登录用户本身为 `root`（或连接后确认实际 UID 为 `0`）时，允许随连接身份运行，无须额外授权；Web UI 与 CLI 必须提示该身份下包括自定义 Shell 在内的检查将以 root 权限执行，并在证据中强制记录 `high_risk_root_audit=true`。不再使用 `approve_root_ssh` 或 `ROOT_SSH_UNAPPROVED`。

### 7.2 基础断言模式与离线模拟沙盘

- 支持断言：期望退出码、stdout/stderr 包含/不包含/精确相等/正则匹配、以及单数值提取比较（`==, !=, >=, >, <=, <, range`）。命令不存在、超时或单数值无法解析为合法有限浮点数时判 `ERROR`，不混同于正常执行完成后的业务断言 `FAIL`。
- 工作台提供纯本地**断言模拟自测器**：用户输入模拟退出码与模拟 stdout/stderr 文本，后端直接调用同一套断言求值函数返回判定预览，全程不发起 SSH、不在本机执行 Shell 脚本。

### 7.3 结构化结果协议（`readyspec.custom_result/v1`，条件能力）

- 当自定义模板声明使用结构化结果协议时，stdout 仅输出单行有界 UTF-8 JSON（上限 `16 KiB`），包含协议版本、业务判定（仅限 `PASS / FAIL / ERROR`）、摘要及可选 `expected`/`observed`；自定义脚本不得输出 `UNSUPPORTED`/`SKIP` 或篡改主机身份、执行方式与全局 `Verdict`。若 M0 实测原装 Goss JSON 无法无损回传该记录，则冻结禁用该模式，不影响基础断言模式交付。

---

## 8. Run Manager、远端执行、失败与远端残留清理

### 8.1 任务队列与并发控制

- **单活动检查任务与 FIFO 队列**：单个工作区同一时刻最多运行 1 个活动检查任务（`Run`），等待队列最多容纳 10 个排队任务；超出队列上限时拒绝提交并返回 `WORKSPACE_BUSY`（CLI 退出码 `16`）。
- **主机并发与执行顺序**：单个检查任务默认最多并发连接 5 台目标主机；单台目标主机内 Goss 默认并发数为 10；同一台目标主机必须先完成 `STANDARD` 普通执行，再串行进入获授权的 `PRIVILEGED` 特权执行，严禁二者并发共享远端工作目录。
- **幂等提交**：`RunConfig` 携带稳定 `idempotency_key`；工作区提交锁串行化查重、队列容量预留和接受。相同幂等键与相同配置语义摘要重复提交时返回原任务 ID，相同幂等键但负载不同时立即拒绝并报冲突。重复提交不能替换原任务凭据或引起第二次执行；新提交的未使用凭据立即销毁。摘要包含执行器、认证方式、选定公钥身份等非秘密语义，排除秘密和可取回凭据的句柄。
- **静态校验与执行范围**：提交校验复用清单形状、绑定引用、参数覆盖和编译规则，不把角色数量或混部核对提前改为 `PLAN_ERROR`；最终拓扑遵循 §6.2。局部复测不得仅因未满足完整角色节点数量而拒绝提交。仅包含启用检查项的方式属于计划执行模式；空工件不投递、不执行，不要求 sudo 或空结果作为完成条件。

### 8.2 原生 SSH 九阶段闭环与内嵌 Goss 投递

**执行前凭据与权限校验**：工具依据选定可信内置检查模板的权限声明自动计算每台主机的权限需求，无须用户判断或批准。零 SSH 静态预览只校验本地可确定的认证配置，不宣称远端凭据已验证；缺少可用认证方式等本地可确定的问题在提交前拒绝。实际执行入口先对本次全部目标主机进行 SSH 认证、实际 UID 与所需提权能力校验，通过后才进入 Goss 投递和检查执行；任何主机明确缺少必要凭据、认证失败或所需 sudo 权限不足，拒绝整次检查执行，返回 `AUTH_ERROR`（CLI `15`），不执行其他主机的检查项，也不生成虚构的单项结果。记录失败主机、阶段与脱敏原因，拒绝后销毁本次短期凭据；不得记录凭据内容。网络不可达或主机身份核验失败不得误报为缺少凭据，仍遵循既有连接故障处理规则。

SSH 密钥、系统 Agent 或密码均可满足 SSH 认证；实际 UID 为 `0` 时无需 sudo 凭据，普通账户无特权检查时无需 sudo，免密 sudo 满足所需权限时也无需额外密码。sudo 校验应针对将要执行的受控命令及策略，不得仅凭账户属于某个用户组或 `sudo -v` 成功推断命令可执行，也不得执行检查脚本来试探权限；校验不得依赖交互提示或已有 sudo 缓存票据。Web UI 提供缺失凭据的补充入口，CLI 立即拒绝、不得等待输入。前置校验不代替执行时权限检查；通过校验后发生的权限变化或执行故障仍记对应检查项 `ERROR`。

**sudo 无法核验与明确拒绝分离**：仅当存在启用特权项且实际 UID 非 `0` 时要求 sudo 预检。已证明允许才通过；明确拒绝或必要凭据缺失按 `AUTH_ERROR / 15` 拒绝整次任务。无法证明允许、也无法判定明确拒绝（例如不支持精确策略查询或结果无法可靠解析）时，同样拒绝整次任务，全部目标零 Goss 投递、零检查执行，不虚构 ItemResult；成功归档报告时记 `FAILED` 及核验失败原因，无其他明确必检/拓扑 `FAIL` 或更高优先级错误则为 `UNKNOWN / 11`。无法形成有效报告的控制面致命故障仍适用 `13`。不得降级为仅执行 STANDARD。预检不得留下持久 sudo 时间戳；M2-1 须验证最终受控命令的精确查询可行性，不能把上述行为决定当作技术验证已通过。

全主机屏障由 RunManager 从模拟器阶段开始统一执行。重复实机身份使用 §6.2 三元组：阻止冲突主机执行，其他合格主机在屏障后继续；身份冲突贡献拓扑 `ERROR`，不得覆盖已有明确必检或拓扑 `FAIL` 的 `NOT_READY` 优先级。

管理端通过 `golang.org/x/crypto/ssh` 直连 Linux 目标主机（Windows 与 Linux 管理端行为完全一致）：

1. **连接与主机身份核验（`CONNECTING`）**：默认开启 `known_hosts` 主机公钥校验；支持私钥文件、系统 SSH Agent（Linux `SSH_AUTH_SOCK` / Windows `\\.\pipe\openssh-ssh-agent`）及来自内存保险箱的短期密码。仅在尚未进入执行阶段的瞬时连接/传输故障允许有限重试。
2. **能力发现（`DISCOVERING`）**：单次 `/bin/sh` 采集发行版、内核、CPU 架构（`x86_64` / `aarch64`）、`machine-id`、`boot_id`、登录 UID、`sudo` 能力、SHA-256 校验工具（`sha256sum` → `openssl dgst -sha256`）及非 `noexec` 可执行临时目录（`STANDARD` 候选：`显式指定 → /tmp → /var/tmp → $HOME/.cache`；`PRIVILEGED` 候选：`显式指定 → /run → /var/tmp → /tmp`）。
3. **分方式编译与残留预登记（`PREPARING`）**：生成目标主机专属 Goss 工件；在远端创建目录前先在本地持久化 `CleanupItem(PENDING)`（记录 `remote_dir`、128-bit `owner_token`、`machine_id`、`boot_id`）。
4. **流式投递与远端摘要验真**：远端执行 `umask 0077 && mkdir <unique_dir>`（严禁 `-p` 防符号链接抢占），写入 `.readyspec_owner` 归属令牌，按发现的 CPU 架构从单二进制内嵌镜像流式解压写入原装 `Goss v0.4.9` 与 `standard-goss.yaml`，并在远端校验 SHA-256 一致（缺少校验工具或摘要不符立即 fail closed 中止）。
5. **普通执行（`RUNNING_STANDARD`）**：若该方式有启用检查项，先按 `(run_id, host_id, execution_mode)` 追加并同步“远端可能已开始执行”标记，成功后才能发送执行命令（一旦置位，任何断线或超时均严禁自动重跑）；运行 `goss -g standard-goss.yaml validate --format json --retry-timeout 0s --no-color --max-concurrent 10`，有界读取输出（上限 `16 MiB`）。
6. **受控特权执行（`RUNNING_PRIVILEGED`）**：仅对本地可信内置库摘要精确匹配的 `PRIVILEGED` 模板执行，依据模板声明的权限需求自动选择执行方式，无须额外批准。实际 UID 为 `0` 时直接执行特权闭环，无须调用 sudo；其他账户按需使用 sudo。执行前提示受影响主机、检查项及 root/sudo 执行方式；必要凭据及权限不足按前置校验拒绝，通过校验后的执行故障记对应检查项 `ERROR`，不得伪装为 `PASS` 或降级证据。非 `root` 登录下采用单次 `sudo -S -p '' /bin/sh -c` 原子闭环：原子创建 `root:root 0700` 隔离目录 → 独立拷入本地已上传的 `goss` 字节与特权规则 → **`root` 态重新计算校验 SHA-256（防普通用户态 TOCTOU 篡改）** → `env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C HOME=/root` 清洗环境与绝对路径运行 → 正常态当场自清理特权目录 → 末尾强制 `sudo -k` 清除缓存票据。root 直连同样保留隔离、摘要校验、环境清洗与清理，免除 sudo 调用及票据处理。
   - PRIVILEGED 仅在有启用特权项时执行；发送受控命令前必须追加并同步该主机 **独立的 PRIVILEGED 执行标记**，写入失败不发命令，标记后的歧义结果禁止重跑。空 STANDARD 无需执行。正常及可控失败路径均尝试清理和 `sudo -k`，无法确认时保留清理记录，不宣称成功。
7. **结果回收（`COLLECTING`）**、**归属校验清理（`CLEANING`）** 与 **终态归档（`FINISHED`）**：核对远端 `machine_id`、`boot_id` 与 `.readyspec_owner` 完全一致且不跟随符号链接后删除临时工作目录，将 `CleanupItem` 更新为 `CLEAN`。

### 8.3 协作式取消、异常重启与远端残留清理（`RemoteCleanup`）

- **取消语义**：取消接受与终态归档选择在同一个 Run 串行入口排序；接受取消须追加并同步 Journal，立即停止新主机分派，进入 `CANCELLING` 并尝试协作终止。取消先持久化时最终归档为 `CANCELLED`，不要求终止信号送达或派生进程退出已确认；不确定性保留为 `CleanupItem(PENDING/UNKNOWN)`。已选定终态归档流程或已有有效报告时不再接受新取消，不能改写终态；崩溃前未持久化的取消不算已接受。
- **崩溃重启恢复**：优先校验已提交的 `report.json`，有效则按报告补齐终态和最终事件，不重算 Verdict。无有效报告但 Journal 有已接受取消时归档为 `CANCELLED`；其余已接受未完成任务（含排队任务）归档为 `FAILED / CONTROLLER_INTERRUPTED`，保留证据和清理记录。**任何恢复路径都绝不自动重新调度旧任务**。
- **待清理残留处置**：操作者在 Web UI「模板与清理」或 CLI `readyspec cleanup resolve` 中处置未结案 `CleanupItem` 时，重新提供操作级短期凭据，不依赖已销毁的 Run 保险箱；重新核验 SSH 主机身份、机器码、开机标识、`.readyspec_owner` 归属令牌及非符号链接路径归属，并探查 `/proc` 关联进程。若检测到存活进程，须经操作者显式确认后方可终止或删除并置为 `CLEAN`；CLI 使用显式非交互确认。若无需远程清理，必须填写审计原因标记为 **`IGNORED`（人工忽略）**，不伪装成 `CLEAN`，永久保留审计。操作结束销毁其凭据。

---

## 9. 安全与信任边界

- **自包含快照不传递特权信任**：导入自包含检查方案时，任何声明为 `PRIVILEGED` 的模板必须与当前单二进制内置可信注册表的 ID、版本与语义摘要完全一致，单靠导入文件自称内置来源一律拒绝特权执行。
- **短期凭据纯内存即焚**：SSH/Sudo 密码、私钥材料及 Agent 连接仅由任务内存上下文/`EphemeralVault` 持有，`credential_handle` 不进入磁盘 RunConfig。终态归档、排队取消/过期、提交失败及正常服务关闭时销毁任务持有的材料/连接；重复提交立即销毁新提供但未使用的凭据。**秘密、Agent 连接和可取回凭据的句柄绝不写入检查方案、执行配置快照、检查报告、事件或日志**。尽量覆写自有缓冲区，但 Go 运行时及 SSH 库可能保留副本，不承诺完整进程内存擦除；异常进程终止不依赖 `defer` 必然运行。
- **Web 监听与跨站防护**：
  - 默认仅监听 `127.0.0.1`；所有 API 强制校验会话凭证、CSRF 令牌及 `Origin` 头，阻断恶意网页跨站请求本地端口与 DNS Rebinding。
  - 回环监听时 `Host` 头严格校验（监听地址、显式白名单或 `localhost`）；显式非回环监听面向内网部署，只校验请求端口与监听端口一致，不再按主机名拦截（见 Web 凭据与内网明文 HTTP 部署决策）。
  - 非回环地址监听强制要求高熵访问令牌鉴权（常量时间比对），支持 TLS 证书配置，并对非 TLS 远程连接在密码输入处展示显著明文风险警示。
- **跨平台本地存储隔离**：Linux 强制目录 `0700`、文件 `0600`；Windows 强制在 `%LOCALAPPDATA%` 下收紧用户专属 DACL，防止同机其他低权账户读取运行记录或伪造 `runtime/server.json`。

---

## 10. Web UI、CLI、CI/CD 与受控本地 API

### 10.1 Web UI 业务视图与交互规范

Web UI 的视觉气质、深浅色语义令牌、字体与等宽数字规则、间距几何、应用壳层（左侧可折叠导航栏 + 顶栏 + 主工作区 + 右侧上下文抽屉）、基础组件状态、响应式断点与线性图标规范**统一以 [`DESIGN.md`](./DESIGN.md)（含其顶部的 ReadySpec 适用与排除章节声明）为唯一事实来源**，本节不再重复抄写视觉令牌数值，仅规定 ReadySpec 的四个一级业务视图与交互行为：

1. **总览（Overview）**：
   - 首屏并列展示**部署工程师快捷入口**（导入检查方案、立即发起检查任务、重跑最近配置）与**方案作者快捷入口**（新建检查方案草稿、打开方案工作台、浏览检查模板），下方以紧凑表格列出最近检查任务（明确展示任务类别 `GateRun/TrialRun`、执行范围 `FullRun/PartialRun`、执行器 `native_ssh/mock_simulator`、任务状态 `RunStatus` 与就绪结论 `Verdict`）。
2. **方案工作台（Plans & Workbench）**：
   - 顶栏展示当前检查方案名称、版本、状态徽章（`DRAFT / RELEASED / ARCHIVED`）与语义摘要，提供静态校验、发起**草稿试跑（`TrialRun`）**、正式发布（`RELEASED`）、停用（`ARCHIVED`）与导出单文件 YAML 动作。
   - 主体采用三栏协同工作区（窄屏按 `DESIGN.md` 断点折叠为抽屉）：
     - **左栏**：方案级**环境与拓扑要求（`TopologyPolicy`）**（目标 CPU 架构多选）及**节点角色（`Role`）**列表（配置最小/最大节点数与同机混部开关）；OS 发行版与版本要求通过具体检查项配置。
     - **中栏**：当前节点角色的专属**检查项（`CheckItem`）**表格（支持过滤、批量启停/修改重要性、复制到同角色或其他角色生成全新独立 ID、删除与撤销；提供从检查模板库批量添加与创建方案局部自定义 Shell 入口）。
     - **右栏**：选中检查项的强类型参数编辑器（枚举下拉、列表枚举多选框、带明确单位的数值/容量控件、显式空值切换、**现场可调参数（`OverridableParam`）**开关）；点击静态校验错误可直接聚焦定位到对应节点角色、检查项与参数字段；自定义 Shell 编辑器内置纯本地**断言模拟自测器**。
3. **检查与报告（Runs & Reports）**：
    - **四步执行向导**：`① 选定或上传/粘贴检查方案 → ② 录入主机清单（支持 Ansible INI/YAML 编辑与快速主机表切换）、配置 SSH 认证（私钥/Agent/无回显短期内存密码）及选择真实 SSH 或离线仿真执行器 → ③ 节点角色自动建议分配与填写现场参数值（仅开放 OverridableParam 标记字段） → ④ 零 SSH 静态预览单与特权执行 / root 直连提示`。权限提示展示账户、受影响检查项与预期执行方式，无额外授权勾选；静态阶段无法确认的实际 UID 或 sudo 能力在连接后核实并补充提示与审计。
   - **实时执行看板**：通过 SSE 实时渲染每台目标主机的 9 阶段进度、阶段耗时与协作式取消操作。
   - **检查报告视图**：显著展示最终**就绪结论（`Verdict`）**及原因（非正式门禁任务固定展示醒目范围提示条）；支持**按目标主机视角**与**按检查项视角**切换、“仅看异常”持久过滤、点击行在**右侧上下文抽屉**查看完整观测证据（期望值、实测值、取证方式 `EvidenceMode`、参数生效来源、执行身份与采集时间），支持一键发起**异常主机局部复测（`PartialRun`）**、**全量检查（`FullRun`）**与下载 JSON 检查报告。
4. **模板与清理（Templates & Cleanup）**：
   - **检查模板浏览**：按八大领域中英文检索 36 项预置模板与用户公共模板，点击在右侧抽屉查看完整参数契约与取证方式，支持将方案局部 Shell 模板提升为公共模板。
   - **远端残留清理（`RemoteCleanup`）**：表格呈现未结案的**待清理残留（`CleanupItem`）**，支持核对归属信息执行安全清理（发现存活进程时要求显式二次确认）或填写原因标记为**人工忽略（`IGNORED`）**。

### 10.2 受控 REST + SSE API 契约（`/api/v1`）

| 能力分组 | 核心端点 | 行为与安全约束 |
|---|---|---|
| 系统与鉴权 | `GET /system/info`、`POST /auth/session` | 交换会话 Cookie 与 CSRF 令牌；校验 `Origin`/`Host` |
| 检查模板 | `GET /templates`、`POST /templates/promote`、`POST /templates/shell-selftest` | 查询内置与公共模板；纯本地运行 Shell 断言模拟自测 |
| 检查方案 | `GET\|POST /plans`、`GET\|PUT\|DELETE /plans/{id}`、`POST /plans/{id}/validate`、`POST /plans/{id}/release`、`POST /plans/{id}/archive`、`POST /plans/import`、`GET /plans/{id}/export` | 静态校验、不可变发布、停用归档与自包含 YAML 导入导出（零 SSH）。`POST /plans` 保存可编辑的未完成 `DRAFT`，`draft_key` 稳定标识同一记录；保存不声明校验通过或返回有效摘要。当前校验入口为 `POST /plans/validate`，导出前严格校验，发布与执行仍须使用完整校验通过的文档 |
| 主机清单与任务 | `POST /inventory/parse`、`POST /runs/preview`、`POST /runs`、`GET /runs`、`GET /runs/{id}`、`POST /runs/{id}/cancel` | 密码字段当场转入内存保险箱；幂等提交；零 SSH 预览 |
| 实时事件流 | `GET /runs/{id}/events?after_seq=N` | 标准 SSE 流：首帧推送当前状态快照与最新单调序号，后续增量推送阶段事件，支持 `Last-Event-ID` 断线续传 |
| 远端残留清理 | `GET /cleanups`、`POST /cleanups/{id}/resolve` | 验证机器码/开机标识/归属令牌后清理或记原因置为 `IGNORED` |

### 10.3 CLI 命令树与确定性门禁退出码推导规则

单二进制命令结构：
- `readyspec`（无子命令）或 `readyspec serve`：启动内置 `RunManager` 与 Web UI 服务。
- `readyspec plan list | validate | release | archive | import | export`
- `readyspec template list`
- `readyspec run preview | submit | status | cancel`
- `readyspec report list | show | export`
- `readyspec cleanup list | resolve`

CI/CD 调用必须全非交互（缺少必填参数直接按对应退出码失败退出，绝不阻塞等待人工输入）。root/sudo 执行提示输出到 stderr，不要求确认或额外批准参数；缺少必要 SSH/sudo 凭据、认证失败或所需提权权限不足时拒绝执行并返回 `15 AUTH_ERROR`，不得等待交互输入。

#### CLI 退出码优先级推导规则（严格按序号 1 → 8 顺序匹配）

以下门禁推导规则用于 `run submit` 及其受控 API 委托结果。`plan validate`、`serve` 等其他命令的退出码 `0` 仅表示自身操作成功，不得解释为目标环境已就绪。

CLI 退出码综合前置校验、控制面状态、`RunStatus` 与 `Verdict` 推导，**不由单一 `Verdict` 字段孤立映射**，严格按以下先后优先级判定（命中即返回，消除任何组合场景歧义）：

| 判定优先级 | 退出码 | 标识符 | 精确触发条件 |
| :---: | :---: | :--- | :--- |
| 1 | `15` | `AUTH_ERROR` | 方案实际摘要与 `--expected-plan-digest` 不符、本地受控 API 鉴权被拒，或执行前校验发现必要 SSH/sudo 凭据缺失、认证失败或所需提权权限不足；root 直连及可信内置特权检查无需额外批准，不因缺少批准参数返回此码 |
| 2 | `16` | `WORKSPACE_BUSY` | 传入 `--no-wait` 时工作区排他锁已被占用，或活动 `RunManager` 的等待队列已满（超出 10 个上限） |
| 3 | `12` | `PLAN_ERROR` | 检查方案 YAML 语法/重复键非法、JSON Schema 校验失败、主机清单解析冲突、现场参数值越权/多组冲突、或 Goss 资源属性编译矛盾 |
| 4 | `13` | `INFRA_ERROR` | 本地工作区读写失败、内嵌 Goss 镜像完整性自检失败等导致无法形成有效检查报告的控制面致命故障 |
| 5 | `14` | `CANCELLED` | 检查任务被用户或系统信号主动取消（`RunStatus == CANCELLED`；**优先于报告中的 `Verdict`**，即使报告 `Verdict` 记为 `UNKNOWN` 也固定返回 `14`） |
| 6 | `10` | `NOT_READY` | 任务未取消，且 `Verdict == NOT_READY`（**无论该次任务是 `FullRun`、`PartialRun`、`TrialRun` 还是 `mock_simulator`，只要实测发现至少一项必检检查项或 `TopologyPolicy` 明确 `FAIL`，一律返回 `10`**） |
| 7 | `11` | `UNKNOWN` | 任务未取消且无必检 `FAIL`，`Verdict == UNKNOWN`（存在必检 `ERROR`/`UNSUPPORTED`，无启用必检项，或因属于 `TrialRun` / `PartialRun` / `mock_simulator` 而被禁止放行） |
| 8 | `0` | `READY` | **唯一自动放行信号**：`RELEASED` 方案 + `GateRun` + `FullRun` + 真实 `native_ssh` 执行正常完成（`RunStatus == COMPLETED`）且 `Verdict == READY` |

---

## 11. 检查任务结果、观测证据与检查报告

### 11.1 三套状态正交独立与 `Verdict` 推导契约

ReadySpec 严格保持**三套状态模型相互独立**，绝不混用：
1. **任务状态（`RunStatus`）**：描述调度与执行过程（`PENDING / RUNNING / CANCELLING / COMPLETED / PARTIAL / FAILED / CANCELLED`）。
2. **单项结果（`ItemResult`）**：描述单条检查项或单条 `TopologyPolicy` 核对在目标上的业务结论：
   - `PASS`：采集到满足声明 `EvidenceMode` 的完整证据且符合断言。
   - `FAIL`：采集到充分证据明确证实目标环境不符合断言。
   - `ERROR`：连接中断、权限不足、缺探测工具、日志时间窗口覆盖不足或输出异常，无法确认断言是否成立。
   - `UNSUPPORTED`：已确认目标平台缺乏该检查机制且无适用变体。
   - `SKIP`：方案中显式禁用的检查项（不等于 `PASS`）。
3. **就绪结论（`Verdict`）**：描述整次检查任务对待部署软件给出的门禁判定，按以下确定性顺序计算：
   - **规则 1（明确不满足优先）**：只要存在至少一项启用的 `REQUIRED` 检查项或 `TopologyPolicy` 核对结果为 `FAIL`（无论当前是 `GateRun`、`TrialRun`、`FullRun`、`PartialRun` 或仿真模式，但已取消任务除外：若 `RunStatus == CANCELLED` 且尚未发现明确 `FAIL` 则归入规则 2，若取消前已实锤测出必检 `FAIL` 仍记 `NOT_READY`；注：CLI 退出码对 `CANCELLED` 任务由 §10.3 第 5 步优先返回 `14`） → **`NOT_READY`**，并同时保留所有并存的 `ERROR` 项供排障。
   - **规则 2（证据不足或非门禁范围封顶）**：不满足规则 1（无明确必检 `FAIL`），但存在任一启用 `REQUIRED` 项或 `TopologyPolicy` 为 `ERROR`/`UNSUPPORTED`、任务未正常完成（`RunStatus != COMPLETED`）、方案中没有任何启用的 `REQUIRED` 检查项、或当次任务属于 `TrialRun` / `PartialRun` / `mock_simulator` → **`UNKNOWN`**。
   - **规则 3（严格门禁放行）**：`Plan.state == RELEASED`、`RunConfig.run_mode == GATE`、`RunConfig.run_scope == FULL`、`executor_backend == native_ssh`、`RunStatus == COMPLETED`，存在至少一项启用的 `REQUIRED` 检查项，且 `TopologyPolicy` 与全部启用的 `REQUIRED` 检查项均为 `PASS`（非必检的 `WARNING`/`INFO` 项若为 `FAIL` 仅在报告告警，不阻断 `READY`） → **`READY`**。

终态进入条件如下，`PartialRun` 范围与 `PARTIAL` 状态无关：

| RunStatus | 进入条件 |
|---|---|
| `COMPLETED` | 全部范围内目标主机及计划执行模式均完成并取得有效规范化结果；业务 `FAIL` 或单项取证 `ERROR` 本身不表示调度未完成。 |
| `PARTIAL` | 至少一个计划模式有有效规范化结果，但其他主机/模式因超时、断线、输出无效或其他执行故障未完成。 |
| `FAILED` | 全局拒绝、控制面致命失败，或没有任何计划模式产生有效规范化结果；保留诊断及清理记录。 |
| `CANCELLED` | 取消已持久化接受，停止新分派并尽力终止，归档已有证据；终止信号是否成功不影响取消终态。 |

执行与结果已确认完整时，仅清理失败不改写 `COMPLETED` 或证据，按正常规则仍可能 `READY / 0`；保留清理警告并保护历史记录。预算超时不等同于主动取消：排队过期为 `FAILED / QUEUE_TIMEOUT + UNKNOWN / 11`；运行超时按有无有效模式结果为 `PARTIAL` 或 `FAILED`，已有必检/拓扑 `FAIL` 则 `NOT_READY / 10`，否则 `UNKNOWN / 11`。已接受取消保留已证实的 Verdict，但 CLI 按 §10.3 返回 `14`。以上退出码均以无更高优先级错误且报告可归档为前提，无法形成有效报告的控制面故障仍为 `13`。

### 11.2 观测证据与检查报告结构

- 每条观测证据至少包含：`run_id`、`host_name`、`role_id`、`item_id`、`template_id`、`template_version`、`template_digest`、`goss_subrule_id`、`execution_identity`、`execution_mode`（`STANDARD/PRIVILEGED`）、`expected`、`observed`（经敏感脱敏且限制 `≤ 64 KiB`）、`evidence_mode`、`source`、`window_coverage`（日志类必填）、`high_risk_root_audit`、`collected_at`、`goss_version`、`compiler_version`。
- 每个 `ItemResult` 的 `parameter_sources` 记录现场参数解析来源（模板默认、检查项值、组级覆盖或主机级覆盖），只记录来源类别，不记录参数值；报告详情按检查项展示该映射。兼容读取旧报告时，缺少来源字段显示“未记录”。
- Run 报告可记录非秘密的 `auth_method` 及目标主机登录 `login_uid`、架构，供操作者核对实际连接身份；旧归档缺少这些可选字段时显示“未记录”。`login_uid` 描述 SSH 登录身份，不代表 `sudo` 子进程的有效身份，也不记录密码、私钥、Agent endpoint 或公钥指纹。
- JSON 检查报告（`readyspec.report/v1`）是机器可读的唯一权威结果事实源；Web UI 与 CLI 直接读取并呈现已归档报告，绝不在展示层二次重算 `Verdict`。

---

## 12. 检查方案发布、导入导出与兼容性

- **发布资格**：方案从 `DRAFT` 发布为 `RELEASED` 是纯本地静态操作（零 SSH）；方案引用的全部内置模板必须处于已集成可用态，自定义 `STANDARD` Shell 模板须通过参数化与静态编译校验。已发布方案不可原地修改，变更必须递增版本号；旧版本可标记为 `ARCHIVED`。
- **自包含单文件导入导出**：导出的 `.yaml` 默认内嵌全部被引用的模板快照与发布清单摘要；导入流程依次执行 `文件体积与 SafeYAML 校验 → JSON Schema 校验 → 模板与方案 RFC 8785 摘要核验 → 特权模板本地内置库精确匹配审查 → 同 ID+版本异摘要冲突拦截`，全程零命令执行、零联网。
- **目标与管理端平台矩阵**：
  - **六个目标主机认证组合**：Ubuntu Server 24.04 LTS、Debian 13、Rocky Linux 9 × `x86_64` / `aarch64`（待实机完成 M4 认证签署后方可标记目标平台 `Verified`）。
  - **三个管理端单二进制构建目标**：`windows/amd64`、`linux/amd64`、`linux/arm64`。

---

## 13. 非功能需求与运维规则

| 主题 | 约束基线 |
|---|---|
| 正确性 | 严禁误报 `READY`；明确 `FAIL` 与证据缺失 `ERROR` 严格区分；参数生效来源与取证方式全程可追溯 |
| 可复现性 | 相同检查方案、执行配置与目标主机能力下编译出语义完全一致的 Goss 规则 |
| 故障隔离 | 单台目标主机连接失败或超时不阻塞同一检查任务中其他目标主机的正常执行与证据归档 |
| 并发与队列预算 | 默认单活动检查任务、最多 10 排队、5 台目标主机并发、单主机 Goss 10 并发 |
| 超时与体积上限 | 默认 SSH 连接 `10s`、发现 `30s`、投递 `60s`、网络探测 `5s`（上限 `30s`）、单条 Shell `15s`（上限 `120s`）、单机 Goss `5m`、单机总计 `10m`、排队 `30m`、整任务 `60m`；方案文件 `≤ 10 MiB`、单条证据 `≤ 64 KiB`、单机 JSON `≤ 16 MiB`、整份报告 `≤ 64 MiB` |
| 历史留存与清理保护 | 已完成普通任务默认保留 `30 天` 且最多 `100 条`（取更严者）；含未结案 `CleanupItem` 的记录永久豁免自动淘汰 |
| 离线供应链 | 运行时零外网依赖；内嵌官方原装 `Goss v0.4.9` 二进制经 SHA-256 锁定，附完整 `LICENSE` 与 `NOTICE` |

**预算起点与收尾**：排队 `30m` 和整任务 `60m` 均从 `accepted.json` 提交成功时起算，整任务包含排队和预检。单主机 `10m` 累计预检及执行的处理时间，不包含等待全主机屏障的空闲时间，重连不重置；每个操作同时受阶段、剩余主机和剩余整任务预算约束，先到期者生效。超时停止受影响的新操作并尝试终止活动命令；清理归档使用独立有界上下文，不沿用已过期的执行上下文。具体收尾上限及关闭策略须在 M2-1 明确验证后落实，不能无限等待。

---

## 14. 需求追踪矩阵 RTM

每条 `REQ-*` 为唯一可追踪需求标识，对应具体实现模块、验收门槛（`F01–F14`）与自动化/实机测试用例族（`TC-*`）；此外 `H01–L03` 共 36 项预置检查模板各自拥有独立用例族 `TC-H01` 至 `TC-L03`。

### 14.1 检查方案与版本发布（`REQ-PLAN`）

| ID | 可验证需求 | 主责模块 | 关联门槛 | 测试族 |
|---|---|---|---|---|
| REQ-PLAN-01 | 检查方案声明软件名、版本、`TopologyPolicy` 及各节点角色专属独立 `CheckItem` 列表 | Domain/Registry | F07/F08 | TC-PLAN-01 |
| REQ-PLAN-02 | 检查方案导出为单个自包含 YAML 文件，内嵌全部引用模板快照与发布清单 | Registry | F08 | TC-PLAN-02 |
| REQ-PLAN-03 | 方案状态仅含 `DRAFT`、`RELEASED`、`ARCHIVED` 三态；未通过静态校验的草稿禁止发起检查任务 | Registry | F07/F08 | TC-PLAN-03 |
| REQ-PLAN-04 | 校验通过的草稿支持以 `TrialRun` 发起试跑，不产生额外方案状态且不开放正式门禁放行 | Service | F06/F07/F12 | TC-PLAN-04 |
| REQ-PLAN-05 | `RELEASED` 发布仅需通过本地静态校验，不要求先连接真实目标主机 | Registry | F08 | TC-PLAN-05 |
| REQ-PLAN-06 | 相同 `plan_id + version` 但语义摘要不同时必须报冲突并拒绝覆盖 | Registry | F08 | TC-PLAN-06 |
| REQ-PLAN-07 | `RELEASED` 方案不可原地修改；业务变更须生成新版本号，旧版可标为 `ARCHIVED` | Registry | F08/F13 | TC-PLAN-07 |
| REQ-PLAN-08 | 检查方案与导出包绝不包含任何 SSH/Sudo 凭据或历史运行结果 | Registry/Security | F08/F10 | TC-PLAN-08 |
| REQ-PLAN-09 | 导入检查方案纯静态执行，绝不发起网络连接或运行任何 Shell 命令 | Registry | F08/F10 | TC-PLAN-09 |
| REQ-PLAN-10 | SafeYAML 强制拦截文件超限（`>10 MiB`）、重复映射键、非法标签与超限锚点别名展开 | Domain | F08/F10 | TC-PLAN-10 |
| REQ-PLAN-11 | `Plan`、`CheckTemplate`、`CustomResult`、`RunReport` 四套 Schema 版本独立校验；摘要基于 RFC 8785 排除自引用字段 | Domain/Schemas | F04/F08/F13 | TC-PLAN-11 |

### 14.2 检查模板、检查项与参数解析（`REQ-TPL`）

| ID | 可验证需求 | 主责模块 | 关联门槛 | 测试族 |
|---|---|---|---|---|
| REQ-TPL-01 | 八大领域 36 项预置 `CheckTemplate` 均可按领域/中英文关键词检索浏览 | Registry/Web | F01/F07 | TC-TPL-01 |
| REQ-TPL-02 | 每项预置检查模板完整定义参数 Schema、依赖、执行方式（`STANDARD/PRIVILEGED`）与取证方式（`EvidenceMode`） | Registry | F01/F04 | TC-TPL-02 |
| REQ-TPL-03 | 相同 `template_id + version` 不同摘要禁止静默覆盖 | Registry | F04/F08 | TC-TPL-03 |
| REQ-TPL-04 | 每个 `CheckItem` 严格隶属单一节点角色，角色间修改互不牵连 | Domain | F07 | TC-TPL-04 |
| REQ-TPL-05 | 同角色或跨角色复制检查项必须生成全新的稳定 `item_id` | Domain/Web | F07/F13 | TC-TPL-05 |
| REQ-TPL-06 | 检查项删除支持撤销，不影响其他节点角色的检查项 | Web | F07 | TC-TPL-06 |
| REQ-TPL-07 | 参数类型系统支持容量单位（`GB/GiB`）、区间、正则、单选枚举、多选列表枚举、默认继承与显式空值 | Domain/Compiler | F04/F07 | TC-TPL-07 |
| REQ-TPL-08 | 参数遵循“模板默认 → 检查项设定 → 现场参数值（组级 → 主机级）”三级确定性线性解析并记录来源 | Compiler | F04/F07 | TC-TPL-08 |
| REQ-TPL-09 | 现场参数值仅允许修改 `OverridableParam` 标记字段；修改未开放字段立即报 `PLAN_ERROR` | Compiler/Security | F04/F10 | TC-TPL-09 |
| REQ-TPL-10 | 同一目标主机命中多个不同取值的同级主机组 `FieldOverride` 时显式报 `OVERRIDE_CONFLICT` | Compiler | F04/F09 | TC-TPL-10 |
| REQ-TPL-11 | 支持在检查方案内创建局部自定义 Shell 检查模板并由多个检查项引用 | Registry/Web | F07/F08 | TC-TPL-11 |
| REQ-TPL-12 | 方案局部自定义 Shell 模板经校验可提升为不可变用户公共模板，且不自动改动原方案锁定引用 | Registry | F08 | TC-TPL-12 |
| REQ-TPL-13 | 自定义 Shell 断言模拟自测器在纯本地求值，不发起 SSH 或执行本地 Shell | Normalizer/Server | F07/F10 | TC-TPL-13 |

### 14.3 编译与资源合并引擎（`REQ-COMP`）

| ID | 可验证需求 | 主责模块 | 关联门槛 | 测试族 |
|---|---|---|---|---|
| REQ-COMP-01 | 执行前静态预览不发起任何 SSH 连接，准确输出目标主机数、候选检查项数与特权/`root`/网络探测提示 | Compiler | F04/F07 | TC-COMP-01 |
| REQ-COMP-02 | 完成目标主机能力发现后再生成锁定的分方式执行工件 | Compiler/Execution | F02/F04 | TC-COMP-02 |
| REQ-COMP-03 | 原生 Goss 资源与内嵌 `command` 检查均可编译，禁止跨检查项运行时条件分支或顺序依赖 | Compiler | F01/F04 | TC-COMP-03 |
| REQ-COMP-04 | 每台目标主机按 `STANDARD` 与 `PRIVILEGED` 分别编译生成完全独立的 Goss 工件 | Compiler | F04/F10 | TC-COMP-04 |
| REQ-COMP-05 | 指向同一 Goss 资源键的无矛盾属性自动合并，矛盾属性断言显式报 `RESOURCE_CONFLICT` 拒绝编译 | Compiler | F04 | TC-COMP-05 |
| REQ-COMP-06 | 编译期建立稳定的属性溯源映射（`PropertyOrigin`），确保合并执行结果精确拆回对应节点角色与检查项 | Compiler/Normalizer | F04/F13 | TC-COMP-06 |
| REQ-COMP-07 | 相同语义输入产生确定性规范化编译产物与摘要 | Compiler | F04 | TC-COMP-07 |
| REQ-COMP-08 | 无满足声明 `EvidenceMode` 的实现时如实标记 `ERROR`/`UNSUPPORTED`，严禁静默降级为浅层检查 | Compiler/Normalizer | F05/F06 | TC-COMP-08 |
| REQ-COMP-09 | 编译期错误归类为 `PLAN_ERROR`，绝不伪装成目标主机业务断言 `FAIL` | Compiler/CLI | F05/F12 | TC-COMP-09 |

### 14.4 原生 SSH 与远端执行生命周期（`REQ-EXEC`）

| ID | 可验证需求 | 主责模块 | 关联门槛 | 测试族 |
|---|---|---|---|---|
| REQ-EXEC-01 | 单二进制内嵌未经修改的官方 `Goss v0.4.9`（`linux-amd64` / `linux-arm64`）并在启动时自检锁定 SHA-256 | Assets | F02/F03/F10 | TC-EXEC-01 |
| REQ-EXEC-02 | 基于纯 Go 原生 SSH 连接无 Python、无预装 Goss、无 Agent 的 Linux 目标主机（Windows/Linux 管理端一致） | Execution | F03/F12 | TC-EXEC-02 |
| REQ-EXEC-03 | 支持私钥、SSH Agent（含 Windows OpenSSH Named Pipe）与内存短期密码认证 | Execution | F03/F10/F12 | TC-EXEC-03 |
| REQ-EXEC-04 | 每台目标主机创建唯一 `0700` 临时工作目录（禁 `-p`）并写入 `.readyspec_owner` 归属令牌 | Execution | F10/F11 | TC-EXEC-04 |
| REQ-EXEC-05 | 远端运行 Goss 前通过 `sha256sum`/`openssl` 强制核验二进制与规则摘要，缺失工具或摘要不符立即中止 | Execution | F10 | TC-EXEC-05 |
| REQ-EXEC-06 | 目标临时目录挂载 `noexec` 时按受控候选列表回退，均不可执行则报 `ERROR`，严禁修改挂载参数 | Execution | F03/F10 | TC-EXEC-06 |
| REQ-EXEC-07 | 单任务默认 5 主机有界并发，单台主机失败隔离不阻断其他目标主机 | Service/Execution | F09 | TC-EXEC-07 |
| REQ-EXEC-08 | 同一目标主机严格先运行 `STANDARD`、后串行运行已获授权的 `PRIVILEGED` | Execution | F09/F10 | TC-EXEC-08 |
| REQ-EXEC-09 | 每主机每执行方式分别持久化“可能已开始执行”标记，成功后才发命令；标记后严禁自动重跑，仅此前瞬时连接故障可有限重试 | Execution | F11 | TC-EXEC-09 |
| REQ-EXEC-10 | 调用 Goss 时显式关闭内部重试（`--retry-timeout 0s`）并强制执行单命令与单主机总超时预算 | Execution | F09/F11 | TC-EXEC-10 |
| REQ-EXEC-11 | 支持协作式取消，立即停止新分派并保留已完成主机的有效证据 | Service/Execution | F11 | TC-EXEC-11 |
| REQ-EXEC-12 | 远端清理严格验证 `machine_id`、`boot_id` 与 `.readyspec_owner` 且不跟随符号链接越界删除 | Execution | F10/F11 | TC-EXEC-12 |
| REQ-EXEC-13 | 内置仿真执行器支持离线预演与测试，并在界面及报告中强制标识 `mock_simulator` | Execution | F06/F07 | TC-EXEC-13 |

### 14.5 任务协调、工作区锁与残留清理（`REQ-RUN`）

| ID | 可验证需求 | 主责模块 | 关联门槛 | 测试族 |
|---|---|---|---|---|
| REQ-RUN-01 | 跨平台工作区文件锁（Linux `flock` / Windows `LockFileEx`）保证同一工作区任一时刻仅有一个活动控制器 | Platform/Store | F09/F11 | TC-RUN-01 |
| REQ-RUN-02 | CLI 自适应双模：有活动 `readyspec serve` 时经本地受控 API 委托执行，无活动服务时进程内持锁直跑 | Coordinator/CLI | F09/F12 | TC-RUN-02 |
| REQ-RUN-03 | 单活动检查任务 + 最多 10 排队 FIFO；超出或 `--no-wait` 锁占用返回 `WORKSPACE_BUSY`（退出码 `16`） | Service/CLI | F09/F12 | TC-RUN-03 |
| REQ-RUN-04 | 工作区串行提交与原子接受记录保护幂等，同键同摘要返回原Run、异负载拒绝，重复提交不替换凭据；索引可重建 | Service/Store | F09/F11 | TC-RUN-04 |
| REQ-RUN-05 | 提交任务即冻结检查方案快照、主机清单、角色分配与现场参数值，后续修改外部文件不影响运行中任务 | Service | F04/F09 | TC-RUN-05 |
| REQ-RUN-06 | 关闭浏览器页面不中断 `readyspec serve` 中的活动任务，重连可通过快照 + SSE 单调序号续传 | Server/Web | F09/F11 | TC-RUN-06 |
| REQ-RUN-07 | 重启优先按有效报告补归档，无报告但已接受取消则CANCELLED，其余旧未完成任务为中断终态，绝不自动重跑 | Store/Service | F11 | TC-RUN-07 |
| REQ-RUN-08 | 状态文件采用同目录临时文件 + 刷盘 + 原子替换写入；事件流尾部半行损坏可安全恢复有效前缀 | Platform/Store | F11/F13 | TC-RUN-08 |
| REQ-RUN-09 | 普通已完成任务保留 30 天且最多 100 条；含未结案 `CleanupItem` 的记录永久受保护不被淘汰 | Store | F11/F13 | TC-RUN-09 |
| REQ-RUN-10 | `RemoteCleanup` 检测到远端存活关联进程时，须经操作者显式确认后方可终止并清理 | Execution/Server | F10/F11 | TC-RUN-10 |
| REQ-RUN-11 | 待清理残留标为 `IGNORED`（人工忽略）时必须录入原因并永久保留审计记录，不伪装成 `CLEAN` | Store/Server | F11/F13 | TC-RUN-11 |

### 14.6 安全与信任边界（`REQ-SEC`）

| ID | 可验证需求 | 主责模块 | 关联门槛 | 测试族 |
|---|---|---|---|---|
| REQ-SEC-01 | 严禁将非 `root` 连接通过 `sudo` 提权执行任何用户自定义 Shell | Compiler/Execution | F10 | TC-SEC-01 |
| REQ-SEC-02 | root 登录或实际 UID 为 0 时提示实际执行身份并允许执行，不要求额外授权；强制记录 `high_risk_root_audit=true`，覆盖自定义 Shell 场景 | Service/Execution | F10 | TC-SEC-02 |
| REQ-SEC-03 | 用户模板与外部导入模板快照不可自行获取特权资格；`PRIVILEGED` 必须精确匹配本地内置可信注册表摘要 | Registry/Security | F08/F10 | TC-SEC-03 |
| REQ-SEC-04 | 按可信模板与实际UID选择root或按需sudo，无额外批准；全主机屏障先于投递，明确缺凭据/认证拒绝/权限不足整次拒绝并返15；sudo无法核验同样整次零投递/检查，报告有效且无更高优先级结果时返11；root、免密sudo及无需提权不多要求密码 | Service/Security | F10 | TC-SEC-04 |
| REQ-SEC-05 | 特权执行创建 `root:root 0700` 隔离区、`root` 态二次校验 SHA-256 并以 `env -i` 清洗环境；非 root 经单次 sudo 执行并 `sudo -k` 销票据，root 直接执行同等闭环 | Execution | F10 | TC-SEC-05 |
| REQ-SEC-06 | 严禁修改目标主机 `sudoers` 或安装常驻提权程序 | Execution | F10 | TC-SEC-06 |
| REQ-SEC-07 | 默认开启 SSH `known_hosts` 主机身份校验；检测不同主机名指向同一实机指纹时报错拦截 | Execution/Normalizer | F10 | TC-SEC-07 |
| REQ-SEC-08 | 自定义 Shell 参数仅通过安全单引号转义的只读环境变量 `$RS_PARAM_<KEY>` 传递 | Compiler | F10 | TC-SEC-08 |
| REQ-SEC-09 | SSH/Sudo 密码仅驻留进程内存 `EphemeralVault` 并在终态立即覆写清零，绝不落盘或进入日志 | Service/Security | F10/F13 | TC-SEC-09 |
| REQ-SEC-10 | Web 服务默认仅绑定 `127.0.0.1` 并校验会话 Cookie、CSRF 令牌与 `Origin`/`Host`；非回环监听强制访问令牌鉴权 | Server | F10 | TC-SEC-10 |
| REQ-SEC-11 | Linux 工作区强制 `0700`/`0600` 权限，Windows 工作区强制收紧 `%LOCALAPPDATA%` 用户专属 DACL | Platform | F10/F12 | TC-SEC-11 |

### 14.7 结果、取证与检查报告（`REQ-RES`）

| ID | 可验证需求 | 主责模块 | 关联门槛 | 测试族 |
|---|---|---|---|---|
| REQ-RES-01 | `ItemResult` 严格区分 `PASS / FAIL / ERROR / UNSUPPORTED / SKIP` 五态 | Normalizer | F05 | TC-RES-01 |
| REQ-RES-02 | `RunStatus`、`ItemResult`、`Verdict` 三套状态正交独立管理 | Domain/Normalizer | F06 | TC-RES-02 |
| REQ-RES-03 | 任一启用必检检查项或 `TopologyPolicy` 为 `FAIL` 时（非取消无果态），`Verdict` 优先推导为 `NOT_READY` | Normalizer | F06 | TC-RES-03 |
| REQ-RES-04 | 无必检 `FAIL` 但存在必检 `ERROR`/`UNSUPPORTED`、空必检集、或属 `TrialRun`/`PartialRun`/`mock_simulator` 时，`Verdict` 为 `UNKNOWN` | Normalizer | F06 | TC-RES-04 |
| REQ-RES-05 | 仅 `RELEASED + GateRun + FullRun + native_ssh` 正常完成且必检与拓扑全 `PASS` 时产出 `Verdict = READY` | Normalizer | F06/F12 | TC-RES-05 |
| REQ-RES-06 | 每条证据完整记录 `expected`、`observed`、`evidence_mode`、参数来源、执行身份与采集时间 | Normalizer | F05/F13 | TC-RES-06 |
| REQ-RES-07 | 日志历史类检查（`L02`/`L03`）强制验证可读最早日志时间覆盖要求窗口，缺口时严禁判 `PASS` | Registry/Normalizer | F01/F05/F13 | TC-RES-07 |
| REQ-RES-08 | `R04` 严禁以 SSH Shell `ulimit` 冒充目标进程限制，`A03` 严禁降级 `grep` 静态配置文件，`D04` 严禁调用 `ldd` | Registry/Normalizer | F01/F05/F10 | TC-RES-08 |
| REQ-RES-09 | JSON 检查报告符合 `readyspec.report/v1` Schema 并经原子写入归档；Web UI 与 CLI 直接读取同一结论 | Store/Server/CLI | F12/F13 | TC-RES-09 |
| REQ-RES-10 | 失败、超时或取消的任务仍尽最大努力归档已采集到的目标主机事实与诊断记录 | Service/Store | F13 | TC-RES-10 |

### 14.8 Web UI、CLI 与跨平台交付（`REQ-WEB / REQ-CLI / REQ-PLAT`）

| ID | 可验证需求 | 主责模块 | 关联门槛 | 测试族 |
|---|---|---|---|---|
| REQ-WEB-01 | 遵循 `DESIGN.md` 令牌与组件规范提供总览、方案工作台、检查与报告、模板与清理四个主视图及常驻待清理提醒 | Web | F07 | TC-WEB-01 |
| REQ-WEB-02 | 部署工程师可从总览直接导入方案进入四步执行向导，无需先进入方案工作台 | Web | F07 | TC-WEB-02 |
| REQ-WEB-03 | 方案工作台支持三栏协同、强类型参数控件（含单选下拉与多选框）、`OverridableParam` 开关与错误直达定位 | Web | F07 | TC-WEB-03 |
| REQ-WEB-04 | 支持上传文件与粘贴文本导入检查方案，支持 Ansible INI/YAML 与快速主机表双模式录入主机清单 | Web/Inventory | F07 | TC-WEB-04 |
| REQ-WEB-05 | 报告页提供按目标主机/按检查项双视角切换、右侧观测证据抽屉、异常过滤及局部复测/全量复测快捷动作 | Web | F07/F13 | TC-WEB-05 |
| REQ-WEB-06 | 非 TLS 远程监听场景下，密码输入控件显著提示非加密通道传输风险，且提交后前端立即清空明文状态 | Web/Security | F07/F10 | TC-WEB-06 |
| REQ-CLI-01 | CLI 完整提供 `serve / plan / template / run / report / cleanup` 命令树，缺必填项直接非交互报错 | CLI | F12 | TC-CLI-01 |
| REQ-CLI-02 | CLI 退出码严格遵循 §10.3 的 8 步有序推导规则（`0 / 10 / 11 / 12 / 13 / 14 / 15 / 16`） | CLI | F06/F12 | TC-CLI-02 |
| REQ-CLI-03 | CLI 支持 `--expected-plan-digest` 强校验与 JSON 报告导出 | CLI | F10/F12/F13 | TC-CLI-03 |
| REQ-PLAT-01 | `CGO_ENABLED=0` 静态构建产出 `windows/amd64`、`linux/amd64`、`linux/arm64` 三个独立单二进制 | Build | F12 | TC-PLAT-01 |
| REQ-PLAT-02 | 六个首批目标 Linux 组合（Ubuntu 24.04 / Debian 13 / Rocky 9 × `x86_64`/`aarch64`）通过实机集成验证 | Execution/Platform | F02/F03 | TC-PLAT-02 |
| REQ-PLAT-03 | 在 `20 台目标主机 × 100 检查项` 标准负载下通过竞态检测与资源预算测试 | Service/Execution | F14 | TC-PLAT-03 |

---

## 15. 测试与发布验收基线

### 15.1 F01–F14 发布阻断门槛

| 编号 | 验收门槛 | 必须出示的测试结果与通过判据 |
|---|---|---|
| **F01** | 36 项预置模板与拓扑核对 | `H01–L03` 全部实现并集成，每项通过 PASS/FAIL/边界/异常/现场覆盖/映射六类测试；`TopologyPolicy` 三项核对完整通过 |
| **F02** | 六目标平台实测认证 | Ubuntu 24.04、Debian 13、Rocky 9 × `x86_64`/`aarch64` 实机通过原生 SSH + 内嵌 Goss 全链路认证 |
| **F03** | 零目标端预装依赖 | 在无 Python、无预装 Goss、无 Agent 的最小化 Linux 主机上完成流式投递、SHA-256 验真、执行、回收与清理 |
| **F04** | 编译确定性与冲突阻断 | 同输入生成确定性规则；同机多角色资源智能合并正确，矛盾断言与同级多组覆盖冲突稳定拦截 |
| **F05** | `ItemResult` 五态与取证保真 | `PASS/FAIL/ERROR/UNSUPPORTED/SKIP` 严格分类；`R04`/`A03`/`D04`/`L02`/`L03` 反向测试证明绝不静默降级冒充 `PASS` |
| **F06** | `Verdict` 门禁严谨性 | 仅 `RELEASED + GateRun + FullRun + native_ssh` 全必检通过产出 `READY`；任何必检 `FAIL` 优先产出 `NOT_READY`；其余异常或试跑/局部/仿真均为 `UNKNOWN` |
| **F07** | Web UI 全流程与设计合规 | 符合 `DESIGN.md` 规范；完整验证编排、Shell 离线自测、导入、四步向导、SSE 看板、双视角抽屉报告与残留清理 |
| **F08** | 自包含方案发布与跨机导入 | 单 YAML 文件跨工作区离线导入摘要一致；同版本异摘要拒绝覆盖；伪造特权模板被拦截 |
| **F09** | 多主机并发、队列与双模协调 | 5 主机并发隔离、单主机普通后特权串行；`readyspec serve` 运行时 CLI 提交自动委托并在 Web UI 同步可见 |
| **F10** | 安全基线与跨平台隔离 | 非 root 禁 `sudo` 跑自定义 Shell；root 直连及可信内置特权检查仅提示、不额外授权，并保留身份审计；全部目标凭据与权限前置校验，必要凭据缺失、认证失败或提权权限不足拒绝执行；特权 `root:root 0700` 二次摘要验真与按需 `sudo -k`；密码内存即焚；Web `Origin`/Token/CSRF 防护与 Windows/Linux 工作区权限生效 |
| **F11** | 取消、中断与远端残留清理 | 中断后不自动重跑已启动检查；`CleanupItem` 持久保护，校验归属后精确清理且不越界删除，`IGNORED` 保留审计 |
| **F12** | 三架构单二进制与 CLI 门禁 | `windows/amd64`、`linux/amd64`、`linux/arm64` 单二进制构建通过；CLI 退出码严格遵循 §10.3 推导链（`0 / 10~16`） |
| **F13** | 检查报告完整性与可追溯 | JSON 检查报告与 Web UI 完整呈现期望/实测、取证方式、参数来源、执行身份与审计记录 |
| **F14** | 性能与并发稳定性 | 通过 §15.4 标准负载压测与 `go test -race` 零竞态告警 |

### 15.2 五个必须成功的端到端验收场景

1. **方案作者完整交付闭环**：在 Web 方案工作台新建含 `web`/`db` 两个节点角色与 `TopologyPolicy` 的检查方案，挂载内置模板与一条局部自定义 Shell 检查项，开放现场可调参数，经断言模拟自测与 `TrialRun` 验证后发布为 `RELEASED` 并导出自包含单文件 YAML，核对导出文件 RFC 8785 摘要一致。
2. **跨机器零依赖离线导入执行**：将单二进制与导出的检查方案 YAML 拷贝至全新工作区，验证无需外部模板文件即可静态导入、核验摘要，并拦截篡改伪造为 `PRIVILEGED` 的非受信模板。
3. **混合发行版与双架构目标集群实检**：在包含 `x86_64` 与 `aarch64` 多发行版 Linux 目标主机的清单上通过 `NativeSSHExecutor` 执行全量检查，验证内嵌双架构 Goss 精准匹配投递、`STANDARD` 与受控 `PRIVILEGED` 串行隔离、以及包含 `PASS/FAIL/ERROR` 的证据准确归档。
4. **CI/CD 非交互门禁阻断与放行**：使用 CLI 执行带 `--expected-plan-digest` 的正式门禁检查，分别验证全部满足返 `0`、必检 `FAIL`（含在 `PartialRun` 中出现必检 `FAIL`）返 `10`、`TrialRun`/`PartialRun` 全绿返 `11`、编译冲突返 `12`、主动取消返 `14`、摘要不匹配返 `15`、`--no-wait` 锁占用返 `16`；root 直连与可信内置特权检查仅输出提示，不等待确认或要求批准参数；缺少必要凭据、认证失败或提权权限不足时返 `15`，验证任何目标均未投递或执行检查；root 直连、免密 sudo、密钥/Agent 认证成功时不额外要求密码。
5. **故障中断与远端残留安全清理**：模拟在投递后、Goss 执行中、清理前三个窗口断开连接或强杀管理端进程，验证重启后旧任务绝不自动重跑、`CleanupItem` 完整保留、且通过 `RemoteCleanup` 校验归属令牌后可安全清理或记原因置为 `IGNORED`。

### 15.3 兼容矩阵最低测试集

| 测试维度 | 必测组合与边界用例 |
|---|---|
| 目标发行版与 CPU 架构 | Ubuntu 24.04、Debian 13、Rocky Linux 9 × `x86_64` / `aarch64` 原装 `Goss v0.4.9` 启动与 JSON 输出解析 |
| 最小化无依赖目标环境 | 无 Python、无预装 Goss、仅具 POSIX `/bin/sh` 与 `sha256sum`/`openssl` 的精简系统流式投递与清理 |
| 登录与提权组合 | 普通非 root 登录无 `sudo`、免密 `sudo`、需密码 `sudo`、受限命令白名单 `sudo`（所需命令不被允许时前置拒绝，CLI 返回 `15`）、root 直连及其他用户名实际 UID 为 0（均仅提示、不额外授权并记强审计）；验证凭据缺失或错误时无检查项执行，root 直连不调用 sudo，非 root 自定义 Shell 不自动提权 |
| 临时目录受限环境 | `/tmp` 挂载 `noexec` 或只读时自动回退受控候选目录，全部受限时准确报 `ERROR` 且绝不修改挂载 |
| 发行版工具链差异 | `deb`/`rpm` 包查询、`chrony`/`systemd-timesyncd` 时间同步、日志权限受限与窗口覆盖缺口（`L02`/`L03`）、缺 `readelf` 时 `D04` 分类 |
| 拓扑与覆盖冲突 | 单主机多角色同机混部、同级多主机组 `FieldOverride` 冲突、不同主机名指向同一实机指纹拦截 |
| 管理端三二进制运行 | `linux/amd64`、`linux/arm64`、`windows/amd64` 启动 Web UI、跨平台文件锁互斥及 CLI 自适应委托联动 |

### 15.4 性能与资源稳定性验收方案

以 **20 台目标主机、每台 100 个 `STANDARD` 检查项、默认 5 台主机并发** 为标准基准负载，叠加“部分目标不可达超时”、“单条命令大量 stdout 输出截断”、“长等待队列（10 任务满队）”与“SSE 客户端频繁断线重连”四类压力注入：
- 全程开启 `go test -race` 零数据竞态；
- 管理端推荐 `2C4G` 环境下峰值驻留内存 `≤ 512 MiB`（硬上限 `≤ 2 GiB`），无文件描述符或内存持续泄漏，零检查项结果丢失或串位；最终实测指标在 M4 签署归档。

---

## 16. M0–M4 实施路线与里程碑

1. **M0（基础设施与跨平台原语）**：落地 `GLOSSARY.md`、`SPEC.md` 与 `DESIGN.md` 裁剪声明；建立 Go 模块与跨平台适配层（`flock`/`LockFileEx`、原子写、Windows DACL）、SafeYAML、RFC 8785 JCS 摘要、新版 JSON Schema，拉取并校验内嵌双架构官方 `Goss v0.4.9`。
2. **M1（核心控制面与编译归一化引擎）**：实现 36 项预置 `CheckTemplate`、`WorkspaceStore`、Ansible INI/YAML 与快速主机表解析器、三级参数线性解析、Goss 智能合并编译器、`TopologyPolicy` 与 `Verdict` 归一化引擎及全量单元测试。
3. **M2（双执行器、Run Manager 与自适应 CLI/API）**：实现 `NativeSSHExecutor`（含进程内回环 SSH Server + 真实内嵌 Goss 全链路集成测试）、`MockSimulatorExecutor`、`RunManager`、受控 HTTP/SSE 服务与自适应双模 CLI（严格验证退出码 `0 / 10~16`）。
4. **M3（Web UI 四视图控制台与单二进制合流）**：按 `DESIGN.md` 实现 React + TypeScript 前端和 Go 原生生命周期、公共模板、断言、Run 历史及清理闭环；静态产物编译嵌入 Go 单二进制。旧 Python/Docker 遗留物仅在替代用例与迁移验收后清理。
5. **M4（三架构交叉构建与矩阵验收）**：构建并检验 `windows/amd64`、`linux/amd64`、`linux/arm64` 单二进制产物，执行端到端验收场景、性能压测与六目标 Linux 平台实机认证。

---

## 17. TODO、技术风险与变更控制

### 17.1 待实机环境签署的验证项（`TODO`）

| 编号 | 关口 | 待实测签署事项 | 闭环判据 |
|---|---|---|---|
| **TODO-01** | M0 / M4 | 内嵌 `Goss v0.4.9`（`linux-amd64` / `linux-arm64`）与 36 项预置模板在 Ubuntu 24.04、Debian 13、Rocky 9 双架构真实虚拟机上的全矩阵实测 | 出具六组合真实运行报告，未实测前禁止对外宣称目标平台已认证 |
| **TODO-02** | M0 | 验证原装 Goss JSON 在 `16 KiB` 边界、特殊转义字符与并发场景下对 `readyspec.custom_result/v1` 单行记录的无损回收 | 全用例无损关联则启用结构化结果协议；否则冻结禁用该高级模式，仅开放基础断言模式 |
| **TODO-03** | M4 | 在原生 Windows 11 (`amd64`) 环境实测 `%LOCALAPPDATA%` DACL、`LockFileEx`、浏览器安全握手及 Windows OpenSSH Named Pipe Agent | Windows 管理端权限隔离、排他锁与实机 Linux SSH 检查全链路通过 |
| **TODO-04** | M4 | 执行 §15.4 标准负载压测（20 主机 × 100 检查项）并采集真实 CPU、峰值内存与端到端耗时 | 签署性能基准报告，满足 `F14` 门槛 |

### 17.2 已批准基线变更登记

当前权限交互以 `CHG-007` 为准；`CHG-001`、`CHG-003` 中涉及额外 root/特权授权的历史描述已被替代，其余约束继续有效。

| 变更号 | 日期 | 变更摘要与原因 | 受影响范围 | 风险与强制缓解措施 | 批准状态 |
|---|---|---|---|---|---|
| **CHG-001** | 2026-11-03 | 允许在显式风险授权前提下使用 `root` SSH 直连执行检查，并合入特权执行通道 `root:root 0700` 二次摘要验真与 `sudo -k` 闭环加固 | §7/§8/§9、`REQ-SEC-01~05`、`F10` | 未获显式 `approve_root_ssh` 授权一律拒绝并报 `ROOT_SSH_UNAPPROVED`；非 root 连接严禁 `sudo` 提权执行自定义 Shell | 已批准 |
| **CHG-002** | 2026-11-03 | 简化检查方案为单文件扁平结构与三级线性参数解析，引入短期纯内存凭据保险箱（终态即焚） | §4/§5/§9、`REQ-TPL-08/09`、`REQ-SEC-09` | 现场参数值仅允许修改 `OverridableParam` 标记字段；密码绝不落盘 | 已批准 |
| **CHG-003** | 2026-11-03 | 确立四视图核心工作流、双模式主机清单录入、`$RS_PARAM_<KEY>` 只读注入与局部复测禁放行规则 | §5/§10/§11、`REQ-SEC-08`、`F06/F07` | 局部复测（`PartialRun`）与仿真执行（`mock_simulator`）严禁产出放行级 `READY` 或退出码 `0` | 已批准 |
| **CHG-004** | 2026-11-03 | 宽松开源许可证裁剪：采用净室自研 Ansible Inventory 解析器与未经修改的官方 `Goss v0.4.9`，保持全栈 `Apache-2.0 / MIT / BSD` 零 `GPL` 依赖 | §1.2/§3.1 | 随仓维护完整 `LICENSE` 与 `NOTICE` 第三方开源声明 | 已批准 |
| **CHG-005** | 2026-11-04 | **重大架构、设计规范与领域词汇升级**：① 管理端从 Python + Docker + Textual TUI 全面重构为 **Go 原生后端 + 内嵌 React Web UI + 内嵌双架构官方 Goss v0.4.9** 的零依赖单二进制（交付 `windows/amd64`、`linux/amd64`、`linux/arm64` 三个可执行产物）；② 同时交付纯 Go 原生 SSH 执行器与内置仿真执行器，CLI 与 Web 服务采用跨平台文件锁 + 本地受控 API 自适应双模协调；③ 引入 `DESIGN.md` 作为 Web UI 唯一视觉与组件令牌来源（在 `DESIGN.md` 顶部明确排除 CMDB/持续监控/流水线/事故管理章节）；④ 建立根目录 `GLOSSARY.md`，一步到位统一全栈命名（收敛为 36 项预置 `CheckTemplate` + 方案级 `TopologyPolicy`、`Plan` 三态、`CheckItem`、`RunConfig`、`GateRun/TrialRun`、`FullRun/PartialRun`、`ItemResult`、`Verdict`、`EvidenceMode`、`RemoteCleanup/CleanupItem/IGNORED`）；⑤ 锁死 CLI 退出码 8 步有序推导优先级，并新增独立退出码 `16 WORKSPACE_BUSY` 以保持 `15 AUTH_ERROR` 语义纯粹 | 全文各章、`GLOSSARY.md`、`DESIGN.md`、`README.md`、Schema/CLI/API | Web 服务默认仅监听 `127.0.0.1` 并强制会话/CSRF/`Origin` 校验，非回环监听强制访问令牌鉴权并警示非 TLS 明文风险；Windows 采用 `%LOCALAPPDATA%` 用户专属 DACL 与 `LockFileEx`；在 `M3` 完成前保留旧 Python 目录并于 `M3` 统一移除 | 已由用户确认批准 |
| **CHG-006** | 2026-11-06 | M3 增量：方案生命周期使用不可变 RELEASED 与 ARCHIVED 快照和 `readyspec.release-manifest/v1`；GATE 接受前核对发布清单；加入标准权限的 USER_PUBLIC 模板提升、基础 Shell 断言及纯函数自测；任务历史可读取冻结配置、阶段投影、报告证据与清理审计。具体兼容、HTTP/CLI 契约和验收限制及最终验证记录见 [`docs/m3-implementation-design.md`](./docs/m3-implementation-design.md)。 | 方案 Schema/CLI/API、模板 Schema/API、Run API、Web 四视图 | 旧外部发布物缺失或无法核验清单时拒绝执行；试跑/模拟/局部结果不能作为正式 GATE READY 依据；M4 平台认证和正式性能验收仍未包含 | M3 实现验收完成 |

---

### 17.3 本轮已确认的部署与权限决策

- **CHG-006（2026-11-04，用户已确认）**：部署范围为受信任内网，允许非回环 HTTP，TLS 可选；保留访问令牌、会话、CSRF、Origin/Host 防护、明文传输提示与提交后清空前端密码状态。接受 HTTP 下令牌、会话、密码与业务数据明文传输的取舍，不强制 HTTPS。
- **CHG-007（2026-11-04，用户已确认）**：依据账户身份和可信内置检查模板的权限需求执行，root 直连及按需 sudo 只提示并审计，无额外授权勾选或批准参数；替代旧版 `approve_root_ssh`、`--approve-root-ssh`、`--approve-privileged` 与 `ROOT_SSH_UNAPPROVED` 契约。工具自动判定权限需求，缺少必要凭据、认证失败或所需提权权限不足时在检查开始前拒绝执行，CLI 返回 `15 AUTH_ERROR`；root、免密 sudo、密钥/Agent 已满足需求时不额外要求密码。前置校验通过后的实际执行故障仍记对应检查项 `ERROR`。不改变内置特权模板可信校验、特权隔离与自定义 Shell 不自动 sudo 提权的边界。
- **CHG-008（2026-11-05，用户已确认）**：移除方案级 `allowed_os` 及目标 OS 白名单核对，避免用硬编码发行版列表代替具体软件环境要求；`TopologyPolicy` 保留 CPU 架构、节点数量与混部约束，发行版与版本要求由 `O01 发行版与版本` 等具体检查项表达。当前 Schema 拒绝仍含 `allowed_os` 的旧方案，迁移时须删除该字段并重新校验、导出，不保留静默忽略或隐式兼容。
- **CHG-009（2026-11-06，用户已逐项确认）**：M2 四组行为契约收敛：① sudo 明确拒绝为 `15`，无法核验同样整次零投递/检查，报告有效且无其他优先结果时为 `FAILED + UNKNOWN / 11`；② 允许本机服务任务期持有 CLI 交接私钥材料或直接连接明确指定的本机 Agent，不落盘、不替换身份、不依赖 CLI 存活；③ 原子接受记录为提交点，索引/快照可重建，每 Run 串行持久化 Journal，恢复优先报告、其次已接受取消、最后中断归档，绝不重跑；④ 整任务 `60m` 包含排队，预算超时不算主动取消，取消与归档串行排序，清理失败独立于有效结果。详见 [`docs/m2-implementation-design.md`](docs/m2-implementation-design.md)。这是行为和实施计划批准，sudo/Agent 技术可行性、Schema 与具体存储/收尾细节仍须 M2-1 验证，不代表 M2 已实现。

## 18. 参考资料与领域词汇对照

### 18.1 上游技术参考

1. [Goss v0.4.9 官方文档与发布工件](https://github.com/goss-org/goss/releases/tag/v0.4.9)（`Apache-2.0`）
2. [RFC 8785: JSON Canonicalization Scheme (JCS)](https://www.rfc-editor.org/rfc/rfc8785)
3. [JSON Schema Draft 2020-12 Specification](https://json-schema.org/specification)
4. [Ansible Inventory 语法格式参考](https://docs.ansible.com/ansible/latest/inventory_guide/intro_inventory.html)（仅作输入语法兼容参考，零代码依赖）

### 18.2 权威领域词汇索引

本项目所有领域概念的唯一权威定义及禁用词清单见仓库根目录 [`GLOSSARY.md`](./GLOSSARY.md)。任何新增文档、Schema 字段、Go 类型、API 路径、CLI 参数与 Web UI 文案均须与 [`GLOSSARY.md`](./GLOSSARY.md) 保持严格一致。
