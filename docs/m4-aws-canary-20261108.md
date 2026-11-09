# M4 AWS canary 执行记录（2026-11-08）

当前状态：**Linux 补测、异常矩阵与恢复证据已归档；本 Session AWS 资源回收及零残留复核完成**。下方IAM拒绝与空资源记录是早期历史，不能代表当前资源存量。用户已将本轮执行成本上限提高至30美元；Windows11及压力测试继续排除。不将本记录当作M4全量通过证据。

## 代码回归

`NativeSSH.Execute`重查身份/目录后，恢复Root并写回已核验的STANDARD/PRIVILEGED目录。RunManager→NativeSSH→回环sshd→归档RunReport测试增加实际选中绝对目录与目录消失断言；修复前失败，报告路径为相对路径，修复后通过。

另一个受控shell回归复现特权目录预占时退出trap误删已有数据的问题；清理现以本脚本成功创建目录为前提，失败的mkdir不清理已有目录。回归先失败（existing-data消失），修复后通过。

本轮`go test ./...`、`go test -race ./...`、`go vet ./...`及`git diff --check`通过。回环验证不替代云端双架构平台认证。

## 真实AWS操作

账号`891376991548`，区域`us-east-1`，所有显式创建资源均带`Project=readyspec-m4`及`Session=d6b321143e61d7bf2a6c6b5f`。SSH源`112.93.142.174/32`已在创建前重新核实。任务脚本、原子manifest、受限私钥和原始清理核对位于`/tmp/opencode/readyspec-m4-live-rHKLFs`，私钥不进入仓库或目标VM。

成功创建后已成功删除：

| 资源 | ID |
|---|---|
| 专用VPC | vpc-05780584702b78a6b |
| public subnet | subnet-0ff8287724db978c3 |
| private subnet | subnet-0375d4017e45f5b4e |
| public route table | rtb-0bcd59a3142197ad2 |
| private route table | rtb-0503833250e40e2a1 |
| IGW | igw-06860b56d1715270c |
| bootstrap SG | sg-0526f1ae2ad6d3d8e |
| target SG | sg-013f3281611aa5ce1 |
| 导入公钥 | readyspec-m4-d6b321143e61d7bf2a6c6b5f |

两子网显式关联自建路由表，只有public表有IGW默认路由；bootstrap SSH仅操作者/32，target SSH仅bootstrap SG。撤销SG默认开放出站：bootstrap仅HTTP/HTTPS与VPC内流量，target仅VPC内流量。未修改已有VPC或默认资源。

首次启动B01（官方Ubuntu x86 AMI）使用count=1、幂等client-token、IMDSv2、standard CPU credits、80GiB加密gp3、aws/ebs、3000IOPS/125MiB/s、DeleteOnTermination，instance/volume/ENI创建标签完整。AWS拒绝摘要：

```text
UnauthorizedOperation: ec2:RunInstances
resource: arn:aws:ec2:us-east-1::image/ami-0d6dd9e8265d48906
because no identity-based policy allows the ec2:RunInstances action
```

没有实例成功建立。另一次无创建副作用的最小dry-run也被拒绝；请求不含完整标签/元数据约束，该拒绝不用于证明AMI条件根因。`sts:DecodeAuthorizationMessage`也返回AccessDenied，因此不声称已解码精确不匹配条件。

## 修复候选及继续条件

[Compute策略](aws/m4-linux-compute-policy.json)仅将`UsePublicPublisherImages`替换为六个已核验AMI的精确ARN并删除该语句Owner条件，保留ebs/machine/public；其他17条语句不变。[完整填值策略](aws/m4-full-linux-policy.json)同步，模板保留通用发布者设计。IAM Owner上下文可为别名，DescribeImages的数值OwnerId不足以证明条件匹配。本候选避免该条件歧义，同时限制只能使用冻结的六个官方AMI；不能声称更新后已实际成功。

Access Analyzer findings=`[]`；compute去空白5,524/6,144字符；network仍3,169/6,144；两份并集与完整填值文件完全一致。需要管理员更新已有ReadySpecM4Compute客户托管策略并设为默认版本，随后重新创建网络、完整启动dry-run和两机canary。代理未获得IAM写入授权，也不修改IAM。

