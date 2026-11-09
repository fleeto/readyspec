# M4 验收矩阵与环境清单

状态：**评审通过；AWS Linux 非压力补测及资源回收完成，Windows11待提供VM**；通过 pi 调用 magpie 的 K3.1 Pro 完成两轮评审，首轮两项阻断均已关闭。计划评审与本地回归不代表 M4 实机执行或验收完成。设计基线：`main@d370b54`。依据：`SPEC.md` §12–17、`DESIGN.md` 的 ReadySpec 适用范围，以及 M1/M2/M3 验收记录。当前实测与资源状态见[执行记录](m4-aws-canary-20261108.md)，下方早期本机盘点保留为历史。

## 1. 目标、边界与退出条件

M4 验证三个管理端单二进制、六个 Linux 目标组合、五个端到端场景及标准负载。输出可追溯的实测记录后，才能关闭 TODO-01/03/04、签署 F02/F03/F12/F14 和整体验收 F01–F14；本计划评审通过不表示这些门槛已通过。

不增加自动修复、常驻 Agent、用户管理或多工作区产品能力。TODO-02 单独验证原装 Goss 的自定义结构化结果回收；未完成前 `readyspec.custom_result/v1` 保持禁用，基础 Shell 断言继续使用 M3 契约。TODO-02 可以以“不可行，保持禁用”的证据结案，不以启用高级模式为 M4 必选功能。

用户后续已授权30美元执行成本上限下的AWS Linux资源准备、验收与回收，具体身份、双标签资源边界、镜像恢复与网络限制见[完整Linux计划](m4-aws-full-linux-plan.md)及执行记录；允许专用VM的单目标功能性故障/中断/恢复。该上限不是AWS硬性花费限制。压力负载、压力注入、性能计量与20×100集群排除，Windows11等待用户提供VM；不能据此缩减原M4签署门槛或宣称整体完成。

退出条件：全部必测单元有证据且符合预期，F01–F14 逐项签署，六目标各有完整模板覆盖表，Windows 11 有原生全链路证据，性能报告符合 §7，所有发布阻断缺陷关闭并回归。任何必测 `BLOCKED`、未解释 `SKIP` 或只有模拟证据的实机格子均阻止宣称 M4 完成。

## 2. 状态、证据与版本规则

- 状态统一为 `NOT_RUN / BLOCKED / PASS / FAIL`；不适用的模板分支必须有理由、反向分类证据及对应其他环境的覆盖链接，不得以 `N/A` 跳过整个模板或平台。
- 测试预期为业务 `FAIL/ERROR/UNSUPPORTED/SKIP` 时，测试本身可以 `PASS`，前提是分类、证据、RunStatus、Verdict 和 CLI 退出码全部匹配；不能把目标全绿当作唯一验收标准。
- 每次运行绑定唯一 case ID、完整 commit SHA、管理端产物 SHA-256、嵌入 Goss 两架构摘要、方案摘要、工具版本、环境 ID、开始/结束时间（含时区）、命令与退出码、run_id、预期/实际、证据路径和执行/复核人。归档报告使用原始 `readyspec.report/v1`，不由 Web 截图重算结论。
- 产物冻结后跨平台复用同一版本；修复形成新候选版本，旧证据保留且注明失效范围。直接受影响用例、六平台基础 smoke、三管理端 smoke 及完整发布回归必须在最终候选版本上重跑；平台全量和性能证据若非最终版本，必须给出逐项不受影响分析并由复核人签署，否则重跑。
- 工作目录建议 `artifacts/m4/<candidate>/<case>/<attempt>/`（执行时创建并排除 Git 跟踪）。保留脱敏命令输出、报告、事件、测量序列与校验清单。私钥、密码、Agent endpoint、会话 Cookie、CSRF/token、owner token 不进入共享证据；私有 Run Journal 留在受限工作区，导出前脱敏。
- 签署表字段：`case_id | env_ids | candidate_sha | expected | actual | status | evidence | defect | executor | reviewer | timestamp`。尚未配置的实名、镜像、地址、凭据引用、证据均留空或 `TBD`，不得填写虚构资产。

## 3. 环境清单

### 3.1 管理端与构建环境

| ID | 环境要求 | 建议资源 / 用途 | 当前状态 |
|---|---|---|---|
| B01 | Linux amd64 构建/测试环境；Go 版本遵循 go.mod（当前 1.26.0），Node 24，npm lockfile；Go race 所需 C 工具链；bubblewrap/iproute2/binutils/Python 等测试依赖 | ≥4 vCPU / 8 GiB / 30 GiB；生成、检查三目标产物与前端，测试工具只在构建环境使用 | 需求已定义，具体资源 TBD |
| M01 | Linux amd64 原生管理端；普通用户、全新受限工作区、浏览器 | 2 vCPU / 4 GiB / ≥20 GiB；主要六平台认证与正式压测，负载期间不兼任目标或构建机 | TBD |
| M02 | Linux arm64 原生管理端；不采用 QEMU 模拟来签署原生运行 | 2 vCPU / 4 GiB / ≥10 GiB；Web、CLI、锁、SSH、故障恢复 | TBD |
| M03 | 原生 Windows 11 amd64；NTFS、普通用户会话、第二个隔离测试账号、Edge/Chromium、Windows OpenSSH Agent | ≥2 vCPU / 4 GiB / 30 GiB；DACL、LockFileEx、Named Pipe Agent、浏览器与实机 SSH | TBD |

