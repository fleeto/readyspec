"""Authoritative 9-domain / 54-group catalog and 40 executable P0 templates (Section 2.3 & Chapter 6)."""

from __future__ import annotations

from functools import lru_cache

from readyspec.constants import (
     EvidenceDepth,
    PrivilegeGroup,
    TemplateMaturity,
    TemplateSource,
)
from readyspec.domain.models import (
    CatalogGroup,
    ParameterSpec,
    ParamType,
    TemplateKind,
    TemplateSpec,
)

DOMAINS: tuple[tuple[str, str], ...] = (
    ("H", "硬件与计算"),
    ("O", "操作系统与内核"),
    ("S", "存储与文件系统"),
    ("N", "网络与通信"),
    ("R", "系统资源与基础服务"),
    ("A", "安全与访问"),
    ("D", "软件依赖与运行时"),
    ("L", "系统健康与生命周期"),
    ("P", "部署前置与兼容性"),
)


def _p(
    name: str,
    label_zh: str,
    param_type: ParamType = ParamType.STRING,
    *,
    required: bool = True,
    default: object = None,
    unit: str = "",
    min_value: float | None = None,
    max_value: float | None = None,
    enum_values: list[str] | None = None,
    overridable_by_default: bool = False,
    sensitive: bool = False,
    allow_null: bool = False,
    description: str = "",
) -> ParameterSpec:
    return ParameterSpec(
        name=name,
        label_zh=label_zh,
        param_type=param_type,
        required=required,
        default=default,
        unit=unit,
        min_value=min_value,
        max_value=max_value,
        enum_values=enum_values or [],
        overridable_by_default=overridable_by_default,
        sensitive=sensitive,
        allow_null=allow_null,
        description=description,
    )


def _tpl(
    tid: str,
    domain: str,
    name_zh: str,
    name_en: str,
    description: str,
    *,
    kind: TemplateKind = TemplateKind.NATIVE,
    depth: EvidenceDepth = EvidenceDepth.METADATA,
    priv: PrivilegeGroup = PrivilegeGroup.STANDARD,
    keywords: list[str] | None = None,
    deps: list[str] | None = None,
    conditional: str = "",
    active_probe: bool = False,
    params: list[ParameterSpec] | None = None,
) -> TemplateSpec:
    return TemplateSpec(
        template_id=tid,
        version="1.0.0",
        name_zh=name_zh,
        name_en=name_en,
        domain_code=domain,
        group_code=tid,
        description=description,
        source=TemplateSource.BUILTIN,
        maturity=TemplateMaturity.INTEGRATED,
        privilege_group=priv,
        evidence_depth=depth,
        kind=kind,
        keywords=keywords or [],
        dependencies=deps or [],
        conditional_notes=conditional,
        active_network_probe=active_probe,
        parameters=params or [],
    ).with_digest()