Pi + `magpie / kimi-code-cn/k3.1-pro`独立复核已完成：**“静态评审通过，未发现 P0/P1 阻断。”** 确认精确AMI清单、ARN形式和保留条件合理，不需要扩大权限或删除其他条件。首次调用120秒超时且无输出，第二次完成；原始结论保存在`/tmp/opencode/readyspec-m4-live-rHKLFs/iam-review-retry.md`。该结论仅认可待实证修复候选；仍须管理员更新默认版本后，以完整参数取得`DryRunOperation`并完成真实canary，不将静态评审冒充启动成功或M4验收通过。

## 资源回收核对

执行清理后按Project+Session双标签调用Describe复核，原始结果`cleanup-verification.json`：

```json
{
  "instances": 0,
  "volumes": 0,
  "network-interfaces": 0,
  "images": 0,
  "snapshots": 0,
  "security-groups": 0,
  "route-tables": 0,
  "subnets": 0,
  "internet-gateways": 0,
  "vpcs": 0,
  "key-pairs": 0
}
```

本轮没有启动EC2、建立EBS或分配公网IPv4；不将此结果冒充AWS最终账单查询。网络创建与回收仅证明实际涉及的API闭环，不证明镜像准备/快照/完整计算权限全部有效。

## Goal 再次恢复后的权限复查

用户再次恢复Goal后，先对原完整启动参数执行无副作用dry-run，因原子网已回收而返回`InvalidSubnetID.NotFound`；该结果不证明IAM已通过。随后在同账号/区域/双标签下重建专用网络，用新有效subnet/SG替换原参数，再次完整dry-run仍返回：

```text
UnauthorizedOperation: ec2:RunInstances
resource: arn:aws:ec2:us-east-1::image/ami-0d6dd9e8265d48906
because no identity-based policy allows the ec2:RunInstances action
```

本次VPC为`vpc-0bb411eedb93db347`，public/private subnet分别为`subnet-080d158debf59c4f3`/`subnet-0d38ce4fe1ed01d54`，IGW为`igw-003c567fc20a1cd73`；所有本次资源及公钥已再次回收。完整dry-run证据`resume-full-dry-run-result.txt`和11类资源全部为0的复查`resume-cleanup-verification.json`保存在同一受限任务目录。没有实例启动。此结果只证明当前有效身份仍未取得本次请求所需AMI授权，无法据此断言管理员是否编辑过策略或默认版本是否正确。下一步仍需管理员更新Compute策略并核实默认版本与附加身份；不得将恢复Goal指令当作IAM已经更新的证据。

## 新版生效后的真实执行

完整Compute覆盖文件另存为`docs/aws/m4-linux-compute-policy-v2-exact-amis.json`后，再次恢复执行。新的有效网络下完整启动dry-run返回`DryRunOperation`，真实B01与T02准备机启动成功。没有继续IAM写入或重复预算确认。

当前资源ID以受限任务目录`manifest.json`为准：VPC `vpc-047a8c1b34e9bddd8`；B01 `i-0a65cbab42b28045b`；六目标准备实例如下。B01 4 vCPU、六目标各2 vCPU，共16 vCPU、7台在册。启动使用standard CPU credits；不以Unlimited credits扩大费用。7个准备期公网IPv4用于联网准备，目标SSH入站仅bootstrap SG，bootstrap SSH允许操作者/32及同VPC bootstrap SG引用。target SG临时HTTP/HTTPS出站仅用于安装，正式私网验收前须撤销并从镜像重建无公网目标。