M01/M02 的发行版、内核与文件系统必须登记；管理端 Linux 发行版不等同于六目标认证范围。M03 记录 Windows 版本/build、浏览器与 OpenSSH 版本。Windows Server CI 不能代替 Windows 11 实测。

构建机可联网取得已有锁文件对应依赖；正式运行环境预置后禁止访问外网。部署到管理端只需要对应单二进制、方案与独立输入资料；浏览器不需要外部 CDN。运行 smoke 不使用 Go/Node/npm/Python 等管理端开发工具，验证无外部静态资产依赖。SSH 配置和 known_hosts 属于运行输入，不能把“单二进制”解释为不需要身份材料。

### 3.2 六个目标组合及镜像变体

| ID | 发行版 | 原生架构 / Goss 资产 | 必备镜像变体 |
|---|---|---|---|
| T01 | Ubuntu Server 24.04 LTS | x86_64 / linux-amd64 | BASE、MIN、FULL、FAULT |
| T02 | Ubuntu Server 24.04 LTS | aarch64 / linux-arm64 | BASE、MIN、FULL、FAULT |
| T03 | Debian 13 | x86_64 / linux-amd64 | BASE、MIN、FULL、FAULT |
| T04 | Debian 13 | aarch64 / linux-arm64 | BASE、MIN、FULL、FAULT |
| T05 | Rocky Linux 9 | x86_64 / linux-amd64 | BASE、MIN、FULL、FAULT |
| T06 | Rocky Linux 9 | aarch64 / linux-arm64 | BASE、MIN、FULL、FAULT |

每个组合建议 ≥2 vCPU / 2 GiB / 10 GiB，是专用 VM 或实机，具有真实内核、systemd、SSH、独立 machine-id/boot-id 与主机密钥。容器、回环 SSH、模拟器可补充自动测试，不能代替这六格。镜像变体可在同一 VM 用快照顺序执行，不要求同时存在 24 台 VM；状态切换必须留镜像/快照 ID 和恢复记录。

| 变体 | 准备与目的 | 禁止混淆的边界 |
|---|---|---|
| BASE | 登记 image ID/digest、补丁版本、CPU/内核/libc、时钟、挂载、SSH 主机公钥可信来源及网络规则；保留可恢复快照 | 云镜像“最小化”标签不证明无 Python/Goss/Agent；需实际盘点 |
| MIN | 无 Python、无预装 Goss、无 ReadySpec Agent；保留 sshd、POSIX sh 及执行生命周期需要的普通系统工具与身份文件；校验工具分 sha256sum-only、openssl-only、全部缺失子变体 | F03 只要求执行基础原生检查的投递/验真/回收/清理闭环；不要求依赖 Python 的所有模板在 MIN 上 PASS |
| FULL | 为 36 模板配置明确依赖与可控事实：deb/rpm、Python3、ip/ss、systemctl/journalctl、NSS、sshd、readelf/ldconfig、运行时、时间同步等；按模板注册表 requires 与实际编译器核对版本 | 安装依赖是验收夹具准备，不是 ReadySpec 运行时自动安装；记录准备前后差异 |
| FAULT | 从快照派生 noexec/只读候选目录、受限 sudo/日志、异常网络与进程、损坏 journal 副本等；所有修改仅在专用 VM | 禁止修改生产、宿主挂载/sudoers、真实业务日志；使用独立测试 sshd，保留恢复通道 |

### 3.3 身份、辅助服务与负载集群

