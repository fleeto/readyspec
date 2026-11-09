# M1 模板验收对应表

依据：SPEC §6.1。此表区分参数/判定契约、官方 Goss 匹配器协议与实际采集行为。前两者通过不能代替采集验收。`contracts_test.go`、`goss_contracts_test.go` 覆盖全部默认模板的固定观测正反向测试；`catalog_test.go` 验证编译及 Shell 语法。下表只列采集证据及尚待补齐的规格要求，不把注册数量作为完成标准。

状态：**M1 验收完成**。强制隔离采集子集在 AlmaLinux 9.8 VM 上 875 PASS、0 FAIL、0 SKIP，未排除 N01；A01 LDAP→SSSD→NSS、D03 三类真实运行时、L02/L03 真实 rotated/corrupt journal 原工件验证均另行通过。M1 与 M4 平台认证边界及余项依据见“范围对照”。

| ID | 参数与取证要求 | 已有采集证据 | 待补验收/差异 |
|---|---|---|---|
| H01 | 架构集合；uname 原文、别名归一 | 原工件调用隔离 uname 接口：amd64/arm64 别名、x86_64/aarch64 原名、未知架构、无换行、空/多行/额外空行/空白/NUL 输出、查询失败及退出码；断言原文/规范值/来源/起止时间均保留 | 本轮原始架构证据与指定异常分支已补齐；有效未知架构 FAIL，异常输出或命令失败 ERROR，不再在 Shell 中覆盖或吞掉原文 |
| H02 | 最低在线核数；来源、采集时间 | 原工件调用真实 getconf/libc：隔离 sysfs 配置 8 核、在线 2 核；独立 _NPROCESSORS_CONF 验证配置数 8，原工件观察在线数 2；阈值 2 PASS、3/8 FAIL；非零编号/稀疏集合及来源/时间均验证 | 本轮在线与配置核数区分缺口已补齐；实际工具读取隔离数据，不替代所有目标发行版的 libc 实机认证 |
| H03 | MemTotal；容量精确换算 | 原工件读取隔离 meminfo：重复同值/冲突/损坏字段、符号/零/小数/指数/错误单位/缺字段；阈值精确及 ±1 字节、GB/GiB 差异、大整数邻界；保留原始 kB 与字节证据 | 本轮重复字段/源格式与阈值分支已补齐；计数必须是正的无符号十进制，缺失或异常 ERROR |
| H04 | 禁用/启用/最低总量；活动设备 | 原工件读取隔离 swaps：损坏表头/记录、非法计数/优先级、used 超 size、重复设备/未知类型/相对路径；文件和分区累加、转义路径、零容量活动；精确及 ±1 字节、大整数邻界 | 本轮损坏记录与阈值邻界缺口已补齐；完整验证所有记录后才判定总量，重复设备不能累加为通过 |
| O01 | OS ID/版本范围；os-release | 原工件实际 cat 读取隔离文件：重复/缺失/空字段、单/双引号、shell 转义/注释、不闭合/拼接引号、NUL、未知发行版、数字上下界/多位数/前缀边界、滚动标识、源不可读/缺失 | 本轮源格式与版本边界缺口已补齐；原始 ID/VERSION_ID 赋值与解析值保留，不输出无关字段；重复字段/无法比较的版本 ERROR |
| O02 | 数字段版本区间；后缀保留 | 原工件调用 uname -r 接口夹具：发行版/加号后缀、数值上下界、多位数、缺修订号；空/多行/额外空行/空白/NUL/非法或溢出分段、命令失败；原文/规范版本/后缀/来源/时间保留 | 本轮异常输出与版本邻界缺口已补齐；仅数字分段参与比较，后缀保留为证据，不推断发行版补丁或预发布排序 |
| O03 | 精确值/数值区间；proc/sys | 原工件读取隔离 proc/sys：上下界/越界、单边/负数/小数、大整数邻界、字符串/数值断言；分数/十六进制/指数/NaN/空/多值/多行/NUL 等异常；缺键 FAIL、文件及父目录拒读 ERROR | 本轮区间与非法观测缺口已补齐；保留原观测，数值模式校验十进制标量，显式字符串模式仍支持文本与向量；新增 stat 能力声明 |
| O04 | 加载/未加载；模块名归一 | 原工件 cat 读取隔离 proc/modules，整表校验；空表/多模块、连字符归一、缺列/非法字段/重复模块/无关行损坏；卸载禁用的 '-' 引用计数、永久标记/taint 列；加载/卸载过渡及源失败 | 本轮模块表缺口已补齐；完整验证后才判存在性，目标过渡态 ERROR；不输出无关内核地址 |
| S01 | 存在/类型/链接策略；目标信息 | 原工件访问隔离普通文件/目录/链接：跟随/不跟随、相对/绝对目标、链接链、含换行目标名、悬空目标、目标父目录拒读与循环；节点类型/链接文本/目标存在性与类型/错误原因/策略/时间证据 | 本轮链接目标证据缺口已补齐；新增 Python 3 元数据采集能力。不跟随时目标故障只记录，不增加目标可访问性断言；跟随时无法访问目标 ERROR |
| S02 | 属主/属组/模式；UID/GID | 原 Goss 读取真实隔离文件及 passwd/group：无属主断言也保留 UID/GID/名称/模式；权限与名称断言失败；名称无法解析 ERROR 且保留数字身份；路径缺失/拒读 ERROR | 本轮所列证据缺口已补齐；元数据采集不增加业务要求，不同显式要求仍冲突 |
| S03 | 文件系统、必需/禁止挂载选项 | 原 Goss 读隔离 mountinfo：ro/noexec、源设备、非独立挂载 FAIL；独立命名空间内真实只读 bind mount 采集 source/类型/选项 | 本轮 bind/source 缺口已补齐；采集元数据与约束分开 |
| S04 | 非特权可用字节；总量/挂载点 | 原工件实际调用 GNU stat，只读 statfs 与独立 syscall.Statfs 总量交叉核对；保留块用受控工具统计验证 f_bavail；精确大整数边界；真实 bind 子目录挂载点 | 本轮证据缺口已补齐：总/空闲/非特权可用字节、挂载点、精确 FS 类型/源设备/挂载根；非特权可用量不使用 f_bfree |
| S05 | 可用数/比例；总量/类型 | 原工件实际调用 GNU stat，inode 总量与 syscall.Statfs 交叉核对；数量/精确比例、未知总数 ERROR；真实 bind 子目录 | 本轮证据缺口已补齐：总/可用 inode、空闲比例、挂载点与 FS 类型；总数 0 不伪造百分比 |
| N01 | UP、IPv4/IPv6 CIDR；接口集合 | AlmaLinux 9.8 VM 上强制隔离网络命名空间运行原工件与真实 `ip -j address`：UP 双栈、多地址、CIDR 成员、缺失地址、全地址必需、DOWN、接口不存在；归一化保留接口存在/状态/地址 | 本轮真实网络配置与缺失接口分支已通过；其他发行版 iproute2 输出仍需平台认证 |
| N02 | 协议、地址、端口、监听/空闲 | 独立网络命名空间真实 ss 与 TCP/UDP；IPv4 映射地址、`%` 作用域地址、users/ino 元数据、通配、IPv6-only/双栈、v6only 缺失不确定、非法端口/状态/截断、tcp6 跳过 v4 查询 | 本轮接口作用域与元数据异常已补齐；ss 缺失仍 ERROR；真实工具替身不等于所有发行版 ss 输出认证 |
| N03 | TCP、超时、期望可达 | AlmaLinux 9.8 VM 原工件与本地 UDP DNS/TCP 服务：多地址回退到可达地址、所有地址拒绝、拒绝与预期不符、黑洞路由超时分类；原因与耗时保留 | 本轮多地址回退和真实超时分支已通过；其他解析器/内核版本仍需平台认证 |
| N04 | DNS 类型与记录集合 | 本地解析器：A/AAAA/CNAME/MX/NS/PTR/TXT/SRV、不匹配/NXDOMAIN/超时；额外头部观测复用同一查询：NOERROR/NXDOMAIN 允许记录集断言、SERVFAIL/REFUSED/TC 截断/超时/畸形响应 ERROR；AlmaLinux 9.8 配置递归解析器对 example.com A 记录的真实原工件查询通过；指定 server 视角生效 | 本轮 RCODE/截断分支与真实递归解析器查询已通过；记录集仍由 Goss dns 资源判定，证据保留 rcode/truncated/耗时；新增 python3 目标能力 |
| N05 | GET/HEAD、状态/头/正文/TLS | 原工件访问本地服务：慢速正文/连续重定向共用总超时、状态与正文分支超大响应头；64 KiB 头边界/跨重定向累计超限、总响应恰好 1 MiB/超 1 字节、分块超限、缺正文、TLS、HEAD、重定向次数上限 | 本轮总超时/响应头缺口已补齐；全部分支统一受控 Python 采集，显式依赖；状态行/头和读取正文合计限 1 MiB |
| N06 | 目标/源 IP、出口、网关 | 原工件在隔离网络命名空间调用真实 `ip -j route get`：回环出口、from 源地址、不可达目标 ERROR、真实策略规则/表 100 路由选择 dummy0 与网关；策略表/prefsrc/uid 元数据解析、缺 dev、多路由、非法 JSON；缺工具 ERROR | 本轮策略路由选择与归一化证据已通过；其他发行版 iproute2 输出仍需平台认证 |
| R01 | 独立运行/启用状态 | 原工件 systemctl 接口夹具：四种运行/启用组合、未安装/停止/失败、静态/间接/链接/运行时/临时/生成状态；活动/重载/刷新；缺字段/重复/未知状态、查询故障/缺工具；AlmaLinux 9.8 真实 systemd manager 查询 chronyd.service 得到 loaded/active/enabled 并通过；来源/时间证据 | 本轮真实 manager 查询及状态归一化已通过；其他 unit 状态和发行版 systemd 行为由接口分支覆盖 |
| R02 | 进程名及数量区间；脱敏 | 原工件与真实 ps：隔离 PID 命名空间内启动两个同名进程，零匹配/精确数量/超上限；工具接口夹具验证上下界、精确名称、空/损坏表、非法或重复 PID、NUL/额外空行；原查询失败/工具缺失 | 本轮零匹配/超上限/异常输出缺口已补齐；完整验证 PID+comm 表后计数，仅保留请求名与统计，不输出命令行、PID 或无关进程名 |
| R03 | 1/5/15 窗口上限 | 原工件通过真实 cat 读取隔离 loadavg：五字段完整性、全部窗口数值、队列/PID 格式、空/多行/NUL、源访问失败；各窗口精确/上下邻界、零负载、大数小数边界及合法布局；来源/时间/窗口/阈值证据 | 本轮损坏源与阈值边界缺口已补齐；先完整校验再选窗口，异常 ERROR；读取工具依赖由 awk 改为 cat，负载不是瞬时 CPU 使用率 |
| R04 | PID/Unit、NOFILE/NPROC、软硬限制 | 原工件读取真实隔离子进程 limits；NOFILE/NPROC 各自软/硬值与临界值、PID 消失；隔离文件校验重复/损坏行、单位/数值、软高于硬；unlimited/infinity；systemctl 配置读取仍标 CONFIG | 本轮 PID 消失、NPROC、无限值及完整边界/格式分支已补齐；有效值证据同时保留 soft/hard/units，区分 CONFIG 与 EFFECTIVE_STATE |
| R05 | 同步标记、chrony/timesyncd/ntp | 原工件工具接口夹具：全部五种服务别名、多允许服务、已同步/未同步、活动/重载/未活动/失败/启动中/缺失；状态与退出码一致性、多行/NUL、查询失败与工具缺失；AlmaLinux 9.8 的 chronyd 实际 active 且同步，R05 原工件 PASS | M1 的选择/分类行为及 chronyd 实际部署已验证；timesyncd/ntpd 在其他发行版的部署组合纳入 SPEC §17 TODO-01 的 M4 六平台认证矩阵 |
| A01 | 存在/UID/GID/Shell；NSS | 原工件通过 libc getpwnam_r 查询；隔离 NSS 模块覆盖 NOTFOUND/UNAVAIL/TRYAGAIN/拒绝；另在 AlmaLinux 9.8 配置真实 OpenLDAP + SSSD LDAP provider，NSS 查到 LDAP 用户 `m1ldap`，A01 原工件核对 UID/GID/Shell 并 PASS | M1 的 NSS 查询契约和真实 LDAP→SSSD→NSS 链路已验证；企业目录 TLS/Kerberos、访问策略及其他发行版接入不属于 A01 §6.1 断言，跨平台认证依据 SPEC §17 TODO-01 |
| A02 | 组、GID、主组及附加成员 | 原工件通过 getgrnam_r 读取配置的 NSS 组条目，并对必需成员 getpwnam_r 查询；主 GID 与 gr_mem 分别验证主组/附加关系；不存在、后端/成员查询失败、group/passwd 不可读均已覆盖 | 本轮本地文件限定与查询故障缺口已补齐；证据明确标记 group_entry_and_required_users 范围，不冒充完整 NSS 用户枚举 |
| A03 | 生效键、Match 上下文 | 原工件调用宿主真实 `/usr/sbin/sshd -T` 解析隔离配置：默认值与显式项、Match `-C user=app` 内外差异、非法配置、缺主机密钥且配置存在时不回退；同时保留参数替身失败用例 | 本轮实际 sshd 配置解析与 Match 上下文缺口已补齐；测试使用临时主机密钥，不启动监听、不读取真实凭据；认证前置仍属 M2 |
| A04 | 框架存在性及有效运行模式 | 原工件读取隔离 AppArmor 开关/内核 profiles：enforce/complain/混合/关闭/未知/拒读；AlmaLinux 9.8 SELinux Enforcing VM 上运行真实 `getenforce` 与 `/sys/fs/selinux/enforce` 分支；enforcing/permissive/disabled、非法值、缺失框架、enforce 不可读 | 本轮 SELinux Enforcing 实机认证及查询分支已通过；其他 SELinux 策略/发行版仍需平台认证 |
| D01 | deb/rpm 状态及完整版本区间 | 原工件在隔离 dpkg 数据库上运行真实 dpkg-query 与 dpkg --compare-versions：epoch/release、tilde 预发布、上下界、未安装/非 installed 状态、缺失包；AlmaLinux 9.8 上真实 rpmdb 查询已安装 bash 并验证 epoch 版本下界；RPM 调用参数、未安装、锁/数据库错误与成功时 stderr 警告;查询诊断与版本数据分流 | 本轮真实 RPM rpmdb 查询与版本比较已通过；其他 RPM 发行版/rpmdb 状态仍需平台认证 |
| D02 | 路径/命令、权限，不运行目标 | 原工件检查真实隔离文件：绝对/相对路径、PATH 解析及空/相对项、无执行位、后续可执行候选、目录、缺失/断链、链接/循环、父目录拒读；可写执行哨兵及阳性对照 | 本轮执行位与路径分支已补齐；缺失/非普通文件/不满足权限 FAIL、查询访问失败 ERROR；新增 python3 目标能力，不读取或执行目标内容 |
| D03 | Java/Python/Node、路径/版本 | 原工件接口夹具核对实际 --version/-version 参数：三类运行时指定/查找到的路径、未安装、低于/高于上下界、预发布/构建/EA 后缀、垃圾后缀/未闭合引号/横幅版本/重复版本行/NUL/空行、查询失败；AlmaLinux 9.8 上真实 Python 3.9.25、Node 16.20.2、Java 17.0.21 由原工件查询并 PASS；缺失/目录/父目录拒读/不可执行分辨 ERROR | M1 三种运行时输出解析及真实部署路径均已验证；其他版本/发行版的组合属于 SPEC §17 TODO-01 的 M4 六平台矩阵 |
| D04 | ELF 架构、NEEDED、缓存 | 原工件读取实际动态 ELF/共享库、真实私有 ld.so.cache：架构/NEEDED、命中/缺失、失效路径、非 ELF/截断/PIE 候选、不可读、缓存与文件 ABI 不符、工具失败及损坏缓存；三类执行哨兵与阳性对照 | 本轮动态 ELF/缓存缺口已补齐；检查直接 NEEDED 与缓存候选的静态兼容性，不声称证明完整运行时加载成功 |
| D05 | env/ini/json/yaml、键值/正则 | 原工件读取四格式实际文件：env/ini 缺键/重复键/引号、json/yaml 拒绝互相误解析、重复键；JSON Pointer 转义（`~0`/`~1`）、数组索引与越界、前导零/非法转义、标量类型、脱敏摘要 | 本轮 JSON Pointer 转义/数组键路径缺口已补齐；非法指针或数组索引 ERROR，缺键/越界 FAIL；expected 按 JSON 类型严格比较，敏感值仅保留脱敏摘要 |
| L01 | 最短/最长运行时间 | 原工件通过真实 cat 读取隔离 uptime：完整双字段校验、缺 idle/额外字段/idle 非数值/多行/NUL/空源、源缺失与父目录拒读；上下界等于与 ±0.01、零值、2^53 精确小数邻界；来源/时间/实测值证据 | 本轮字段异常与精确小数临界缺口已补齐；先完整校验再比较 uptime 秒数，idle 字段独立保留；读取工具依赖由 awk 改为 cat |
| L02 | 窗口、阈值、since_boot；覆盖/去重 | 原工件调用 journalctl 接口夹具：allow_since_boot 开关/缺启动证据、连续轮转/缺档间隙、消息抑制、截断、读取失败与警告；另以真实 systemd 252 active+rotated journal 副本运行原工件，并损坏副本 header 验证损坏文件引发 ERROR；宿主原日志只读 | M1 的轮转连续性与损坏日志错误分类已由真实 journal 副本和接口边界覆盖；其他发行版 systemd journal 格式属于 M4 TODO-01 |
| L03 | 服务、窗口、失败/重启阈值 | 同上窗口与连续性反向用例；原命令校验 unit 过滤参数；真实 systemd journal 副本中对 chronyd.service 运行原工件：正常轮转 PASS、损坏 active journal ERROR；失败 invocation 去重、失败/重启计数由隔离原工件分支覆盖 | M1 的真实 journal 读取、轮转及损坏分类已通过；不同发行版 systemd journal 行为属于 M4 TODO-01，不推断完整历史 |