@lru_cache(maxsize=1)
def get_builtin_templates() -> dict[str, TemplateSpec]:
    """Return all 40 executable P0 built-in templates indexed by template_id."""
    items: list[TemplateSpec] = [
        # 6.1 Hardware & Compute (H01-H04)
        _tpl(
            "H01",
            "H",
            "CPU 架构检查",
            "CPU Architecture",
            "验证目标主机规范化 CPU 架构是否属于允许列表（如 x86_64 / aarch64）。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.METADATA,
            keywords=["cpu", "arch", "x86_64", "aarch64", "arm64", "架构"],
            deps=["uname"],
            params=[
                _p(
                    "allowed_arch",
                    "允许的 CPU 架构",
                    ParamType.LIST,
                    default=["x86_64"],
                    enum_values=["x86_64", "aarch64"],
                    description="规范化架构，可多选",
                ),
            ],
        ),
        _tpl(
            "H02",
            "H",
            "逻辑 CPU 核数",
            "Online Logical CPUs",
            "检查当前在线逻辑 CPU 核心数是否达到最低要求（非 cgroup 配额）。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.METADATA,
            keywords=["cpu", "cores", "vcpu", "nproc", "核数"],
            deps=["/proc/cpuinfo"],
            params=[
                _p("min_cores", "最少逻辑核数", ParamType.INTEGER, default=4, min_value=1, unit="cores"),
            ],
        ),
        _tpl(
            "H03",
            "H",
            "主机总物理内存",
            "Total Physical Memory",
            "基于 /proc/meminfo MemTotal 检查主机总物理内存（支持 GiB/GB 单位）。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.METADATA,
            keywords=["memory", "ram", "memtotal", "内存"],
            deps=["/proc/meminfo"],
            params=[
                _p("min_gib", "最低总内存", ParamType.NUMBER, default=8.0, min_value=0.5, unit="GiB"),
            ],
        ),
        _tpl(
            "H04",
            "H",
            "Swap 交换分区策略",
            "Swap Policy",
            "检查目标机 Swap 是否按要求禁用（如 K8s/低延迟数据库）或达到最低容量。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["swap", "交换分区", "虚拟内存"],
            deps=["/proc/swaps", "/proc/meminfo"],
            params=[
                _p(
                    "policy",
                    "Swap 策略",
                    ParamType.ENUM,
                    default="disabled",
                    enum_values=["disabled", "enabled", "min_size"],
                ),
                _p(
                    "min_mib",
                    "最低 Swap 容量（min_size 时生效）",
                    ParamType.INTEGER,
                    required=False,
                    default=0,
                    min_value=0,
                    unit="MiB",
                ),
            ],
        ),
        # 6.2 OS & Kernel (O01-O04)
        _tpl(
            "O01",
            "O",
            "操作系统发行版与版本",
            "OS Distribution & Version",
            "基于 /etc/os-release 校验发行版 ID 与版本号是否在支持矩阵内。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.METADATA,
            keywords=["os", "distro", "ubuntu", "debian", "rocky", "发行版"],
            deps=["/etc/os-release"],
            params=[
                _p(
                    "allowed_distros",
                    "允许的发行版与版本",
                    ParamType.LIST,
                    default=["ubuntu:24.04", "debian:13", "rocky:9"],
                ),
            ],
        ),
        _tpl(
            "O02",
            "O",
            "Linux 内核最低版本",
            "Minimum Kernel Version",
            "按数值段比较 uname -r 主次修订版本是否满足最低内核要求。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.METADATA,
            keywords=["kernel", "uname", "内核"],
            deps=["uname"],
            params=[
                _p("min_kernel", "最低内核主次版本", ParamType.VERSION_CONSTRAINT, default="5.14.0"),
            ],
        ),
        _tpl(
            "O03",
            "O",
            "内核 sysctl 参数值",
            "Kernel sysctl Parameter",
            "使用 Goss kernel-param 原生资源检查运行态内核参数值。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["sysctl", "kernel-param", "vm.max_map_count", "somaxconn", "内核参数"],
            params=[
                _p("key", "sysctl 参数名", ParamType.STRING, default="vm.max_map_count"),
                _p("expected_value", "期望参数值", ParamType.STRING, default="262144"),
            ],
        ),
        _tpl(
            "O04",
            "O",
            "内核模块加载状态",
            "Kernel Module State",
            "基于 /proc/modules 检查指定内核模块当前已加载或未加载。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["lsmod", "modprobe", "module", "br_netfilter", "overlay", "内核模块"],
            deps=["/proc/modules"],
            params=[
                _p("module_name", "内核模块名", ParamType.STRING, default="overlay"),
                _p("should_be_loaded", "期望已加载", ParamType.BOOLEAN, default=True),
            ],
        ),
        # 6.3 Storage & Filesystem (S01-S05)
        _tpl(
            "S01",
            "S",
            "目录/文件路径存在与类型",
            "Path Existence & Type",
            "通过 Goss file 原生资源校验指定路径是否存在及类型（目录/普通文件/符号链接）。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.EXISTENCE,
            keywords=["path", "file", "dir", "directory", "路径", "目录", "文件"],
            params=[
                _p(
                    "path",
                    "目标绝对路径",
                    ParamType.PATH,
                    default="/data",
                    overridable_by_default=True,
                ),
                _p("exists", "期望存在", ParamType.BOOLEAN, default=True),
                _p(
                    "filetype",
                    "期望类型",
                    ParamType.ENUM,
                    default="directory",
                    enum_values=["directory", "file", "symlink"],
                ),
            ],
        ),
        _tpl(
            "S02",
            "S",
            "文件或目录权限与属主",
            "Path Owner & Mode",
            "通过 Goss file 原生资源检查目标路径的属主、属组与八进制权限位。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.METADATA,
            keywords=["chmod", "chown", "owner", "mode", "权限", "属主"],
            params=[
                _p(
                    "path",
                    "目标绝对路径",
                    ParamType.PATH,
                    default="/data",
                    overridable_by_default=True,
                ),
                _p("owner", "期望属主", ParamType.STRING, default="root"),
                _p("group", "期望属组", ParamType.STRING, default="root"),
                _p("mode", "八进制权限位", ParamType.STRING, default="0755"),
            ],
        ),
        _tpl(
            "S03",
            "S",
            "挂载点与文件系统属性",
            "Mount Point Attributes",
            "通过 Goss mount 原生资源检查挂载点存在性、文件系统类型与挂载选项（如禁 noexec）。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["mount", "filesystem", "xfs", "ext4", "noexec", "挂载"],
            params=[
                _p(
                    "mount_point",
                    "挂载点路径",
                    ParamType.PATH,
                    default="/data",
                    overridable_by_default=True,
                ),
                _p("filesystem", "期望文件系统类型", ParamType.STRING, default="xfs"),
                _p("required_opts", "必需挂载选项", ParamType.LIST, default=["rw"]),
            ],
        ),
        _tpl(
            "S04",
            "S",
            "目录可用磁盘空间",
            "Available Disk Space",
            "使用 POSIX df -Pk 非破坏性检查指定路径所在挂载点的剩余可用空间。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.METADATA,
            keywords=["disk", "df", "space", "storage", "磁盘空间", "剩余空间"],
            deps=["df"],
            params=[
                _p(
                    "path",
                    "检查路径",
                    ParamType.PATH,
                    default="/data",
                    overridable_by_default=True,
                ),
                _p(
                    "min_free_gib",
                    "最少可用空间",
                    ParamType.NUMBER,
                    default=50.0,
                    min_value=0.1,
                    unit="GiB",
                    overridable_by_default=True,
                ),
            ],
        ),
        _tpl(
            "S05",
            "S",
            "目录可用 Inode 数量与比例",
            "Available Inodes",
            "检查目标路径文件系统的剩余可用 inode 百分比（对不提供 inode 的文件系统准确报 UNSUPPORTED/ERROR）。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.METADATA,
            keywords=["inode", "df -i", "文件句柄节点"],
            deps=["df"],
            params=[
                _p("path", "检查路径", ParamType.PATH, default="/data", overridable_by_default=True),
                _p("min_free_pct", "最低可用 Inode 比例", ParamType.NUMBER, default=15.0, min_value=1.0, max_value=100.0, unit="%"),
            ],
        ),
        # 6.4 Networking (N01-N06)
        _tpl(
            "N01",
            "N",
            "网络接口存在与地址状态",
            "Network Interface & CIDR",
            "通过 Goss interface 原生资源检查指定网卡是否存在及承载的地址前缀。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["nic", "interface", "eth0", "ens", "ip", "网卡"],
            params=[
                _p(
                    "interface_name",
                    "网卡接口名",
                    ParamType.STRING,
                    default="eth0",
                    overridable_by_default=True,
                ),
                _p("exists", "期望存在", ParamType.BOOLEAN, default=True),
            ],
        ),
        _tpl(
            "N02",
            "N",
            "本地端口监听/空闲检查",
            "Local Port Listening / Free",
            "通过 Goss port 原生资源检查业务规划端口是否空闲未被占用（或依赖端口已监听）。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["port", "listen", "tcp", "udp", "端口占用", "端口"],
            params=[
                _p("protocol", "协议", ParamType.ENUM, default="tcp", enum_values=["tcp", "udp"]),
                _p(
                    "port",
                    "端口号",
                    ParamType.PORT,
                    default=8080,
                    min_value=1,
                    max_value=65535,
                    overridable_by_default=True,
                ),
                _p("should_listen", "期望已监听（False 表示要求空闲）", ParamType.BOOLEAN, default=False),
            ],
        ),
        _tpl(
            "N03",
            "N",
            "目标端出向 TCP 连通探测",
            "Outbound TCP Reachability",
            "从目标主机向指定上游地址:端口发起低影响 TCP 握手探测（Goss addr）。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.ACTIVE_PROBE,
            active_probe=True,
            keywords=["tcp", "connect", "addr", "telnet", "连通性", "防火墙"],
            params=[
                _p(
                    "target_host_port",
                    "远端地址与端口 (host:port)",
                    ParamType.STRING,
                    default="127.0.0.1:5432",
                    overridable_by_default=True,
                ),
                _p("reachable", "期望可达", ParamType.BOOLEAN, default=True),
                _p("timeout_ms", "连接超时", ParamType.INTEGER, default=3000, min_value=200, max_value=30000, unit="ms"),
            ],
        ),
        _tpl(
            "N04",
            "N",
            "DNS 域名解析检查",
            "DNS Resolution",
            "通过 Goss dns 原生资源从目标机视角验证域名能否正确解析。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.ACTIVE_PROBE,
            active_probe=True,
            keywords=["dns", "resolve", "nslookup", "dig", "域名解析"],
            params=[
                _p("fqdn", "待解析域名", ParamType.STRING, default="db.internal.local", overridable_by_default=True),
                _p("resolvable", "期望可解析", ParamType.BOOLEAN, default=True),
            ],
        ),
        _tpl(
            "N05",
            "N",
            "HTTP/HTTPS 端点响应检查",
            "HTTP(S) Endpoint Probe",
            "低影响 GET/HEAD 探测（TODO-015：禁写方法，默认不跟随重定向，默认强校验 TLS，响应上限 1 MiB）。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.ACTIVE_PROBE,
            active_probe=True,
            keywords=["http", "https", "url", "healthz", "tls", "接口探测"],
            params=[
                _p("url", "探测 URL", ParamType.STRING, default="https://repo.internal.local/healthz", overridable_by_default=True),
                _p("method", "HTTP 方法", ParamType.ENUM, default="GET", enum_values=["GET", "HEAD"]),
                _p("expected_status", "期望 HTTP 状态码", ParamType.INTEGER, default=200, min_value=100, max_value=599),
                _p("allow_insecure_tls", "跳过 TLS 证书校验（降低验证深度）", ParamType.BOOLEAN, default=False),
                _p("follow_redirects", "允许最多 3 跳重定向", ParamType.BOOLEAN, default=False),
            ],
        ),
        _tpl(
            "N06",
            "N",
            "目标 IP 路由出口决策",
            "IP Route Decision",
            "调用 ip route get 查询目标 IP 的实际选路出口网卡与下一跳。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["route", "ip route", "gateway", "路由", "下一跳"],
            deps=["ip"],
            conditional="需目标机预装 iproute2 (`ip` 命令)",
            params=[
                _p("destination_ip", "目标 IP 地址", ParamType.IP_OR_CIDR, default="10.0.0.1", overridable_by_default=True),
                _p("expected_dev", "期望出口网卡（可留空仅验证有路由）", ParamType.STRING, required=False, default="", overridable_by_default=True),
            ],
        ),
        # 6.5 System Resources & Services (R01-R05)
        _tpl(
            "R01",
            "R",
            "系统服务运行与开机自启状态",
            "Systemd Service State",
            "通过 Goss service 原生资源检查指定服务的运行态与 enabled 状态（区分 static/indirect）。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["systemd", "service", "systemctl", "daemon", "服务"],
            params=[
                _p("service_name", "服务名称", ParamType.STRING, default="chronyd"),
                _p("running", "期望运行中", ParamType.BOOLEAN, default=True),
                _p("enabled", "期望开机启用", ParamType.BOOLEAN, default=True),
            ],
        ),
        _tpl(
            "R02",
            "R",
            "指定进程存在性检查",
            "Process Running Check",
            "通过 Goss process 原生资源检查目标守护进程存在（或冲突进程未运行）。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["process", "ps", "pgrep", "进程"],
            params=[
                _p("comm_name", "进程可执行名", ParamType.STRING, default="sshd"),
                _p("running", "期望正在运行", ParamType.BOOLEAN, default=True),
            ],
        ),
        _tpl(
            "R03",
            "R",
            "系统平均负载上限",
            "System Load Average",
            "读取 /proc/loadavg 验证 1/5/15 分钟平均负载不超过阈值。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.METADATA,
            keywords=["load", "loadavg", "uptime", "系统负载"],
            deps=["/proc/loadavg"],
            params=[
                _p("window_minutes", "统计窗口", ParamType.ENUM, default="5", enum_values=["1", "5", "15"]),
                _p("max_load", "最大允许平均负载", ParamType.NUMBER, default=4.0, min_value=0.1),
            ],
        ),
        _tpl(
            "R04",
            "R",
            "现有服务/Unit 资源限制 (ulimit/TasksMax)",
            "Unit / Process Resource Limits",
            "读取 systemctl show 或 /proc/<pid>/limits 验证真实 LimitNOFILE/LimitNPROC，严禁拿当前 SSH ulimit 冒充（TODO-010）。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.CONFIGURATION,
            keywords=["ulimit", "nofile", "nproc", "limits", "句柄数"],
            deps=["systemctl"],
            conditional="配置态检查依赖 systemd；运行态跨用户 /proc/<pid>/limits 受 hidepid 或权限限制",
            params=[
                _p("unit_name", "目标 systemd Unit 名", ParamType.STRING, default="sshd.service"),
                _p("property_name", "限制属性名", ParamType.ENUM, default="LimitNOFILE", enum_values=["LimitNOFILE", "LimitNPROC", "TasksMax"]),
                _p("min_limit", "最低限制阈值", ParamType.INTEGER, default=65535, min_value=64),
            ],
        ),
        _tpl(
            "R05",
            "R",
            "系统时钟同步状态 (NTP/Chrony)",
            "Clock Synchronization",
            "结合 timedatectl show 适配 chrony、systemd-timesyncd 与传统 ntpd/ntpsec（TODO-010）。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["ntp", "chrony", "timesyncd", "clock", "时间同步", "时钟"],
            deps=["timedatectl"],
            params=[
                _p("require_synchronized", "要求已完成时钟同步", ParamType.BOOLEAN, default=True),
            ],
        ),
        # 6.6 Security & Access (A01-A04)
        _tpl(
            "A01",
            "A",
            "操作系统用户属性检查",
            "OS User Attributes",
            "通过 Goss user 原生资源检查业务运行账号是否存在及登录 Shell 策略。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.METADATA,
            keywords=["user", "uid", "passwd", "账号", "用户"],
            params=[
                _p("username", "用户名", ParamType.STRING, default="appuser", overridable_by_default=True),
                _p("exists", "期望存在", ParamType.BOOLEAN, default=True),
            ],
        ),
        _tpl(
            "A02",
            "A",
            "操作系统用户组检查",
            "OS Group Attributes",
            "通过 Goss group 原生资源检查指定系统用户组是否存在。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.METADATA,
            keywords=["group", "gid", "用户组"],
            params=[
                _p("groupname", "用户组名", ParamType.STRING, default="appgroup", overridable_by_default=True),
                _p("exists", "期望存在", ParamType.BOOLEAN, default=True),
            ],
        ),
        _tpl(
            "A03",
            "A",
            "SSH 服务端生效配置 (sshd -T)",
            "Effective sshd Configuration",
            "内置受控 PRIVILEGED 模板：调用 /usr/sbin/sshd -T 检查真实合并生效配置，绝不降级 grep 静态文件（TODO-010）。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            priv=PrivilegeGroup.PRIVILEGED,
            keywords=["ssh", "sshd", "permitrootlogin", "安全基线", "特权"],
            deps=["/usr/sbin/sshd"],
            conditional="强制要求 PRIVILEGED 特权授权与 /usr/sbin/sshd",
            params=[
                _p("option_key", "sshd 配置项名（小写）", ParamType.STRING, default="maxauthtries"),
                _p("expected_value", "期望生效值", ParamType.STRING, default="4"),
            ],
        ),
        _tpl(
            "A04",
            "A",
            "SELinux / AppArmor 强制访问控制状态",
            "SELinux / AppArmor Mode",
            "按发行版检测 SELinux (getenforce) 或 AppArmor 状态是否符合部署要求。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.EFFECTIVE_STATE,
            keywords=["selinux", "apparmor", "enforcing", "permissive", "安全模块"],
            params=[
                _p(
                    "expected_policy",
                    "期望策略模式",
                    ParamType.ENUM,
                    default="disabled_or_permissive",
                    enum_values=["disabled_or_permissive", "enforcing", "any_recorded"],
                ),
            ],
        ),
        # 6.7 Software Dependencies & Runtime (D01-D05)
        _tpl(
            "D01",
            "D",
            "系统软件包安装状态",
            "System Package Installed",
            "通过 Goss package 原生资源检查 deb/rpm 系统包是否已安装或禁止安装。",
            kind=TemplateKind.NATIVE,
            depth=EvidenceDepth.METADATA,
            keywords=["package", "apt", "dpkg", "rpm", "dnf", "软件包"],
            params=[
                _p("package_name", "软件包名称", ParamType.STRING, default="ca-certificates"),
                _p("installed", "期望已安装", ParamType.BOOLEAN, default=True),
            ],
        ),
        _tpl(
            "D02",
            "D",
            "可执行命令存在与权限位",
            "Executable Command Presence",
            "在不执行目标程序的前提下验证指定命令或二进制路径存在且具备可执行位。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.EXISTENCE,
            keywords=["which", "command", "binary", "executable", "命令"],
            params=[
                _p("command_or_path", "命令名或绝对路径", ParamType.STRING, default="tar"),
            ],
        ),
        _tpl(
            "D03",
            "D",
            "语言运行时版本检查",
            "Language Runtime Version",
            "受控低风险查询预置运行时（Java / Python3 / OpenSSL）版本是否满足下限。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.METADATA,
            keywords=["java", "jdk", "python", "openssl", "runtime", "运行时版本"],
            params=[
                _p("runtime", "运行时类型", ParamType.ENUM, default="openssl", enum_values=["openssl", "java", "python3"]),
                _p("min_version", "最低版本号", ParamType.VERSION_CONSTRAINT, default="3.0.0"),
            ],
        ),
        _tpl(
            "D04",
            "D",
            "静态 ELF 架构与动态库依赖解析",
            "Static ELF Dependency Check",
            "严禁调用 ldd（TODO-008）！依次通过 readelf / eu-readelf / objdump + ldconfig -p 静态核验 ELF 架构与 NEEDED SONAME。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.STATIC_COMPATIBILITY,
            keywords=["elf", "so", "soname", "glibc", "readelf", "动态库"],
            deps=["readelf|eu-readelf|objdump", "ldconfig"],
            conditional="要求目标机存在 readelf、eu-readelf 或 objdump 之一，缺失报 ERROR",
            params=[
                _p("elf_path", "目标 ELF 文件绝对路径", ParamType.PATH, default="/usr/bin/openssl", overridable_by_default=True),
                _p("required_soname", "必需依赖库 SONAME", ParamType.STRING, default="libc.so.6"),
            ],
        ),
        _tpl(
            "D05",
            "D",
            "系统/服务配置文件键值检查",
            "Configuration File Entry",
            "受控读取目标配置文件中的非敏感键值设置（脱敏回传摘要，不把当前 SSH 环境变量当业务运行环境）。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.CONFIGURATION,
            keywords=["config", "env", "conf", "配置文件"],
            params=[
                _p("config_path", "配置文件路径", ParamType.PATH, default="/etc/locale.conf", overridable_by_default=True),
                _p("expected_pattern", "期望匹配正则", ParamType.REGEX, default="UTF-8"),
            ],
        ),
        # 6.8 System Health & Lifecycle (L01-L03)
        _tpl(
            "L01",
            "L",
            "系统启动时长 (Uptime) 检查",
            "System Uptime Window",
            "基于 /proc/uptime 检查主机已稳定运行时长是否达到最少分钟数。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.METADATA,
            keywords=["uptime", "boot", "reboot", "运行时长", "重启"],
            deps=["/proc/uptime"],
            params=[
                _p("min_uptime_minutes", "最少已运行分钟数", ParamType.INTEGER, default=10, min_value=0, unit="min"),
            ],
        ),
        _tpl(
            "L02",
            "L",
            "近期内核 OOM 杀进程历史检查",
            "Historical Kernel OOM Events",
            "验证指定时间窗口内无内核 Out-of-Memory Killed process 事件；日志覆盖不足时拒绝虚假 PASS（TODO-009）。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.HISTORICAL_EVIDENCE,
            keywords=["oom", "dmesg", "journalctl", "内存溢出", "内核日志"],
            deps=["journalctl"],
            conditional="要求可读系统日志且覆盖窗口，或显式开启 allow_since_boot",
            params=[
                _p("window_hours", "回溯时间窗口", ParamType.INTEGER, default=24, min_value=1, max_value=720, unit="hours"),
                _p("max_events", "允许最大 OOM 次数", ParamType.INTEGER, default=0, min_value=0),
                _p("allow_since_boot", "允许新开机周期小于窗口时按开机起算", ParamType.BOOLEAN, default=False),
            ],
        ),
        _tpl(
            "L03",
            "L",
            "关键服务异常崩溃/重启历史",
            "Service Failure / Restart History",
            "基于 systemd/journal 结构化记录核查指定服务在观察窗口内的异常失败次数（TODO-009）。",
            kind=TemplateKind.SHELL,
            depth=EvidenceDepth.HISTORICAL_EVIDENCE,
            keywords=["crash", "restart", "failed", "journalctl", "服务崩溃"],
            deps=["journalctl", "systemctl"],
            conditional="要求可读 systemd journal 日志并证明时间窗口覆盖",
            params=[
                _p("service_name", "服务名称", ParamType.STRING, default="sshd.service"),
                _p("window_hours", "回溯时间窗口", ParamType.INTEGER, default=24, min_value=1, max_value=720, unit="hours"),
                _p("max_failures", "最大允许异常次数", ParamType.INTEGER, default=0, min_value=0),
                _p("allow_since_boot", "允许新开机周期小于窗口时按开机起算", ParamType.BOOLEAN, default=False),
            ],
        ),
        # 6.9 Deployment Topology & Control-Plane Readiness (P01-P04)
        _tpl(
            "P01",
            "P",
            "业务软件目标平台兼容矩阵匹配",
            "Software Platform Compatibility",
            "控制端聚合检查：验证所有目标机实际 OS 发行版与 CPU 架构符合方案声明的软件支持矩阵（与 ReadySpec 自身认证独立）。",
            kind=TemplateKind.CONTROL,
            depth=EvidenceDepth.CONTROL_AGGREGATE,
            keywords=["compatibility", "platform", "matrix", "平台兼容"],
        ),
        _tpl(
            "P02",
            "P",
            "业务角色节点数量与共址拓扑",
            "Role Node Count & Colocation Topology",
            "控制端拓扑检查：验证每个角色的实际绑定节点数满足 min/max 约束及共址策略，且无重复连接端点。",
            kind=TemplateKind.CONTROL,
            depth=EvidenceDepth.CONTROL_AGGREGATE,
            keywords=["topology", "nodes", "role", "cluster", "节点数", "拓扑"],
        ),
        _tpl(
            "P03",
            "P",
            "必需检查项执行覆盖完整性",
            "Required Obligation Coverage",
            "控制端完整性检查：确认所有启用角色下的全部 REQUIRED 实例均已获得确定结论且无缺失。",
            kind=TemplateKind.CONTROL,
            depth=EvidenceDepth.CONTROL_AGGREGATE,
            keywords=["coverage", "obligation", "required", "覆盖率"],
        ),
        _tpl(
            "P04",
            "P",
            "部署放行就绪度总聚合 (Readiness)",
            "Deployment Readiness Gate Aggregation",
            "控制端最终门禁推导：仅当正式 RELEASED 方案在完整 FULL 范围下全部有效必需项充分证明 PASS 时给出 READY。",
            kind=TemplateKind.CONTROL,
            depth=EvidenceDepth.CONTROL_AGGREGATE,
            keywords=["readiness", "gate", "ci", "放行门禁"],
        ),
    ]
    return {tpl.template_id: tpl for tpl in items}