| 准备环境 | Instance ID | H01/S01/A03 | 清理 | Run ID |
|---|---|---|---|---|
| T01 Ubuntu x86 | i-0dc1f5831faca10a4 | PASS/PASS/PASS | CLEAN | run-2ICGEH3DVYVCP5IC7W5ELMFUH5 |
| T02 Ubuntu ARM | i-09fc49d1b9db29b0e | PASS/PASS/PASS | CLEAN | run-44UBEVU32C7JFWMOUOS3D64FIE |
| T03 Debian x86 | i-0a84604c2a5d78d19 | PASS/PASS/PASS | CLEAN | run-F5BEKGZ22ETMBO6VH2JHS5PYPW |
| T04 Debian ARM | i-0f27faa991598e64e | PASS/PASS/PASS | CLEAN | run-7GX73TBYCRWHLRQUX2UY7HFXTL |
| T05 Rocky x86 | i-0b4b87aa36d5323e2 | PASS/PASS/PASS | CLEAN | run-T556JUW4WSOZIGEW5YM2CIYGGS |
| T06 Rocky ARM | i-0327ce423d31b608c | PASS/PASS/PASS | CLEAN | run-MWZWEPS5EWAL6ZY6CG5536GN5P |

这些是准备期冒烟，不替代正式私网12格或36模板六类认证。管理端以`env -i HOME=/home/ubuntu PATH=/nonexistent`执行独立二进制；任务均为TRIAL，全绿仍UNKNOWN/11。主机密钥由AWS认证控制台输出取得；本地引导私钥不上传VM。B01自行生成任务SSH私钥，仅公钥分发目标。

### 实机发现及回归

1. 仅登记ed25519时，OpenSSH服务还提供ECDSA/RSA，原执行器可能优先协商未登记类型，产生IDENTITY_ERROR。真实报告`canary-report.json`保留该失败；回环sshd增加未登记ECDSA密钥后同样先失败。修复使协商优先使用该端点已信任的密钥类型，仍通过原known_hosts回调验证；未知/变更密钥继续拒绝。修复后真实Run `run-4PKVM4PXU7QNJRDSUXQ3LBTYXL`三项PASS、双目录CLEAN，且目标管理端known_hosts仅登记可信ed25519。`go test -race ./...`、`go vet ./...`通过。
2. 原A03正例写`prohibit-password`，但该OpenSSH的`sshd -T`规范输出`without-password`，真实报告如实FAIL/10（`canary-report-all-hostkeys.json`）。保留反例，将正例期望改成独立核对的实际输出后PASS；没有改模板比较语义来掩盖FAIL。
3. B01 Ubuntu24的AppArmor非特权userns策略拒绝`net_admin`，已用dmesg确认。仅专用B01 VM设置`kernel.apparmor_restrict_unprivileged_userns=0`，未改本机宿主。安装测试夹具所需C编译器/binutils后，发现Ubuntu`ldconfig`是调用`ldconfig.real`的脚本；ELF测试改为复制真实ELF工具，避免隔离夹具依赖宿主脚本路径。先失败后修复，针对性ELF及完整强制采集通过。

### 已完成验证

- B01强制采集：`READYSPEC_REQUIRE_COLLECTION_SANDBOX=1 go test ./internal/compiler -run 'Collect|Collector|Symlink' -count=1 -json`，**875 PASS、0 FAIL、0 SKIP**。原始`b01-collection-final.jsonl`包含实际网络命名空间用例，不排除N01/TCP/route测试。
- B01 Web单元测试与构建通过；补齐浏览器共享库后Playwright **7/7通过，15.1秒**。这是现有浏览器回归，不代表新增真实SSH浏览器链路全部验收。
- 十项合法构造的IAM负例dry-run均`UnauthorizedOperation`：错误Session、超规格实例、可选IMDS token、未加密卷、gp2、81GiB、超IOPS、超吞吐量、private标签配public IP、无创建标签。最初未加密/gp2请求存在相互冲突参数，返回参数错误，不计IAM证据；移除相应KmsKeyId或Iops/Throughput后才计入。汇总`iam-negatives.json`。
- 两架构最新二进制摘要见`binary-digests-negotiation-fix.json`。原始SSH报告及准备服务事实均已归档；DNS/HTTP服务仅绑定127.0.0.1。

