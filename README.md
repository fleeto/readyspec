# ReadySpec

**Linux 软件部署前环境要求定义、目标主机无代理检查与就绪结论门禁工具**  
*Pre-deployment Linux environment specification, agentless target inspection, and readiness gate tool*

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

> **当前分支状态说明（`feat/single-binary-webui`）**：本项目已完成架构与领域词汇基线修订（见 [`SPEC.md`](./SPEC.md) `CHG-005`、[`GLOSSARY.md`](./GLOSSARY.md) 与 [`DESIGN.md`](./DESIGN.md)），确立为 **Go 原生后端 + 内嵌 Web UI + 内嵌双架构官方 `Goss v0.4.9`** 的跨平台单可执行文件形态。仓库当前处于按 `M0–M4` 里程碑实施替换阶段，待 Go 与 Web UI 模块验收完成后将移除遗留的旧版 Python 原型目录（`src/readyspec/`）。

---

## 当前可运行的 Go 与 Web UI

开发环境要求 **Go 1.26+、Node.js 22.12+ 与 npm**。发布产物为内嵌前端的独立可执行文件，运行时不需要 Python、Node.js 或外部静态资源。

```bash
# 安装锁定的前端开发依赖
make web-install

# 测试与静态检查
make test
go vet ./...

# 离线校验新格式方案，并输出方案标识、状态与语义摘要
go run ./cmd/readyspec plan validate ./examples/minimal-plan.yaml --json

# 构建前端、校验内嵌 Goss，再生成单文件程序
make build

# 启动 Web UI；桌面环境自动打开浏览器，终端也会显示访问链接
./dist/readyspec

# 构建 Linux amd64/arm64 与 Windows amd64 三个产物
make build-all
```

当前 Web UI 提供总览、方案工作台、检查与报告、模板与清理四个视图，支持深浅主题、响应式导航、草稿编辑与保存、YAML/JSON 导入、静态校验和 YAML 导出。方案工作台可配置环境拓扑、节点角色、自定义 Shell 检查项、检查参数及现场可调范围，支持检查项独立复制、删除确认与仅恢复被删对象的撤销。方案级 `TopologyPolicy` 只声明 CPU 架构与节点拓扑，不再包含 `allowed_os`；发行版与版本要求通过具体检查项表达。含 `allowed_os` 的旧方案须先移除该字段，再按当前 Schema 重新校验和导出。未完成草稿也可保存在持锁工作区的 `plans/drafts/web-index.json`，刷新或重启后可重新打开；保存不代表校验通过，严格校验/导出还会核对参数类型、范围、枚举与语义摘要。

后端已实现安全 YAML、RFC 8785 语义摘要、新方案/模板 Schema、模板引用与快照摘要校验、工作区互斥与原子 JSON 写入、会话 Cookie、CSRF、Origin 防护，以及双架构官方 Goss 内嵌完整性校验。Host 头校验分档：回环监听保持严格校验，非回环监听（内网部署）只校验端口一致，允许通过主机名、网卡 IP、域名等任意入口访问。浏览器启动令牌通过 URL Fragment 传递，交换为会话后立即从地址栏移除；自动生成的启动令牌仅可使用一次，同一浏览器可凭 Cookie 恢复会话。Windows 权限与锁实现已交叉编译，仍需原生环境验收。

CLI 提供 `serve`、`plan validate`、`plan compile`、`template list`、`run preview`、`run submit`、`report list`、`report show`、`cleanup list` 和 `cleanup resolve`。M1 已接通 36 个模板家族、37 个可信版本快照的静态编排与编译，目录统一由后端提供。内置模板（含普通执行）必须精确匹配本地注册表 ID、版本及内容摘要；自称 `BUILTIN` 或重签快照不能获得内置实现或特权资格。Run 生命周期、离线模拟器、Native SSH、报告归档、Web Run API/SSE、活动服务上的本机 CLI 委托、精确 sudo 策略查询与受控特权执行已实现。sudo 密码可通过 `READYSPEC_SUDO_PASSWORD` 提供；策略拒绝或缺少凭据会在全目标屏障处拒绝整次任务。自动及人工清理会重新核对目标身份和 owner token，人工清理发现活动进程时必须显式确认。不能据静态校验、模拟器结果或编译成功宣称环境 `READY`。旧 Python 原型保留至 M3 验收。

```bash
# 查询当前已实现模板的自包含快照
./dist/readyspec template list --json

# 纯本地静态编译，输出分执行方式的 Goss JSON（Goss 同样支持 JSON 配置）
./dist/readyspec plan compile ./your-plan.yaml
```