_PLANNED_EXTENSION_GROUPS: tuple[tuple[str, str, str, str, str], ...] = (
    # N: +1 -> 7
    ("N", "N07", "网卡 MTU 与多队列配置", "NIC MTU & Queue Discipline", "规划中 (P1)：巨型帧 MTU 一致性与队列长度检查"),
    # R: +1 -> 6
    ("R", "R06", "Cgroup v2 控制器委派与配额", "Cgroup v2 Controllers & Quota", "规划中 (P1)：容器化底座 cgroup v2 控制器挂载检查"),
    # A: +3 -> 7
    ("A", "A05", "主机防火墙活动规则链", "Host Firewall Active Chains", "规划中 (P1)：nftables/iptables/firewalld 策略冲突审查"),
    ("A", "A06", "PAM 登录与口令复杂度基线", "PAM Auth & Lockout Policy", "规划中 (P2)：PAM 模块锁定阈值只读审查"),
    ("A", "A07", "本地 X.509 证书链剩余有效期", "Local X.509 Certificate Expiry", "规划中 (P1)：预置业务 TLS 证书过期时间检查"),
    # D: +2 -> 7
    ("D", "D06", "系统 Locale 与字符集编码", "System Locale & Charset", "规划中 (P1)：LC_ALL/LANG 编码一致性检查"),
    ("D", "D07", "系统受信任 CA 根证书库", "System Trusted CA Bundle", "规划中 (P1)：内部私有根 CA 注入状态核验"),
    # L: +4 -> 7
    ("L", "L04", "磁盘 SMART / NVMe 介质健康度", "Disk SMART / NVMe Health", "规划中 (P2)：底层物理块设备寿命告警检查"),
    ("L", "L05", "僵尸进程与 D 状态不可中断阻塞", "Zombie & D-State Processes", "规划中 (P1)：内核 I/O 挂死进程堆积检查"),
    ("L", "L06", "待生效内核/固件重启挂起标记", "Pending Reboot Required Flag", "规划中 (P1)：needs-restarting / reboot-required 状态检查"),
    ("L", "L07", "文件系统只读降级与 I/O 错误历史", "Filesystem Remount-RO History", "规划中 (P1)：内核文件系统异常降级日志检查"),
    # P: +3 -> 7
    ("P", "P05", "跨节点相对时钟偏差对比", "Cross-Node Clock Skew", "规划中 (P1)：集群多节点间采集时刻相对偏差评估"),
    ("P", "P06", "集群节点间互信端口矩阵", "Inter-Node Mesh Connectivity", "规划中 (P1)：多角色跨节点通信矩阵生成"),
    ("P", "P07", "历史部署残留目录与锁文件冲突", "Legacy Deployment Residue Check", "规划中 (P1)：旧版本残留 PID/数据目录冲突检查"),
)