尚待：正式私网环境与12格、各平台36模板六类认证、MIN/checksum/目录逐层回退、完整身份认证与故障恢复、Linux E2E与最终回收。当前正在执行六平台36模板真实SSH冒烟，不能提前签署这些条目。初步基础费用约0.35美元/小时（7实例、320GiB及7公网IPv4），另计快照/传输/税；以30美元执行成本上限持续核对，非账单实测值。该上限不是AWS硬性封顶；当前身份无权读取或修改AWS Budgets，须由管理员核实预算并配置告警。

### 36模板准备期最终结果及镜像切换

最终六平台各36项全部PASS，**216项PASS，6份Run均COMPLETED、双目录CLEAN**；TRIAL返回UNKNOWN/11，未冒充READY。六份原始权威RunReport及二进制摘要、IAM负例、浏览器同源证据已归档到[证据索引](evidence/m4-20261108/index.json)，原始过程日志继续保留受限任务目录。

| 准备环境 | 最终36模板Run ID | FULL AMI |
|---|---|---|
| T01 | run-6AV4VCVVW7JE44YIDYFZXUVQIW | ami-0bea52c279744831b |
| T02 | run-5SOY3DNRXNZQ4RE7CX5KGI6L7N | ami-012b511f3f143cdf4 |
| T03 | run-YNF5HMKPGQSWXGSXEKIPSXUKET | ami-0c5d96353be3a4ebb |
| T04 | run-Q4JYG7PQ7Y5RU7IELKVLO2SGFW | ami-0668a3efb741dde39 |
| T05 | run-P4IH5VGRAFTVM3YF77OIXNBGGW | ami-0262c4e9c184828ec |
| T06 | run-OTG47VN3LI25Z6TRJH5UFRHLCN | ami-06734f43029c09991 |

本轮36模板实测追加修复：D04允许非PIE合法共享对象携带PT_INTERP；日志连续性审计纳入共享序号域的可读user记录；旧journalctl历史边界改显式`--reverse -n 1`；启动时间计算对uptime向上取整，避免整秒误差排除启动最早记录。对应原工件回归均先红后绿，保留坏库/PIE、日志缺口/序号域改变/权限/损坏的拒绝检查。最终强制采集**885 PASS、0 FAIL、0 SKIP**。真实日志复制测试在B01完成正常轮转与损坏active副本两类，L02/L03均符合PASS/ERROR预期，原始`b01-real-journal-boot-fixed.jsonl`；仅B01主动rotate，损坏仅作用于测试副本。

Rocky默认独立system/user序号域仍返回`SEQUENCE_DOMAIN_CHANGED` ERROR，保留反例。FULL夹具配置`/etc/systemd/journald.conf.d/90-readyspec-m4.conf`的`SplitMode=none`，使后续要求窗口处于可证明的统一域；没有删除原日志、忽略不同域或降低覆盖判定。这是明确的夹具前置条件，不意味着默认Rocky日志历史已全部认证。

真实报告的浏览器补充验证：使用最新独立二进制服务、真实SSH报告、正常令牌登录与会话/CSRF路径打开报告，页面接口JSON与CLI归档逐项完全一致，36项可读、无pageerror、无非回环请求。证据`native-browser-evidence.json`，截图`native-report-browser.png`在受限任务目录。没有用启动令牌直接冒充已登录API会话。

镜像创建前停止夹具服务、清理cloud-init状态/machine-id与SSH主机密钥、刷盘并停止源实例；CreateImage对image/snapshot均带双标签。源准备实例已停止。私网切换脚本等待所有镜像available后终止六个源实例，停止B01以释放vCPU，再创建M01/M02与六个私网目标；正式目标无公网IP、无外网路由，并撤销target准备期HTTP/HTTPS出站。后续须重新核验新machine-id、boot-id与可信host key，不能沿用准备实例身份。

## 正式私网与身份验收进展

六份镜像 available 后，六台准备机已终止，B01已停止；正式 M01/M02 与 T01–T06 已创建。六目标重新核验 machine-id、boot-id 和可信 host keys，无克隆身份复用。2026-11-08 15:53:36Z 的只读检查确认：9台在册、16 running vCPU；M02与六目标共7台无公网IP，private路由只有local；管理与目标出站均限VPC；9个加密gp3卷共400GiB，IMDSv2与DeleteOnTermination符合要求。该时点仍有收费实例、卷和镜像快照，前文早期资源全0记录不能用于当前回收结论。