当前注册模板覆盖 `H01–H04`、`O01–O04`、`S01–S05`、`N01–N06`、`R01–R05`、`A01–A04`、`D01–D05`、`L01–L03`。R04 默认版本读取目标 PID 的有效限制，额外 `1.0.0-config` 版本读取 systemd Unit 配置并标记为 `CONFIG` 证据。A03 生成特权工件；L02/L03 使用 `STANDARD` 并检查系统日志文件可读性，权限不足返回 `ERROR`。**M1 验收已完成**；采集证据与 M1/M4 范围边界见 [模板验收对应表](docs/m1-template-acceptance.md)。六个目标平台的认证矩阵、真实 SSH 执行及后续里程碑仍按 SPEC 的 M2/M4 计划推进。

H01 架构证据同时保留 `uname_raw`（含原始换行）和 `normalized_architecture`。`amd64` / `arm64` 分别归一为 `x86_64` / `aarch64`；有效但不在允许集合的架构判 FAIL，空、多行或其他格式异常以及命令失败判 ERROR。归一化不会覆盖原始观测。

H03/H04 对 procfs 计数按无符号十进制验证，不接受正负号、小数或指数格式；MemTotal 必须唯一且大于零。Swap 记录需包含有效文件/分区类型、绝对设备路径和合法计数，设备不能重复，已用量不能超过容量；损坏记录返回 ERROR。零容量 Swap 设备仍属于活动设备。容量换算和阈值比较使用精确整数，严格区分 GB 与 GiB。

O01 按 shell 风格的数据赋值解析 os-release，不执行文件或展开变量；重复、缺失关键字段及无法比较的版本返回 ERROR，证据保留 ID/VERSION_ID 的原始赋值与解析值。O02 同时保留内核版本原文、规范化数字版本和发行版后缀；版本范围按数字分段比较，后缀作为证据保留，不推断发行版补丁或预发布版本排序。空、多行或其他格式异常的 uname 输出返回 ERROR。

O03 需提供 `stat` 与 `cat`：缺失 sysctl 键判 FAIL，无法访问判 ERROR，原始值保留在 `value_raw`。数值断言/区间接受十进制整数或小数（可带负号），不接受分数、十六进制、指数及多值观测；显式字符串断言支持文本、空值和同一行向量。O04 使用 `cat` 读取并校验整张模块表，支持 '-' 引用计数与可选 taint 列；损坏记录返回 ERROR，不能误当模块不存在。模块名中的连字符归一为下划线，目标处于加载/卸载过渡状态时返回 ERROR。

S01 通过 Python 3 采集节点及符号链接元数据，显式要求 `python3`，跟随链接时还需 `stat`。证据保留链接节点类型、原始链接目标文本、目标存在性/类型与跟随策略；相对路径和含换行的目标名不会被截断。不跟随时对链接本身判定，悬空链接仍存在；目标无法访问或循环时记录 `target_error`，不将未知状态误作目标缺失。跟随时对目标判定，权限不足或循环返回 ERROR。取证不读取目标内容，各次文件系统观察不是原子快照。

R01 独立记录 systemd 的 `LoadState`、`ActiveState` 和 `UnitFileState`；活动、重载或刷新视为运行，只有 enabled/enabled-runtime 视为启用，static/indirect 等状态不会混同启用。未安装的服务判 FAIL，属性缺失、重复、未知或查询失败判 ERROR；原始状态与判定值同时保留，采集不会启停或启用服务。

R02 使用 `ps -eo pid=,comm=` 采集 PID 与进程名，在验证整张表后按进程名精确匹配并比较数量区间；合法表中的零匹配可按期望数量判定，空输出、损坏记录或查询失败返回 ERROR。证据只保留请求名、匹配数量和表记录数，不包含进程命令行、PID 或无关进程名。该检查针对一次进程表观察，不保证进程在观察后继续存活。

R03 使用 `cat` 完整读取 `/proc/loadavg`，校验三个负载值及队列/PID 字段后，选择 1/5/15 分钟窗口与上限做精确十进制比较；缺字段、损坏数值、多行或访问失败返回 ERROR。证据保留窗口、实测负载与上限。这里的负载表示可运行或不可中断任务的平均数量，不是瞬时 CPU 使用率；目标依赖为 `cat` 和 procfs。