| ID | 需求 | 验收用途 |
|---|---|---|
| I01 | 普通无 sudo 用户；NOPASSWD sudo 用户；需独立密码 sudo 用户；受限命令白名单用户；root；别名但实际 UID=0 用户 | 身份与提权组合；均为隔离夹具，不在报告保存秘密 |
| I02 | 测试私钥、Unix SSH Agent、Windows Named Pipe Agent、短期密码；正确/错误/缺失组合；可信 known_hosts 与预置轮换主机密钥 | 一个 Run 使用一种认证方法/公钥身份；不同方法分 Run 测试，按主机仅覆盖密码/sudo 输入 |
| S01 | 内网 DNS（A/AAAA、多地址）、TCP、HTTP/HTTPS 测试服务，测试 CA/坏证书；LDAP→SSSD→NSS；Java/Python/Node；chrony/timesyncd；本地有效/轮转/损坏 journal | 无公网探测；接口、路由、服务日志、运行时、NSS 等可控正反样本。辅助服务不部署在受测管理进程所在 M01 |
| P01–P20 | 20 台同时可达且身份各异的真实 Linux VM/实机，建议每台 ≥1 vCPU / 1 GiB / 5 GiB | 正式 20×100；可复用 T01–T06 再补 14 台，运行前恢复干净基线；禁止同一主机的 20 个别名冒充 20 台 |
| N01 | 内网 SSH、DNS、HTTP(S)、NTP/辅助服务连通；可按测试主机隔离丢包/断线；测量 RTT、丢包与带宽 | 预置依赖后封禁公网，保留可信管理通道和计量通道；压测不依赖公网 DNS/HTTP |

每台资产登记：`env_id、实际资源引用、负责人、OS/image/arch、CPU/RAM/disk、管理端或目标角色、网络域、快照、工具能力、身份配置、known_hosts 来源、清理负责人、可用状态`。地址与凭据引用保存在执行工作区受限清单，Git 文档只留非秘密的环境别名。§3.4 记录本轮核对结果；未登记的 M01–M03、T01–T06 资产仍按 BLOCKED 处理，不假设历史 VM 仍存在。

### 3.4 本轮本机环境核对（2026-11-07）

| 项目 | 观察结果 | 状态 / 限制 |
|---|---|---|
| B01 候选执行机 | Linux x86_64；Go 1.26.8、Node v26.0.0、npm 11.19.0；`gcc`、`bwrap`、`ip`、`readelf`、`python3`、`sshd`、`sudo` 可用 | Go/Web 测试、格式检查和三目标构建通过；Node 版本高于计划建议的 Node 24，且隔离采集器中的路由/黑洞用例因 `RTNETLINK answers: Operation not permitted` 未通过，不能登记 B01 全通过 |
| M01 | 当前工作区宿主环境与 B01 候选重合 | 未核验独立普通用户、受限全新工作区及浏览器运行条件，不能充当正式 M01 |
| M02 | 未发现原生 Linux arm64 执行环境 | BLOCKED；交叉编译不替代原生运行 |
| M03 | 当前为 Linux；仓库 `d370b54` PR workflow 的 `check (windows-latest)` job 通过 | GitHub-hosted `windows-latest` 当前映射为 [Windows Server 2025](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)，不是 Windows 11；DACL/LockFileEx/Named Pipe Agent 的 M03 实机验收仍 BLOCKED |
| T01–T06 | 本机 Docker 镜像清单未发现 Ubuntu 24.04、Debian 13 或 Rocky Linux 9 目标镜像；无已登记专用 VM/实机及可信 SSH 身份材料 | 六目标均 BLOCKED；不得用通用容器或回环夹具替代平台签署 |
| 网络隔离用例 | `bwrap` 存在，但路由配置测试需要的网络管理权限不可用 | 相关采集器用例 BLOCKED，未修改宿主路由或挂载 |
| 浏览器 E2E | M3 验收记录为 Playwright 7/7 通过；GitHub Actions 在 `d370b54` 的 PR workflow 中 browser job 通过。本机本轮 7 项因缺少 Chromium headless shell 未能启动；系统 PATH 未发现 Chromium/Chrome/Firefox | 历史与 CI 证据保留；本机重跑 BLOCKED，没有下载浏览器或写入用户缓存 |

可复核的本地结果：`go test ./...`、`go test -race ./...`、Web Vitest 23 项、Web Prettier、`go vet ./...`、`make build-all` 与回环 SSH STANDARD/PRIVILEGED 纵向测试均通过。GitHub Actions `d370b54` PR workflow 的 `check (windows-latest)` job 通过；该标签映射到 Windows Server 2025，CI 通过不等于 Windows 11 实测。浏览器历史通过证据见 `docs/m3-implementation-design.md` §12；同一 SHA 的 PR workflow `browser` job 通过。本机重跑命令为 `TMPDIR=/tmp/opencode npm --prefix web run test:e2e -- --output=/tmp/opencode/readyspec-e2e-output`，7 项因缺少浏览器未能启动。`make test-collection` 已执行，但因上述网络能力限制整体失败；失败子项为 policy routing、TCP 多地址和黑洞路由夹具，其余已运行采集器用例通过。没有运行压力协议。

### 3.5 AWS 资源可行性（未创建资源）

用户当前授权范围是除 Windows 11 与压力测试外的资源准备和验收。具体自建网络、镜像与 Linux 实机的步骤、成本/清理边界和权限 JSON 见 [完整 Linux AWS 资源计划](m4-aws-full-linux-plan.md)，Pi + Kimi 3.1 Pro 记录见 [完整范围评审](m4-aws-full-linux-review-k3.1-pro.md)。旧版窄范围计划只保留历史，不再要求管理员预置网络和镜像。Windows 11 相关管理端/用例保留待测；权限模板评审与本地语法验证不代表已附加真实账号策略。