- 正式 `M01/M02 × T01–T06` 12格全部 GATE/FULL、READY/0、三项PASS、CLEAN，见 `platform-matrix-summary.json`。
- 正式 ARM M02 对六目标的36模板正例全部 GATE/FULL、READY/0，共216 PASS、CLEAN，见 `private-full-summary.json`。这只完成每模板六类认证中的正例，不能签署完整模板认证。
- 六平台 I01 各9种组合均符合预期：普通STANDARD成功、普通PRIVILEGED拒绝、NOPASSWD成功、sudo密码正确成功及错误/缺失拒绝、受限白名单拒绝、root和实际UID0别名成功。root两种登录方式均归档主机及每条Evidence的 `high_risk_root_audit=true`。
- M01/M02各跨T01/T02验证SSH密码正确/错误/缺失与Unix Agent。六目标混合清单一台拒绝时返回15、无检查结果；独立投递/执行计数仍待补证。

上述89条正式执行摘要及对应原始报告已检查不含私有测试密码、私钥或owner token后归档至[证据索引](evidence/m4-20261108/index.json)。版本保持分别绑定：12格使用boot-fix版本，正式36模板正例使用root-audit版本；I01包含root-audit及后续sudo-fix版本，不将旧报告冒充最终候选产物。

新增实机缺陷与验证：`sudo -l <command>`明确拒绝可能静默退出1，现按typed SSH退出状态分类为AUTH_ERROR/15，其余诊断与未知状态仍UNKNOWN；真实回环回归先红后绿。空Agent或Agent没有选定公钥原误分类NETWORK_ERROR/11，已用真实回环SSH握手复现并改为AUTH_ERROR/15；Agent endpoint不可用原误返回INFRA_ERROR/13，现区分认证输入不可用与内部凭据安装失败。相关executor/runmanager/CLI race回归通过，实机扩展认证仍在执行。

报告页增加按实际UID0或归档审计字段显示的root风险提示，不依赖登录用户名、不增加确认步骤；Web测试24/24及构建通过。六目标独立2222端口SSH与挂载命名空间夹具、双架构fanotify观察器准备完成，尚不代表故障认证已通过。

## 新候选安全与模板证据补充

最终 mount-ID 候选为 amd64 `fda324aa221464ed9301eb9a50fe9565e54d271ae5abd47730f2b25d1969a622`、arm64 `c3861f3b0882f5802e8da43cb4a7bef38772559b40dc37437c9ecc3034027c6d`。六平台均以该候选完成 root 直连/无 sudo 复验：报告 UID 0 风险标记存在；独立 fanotify 对每台记录 STANDARD 与 PRIVILEGED 各一次 Goss，EUID 均为0；Goss SHA256分别符合 x86/ARM嵌入摘要，属主 root、模式0700；未见 sudo 标记，均 CLEAN。摘要、可脱敏原始报告和观察记录已归档至证据索引。

六平台 36 模板边界值矩阵已各为 GATE/FULL、READY/0、36 PASS、CLEAN。现场覆盖、合并映射和普通正例各平台也均已通过；合并结果按 72 个逻辑项核对稳定项ID和资源映射，区分共享内建Goss与独立Command资源。归档候选字段现更新为 mount-ID 最终候选；此前早期同名用例记录仍保留其原始摘要，不覆盖。