R04 区分指定 PID 的有效 `/proc/<pid>/limits` 与 systemd Unit 配置限制，分别记录为 `EFFECTIVE_STATE` 和 `CONFIG`。PID 路径解析会校验对应限制的软值、硬值、单位及软值不高于硬值，再按要求比较边界；PID 已退出或来源不可读返回 ERROR。`unlimited`/`infinity` 表示无限，满足最低值约束。Unit 配置查询保留 `systemctl` 的原始标量限制值，不等同于目标进程实际继承的限制。

Ansible INI/YAML 支持 `ansible_host`、主机级 `ansible_user` / `ansible_port`、静态组继承，以及组级用户/端口默认值（父组→子组→主机）。同级组冲突须由主机显式消歧；重复主机声明中省略的字段保留已有设置。清单不执行插件、变量模板或脚本，拒绝密码字段及未支持字段。A04 的 AppArmor 支持 enabled/disabled/enforcing/permissive：enforcing 要求所有可观测 profiles 处于 enforce/kill，permissive 要求全部为 complain；混合模式不符合任一单一模式，无法读取 profiles 返回 `ERROR`。SELinux 支持 enforcing/permissive/disabled。

N02 支持整个协议端口或指定 IP 的监听/空闲断言，通过 `ss` 读取 IPv4/IPv6 套接字及 `v6only` 状态，缺工具或无法证实双栈属性返回 `ERROR`。采集会归一 IPv4 映射地址、忽略 `%` 作用域与 users/ino 附加元数据，非法端口、状态或截断记录不当作空闲。

N06 通过 `ip -j route get <target> [from <source>]` 读取实际出接口与网关，缺失设备、多路由或非法 JSON 返回 `ERROR`。接口不符或网关不符判 `FAIL`；创建策略规则/路由表需要额外网络管理权限，不在采集步骤内进行。A03 在隔离配置上用目标真实 `/usr/sbin/sshd -T`（可带 `-C` Match 上下文）读取生效值，查询失败不回退静态配置解析。N03 受控 Python TCP 探测保留连接/DNS/超时原因及耗时；DNS 查询失败不能作为期望不可达判通过。A04 支持 AppArmor 与 SELinux：SELinux 优先读取 `getenforce`，否则读取 `/sys/fs/selinux/enforce`，缺少框架目录时视为 Disabled；非法值或不可读状态返回 ERROR。采集不修改安全模块配置。

N05 的状态码、响应头和正文检查统一使用 Goss command 包装的受控 Python 标准库采集。N03/N05 均显式要求 `python3`，编译工件声明该目标能力，缺少时不能回退到丢失错误原因或无界读取的实现。N04 额外用同一查询读取 DNS 响应头：NOERROR/NXDOMAIN 交由记录集断言，SERVFAIL、REFUSED、截断、超时或畸形响应返回 `ERROR`，因此 N04 也要求 `python3`。`secret_ref` 与通用 `version_constraint` 领域类型明确拒绝，具体模板提供版本上下界及 Debian/RPM 专用比较。

N05 的 `timeout_ms` 是整个探测预算，包含采集子进程启动、DNS、连接/TLS、全部重定向和所需正文读取，不会在每次读数据或重定向时重置。监督进程在超时时终止并回收采集子进程，返回 TIMEOUT；外层 Goss 超时额外预留 2 秒用于启动和结果处理，不扩展请求预算。最多跟随 10 次重定向；整条响应链的状态行/响应头累计最多 64 KiB，与实际读取的正文合计最多 1 MiB（额外读 1 字节仅检测溢出，不作为证据）。超限返回 ERROR；重定向中间正文直接关闭，不自动耗尽；状态/响应头检查不读取无须断言的正文。归一化只保留字节计数、时长、重定向数和正文脱敏摘要。

Linux 的 `make test-collection` 使用 bubblewrap 隔离文件系统和网络，运行原编译工件；工具替身仅提供系统工具接口，真实文件、套接字、HTTP/TLS/DNS 由隔离夹具提供。该目标要求 bubblewrap、iproute2、Python 3、binutils 和标准 Unix 工具，缺失隔离器会失败，不会以跳过冒充验收。

A01/A02 目标需提供 Python 3 与 glibc NSS 接口。查询遵循配置的 NSS 来源，支持本地文件以外的用户和组；A02 同时核对必需用户的主 GID 与组条目中的附加成员。只有经 NSS 确认的 NOTFOUND 才作为不存在，来源不可读、后端不可用或需要重试均返回 ERROR。证据保留身份/成员关系与查询状态，不包含密码、GECOS 或主目录。隔离采集测试会用 C 编译器及 glibc 头文件构建测试 NSS 模块；这些是测试工具要求，目标采集不需要 C 编译器。