## 范围对照

- **M1 验收依据：** SPEC §14 的六项条件，以及 §16 第 2 项定义的 M1 交付。36 项模板、编译与归一化、Web 编排闭环和对应测试均已满足；本表逐项记录实际采集行为。`875` 是强制隔离采集子集的测试/子测试数，不是模板数，也不单独充当完成判据。
- **A01 边界：** §6.1 要求 NSS 用户属性及将查询故障与“不存在”区分。本轮真实 OpenLDAP→SSSD→libc NSS→A01 原工件链路通过。该模板不执行 LDAP 登录认证；企业目录 TLS/Kerberos 和访问策略不在 A01 断言中，因此不作为 M1 缺口。
- **D03 边界：** §6.1 要求受控运行时版本查询。本轮 Python、Node、Java 均以实装运行时通过原工件。其他发行版/架构的运行时组合并入 SPEC §17 TODO-01（关口 M0/M4），不代表已认证六平台。
- **L02/L03 边界：** §6.1 要求可读窗口、事件计数及覆盖不足时不能误报 PASS。本轮除接口边界测试外，还用真实 systemd 252 journal 的 active/rotated 副本运行原工件，并证明损坏副本得到 ERROR。六平台上的 journal 实现差异归 SPEC §17 TODO-01（M4）。
- **明确不属于本轮 M1 完成声明：** SPEC §17 TODO-01/03/04 分别属于 M0/M4 或 M4；M2 的 SSH 认证与执行也未在此声明完成。M1 验收通过不等于 ReadySpec 整体完成或目标平台矩阵已认证。