目录回退：T04/T05 完整矩阵通过。T01、T02、T03、T06 各 32 行原始断言有一项未通过；后续按报告清理路径过滤观察器事件并复核后，T01 的失败断言把混合 Run 的 STANDARD 执行也算进了特权零执行判据，报告显示 STANDARD PASS/CLEAN、无特权 Goss 执行；T06 的额外两条观察事件属于另一个工作区，匹配本 Run 的 STANDARD/PRIVILEGED 各一次且 READY/CLEAN。两者的原始断言结果保留为 false，分析记录支持其目标行为。T03 报告选中 `/var/tmp` 而断言预期 `/run`，本次没有保存 `/run` 基线挂载选项，保持未决；T02 最终只读用例为 NETWORK_ERROR 且零 Goss，连接中断，不能签为通过。逐 Run 分析见证据索引。特权目录 `/run` 叠加挂载记录问题已由 mount-ID 候选修复，本地叠加挂载回归通过，最终候选在T05实测回退至 `/var/tmp`。

模板 FAIL 矩阵正在逐平台修正夹具并验证消费证据。T06受控负例已将34项分类为预期FAIL；L02/L03在SELinux受限的专用sshd上下文中因journal读取权限返回ERROR，未证明本次标记被消费，明确保持未证明，不能改记为预期FAIL。该次运行中的R03使用独立mount namespace将 `/proc/loadavg` 固定为合成值，未产生CPU/内存负载；L02只写带唯一case标识的受控kernel journal文本，L03用Restart=no专用unit。

## 本轮后续证据与未闭合门槛

MIN 已在六目标完成：无 Python/Goss/Agent 预装时，H01 与 POSIX Shell 主闭环为 READY、两项 PASS、CLEAN；sha256sum-only 和 openssl-only 分别在六目标成功；两种校验器均缺失时六目标均以 `IDENTITY_ERROR/UNKNOWN/11` 拒绝，且没有检查项或清理项。独立 S01 缺 Python 反例六目标均为 H01 PASS、S01 ERROR、UNKNOWN/11、CLEAN。报告摘要与可脱敏原始报告已追加到证据索引。

E2E-04 已有 CLI 退出码 12、13、15、16 门禁记录，并对同一已归档 Web 报告完成 CLI/Web 内容与摘要一致性比对。该 Web TrialRun 为 UNKNOWN，不能作为 READY 门禁通过；退出码 0、10、11 及服务委托覆盖仍未完整。E2E-05 的受控单目标 Run 只证明执行哨兵计数为 1、清理 CLEAN；取消请求未被服务接受（CLI 退出 12），Run 后续 COMPLETED，故取消码 14 尚未证明。管理端中断/恢复和清理前断线窗口仍未完整实测。

模板异常矩阵仍缺 T05，且 T06 有一个未通过断言；T01/T04/T06 分类断言尚待逐项复核。目录矩阵的未通过项及其原始报告均保留，不以重新解释断言补签。最终候选完整回归、剩余 E2E 子路径与 Windows 11 原生管理端/跨机器环尚未完成；Windows 11 仍等待用户提供 VM。压力测试、压力注入、性能计量和 20×100 集群按范围排除，不影响上述功能验收缺口。

晚期证据索引已追加 MIN、root 审计、故障安全、目录矩阵逐 Run 分析、E2E 摘要、可用原始报告及 AWS 清理复核，并通过私密字段扫描。AWS 资源按任务 manifest 中精确 ID、Project/Session 双标签回收；2026-11-08T19:02:22Z 复核时实例、AMI、快照、卷、ENI、安全组、子网、路由表、IGW、VPC 和密钥对残留均为 0。尝试重建最小 Linux 拓扑以补剩余实机项时，当前 AWS IAM 身份对 `ec2:CreateVpc` 返回 `UnauthorizedOperation`，没有资源创建；该阻塞已脱敏归档，未尝试绕过权限。

本地 `go test ./...` 全包回归通过；结果摘要及包状态已归档。该本地结果不替代下述实机验收门槛。

因此 Linux 仍有未完成的实机项：T02 目录只读最终拒绝的重测、T03 `/run` 基线挂载证据、T05 模板异常分类、Linux 管理端中断/恢复、E2E-04 剩余退出码/服务委托及 E2E-05 取消码14与恢复窗口。AWS IAM 授权恢复后需重建专用环境继续验证。Windows 11 原生管理端和跨机器导入环仍等待用户提供 VM；不以其他 Windows 版本替代。压力测试、压力注入、性能计量和 20×100 集群按要求排除。整体 M4 不能签署完成。