D01 分别调用 `dpkg-query` 或 `rpm` 读取安装状态与完整版本，再用对应比较器判断上下界。查询诊断与数据分流：成功时只采信标准输出，失败时单独读取诊断区分“未安装”和数据库/锁错误；dpkg 结果必须是单行状态+版本记录。Debian 版本比较使用真实 `dpkg --compare-versions`（含 epoch、release、tilde 语义）；RPM 比较需目标提供 `rpm`；已在临时解包的真实 rpm 6.0.1 上核对 vercmp 的 epoch/release/tilde 语义，但本地替身查询仍不代表 RPM 发行版 rpmdb 认证。

D02 目标需提供 Python 3。采集只读取文件元数据，分别处理显式路径与 PATH 命令搜索，不打开、加载或运行目标程序。相对路径基于采集进程当前目录，最终路径以绝对形式保留；PATH 的空项/相对目录遵循相同目录上下文。普通文件的存在性、模式与执行权限分别取证，`executable: false` 表示不要求执行权限；缺失/断链/目录或执行权限不符判 FAIL，无法访问或链接循环判 ERROR。不把这项静态检查描述为目标程序必定能启动。

D03 区分显式指定路径与按 PATH 查找到的运行时，先确认它是普通可执行文件，再运行受支持的 `--version`/`-version` 查询并保留版本原文。控制面只接受已知格式的版本记录：Python 3 段式（含预发布/构建后缀）、Node v 段式（后缀保留为证据）、Java 引号或裸版本记号（`1.x` 归一回世代），垃圾后缀、未闭合引号、横幅版本或重复版本行判 ERROR。未安装 FAIL，指定路径缺失、拒读、不可执行或非普通文件分辨 ERROR，查询失败与解析失败分开记录。运行其版本查询需要读取或执行目标解释器，这与 D02 的纯元数据检查不同。

D04 目标需提供 Python 3、`readelf` 和 `ldconfig`。采集仅静态读取目标 ELF 的架构与直接 NEEDED，并检查库缓存候选文件的存在性、ELF 结构和 ABI；不会调用 `ldd`、运行目标程序或加载共享库。缓存名称相同但文件失效、截断、ABI 不符或实际为 PIE 可执行文件均不能通过；无法读取候选文件或工具查询失败返回 ERROR。证据保留目标头、NEEDED 列表、缓存候选路径/flags/状态及库头。该静态检查不等于实际动态加载验收。

R05 分别记录 `timedatectl` 的同步标志和 `systemctl is-active` 的状态/退出码，确认原始输出合法且一致后再判定。支持 chronyd/chrony、systemd-timesyncd、ntpd/ntp；同步状态符合预期且至少一个允许服务活动时通过，合法未活动或同步不符判 FAIL，查询失败、缺工具、多行或状态矛盾判 ERROR。证据保留允许服务的状态摘要，不输出原始诊断。采集不调整系统时钟或启动时间服务。

L01 使用 `cat` 完整读取 `/proc/uptime`，校验单行两个十进制字段后，将 uptime 秒数与最短/最长要求做精确比较；缺字段、额外字段、多行、NUL 或读取失败返回 ERROR。证据保留 uptime 与 idle 秒数及界限；idle 字段只用于记录，不参与判定。

D05 的 JSON/YAML 键路径遵循 RFC 6901：`~1` 表示 `/`、`~0` 表示 `~`，数组使用无前导零的十进制索引。非法转义、前导零或非数字索引返回 ERROR，键不存在或索引越界返回 FAIL；env/ini 键按字面匹配。`expected` 按 JSON 类型严格比较，敏感值只保留脱敏摘要。

L02/L03 除事件查询外，还审计未按内核/服务筛选的系统日志游标序列。只有窗口边界与连续性均有证据时，零事件才能通过；日志序号缺口、消息丢失提示、不同序号域无法衔接、读取失败或诊断警告均不能作为完整覆盖。事件和审计分别最多读取 10000 条，达到上限返回 ERROR（已发现超限事件仍可 FAIL）。`allow_since_boot` 仅在本次启动晚于要求窗口起点、启动后 60 秒内有可读证据且审计连续时允许缩短窗口；该选项不会忽略中间缺口。L02 使用内核 transport 条件查询所有可读启动的窗口记录；结果保留覆盖与连续性原因，不包含原日志正文。