AWS EC2 可作为 M4 的候选承载平台：M01 可用 x86_64 EC2，M02 可用原生 Graviton arm64 EC2；Ubuntu 24.04、Debian 13 和 Rocky Linux 9 均有对应架构的 AMI，可组成 T01–T06。每格仍须选定区域/实例类型、固定 AMI ID、记录快照和可信 SSH 主机密钥，并确认镜像内容满足 BASE/MIN/FULL/FAULT 定义。官方镜像可用性不代表本账户区域配额、访问策略或费用已核验。

M03 不能用 Windows Server EC2 或 GitHub `windows-latest` 代替 Windows 11。可评估 Windows 11 Enterprise BYOL 运行于 EC2 Dedicated Host/Dedicated Instance，并先核实组织持有的 VDA/Windows 使用权、区域支持和主机成本；AWS WorkSpaces BYOL 也是候选，但其专用硬件及区域最低用量可能不适合单台验收机。依据：[AWS Microsoft workload licensing](https://docs.aws.amazon.com/prescriptive-guidance/latest/migration-microsoft-workloads-aws/licensing-microsoft-workloads.html)、[AWS Dedicated Hosts](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/dedicated-hosts-overview.html)、[WorkSpaces Windows BYOL](https://docs.aws.amazon.com/workspaces/latest/adminguide/byol-windows-images.html)。尚未检查 AWS 登录身份、账号配额、区域 AMI ID 或报价，也未调用 AWS API、启动实例或产生费用。

## 4. 平台矩阵与认证分层

### 4.1 三管理端 × 六目标基础闭环（18 格全部必测）

| 管理端 \ 目标 | T01 | T02 | T03 | T04 | T05 | T06 |
|---|---|---|---|---|---|---|
| M01 linux/amd64 | PASS | PASS | PASS | PASS | PASS | PASS |
| M02 linux/arm64 | PASS | PASS | PASS | PASS | PASS | PASS |
| M03 windows/amd64 | NOT_RUN | NOT_RUN | NOT_RUN | NOT_RUN | NOT_RUN | NOT_RUN |

上述Linux 12格记录首次正式私网基础闭环：GATE/FULL、READY/0、每格三项PASS及CLEAN，原始报告见[证据索引](evidence/m4-20261108/index.json)。使用boot-fix候选；后续root审计、sudo/Agent拒绝分类及Web提示修复形成新候选，最终候选回归与完整签署仍待完成。M03未执行，不能将12格通过签署为18格完成。

case ID：`M4-PLAT-Mxx-Txx`。每格使用 FULL 变体、最终候选单二进制与 `native_ssh`，完成可信主机校验、架构发现、原装 Goss 正确选型/摘要、S01 普通检查和 A03 可信特权检查、结果归档/CLI 与 Web 同源读取、远端清理。使用私钥认证和已配置 NOPASSWD sudo；对 root 单独用例不要求 sudo。记录实际登录 UID 与特权子进程身份事实，不用报告 login_uid 代替后者。

### 4.2 六目标完整认证（M01→T01–T06，每格必测）

每个目标的 FULL 变体必须覆盖所有 36 模板，不能用跨发行版抽样替代。为每个 `Txx/模板ID` 建立六类用例子表：PASS、FAIL、边界、异常、现场覆盖、资源合并后映射；模板ID全集为 H01–H04、O01–O04、S01–S05、N01–N06、R01–R05、A01–A04、D01–D05、L01–L03。已有原工件测试可复用夹具定义，但必须补本平台真实 SSH/原装 Goss 的执行与报告证据。

一平台不适用的分支（如 deb/rpm、SELinux/AppArmor）记录实际能力和正确的分类，不把缺能力伪装成 PASS；同一模板在该平台至少有适用正/反样本或明确的规格支持结论。若模板依赖可合理安装，应在 FULL 准备并验证；不能因默认镜像缺包直接豁免。不可提供必要事实时标记 BLOCKED 并阻断 TODO-01 签署。

重点取证：R04 查询目标进程/服务限制、A03 使用实际 sshd -T、D04 不执行被检 ELF/ldd；A01 验证 NSS；N01/N06 用受控接口/路由；R05 验证本发行版的时间服务；L02/L03 用真实 journal 覆盖连续窗口、缺口、拒读、轮转与损坏副本。自定义 Shell 另测单次执行计数、确定性启动帧剥离、普通 exit126/127、无/坏帧 ERROR、64 KiB 边界，不启用 TODO-02 协议。

### 4.3 最小目标、受限目录与工具差异

| case | 环境覆盖 | 必须观察的行为 |
|---|---|---|
| M4-MIN-01 | 六目标 MIN，M01 执行 | 无 Python/Goss/Agent 预装；使用 H01（仅需 uname，allowed_arch 与实机一致）+ 基础 Shell（POSIX printf 常量并 exit 0，无外部解释器），取得有效规范化 PASS 结果并完成投递、验真、回收、清理；RELEASED/GATE/FULL 及必检/拓扑全部满足时才可 READY/0；不能为执行器补 Python |
| M4-MIN-02 | 六目标 MIN，各 checksum 子变体 | sha256sum-only 与 openssl-only 均闭环成功；两者均缺失安全拒绝，零 Goss 执行；不能用 shasum 成功替代 openssl-only |
| M4-MIN-03 | 六目标 MIN，M01 执行，独立于成功 Run | S01 缺少 python3 的反例，预期 ERROR 及缺依赖/取证诊断，不得静默 PASS、不得 READY，不作为 F03 成功证据；S01 的完整六类认证仍在 FULL，36模板覆盖不减少 |
| M4-TMP-01 | 六目标 FAULT，M01 执行，分别登记 STANDARD 与含 A03 的 PRIVILEGED | 对每种模式按下方顺序验证显式路径、noexec/只读回退及候选全部受限；分别归档选中目录、实际执行方式、Goss 执行计数、权限/验真与清理证据，不修改挂载 |
| M4-TOOLS-01 | 六目标 FULL/FAULT | 包查询、时间服务、NSS、日志权限/覆盖缺口、缺 readelf 等依赖；正/负结果符合 EvidenceMode，无静默降级 |

M4-TMP-01 依 SPEC §8.2 固定候选顺序：STANDARD 为 `显式指定 → /tmp → /var/tmp → $HOME/.cache`，PRIVILEGED 为 `显式指定 → /run → /var/tmp → /tmp`。每个目标逐层限制前置候选，分别制造 noexec 和只读条件，证明逐一回退而非只测某个可写路径；另覆盖 `/tmp` 单独 noexec/只读，若 PRIVILEGED 已选 `/run`，应保持正确选择而不是伪造发生回退。分别验证显式有效路径优先、无效路径后续受控回退、所有适用候选受限时报 ERROR 且该模式 Goss 执行计数为0。混合 Run 中已有 STANDARD 结果可以保留，不将 PRIVILEGED 拒绝误记为整次零执行。普通目录保持 0700 与归属令牌，特权目录保持 root:root 0700、root 二次验真、env -i 与清理；PRIVILEGED 同时覆盖 root 直连及非 root NOPASSWD sudo（含 sudo -k）。所有挂载/权限变更仅由夹具准备与恢复，ReadySpec 本身不得修改挂载。若候选配置入口尚未实现，同样列为 GAP-01 待修复，不以省略显式路径用例规避。

## 5. 安全、协调与端到端验收

### 5.1 登录/提权与安全矩阵

不是要求所有维度无差别全笛卡尔积；以下明确最低覆盖，不允许自行减少：六目标各测私钥 + I01 全部登录/提权类型；M01/M02 各测 Unix Agent，M03 测真实 Windows OpenSSH Named Pipe Agent；三管理端各测密码认证正确/错误/缺失，并各跨至少一台 amd64 和一台 arm64 目标。认证/提权拒绝的全主机屏障用六目标混合清单，其中一台拒绝，验证所有目标零投递/零检查执行。

| case | 输入/触发 | 判据与主要门槛 |
|---|---|---|
| M4-SEC-01 | 普通无 sudo，且方案仅 STANDARD；加入可信内置 PRIVILEGED；再以用户 Shell 伪造 PRIVILEGED | 首者无需 sudo 密码；缺权限前置拒绝 15，所有主机无检查投递/执行；伪造特权被静态拒绝，不执行用户 Shell（F08/F10） |
| M4-SEC-02 | NOPASSWD、需密码、错误/缺失密码、受限白名单、sudo 查询无法核实 | 可用权限成功，不多要密码；明确拒绝 15；无法核实为 UNKNOWN/11（无更高优先级且有效报告），均全主机零投递/检查（F10） |
| M4-SEC-03 | root、其他用户名实际 UID=0；内置与自定义 Shell | 仅提示、不额外确认或要求 sudo 密码；不调用 sudo；high_risk_root_audit=true；审计实际 UID（F10） |
| M4-SEC-04 | 错误/缺失认证材料、Agent 不可用；host key 未知/变更；同一实机多个别名 | 拒绝错误身份，无自动接受 key 或跳过验证；认证/摘要失败 15，其他错误严格按 SPEC §10.3；证据无秘密（F10/F12） |
| M4-SEC-05 | 篡改 Goss/规则；特权执行环境；模板/参数注入 | 摘要不符先于执行拒绝；root:root 0700 隔离、root 二次验真、env -i、非 root sudo -k；Shell 参数只经安全只读环境变量；无 sudo 用户 Shell（F10） |
| M4-SEC-06 | Web 错 Origin/Host/CSRF、无访问令牌的非回环请求、非 TLS 密码输入 | 拦截越权；正确浏览器握手成功；显示传输风险，提交清除明文；运行时零外网请求（F07/F10） |
| M4-SEC-07 | Linux 其他账号；Windows 第二账号、工作区锁/Named Pipe 非可信端点 | Linux 0700/0600；Windows LOCALAPPDATA DACL 实际拒绝他人；同工作区二控制器互斥；Agent round-trip 和不安全端点/错误服务身份拒绝（F09/F10/F12） |

M4-SEC-05 的真实特权路径覆盖六目标；M4-SEC-06/07 覆盖三管理端。Windows 必须使用原生 Windows 11，不能把 WSL、交叉编译或 Server CI 结果登记为 TODO-03 完成。秘密不落盘的验证扫描任务专用工作区/日志和浏览器请求，不扫描无关个人目录；进程内存清零通过已有行为测试加受控夹具证明，不宣称从日志扫描可证明全部内存状态。

### 5.2 五个端到端场景（SPEC §15.2）

| case | 场景 | 执行范围与证据 |
|---|---|---|
| M4-E2E-01 | 作者交付 | 三管理端 Web：web/db 角色、TopologyPolicy、内置+局部 Shell、现场可调参数、自测、真实 TrialRun、发布、导出摘要；试跑不产生 READY |
| M4-E2E-02 | 跨机器离线导入 | M01→M03→M02→M01 导出/导入环；每站全新工作区、断公网、无外部模板文件；摘要一致、同版异摘要/伪造特权拒绝；静态导入零命令/SSH |
| M4-E2E-03 | 混合发行版双架构实检 | M01 对六目标同一清单，普通后特权串行；PASS/FAIL/ERROR 与合并映射正确；M02/M03 基础跨架构路径由18格覆盖 |
| M4-E2E-04 | CLI 门禁 | 三管理端独立直跑与服务委托，覆盖 0、10–16；包含 PartialRun 必检 FAIL→10、局部/试跑全绿→11、编译冲突12、无法归档13、取消14、摘要/认证15、no-wait锁16；比较 CLI/Web 同一归档报告 |
| M4-E2E-05 | 故障与清理 | 三管理端各在投递后、执行中、清理前三窗口断线/强杀；不自动重跑有执行标记模式，恢复证据/取消状态；真实远端活动进程拒绝未确认终止、确认后 CLEAN、拒绝身份/token/符号链接越界、IGNORE 必填原因并可读审计 |

E2E-05 覆盖 amd64/arm64 目标；每个窗口对管理端强杀与 SSH 断线分别留证。用受控屏障/观测阶段同步故障，不靠猜测 sleep 时刻。保留精确执行次数哨兵，验证重启无二次执行；未知归属不强制删除，重启导致 boot-id 变化也必须安全拒绝。M3 浏览器 CLEAN 注入只证明确认框，不能替代本轮真实进程确认/终止测试。

### 5.3 F01–F14 签署索引

| 门槛 | M4 对应证据 |
|---|---|
| F01 | §4.2 六平台36模板六类用例、TopologyPolicy 三项核对、M4-E2E-01/03 |
| F02 | §4.1 18格 + §4.2 六平台完整认证；TODO-01 |
| F03 | M4-MIN-01/02、M4-TMP-01 |
| F04 | 编译确定性/冲突/PropertyOrigin 自动测试；六平台合并映射、组级冲突/同机多角色实检 |
| F05 | 五态自动测试、六平台反向取证、Shell 启动/输出边界 |
| F06 | Verdict 自动测试、M4-E2E-03/04：空必检、拓扑失败、试跑/局部/模拟与正式门禁 |
| F07 | 三管理端浏览器回归 + M4-E2E-01/02、M4-SEC-06；DESIGN 深浅色、键盘、缩放/断点与状态语义人工复核 |
| F08 | M4-E2E-01/02、M4-SEC-01/05，静态导入零副作用与发布清单/冲突 |
| F09 | §7 满队、5主机并发与串行模式；三管理端委托/锁/幂等/冻结输入与SSE |
| F10 | §5.1 全矩阵、MIN/TMP、清理越界、三管理端秘密与权限证据 |
| F11 | M4-E2E-05、§7 取消/断线/超时；30天/100条历史淘汰与未结案永久保护回归 |
| F12 | 三目标 CGO=0 构建、三管理端原生运行、CLI 0/10–16；TODO-03 |
| F13 | 原始报告 Schema/摘要、证据完整性、原子归档/恢复、三管理端 Web/CLI 一致与清理审计 |
| F14 | §7 全部场景的原始指标、结果守恒校验、零 race；TODO-04 |

## 6. 构建与回归执行顺序

1. 冻结候选版本与输入；检查环境身份、快照、工具和网络能力。先完成 §8 的缺口排查，不能以只读方案评审代替修复验收。
2. B01 按现有入口执行前端 `format:check`、Vitest、Playwright，`go test ./...`、`go test -race ./...`、`go vet ./...`、`make test-collection`、`make build-all`。确认编译产物与嵌入静态资源一致，记录依赖、供应链许可和两个 Goss 锁定摘要。命令需要相应工具/隔离权限，不把跳过报告为通过。
3. 原生 M01/M02/M03 执行管理端启动、全新工作区、权限/锁、四视图与 CLI smoke；产物离线运行不借用构建机资源。构建目标为 CGO_ENABLED=0，race 测试/产物独立（§7），两者不能混淆。
4. 执行18格、六平台 FULL/MIN/FAULT、身份安全矩阵，再执行五类 E2E 和完整回归。故障测试后恢复目标快照并复核基线，才允许性能测试。
5. 执行 §7，出具性能报告；归档 F01–F14、TODO-01/03/04 与 TODO-02 独立结论；复核人签署并更新 SPEC 的状态。未签署前不改成平台 Verified。

现有命令只覆盖自动回归与构建；平台编排、真实浏览器 SSH 场景、测量/守恒校验脚本需要在实施阶段补齐并先自验。本文件不给不存在的测试脚本或命令贴“可直接运行”标签。

## 7. 性能与稳定性协议（SPEC §15.4）

### 7.1 固定负载与测量

- M01 独占 2 vCPU / 4 GiB；20台独立目标、每台100个启用必检 STANDARD 项、5台目标并发、单机 Goss 10并发、默认单活动任务/10排队；记录全部超时与体积预算，不能调大掩盖失败。基准使用 RELEASED GateRun FullRun native_ssh，健康样本预期 READY/0、恰好2000个逻辑 ItemResult。
- 固定方案、清单、种子、服务夹具与镜像摘要。100项由40项基础原生资源、30项依赖工具的内置 command 采集、30项基础自定义 Shell 构成，每项独立 item_id 与可辨识观测；覆盖多角色共享资源再拆分，不仅重复同一廉价检查。实施前归档具体模板/参数构成及去重后的资源/属性数，所有100项必须 STANDARD；负载输入静态校验通过后才冻结。
- 用生产 `CGO_ENABLED=0` 单二进制测正式指标；另用同一源码 race 构建/集成驱动重复完整负载和四类注入，结合 `go test -race ./...`，所有注入全程启用 race 并收集 stderr。race 构建需要其支持平台的 C 工具链，不冒充交付产物；首先在 Linux amd64 执行，三管理端的功能运行仍按 §4/5 全覆盖。
- 每种场景每种构建：1轮预热不计数，3轮正式运行；再做10轮串行健康负载并含闲置收尾的稳定性采样。production/race 原始指标分别报告，不混算；两者都记录资源预算，不以 race 开销忽略硬上限。
- 以外部采样器每秒记录服务进程树 RSS、CPU、FD/句柄、线程/协程可用指标、队列/活动数、目标执行并发与网络量；记录进程生命周期峰值 RSS，避免只看每秒采样遗漏尖峰。CPU含累计时间与窗口占用，端到端耗时从 accepted 到终态归档，另列排队/预检/投递/执行/清理耗时。记录测量工具开销和 RTT；浏览器与辅助服务不计入控制面进程树，但需记录其机器资源。
- 资源判据：目标峰值 RSS ≤512 MiB、硬上限 ≤2 GiB。>2 GiB 为 FAIL；>512 MiB 且≤2 GiB 为未达推荐目标，保留问题和实测数据，本计划不直接签 F14 PASS，须优化重测或按 SPEC 变更流程明确决定。CPU/耗时在 SPEC 没有数值门槛，只报告实测和预算符合情况，不自造达标阈值。
- 泄漏判据：每轮完成后固定闲置60s，比较后8轮空闲末30s的资源中位数；FD应回到同一工作负载基线（解释已知持久连接），RSS/协程不得持续单调增长。若有增长，用堆/资源归属和追加隔离复测解释，未解释则 BLOCKED，不能仅凭最后值未超上限判通过。保留序列与斜率；该检测是最低筛查，不宣称证明不存在任何泄漏。

### 7.2 四类压力注入（分别执行及一轮组合执行）

| case | 固定注入 | 预期与核对 |
|---|---|---|
| M4-PERF-BASE | 20×100 健康基线 | 2000项不丢失/不串位，READY/0，默认并发不越界 |
| M4-PERF-NET | P19连接黑洞、P20在执行标记后断线，其余18台健康 | 连接/阶段/整任务预算有界，其他主机完成；按真实事实 PARTIAL/FAILED 与 UNKNOWN/11（已有必检FAIL则10）；不自动重跑断线模式 |
| M4-PERF-OUT | 一项Shell持续输出超过64 KiB，并另测单机JSON/整报告16/64 MiB边界及越界 | 有界收集、截断/超限明示为取证错误或执行故障，不能错误PASS/READY；保留其他有效结果，控制面不OOM，归档失败按13，不伪造结果 |
| M4-PERF-QUEUE | 一个活动Run + 10排队，额外第12个总提交；中途取消一个排队与活动Run | 容量边界、FIFO、拒绝16、取消14，终态不继续分派；断线重试相同幂等键不增加Run，变负载不替换凭据 |
| M4-PERF-SSE | 5个只读客户端每秒断线重连1次，持续至归档；含旧游标与reset | 服务不断跑；游标续传/快照投影一致，无FD持续增长，报告不因浏览器连接丢失 |
| M4-PERF-MIX | 组合上述网络、输出、满队和SSE，1轮正式运行/构建 | 调度/资源仍有界；对每个Run按实际故障计算预期结果集合，零race；不强求健康样本的2000 PASS |

压力场景中的必检 FAIL 优先级和取消退出码遵循 SPEC §10.3，不用表中简写覆盖规则。结果守恒校验以冻结输入的 `(run_id, host_id, role_id, item_id, mode)` 和编译溯源映射为准：健康Run恰好2000逻辑项、无重复、观察哨兵对应；故障/取消允许尚未执行项无取证，但每个缺口必须可由归档故障/主机/模式状态解释，已有结果不得丢失。2000不是Goss资源数；同资源合并不能减少逻辑结果。

报告至少附三轮各值、峰值与中位数、全部测量序列、race输出、输入/产物摘要、环境与故障时间轴、守恒校验结果及偏差；无实测数据前 TODO-04 保持未完成。

## 8. 已知差异、前置排查与风险

以下记录准备阶段发现的差异及当前修复状态；本地回归证据不替代 M4 目标环境实测：

| ID | 基线事实 / 规格要求 | 进入相应验收前的处理 |
|---|---|---|
| GAP-01 | Native SSH 曾固定使用 `/tmp/readyspec-*`；现已实现 STANDARD/PRIVILEGED 独立目录发现、`ro`/`noexec` 与符号链接排除、首选目录配置及独立特权清理记录；本地选择逻辑回归通过 | M4-TMP-01 仍须在隔离目标上验证 noexec/只读实际挂载回退与崩溃后恢复；通过前阻断 F03/F10 |
| GAP-02 | SHA-256 验真曾使用 sha256sum/shasum；现已按 SPEC 改为 sha256sum/openssl，普通验真与特权脚本均有 openssl-only 回归 | 本地回归通过；仍须在 M4 openssl-only 目标上留存实机证据，不能以本地测试代替 |
| GAP-03 | 若干内置模板声明 Python3/readelf 等能力；F03 的“无 Python”是执行链能力，不代表36模板零工具依赖 | MIN与FULL分镜像；MIN缺能力如实分类，FULL补齐声明依赖；发现未声明依赖或错误PASS必须修复 |
| GAP-04 | M3 浏览器清理用真实 Journal/API，但活动进程 CLEAN 响应有注入；历史 Windows证据来自Server CI | M4补真实远端进程清理与Windows11全链路；不复用成实机已通过 |
| GAP-05 | 现有CI无正式20主机负载计量/平台认证编排 | 实施阶段补可复现驱动、采样与结果守恒校验，先用小负载自验；未就绪记BLOCKED |

优先顺序：环境/身份可信来源 → GAP-01/02 最小复现及必要修复 → 三管理端与六目标基础闭环 → 模板/安全/E2E → 性能 → 最终候选回归与签署。环境不可用时可继续准备本地夹具与脚本，但不得用容器或模拟器关闭实机格子。

## 9. 独立评审

评审工具：pi；指定 provider/model：`magpie / kimi-code-cn/k3.1-pro`。评审输入包括本计划、SPEC、现有构建与验收记录、上述差异代码。评审只判断计划是否覆盖需求、环境是否可执行、判据与证据是否充分，不签署尚未执行的M4。

评审记录见 [M4 评审与讨论记录](m4-design-review-k3.1-pro.md)，包含输入摘要、原始结论、逐项回应和修改位置。首轮 P0-01（MIN 成功用例依赖冲突）和 P1-01（目录回退缺双模式明确覆盖）已修订，第二轮同一模型逐项确认关闭，无新增阻断。主设计者接受此结论，双方对当前计划达成一致。

最终评审原文：**“评审通过，无未关闭的设计阻断，可进入实施；不代表M4实机验收完成。”** 复审后仅更新本文件的评审状态和本节结果，验收规则不再变化。