隔离采集测试应运行原编译工件，保留原采集脚本与 Goss 资源。固定系统文件可由 Linux 挂载命名空间提供；外部系统工具可用检查参数并返回受控系统输出的替身。必须注明工具替身不等于工具本身的平台认证。网络与文件行为使用实际隔离资源，不能用替换整条采集命令证明通过。`journal_platform_linux_test.go` 是显式 opt-in 平台测试，复制真实 journal 后在副本上验证轮转与损坏，不修改宿主日志。

本轮证据文件：`internal/compiler/collection_linux_test.go`（隔离系统文件、工具调用与真实套接字）、`http_collection_test.go`（HTTP/TLS/边界）、`dns_collection_test.go`（本地 DNS 各类型）。`make test-collection` 要求 bubblewrap 存在，缺失会失败；CI Linux 安装测试工具并执行该目标。源码仍包含跨平台的固定契约测试，它们不能替代此目标。

NSS 增量：`internal/compiler/nss_collection_linux_test.go` 在测试临时目录编译 NSS 共享模块，仅在隔离进程的库路径中加载。原工件调用真实 libc，未以固定命令输出代替 NSS 查询；故障测试也验证了类型化 NSS 错误证据，避免把 Python/脚本失败误算成正确分类。查询链中被 continue 掩盖的 UNAVAIL/TRYAGAIN 不能证明身份不存在，返回 ERROR；NOTFOUND=return 策略被保留。密码、GECOS、主目录字段不进入证据。构建测试模块需要 C 编译器及 glibc 开发头文件，CI 已配置这些测试依赖。