S02/S03 会保留未声明为业务要求的文件身份与挂载元数据，并标记为 `observation_only`。S04/S05 目标需提供 GNU `stat`、`cat` 和可读的 `/proc/self/mountinfo`；采集只读文件系统计数，报告总量、非特权可用量、inode 比例、挂载点及精确文件系统类型。容量判定使用 `f_bavail`，不会把保留块计入非特权可用空间，也不会通过写测试文件测量空间。

自定义开发工作区可使用 `./dist/readyspec serve --workspace ./.readyspec_workspace --no-browser`。环境变量 `READYSPEC_WORKSPACE` 优先于命令行参数；同工作区已有服务时，第二个服务返回 `16 WORKSPACE_BUSY`。当前纯静态 `plan validate` 不需获取工作区锁，其他 CLI 的服务委托能力尚待实现。

前端开发与浏览器验证见 [`web/README.md`](./web/README.md)。`internal/webui/dist/` 保存可直接嵌入 Go 的构建产物，修改前端源码后须重新运行 `make web` 或 `make build`。

---

## 核心定位

ReadySpec 解决企业级软件交付中的高频协作断层：**软件研发清楚系统需要什么样的运行环境，现场部署与运维需要一套单文件离线可携、零目标机预装依赖、证据可追溯、结论机器可判的部署前门禁工具。**

### 核心特性

- **零依赖单文件交付（Windows + Linux 三架构）**：
  - 管理端编译为 `CGO_ENABLED=0` 独立单二进制：`windows/amd64`（`readyspec.exe`）、`linux/amd64`、`linux/arm64`。
  - 二进制内部直接内嵌 Web UI 静态资源、JSON Schema 以及经 SHA-256 锁定的**官方原装 `Goss v0.4.9`（`linux-amd64` 与 `linux-arm64`）**；目标主机仅需为具备 SSH 与 POSIX `/bin/sh` 的 Linux 服务器，无需预装 Python、Goss、Docker 或常驻 Agent。
- **双角色闭环工作流**：
  - **方案作者**：在 Web 方案工作台中配置全局 **环境与拓扑要求（`TopologyPolicy`）**，按 **节点角色（`Role`）** 挑选八大领域的 **检查模板（`CheckTemplate`）** 或编写自定义 Shell 生成 **检查项（`CheckItem`）**，开放允许现场修改的 **现场可调参数（`OverridableParam`）**，支持发起 **草稿试跑（`TrialRun`）**，并发布导出带 **RFC 8785 (JCS) SHA-256 语义摘要** 的自包含 **检查方案（`Plan`）**。
   - **部署工程师 / 运维人员**：通过 Web UI 或 CLI 导入已发布检查方案，录入 **主机清单（`Inventory`）**（原生支持快速 IP 表与 Ansible INI/YAML），分配节点角色并填写 **现场参数值（`FieldOverride`）**，查看零 SSH 静态预览与特权/`root` 执行提示后执行检查，无须额外权限批准；获取按 **取证方式（`EvidenceMode`）** 归档的 **检查报告（`RunReport`）** 与 **就绪结论（`Verdict`）**。
- **「内嵌 Web UI + 非交互 CLI」自适应双模**：
  - 无参启动或 `readyspec serve` 默认在 `127.0.0.1:18480` 启动受保护的本地 Web 控制台（亦支持显式指定内网监听并强制访问令牌鉴权）。
  - 非交互 CLI 在检测到同工作区运行中的 `readyspec serve` 时自动通过本地受控 API 委托执行（Web UI 可实时同步观察进度）；未启动服务时直接获取跨平台文件锁（Linux `flock` / Windows `LockFileEx`）在进程内独立完成任务。
- **双执行器与严苛安全边界**：
  - 同时内置基于 `golang.org/x/crypto/ssh` 的真实原生 SSH 执行器与供无目标机预演使用的离线仿真执行器（`mock_simulator`）。
  - 区分 **正式门禁（`GateRun`）** 与 **草稿试跑（`TrialRun`）**、**全量检查（`FullRun`）** 与 **局部复测（`PartialRun`）**；仅 `RELEASED` 方案在真实目标主机上完成的全量正式门禁检查可产出放行级 `READY`（退出码 `0`）。
  - 短期 SSH/Sudo 密码仅驻留内存并在任务终态立即覆写销毁，绝不落盘；异常中断遗留的临时目录纳入 **远端残留清理（`RemoteCleanup`）** 归属核验与处置。