@lru_cache(maxsize=1)
def get_catalog_groups() -> list[CatalogGroup]:
    """Return all 54 check groups across the 9 domains (40 Integrated P0 + 14 Planned P1/P2)."""
    domain_map = dict(DOMAINS)
    builtin = get_builtin_templates()
    groups: list[CatalogGroup] = []

    for tpl in builtin.values():
        groups.append(
            CatalogGroup(
                domain_code=tpl.domain_code,
                domain_name_zh=domain_map[tpl.domain_code],
                group_code=tpl.group_code,
                group_name_zh=tpl.name_zh,
                group_name_en=tpl.name_en,
                maturity=tpl.maturity,
                template_id=tpl.template_id,
                summary=tpl.description,
            )
        )

    for dom_code, grp_code, zh, en, note in _PLANNED_EXTENSION_GROUPS:
        groups.append(
            CatalogGroup(
                domain_code=dom_code,
                domain_name_zh=domain_map[dom_code],
                group_code=grp_code,
                group_name_zh=zh,
                group_name_en=en,
                maturity=TemplateMaturity.PLANNED,
                template_id=None,
                summary=note,
            )
        )

    domain_order = {code: idx for idx, (code, _) in enumerate(DOMAINS)}
    groups.sort(key=lambda g: (domain_order[g.domain_code], g.group_code))
    return groups


def search_templates(
    templates: list[TemplateSpec],
    query: str = "",
    *,
    domain_code: str | None = None,
    include_control: bool = False,
) -> list[TemplateSpec]:
    """Bilingual keyword + synonym filter across templates (REQ-TUI-05)."""
    q = query.strip().lower()
    results: list[TemplateSpec] = []
    for tpl in templates:
        if not include_control and tpl.kind == TemplateKind.CONTROL:
            continue
        if domain_code and tpl.domain_code != domain_code:
            continue
        if not q:
            results.append(tpl)
            continue
        haystack = " ".join(
            [
                tpl.template_id,
                tpl.name_zh,
                tpl.name_en,
                tpl.description,
                " ".join(tpl.keywords),
                tpl.privilege_group.value,
            ]
        ).lower()
        if q in haystack:
            results.append(tpl)
    return results