D04 增量：`internal/compiler/elf_collection_linux_test.go` 编译仅有一个直接依赖的实际动态 ELF 和共享库，以真实 ldconfig 在私有 chroot 内生成缓存；不读取或改写宿主缓存。目标解释器、库构造器与 ldd 分别设置可写执行哨兵，每个原工件采集场景在归一化前检查未触发，并通过独立阳性执行确认哨兵有效。正例明确断言实际 NEEDED、缓存路径/flags 与共享库头；异常断言类型化错误与候选状态。实现通过 Python 静态读 ELF 头、程序头和动态标记，拒绝失效/截断/错误 ABI/PIE 库候选；不可读或查询失败 ERROR。x86 测试额外编译实际 ELF32 库验证 ABI；其余架构的平台认证独立进行。

日志增量：`internal/compiler/journal_collection_linux_test.go` 运行原编译工件，journalctl 替身核对完整查询参数并读取隔离的日志记录夹具；日志源文件可读性仍由原脚本检查。测试通过连续序号模拟归档文件交接，以跳号模拟中间归档丢失，没有声称创建了真实二进制 journal。新增未按 unit/transport 筛选的系统日志审计，核对窗口边界至窗口内游标的序号域与连续性，并检测消息丢失提示；当前识别 systemd 游标的 s/i 序号结构，未知格式、不同序号域、时序异常或达到 10000 条审计/事件上限均不能证明覆盖。该检查不能证明日志在进入 journald 之前未丢失，也不提供日志真实性保证。allow_since_boot 只在启动晚于窗口起点、当前启动附近有可读记录且审计连续时放宽窗口；不豁免缺口。零事件覆盖不足 ERROR，已知超限仍可 FAIL。stderr 诊断纳入验证，不能静默忽略损坏日志警告。证据仅输出计数/窗口/连续性原因/截断信息，未输出原日志正文。

M4 实机增量（2026-11-08）：六平台真实SSH发现D04误拒绝带PT_INTERP的合法glibc共享对象，已增加带解释器的实际共享库原工件回归，保留PIE/ABI/截断拒绝及执行哨兵。L02/L03审计改为包含可读system/user journal，共享序号域不因系统过滤而假报缺口；新增用户记录不泄漏证据的回归。Rocky旧journalctl历史边界需显式`--reverse -n 1`，已先红复现并修复边界查询。此段记录实现与针对性回归，不替代尚在执行的M4正式认证；完整证据见[执行记录](m4-aws-canary-20261108.md)。