## 新 Session 恢复补测结果

权限核查和补权后，Session `117f28ab8bdbd660bf43405d` 的67个任务操作均通过当前身份策略模拟；VPC创建和公钥导入的实际 dry-run 返回 `DryRunOperation`。详见[权限核查报告](m4-aws-permission-audit-20261108.md)。本轮按30美元总执行上限和独立重建控制额度跟踪，不把该数值当作AWS硬性封顶。

通过的新证据已归档到[证据索引](evidence/m4-20261108/index.json)的 `recovery_117f28ab` 节点；55个文件的SHA-256复核通过，原始报告位于 `recovery-117f28ab/`。早期测试脚本/输入失败保留在受限工作区，不计作产品通过证据。

- 目录补测最新5个案例全部符合预期：T02全候选只读为 EXECUTION_ERROR/11且该模式零Goss执行；T03实测 `/run` 原本noexec，NOPASSWD和root均正确回退 `/var/tmp`；T01两级noexec回退也通过。历史失败尝试保留。
- T04、T05的FAIL与异常矩阵补齐。T04/T05最终FAIL均为36项预期FAIL、NOT_READY/10、CLEAN；异常分别为34 ERROR + 2 PASS和33 ERROR + 3 PASS，均为COMPLETED/UNKNOWN/CLEAN。日志权限属于专用夹具前置条件，修正后证明L02/L03负例实际被消费。
- 委托/清理修复候选的M02取消为 RUNNING→CANCELLING→CANCELLED，提交退出14、CANCEL_ACCEPTED、CLEAN、执行次数1。该候选CLI直接/委托覆盖0、10、11、12、13、14、15；既有16证据保留。发布修复候选另完成M01/M02原生Web与离线导入回归。
- M01/M02的投递后、执行中、清理前三窗口分别完成控制器强杀与独立SSH断线实测；恢复不自动重跑，执行次数符合相位预期，残留经身份复核后清理。清理前活跃进程未确认时保持UNKNOWN，显式确认后CLEAN。清理前SSH断线允许Run为COMPLETED/UNKNOWN且cleanup UNKNOWN，再恢复处置，不强制要求FAILED。
- M01与M02原生Web均完成作者交付、真实SSH TrialRun、归档报告读取、发布和YAML导出；TrialRun为COMPLETED/UNKNOWN，检查PASS、CLEAN，无pageerror或非回环请求。最终候选 M01→M02→M01 三站全新工作区离线导入的计划摘要和导出SHA-256一致。
- 最终候选SHA-256：amd64 `afb0d716ee19351b24e7030d2ff06b3cfd7bb67435668d787a3e609d3ad522e7`，arm64 `a1348677524ff0b6b4163e0a859ca402d37e51b35adc63e9e301f0bb1f27fc6a`。

补测中关闭的产品缺陷均有先红后绿回归：CLI委托保留已验证方案原文，避免可选空字段造成摘要不一致；崩溃恢复清理保留已核验主机指纹且公开报告不暴露owner token；PLAN_LOCAL发布计算模板摘要并同步锁与检查项引用。相关改动见 `internal/cli`、`internal/executor`、`internal/runstore`、`internal/runmanager`、`internal/planstore` 与报告Schema；SPEC同步说明清理身份与私有令牌边界。

TODO-02高级自定义结果协议继续冻结，不以本轮启用。TODO-03仍需要用户提供原生Windows 11 VM，Windows跨机器环也保留待测；压力测试、压力注入、性能计量、20×100集群和TODO-04继续排除。整体M4仍不能签署完成。

2026-11-08T23:58:16Z，按精确manifest与Project/Session双标签回收本 Session：终止9台在册实例，注销6份AMI并删除6个快照；分离卷和ENI无残留。随后复核 live_instances、images、snapshots、volumes、network_interfaces、security_groups、subnets、route_tables、internet_gateways、vpcs、key_pairs 均为0。证据见 `recovery-117f28ab/cleanup-verification-final.json`。