- **纯宽松开源许可（零 `GPL / LGPL / AGPL`）**：
  - 全栈依赖严格限定在 `Apache-2.0 / MIT / BSD / ISC`（详见 [`NOTICE`](./NOTICE)）。

---

## 命令入口与门禁退出码基线

### 1. 启动 Web UI 控制台

```bash
# 默认仅监听 127.0.0.1:18480 并自动在桌面浏览器完成安全握手
./readyspec

# 在跳板机显式监听指定地址并启用强制访问令牌鉴权
READYSPEC_AUTH_TOKEN="<at-least-32-character-random-token>" ./readyspec serve --listen 0.0.0.0:18480 --no-browser
```

### 2. 非交互 CLI 与 CI/CD 门禁命令

```bash
# 查询八大领域 36 项预置检查模板与用户公共模板
./readyspec template list

# 静态校验检查方案 YAML（零网络连接）
./readyspec plan validate ./order-platform-1.0.0.yaml

# 执行前零 SSH 静态预览（核对目标主机数、候选检查项数与特权/root 风险）
./readyspec run preview \
  --plan order-platform@1.0.0 \
  --inventory "web-01=10.20.1.11,web-02=10.20.1.12,db-01=10.20.2.21" \
  --role-map web=web-01,web-02 --role-map db=db-01

# 提交正式门禁全量检查（核对期望方案摘要与现场参数值，按需提示 root/sudo 执行）
./readyspec run submit \
  --plan ./order-platform-1.0.0.yaml \
  --expected-plan-digest sha256:... \
  --inventory ./hosts.ini \
  --field-overrides ./field-overrides.json

# 目标要求 sudo 密码时，从任务环境提供；不要求 sudo 密码时无需设置
READYSPEC_SUDO_PASSWORD="$SUDO_PASSWORD" ./readyspec run submit --plan ./order-platform-1.0.0.yaml --inventory ./hosts.ini

# 查看待清理残留；使用 --run-id/--cleanup-id 并提供新凭据处置
./readyspec cleanup list
```

### 3. CLI 门禁退出码契约

下表为摘要，具体优先级以 [`SPEC.md` §10.3](./SPEC.md#103-cli-命令树与确定性门禁退出码推导规则) 为准。门禁放行规则适用于 `run submit`；`plan validate` 等命令返回 `0` 仅表示该命令成功，不代表目标环境 `READY`。

| 退出码 | 标识符 | 判定规则（按严格优先级顺序） |
| :---: | :--- | :--- |
| `15` | `AUTH_ERROR` | 方案摘要不匹配、本地服务鉴权拒绝，或执行前校验发现必要 SSH/sudo 凭据缺失、认证失败或提权权限不足；root/sudo 执行仅提示，无须额外批准 |
| `16` | `WORKSPACE_BUSY` | `--no-wait` 下工作区排他锁已被占用，或检查任务等待队列已满 |
| `12` | `PLAN_ERROR` | YAML 语法/重复键错误、Schema 校验失败、现场参数值越权/冲突、或 Goss 资源属性编译矛盾 |
| `13` | `INFRA_ERROR` | 本地工作区持久化故障、内嵌二进制完整性自检失败等控制面致命错误 |
| `14` | `CANCELLED` | 检查任务被用户或信号主动取消（`RunStatus == CANCELLED`） |
| `10` | `NOT_READY` | 检查任务未取消，且 `Verdict == NOT_READY`（至少一项必检检查项或 `TopologyPolicy` 明确判定 `FAIL`） |
| `11` | `UNKNOWN` | 检查任务未取消，且 `Verdict == UNKNOWN`（无明确必检 `FAIL`，但含 `ERROR`/`UNSUPPORTED`，或属于 `TrialRun` / `PartialRun` / `mock_simulator`） |
| `0` | `READY` | **唯一自动放行信号**：`RELEASED` 方案 + `GateRun` + `FullRun` + 真实 SSH 执行完成且 `Verdict == READY` |

---

## 核心规范与设计文档

- **标准领域词汇表**：[`GLOSSARY.md`](./GLOSSARY.md)（全仓概念定义与禁用旧词表）
- **产品与技术规格**：[`SPEC.md`](./SPEC.md)（架构、模型、36 项预置模板、安全边界、RTM 与 `F01–F14` 验收基线）
- **Web UI 设计规范**：[`DESIGN.md`](./DESIGN.md)（DevOps 控制面视觉令牌、排版与组件规范）
- **开源许可与声明**：[`LICENSE`](./LICENSE)（Apache-2.0）与 [`NOTICE`](./NOTICE)