HTTP 增量：`internal/compiler/http_collection_test.go` 通过本地 HTTP/TLS 与原 Goss 工件复现 250ms 预算下持续滴流/重定向仍 PASS，以及超大响应头被接受。现由监督进程执行并按总期限终止/回收网络采集子进程；阶段性 socket timeout 仍存在，但不再代替总预算。状态/头/正文分支统一声明 python3，Goss 外层预算随 timeout_ms 增加 2 秒处理余量，不会将合法 60 秒预算截成原先固定 10 秒。响应头在解析前限量读取，整条链累计 64 KiB；与实际读取正文合计 1 MiB。边界测试包含原始响应的精确字节数、累计重定向响应头、10 次跳转上限、跳转中间正文不耗尽、声明长度不足以及隐私摘要。之前“恰好 1 MiB 正文仍通过”用例已改为 ERROR：响应头也占用规格中的响应读取预算；合计恰好 1 MiB 的原工件用例 PASS。

H01 增量：`internal/compiler/architecture_collection_linux_test.go` 运行原编译工件，uname 替身核对 -m 参数后输出隔离夹具并返回受控退出码，未替换整条采集命令。旧实现的 Shell 命令替换不仅丢失别名原文，还吞掉末尾空行/NUL；新实现直接采集 stdout，在控制面验证单个架构标识并归一化。证据 uname_raw 保留原文，normalized_architecture 保留规范值；无效格式保留原文和 INVALID_UNAME_OUTPUT，查询失败按原退出码归类 ERROR。接口夹具验证采集和归一化，不将其描述为六类目标平台的硬件实机认证。

H02 增量：`internal/compiler/cpu_collection_linux_test.go` 将隔离 sysfs 实验转为原工件测试，未安装 getconf 替身、未改写采集命令。8 个 cpuN 目录及 present/possible=0-7 表示配置 CPU；online 分别为 0-1、4-5、0,2,4,6。真实 _NPROCESSORS_CONF 独立返回 8，原工件通过真实 _NPROCESSORS_ONLN 分别返回 2、2、4，精确边界通过、较高阈值不通过。证据来源必须是在线核数接口，起止时间可解析且落在实际调用区间内。测试确认现有 H02 实现正确，本增量只增加测试和文档，不修改生产采集逻辑或重建相同发布产物。

H03/H04 增量：`internal/compiler/memory_collection_linux_test.go` 运行原编译工件，实际 awk/cat 读取隔离 procfs 文件；没有改写采集命令。除 MemTotal 缺字段导致原采集器失败外，损坏数值/记录用例均断言文件读取成功（exit-status=0）后由归一化判 ERROR，避免把工件执行失败误算为解析验证成功。实际红绿测试修复了 big.Int 接受正号/负零、零 MemTotal 被当作合法量、Swap 重复设备被重复累加、未知类型/相对路径被接受的问题。Swap 尺寸及已用量仍可为无符号零，优先级允许负数；合法活动设备不能因零容量被误当禁用。阈值和容量换算保留大整数精度，GB/GiB 分别验证。

