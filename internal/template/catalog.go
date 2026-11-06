package template

type CatalogEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Summary    string `json:"summary"`
	Evidence   string `json:"evidence"`
	Privileged bool   `json:"privileged"`
	Available  bool   `json:"available"`
}

// Catalog lists all promised checks; only registered implementations are usable.
func Catalog() []CatalogEntry {
	definitions := [][4]string{
		{"H01", "CPU 架构", "METADATA", "允许的 CPU 架构集合与 uname 实测值"},
		{"H02", "逻辑 CPU 数", "METADATA", "在线逻辑 CPU 核数的最低要求"},
		{"H03", "主机总内存", "METADATA", "物理总内存容量，明确区分 GB 与 GiB"},
		{"H04", "Swap 策略", "EFFECTIVE_STATE", "Swap 激活状态与最低总容量"},
		{"O01", "发行版与版本", "METADATA", "允许的操作系统及版本范围"},
		{"O02", "内核版本", "METADATA", "内核版本的最低要求或允许区间"},
		{"O03", "sysctl 内核参数", "EFFECTIVE_STATE", "运行态内核参数的精确值或数值区间"},
		{"O04", "内核模块状态", "EFFECTIVE_STATE", "模块的期望加载或未加载状态"},
		{"S01", "路径存在与类型", "EXISTENCE", "路径存在性、节点类型和符号链接策略"},
		{"S02", "路径权限与属主", "METADATA", "属主、属组与八进制权限位"},
		{"S03", "挂载点属性", "EFFECTIVE_STATE", "文件系统类型和必需或禁止的挂载选项"},
		{"S04", "目录可用空间", "METADATA", "以只读方式核对非特权可用容量"},
		{"S05", "目录可用 inode", "METADATA", "最低可用 inode 数量或空闲比例"},
		{"N01", "网络接口与地址", "EFFECTIVE_STATE", "网卡 UP 状态与 IPv4 / IPv6 CIDR"},
		{"N02", "本地端口占用状态", "EFFECTIVE_STATE", "TCP / UDP 端口的期望监听或空闲状态"},
		{"N03", "远端 TCP 连通性", "ACTIVE_PROBE", "目标 host:port 的主动连接结果"},
		{"N04", "DNS 域名解析", "ACTIVE_PROBE", "指定类型的 DNS 记录与期望结果"},
		{"N05", "HTTP(S) 端点探测", "ACTIVE_PROBE", "GET / HEAD 状态码、响应内容及 TLS 策略"},
		{"N06", "目标路由决策", "EFFECTIVE_STATE", "目标 IP 的实际出口接口与下一跳"},
		{"R01", "系统服务状态", "EFFECTIVE_STATE", "服务运行与自启动状态独立核对"},
		{"R02", "进程存在与数量", "EFFECTIVE_STATE", "进程匹配与期望存活数量范围"},
		{"R03", "系统平均负载", "METADATA", "1 / 5 / 15 分钟平均负载上限"},
		{"R04", "进程或服务资源限制", "EFFECTIVE_STATE", "指定进程或服务的实际资源限制"},
		{"R05", "时间同步状态", "EFFECTIVE_STATE", "时钟同步标志与活动时间服务"},
		{"A01", "系统用户属性", "METADATA", "用户名、UID / GID 与登录 Shell"},
		{"A02", "用户组与成员", "METADATA", "组名、GID 与主组及附加组成员"},
		{"A03", "SSH 服务端生效配置", "EFFECTIVE_STATE", "sshd -T 生效配置，需要 root / sudo"},
		{"A04", "SELinux / AppArmor 状态", "EFFECTIVE_STATE", "安全框架类型与当前运行模式"},
		{"D01", "系统软件包", "METADATA", "软件包安装存在性与版本区间"},
		{"D02", "可执行命令检查", "EXISTENCE", "命令路径与执行权限，不直接运行目标程序"},
		{"D03", "语言运行时版本", "METADATA", "受支持运行时的路径与版本约束"},
		{"D04", "静态二进制依赖（ELF）", "STATIC_BINARY", "ELF 架构和 NEEDED 动态库，禁止使用 ldd"},
		{"D05", "环境与配置文件键值", "CONFIG", "指定配置文件中的脱敏键值匹配"},
		{"L01", "系统运行时长", "METADATA", "最短或最长运行时长要求"},
		{"L02", "内核 OOM 与致命事件历史", "LOG_HISTORY", "事件数量及日志回溯窗口覆盖证明"},
		{"L03", "系统服务异常重启历史", "LOG_HISTORY", "异常退出与重启次数及日志窗口覆盖"},
	}
	result := make([]CatalogEntry, 0, len(definitions))
	for _, d := range definitions {
		_, available := Lookup(d[0], "1.0.0")
		privileged := d[0] == "A03"
		if snapshot, ok := Lookup(d[0], "1.0.0"); ok {
			privileged = snapshot["execution_mode"] == "PRIVILEGED"
		}
		result = append(result, CatalogEntry{d[0], d[1], d[3], d[2], privileged, available})
	}
	return result
}