O01/O02 增量：`internal/compiler/os_collection_linux_test.go` 运行原编译工件，O01 由实际 cat 读取隔离 os-release，O02 使用核对 -r 参数的 uname 接口替身；不是替换整个采集命令。红绿测试复现并修复 Go Unquote 不符合 shell 转义语义、多余引号被去掉后误 PASS、十六进制转义被误当版本小数点，以及内核额外空行/空白后缀被接受的问题。新解析不执行文件或展开变量，不支持拼接引号；shell 转义依据 [os-release 格式说明](https://www.freedesktop.org/software/systemd/man/latest/os-release.html)。ID 与 VERSION_ID 使用各自允许的字符集，合法的滚动版本标识可在无范围时读取，不能用于数值范围时 ERROR。重复或缺失关键字段作为无法可靠取证处理，保留 ERROR 策略。O02 的 kernel_raw、normalized_version、release_suffix 分别保留原文、数值版本和后缀；不把发行版后缀参与普通数字比较。O01 只保留所需字段的原始赋值及解析值，测试确认无关私有字段不进入归一化结果。

O03/O04 增量：`internal/compiler/kernel_state_collection_linux_test.go` 运行原编译工件，实际 stat/cat 访问隔离文件。红绿测试复现 sysctl 分数被当作合法数值、Shell 替换吞掉末尾空行/NUL，以及 awk 只查模块首列而忽略损坏记录的误判。O03 以 stat 分类路径缺失/不可访问，再直接 cat 保留值原文；显式数值断言/区间只接受十进制整数或小数（可带负号），不把分数、指数或十六进制等形式自动转换为通过；字符串断言独立保留空值、文本和同一行向量。O04 读取整表后校验六个必需字段及可选 taint，兼容卸载禁用时的 '-' 引用计数和 '[permanent]' 依赖标记，格式参照 [Linux procfs 模块输出实现](https://github.com/torvalds/linux/blob/v6.6/kernel/module/procfs.c)。表中任意损坏记录都不能作为模块不存在的证据；目标处于 Loading/Unloading 时不能证明稳定状态。证据只输出查询名称、存在状态、模块数量与目标状态，不保留内核地址。

S01 增量：`internal/compiler/path_collection_linux_test.go` 运行原编译工件，由真实文件系统操作获取隔离链接与目标，没有工具输出替身。红绿测试确认旧实现缺失链接目标信息。新增独立元数据命令使用 Python lstat/readlink/stat，JSON 保留 node_exists/node_type、follow_symlinks、link_target、target_exists/target_type 或 target_error；不读取目标内容，链接文本不经 Shell 替换，保留相对路径和换行。原有存在性/类型断言及资源冲突检查保留；不跟随时，目标 EACCES/ELOOP 只记录为未知状态，不误当目标缺失，也不新增业务约束；跟随时目标不可访问 ERROR。悬空链接本身存在，不跟随可通过；跟随后按目标不存在判定。Python 不可用或元数据采集失败 ERROR，注册表显式声明 tool:python3，跟随分支原有 stat 依赖保留。多个资源的观察不构成文件系统原子快照。

N01 网络夹具已在 Lightsail AlmaLinux 9.8 VM 上运行：强制隔离模式下使用真实 iproute2 创建 dummy 接口并执行原工件，7 个场景（双栈、多地址、CIDR、缺失地址、全地址必需、DOWN、接口不存在）全部通过。VM 使用 SELinux Enforcing，未修改宿主网络配置或权限；本机受限环境的 N01 权限阻塞仍保留为本机限制，不再作为 M1 采集缺口。

R02 增量：`internal/compiler/process_collection_linux_test.go` 运行原编译工件。真实进程场景在独立 PID 命名空间挂载该命名空间的 procfs，Python 测试辅助器启动并等待两个同名子进程就绪，由真实 ps 观察；测试后终止并回收子进程。测试辅助器使用 Python 不构成 R02 目标依赖，目标仍只需 ps。异常格式由明确标识的 ps 接口夹具提供，并核对原查询参数和读取成功退出码。红绿复现最低数量 0 时空输出、错误文本、NUL 被误当零匹配 PASS；现查询 pid=,comm=，先验证全部 PID/记录再统计，拒绝无效/重复 PID 和损坏行。归一化只输出请求名、matched_count、process_records、匹配模式和脱敏标记；带秘密参数的真实进程与无关进程名均不会出现在证据中。保留 N01 权限阻塞，不把排除该测试的检查描述为全部采集验收通过。

历史验收快照（已被文末 2026-11-06 VM 验收取代）：R02 增量时采集子集显式排除受本机权限阻塞的 N01 网络配置测试；当时的 543 PASS 不代表最终强制采集结果。

R03 增量：`internal/compiler/load_collection_linux_test.go` 运行原编译工件，由真实 cat 读取隔离 proc/loadavg，没有工具输出替身。红绿测试复现缺少后续字段、未选中窗口损坏、队列字段损坏仍误 PASS。新实现读取整条记录，校验单行五字段、三个无符号十进制负载值、任务队列计数和最后 PID 字段，再选择窗口比较；不把不完整来源当作可信标量。损坏内容用例明确断言原始读取 exit-status=0 后归一化 INVALID_LOADAVG，源访问失败另断言非零退出码。三个窗口分别覆盖等于阈值、上下相邻 0.01、零负载及超过浮点精度的大数小数；证据保留原始选中数值、窗口、上限、来源和时间，不保留无关 PID。目标要求由 awk 改为 cat 与 procfs。N01 权限阻塞继续保留。

R03 增量最终验证：显式排除 N01 网络配置阻塞测试的强制采集子集 592 个测试/子测试通过、0 失败、0 跳过；全量竞态各包通过（compiler 322.043s，普通模式下 N01 的 7 个配置场景因权限不足跳过）。go vet、默认及三平台 Go 二进制构建通过，发布产物 R03 requires [os:linux, fs:proc, tool:cat]。前端未改，未重复前端/浏览器检查。该结果不代表完整强制采集目标已通过。

R04 增量最终验证：显式排除 N01 网络配置阻塞测试的强制采集子集 638 个测试/子测试通过、0 失败、0 跳过；全量竞态各包通过（compiler 343.486s，普通模式下 N01 的 7 个配置场景因权限不足跳过）。go vet、默认及三平台 Go 二进制构建通过。该结果包含 R03 和 R04 最终实现，不代表完整强制采集目标通过。

R05 增量：`internal/compiler/time_sync_collection_linux_test.go` 运行原编译工件，timedatectl/systemctl 接口夹具核对真实查询参数并提供受控原始输出/退出码。红绿复现退出 0 但状态 inactive/未知仍误 PASS，以及同步标志额外空行被 Shell 消除的问题。采集改为直接输出每个查询的原文和退出码，由控制面验证单标量及状态/退出码对应关系后判断；不以命令成功等同同步或服务活动。保留 synchronized、active_services 和每个允许服务的 state/exit_status，不保留原始诊断文本。支持 chronyd/chrony、systemd-timesyncd、ntpd/ntp 别名，多允许服务逐项查询。活动/重载/刷新状态与非活动状态的退出码参照 [systemd is-active 实现](https://github.com/systemd/systemd/blob/v257/src/systemctl/systemctl-is-active.c)；缺失 Unit 可输出 inactive 并退出 4，不误判为损坏。工具缺失、查询故障、状态矛盾或损坏输出 ERROR，未活动/同步不符 FAIL。未在宿主修改时钟、启动时间服务或调整系统设置；这些接口用例不等同真实时间服务部署认证。

R05 增量最终验证：显式排除 N01 网络配置阻塞测试的强制采集子集 671 个测试/子测试通过、0 失败、0 跳过；全量竞态各包通过（compiler 359.262s，普通模式下 N01 7 个配置场景权限不足跳过）。go vet、默认及三平台 Go 构建通过，前端未改、未重复相关检查。其他待补项继续保留。

R01 增量：`internal/compiler/service_collection_linux_test.go` 运行原编译工件，systemctl 接口夹具核对含 --all 的限定属性查询，确保空属性也显式输出，不以缺属性猜测停止/未启用。红绿复现未知状态被当 false 后误 PASS、重复字段被后值覆盖的问题。控制面校验三个属性各自唯一、已知值和必要完整性，再独立判运行及启用；active/reloading/refreshing 按 systemd 活动态处理，只有 enabled/enabled-runtime 视为启用，static/indirect/linked 等保留原状态。规范 not-found/inactive/空 UnitFileState 判 FAIL；查询非零即使输出看似未安装仍 ERROR。记录 load_state/active_state/unit_file_state、存在性、running/enabled 和采集来源/时间，省略原始诊断文本。测试使用受控工具接口，未在宿主停止或启用服务，不声称证明实际 systemd manager 的所有发行版行为。

历史验收快照（已被文末 2026-11-06 VM 验收取代）：R01 增量时尚未运行真实 systemd manager；本轮已在 VM 对 chronyd.service 执行原工件查询并通过。

D02 增量：`internal/compiler/command_collection_linux_test.go` 运行原编译工件，真实文件与目录权限、符号链接和 PATH 环境提供隔离资源，无工具输出替身。受控复核旧 command-v/[ -f ] 采集器证明父目录拒读会被报为 exists=false/exit0；新受控 Python 仅使用 stat/access 元数据，保留 EACCES/ELOOP 等原因，访问失败不能冒充不存在。带斜杠的命令按显式路径（相对路径按采集进程当前目录）解析；裸命令按 PATH 查找首个可执行普通文件，支持空项/相对目录。若仅找到不可执行普通文件，保留实际路径/模式/权限后按 executable 策略判定，false 表示不要求执行权限。目标文件不被打开或加载。原始脚本含写入哨兵，哨兵目录在采集命名空间内可写，阳性执行证明能触发，清除后原工件各场景均未触发；归一化不输出脚本内容或完整 PATH。注册表由 stat 依赖改为 python3，证据保留 command/path/exists/filetype/mode/executable/execution_required 与错误原因，不保证目标随后一定能启动。

D02 增量最终验证：显式排除 N01 网络配置阻塞测试的强制采集子集 730 个测试/子测试通过、0 失败、0 跳过。全量竞态各包通过（compiler 385.204s，普通模式下 N01 的 7 个配置场景因权限不足跳过），go vet、默认及三平台 Go 二进制构建通过，发布产物 D02 requires [os:linux, tool:python3]。该采集子集不代表完整强制采集目标已通过，N01 及其他待补项继续保留。

D03 增量：`internal/compiler/runtime_collection_linux_test.go` 运行原编译工件，接口夹具核对真实 `--version`/`-version` 参数并返回受控原文与退出码。红绿复现旧 WHITE_LIST 正则匹配垃圾后缀、未闭合版本引号、横幅中版本子串和重复版本行仍误 PASS。采集改为分段帧输出实际路径、版本原文及查询退出码，控制面只接受单条已知格式的版本记录：Python 3 段式（含 rc/alpha/beta 预发布及 `+` 构建后缀）、Node v3 段式（保留 -/+ 后缀证据）、Java 引号或裸版本记号（`1.x` 映射回世代编号），重复版本行判 ERROR。未安装运行时 FAIL 并记录 exists=false；显式 path 缺失/父目录拒读/不可执行/目录分辨 ERROR，搜索路径找不到 FAIL。查询非零 RUNTIME_QUERY_FAILED 与解析失败 UNSUPPORTED_VERSION 分开。未启动真实版本解释器（只调 --version 原件），新 stat 依赖验证执行文件为普通可执行文件后运行其版本查询命令。

D03 增量最终验证：显式排除 N01 网络配置阻塞测试的强制采集子集 762 个测试/子测试通过、0 失败、0 跳过。全量竞态各包通过（compiler 396.410s，普通模式下 N01 的 7 个配置场景因权限不足跳过），go vet、默认及三平台 Go 二进制构建通过，发布产物 D03 requires [os:linux, runtime-query, tool:stat]。该采集子集不代表完整强制采集目标通过，真实各发行版运行时部署认证与其他待补项继续保留。

L01 增量：`internal/compiler/uptime_collection_linux_test.go` 运行原编译工件，由真实 cat 读取隔离 proc/uptime。红绿复现旧 awk 只校验第一字段：缺 idle 字段、额外第三字段、idle 非数值、多行与 NUL 记录均被误 PASS。新实现读取完整单行双字段记录，校验两个无符号十进制值后比较 uptime 秒数与上下界，异常 ERROR；证据保留 uptime_seconds/idle_seconds/minimum/maximum，阈值可为精确十进制（2^53 以上不四舍五入）。覆盖等于/低于/高于界限、零值、大数小数、缺字段、多行、NUL、源缺失与父目录拒读。目标工具由 awk 改为 cat 并保留 procfs。

L01 增量最终验证：显式排除 N01 网络配置阻塞测试的强制采集子集 811 个测试/子测试通过、0 失败、0 跳过；全量竞态各包通过（compiler 437.222s，普通模式下 N01 的 7 个配置场景因权限不足跳过）。go vet、默认及三平台 Go 二进制构建通过。该子集不代表完整强制采集目标通过，其他待补项继续保留。

D05 增量：`internal/compiler/config_collection_linux_test.go` 运行原编译工件读取隔离配置文件，覆盖 JSON Pointer 转义键、嵌套数组索引、越界、前导零、`-` 索引、非法 `~` 转义、类型化 expected、YAML 数组路径与重复 JSON 键。红绿复现旧实现把非法转义当字面键、接受前导零索引，损坏指针与缺键都落为 FAIL。新实现按 RFC 6901 解码 `~1`/`~0` 并只接受规范数组索引；非法指针 ERROR，缺键/越界 FAIL，map 中带前导零的字面键不受影响。敏感值仍只输出脱敏摘要，expected 按 JSON 类型严格比较。

D01 增量：`internal/compiler/package_collection_linux_test.go` 在隔离根目录放置 dpkg status 数据库，由真实 dpkg-query 读取、真实 dpkg --compare-versions 比较 epoch/release/tilde 版本；覆盖未安装、非 installed 状态、高于上限与缺失包。红绿复现 dpkg-query 解析警告经 2>&1 混入数据后把已安装包误报为未安装。修复为诊断与数据分流：成功查询只取 stdout，失败时再查一次 stderr 仅用于区分缺失包与数据库错误；单行双字段与版本字符集校验后才采信。RPM 分支同样不再混入成功查询的 stderr，并以锁/数据库错误替身验证 ERROR。另在本机临时解包的真实 rpm 6.0.1 上验证 vercmp 语义：epoch 优先、release 参与比较、`1.0~rc1 < 1.0`、相等返回 0；未安装或替换宿主 rpm，发行版 rpmdb 安装查询认证仍独立保留。

A03 增量：`internal/compiler/ssh_collection_linux_test.go` 在隔离根目录生成临时 ed25519 主机密钥与 sshd_config，运行宿主真实 `/usr/sbin/sshd -T`：显式 PermitRootLogin、默认值与 Match `-C user=app` 分支分别验证，非法配置返回 ERROR，缺主机密钥但配置文件存在时仍 ERROR（不退回静态配置解析）。测试不启动 sshd 监听、不读取宿主真实密钥或用户凭据；认证前置仍属 M2。

N02 增量：`internal/compiler/ports_collection_linux_test.go` 以 ss 接口夹具运行原工件，覆盖 IPv4 映射监听地址、`%lo` 作用域、users/ino 元数据、v6only 缺失/0/1、非法端口与非监听状态、截断记录，以及 tcp6 协议不查询 IPv4 段。测试确认现有实现能正确 Unmap 映射地址、忽略作用域与附加元数据，并对无法确定双栈可用性的记录返回 ERROR；未修改生产代码。

N06 增量：`internal/compiler/route_collection_linux_test.go` 在隔离网络命名空间运行宿主真实 `/usr/sbin/ip -j route get`，验证回环出口、from 源地址、接口不符与不可达目标；策略路由元数据（table/prefsrc/uid）解析、缺 dev、多路由与非法 JSON 通过接口夹具验证。缺工具用例加入 N06。新增策略规则/路由表仍需 CAP_NET_ADMIN，与 N01 一并保留。

D05/D01/L01 增量最终验证：显式排除 N01 网络配置阻塞测试的强制采集子集 811 个测试/子测试通过、0 失败、0 跳过；全量竞态各包通过（compiler 437.222s，普通模式下 N01 的 7 个配置场景因权限不足跳过）；go vet、默认及三平台 Go 二进制构建通过。该子集不代表完整强制采集目标通过，D01 的真实 RPM 平台认证与其他待补项继续保留。

A03/N02/N06 增量最终验证：含新用例的强制采集子集 845 个测试/子测试通过、0 失败、0 跳过（明确排除 N01）；三个模板的新测试在 -race 下通过（15.218s）。生产采集代码未改动，上一条 437.222s 全量竞态仍适用。新增策略规则仍需 CAP_NET_ADMIN。

N04 增量：`internal/compiler/dns_probe.go` 对同一 DNS 查询增加独立响应头观测，弥补 Goss `dns` 资源只暴露 resolvable、无法区分否定回答与服务失败的缺陷。NOERROR/NXDOMAIN 交给记录集断言；SERVFAIL、REFUSED、TC 截断、超时与畸形响应返回 ERROR；证据保留 rcode/truncated/耗时，不输出响应正文。覆盖 A/AAAA/CNAME/MX/NS/PTR/TXT/SRV 既有记录用例、NXDOMAIN、SERVFAIL、REFUSED、截断、畸形与超时；注册表新增 python3 依赖。新增策略规则/真实递归解析器差异仍属平台认证范围。

历史验收快照（已被文末 2026-11-06 VM 验收取代）：当时的 861 PASS 子集排除了 N01，且真实递归解析器、rpmdb、SELinux 与策略规则认证尚未执行；本轮强制隔离子集及平台验收结果见文末。

A04 增量验证：SELinux 原工件测试在 -race 下通过（5.498s）；新增测试未改动生产代码，当前全量竞态（442.218s）覆盖同一生产实现。

A04 增量：`internal/compiler/selinux_collection_linux_test.go` 运行原工件，getenforce 替身与隔离 `/sys/fs/selinux/enforce` 提供 SELinux 状态：enforcing/permissive/disabled 匹配、非法 getenforce 输出、非法 enforce 值、内核无 SELinux 目录、enforce 不可读。查询不修改内核安全模块配置。该增量记录时本机没有 SELinux 实机；文末 2026-11-06 记录补充了 AlmaLinux 9.8 Enforcing 内核上的真实原工件认证。

存储证据补充：`internal/compiler/storage_collection_linux_test.go` 运行原编译工件，包含实际文件身份、实际 bind mount、只读文件系统统计及独立 statfs 交叉核对。保留块和大整数邻界用系统工具接口替身提供受控计数，未假称在宿主重新格式化文件系统或创建保留块。S04/S05 从 GNU stat 保留原始块计数，在控制面精确换算；从当前命名空间 `/proc/self/mountinfo` 读取精确类型、源设备和挂载根，避免将 stat 的 `ext2/ext3` 族名称当成 ext4 的精确类型。

2026-11-06 Lightsail AlmaLinux 9.8 M1 平台验收：`READYSPEC_REQUIRE_COLLECTION_SANDBOX=1 go test ./internal/compiler -run 'Collect|Collector|Symlink' -count=1 -json` 实际统计 875 个测试/子测试通过、0 失败、0 跳过，未排除 N01；覆盖真实 N01 接口配置、N03 多地址回退与超时、N06 策略路由。全量 `go test -race ./...` 各包通过（compiler 151.977s），`go vet ./...`、Linux/arm64/Windows amd64 构建通过。真实平台原工件检查：RPM bash 查询与版本下界 PASS、SELinux Enforcing PASS、chronyd 同步 PASS、systemd manager 查询 PASS、递归解析器 example.com A 查询 PASS；D03 Python 3.9.25/Node 16.20.2/Java 17.0.21 全 PASS；A01 本地 OpenLDAP→SSSD→NSS 用户查询 PASS；L02/L03 真实 systemd 252 rotated journal PASS、损坏 journal ERROR。真实 journal 专项命令为 `READYSPEC_TEST_REAL_JOURNAL=1 READYSPEC_REQUIRE_COLLECTION_SANDBOX=1 go test ./internal/compiler -run '^TestOriginalLogArtifactsInspectRealRotatedAndCorruptJournals$' -count=1 -v`，其两种 journal 场景各自同时验证 L02/L03。其他发行版差异仍属 SPEC §17 TODO-01 的 M4 平台认证范围；M1 缺口已关闭。
