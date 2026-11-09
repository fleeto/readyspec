# M4 AWS 完整 Linux 资源权限与执行补充

状态：**Pi + Kimi 3.1 Pro 两轮评审通过**，三项P2建议已采纳关闭、无未关闭设计阻断，主设计者核对后接受，记录见 [完整范围评审](m4-aws-full-linux-review-k3.1-pro.md)。本版按用户最新要求覆盖**除 Windows 11 与压力测试外的资源准备、验收和回收**，替代旧版“管理员预置网络/镜像、仅执行EC2”的范围。交付 [完整权限 JSON](aws/m4-full-linux-policy.template.json)。本轮只准备文件与评审，未连接 AWS、修改 IAM 或启动收费资源。

## 1. 资源闭环

执行身份为无其他 Allow 的专用短期角色，填入商业分区的 `ACCOUNT_ID`、单一 `REGION`、本轮唯一 `SESSION_ID`。两标签 `Project=readyspec-m4`、`Session=SESSION_ID` 在创建时写入；不能事后给其他资源贴标签来接管。只读 EC2 元数据可见指定区域内其他资产；pricing/STS/Marketplace 元数据是全局 API，不伪称标签隔离。策略不是组织级权限边界，仍受 SCP/会话策略/权限边界控制。授予此策略不等于批准当前就产生费用。

| 阶段 | 可由本角色完成的 AWS 操作 | 约束与检查 |
|---|---|---|
| 盘点 | STS、EC2资源/配额/默认EBS配置、公开Price List、已订阅Marketplace产品、Access Analyzer | 核对预期账号、区域、配额、官方AMI来源；不读取他人凭据 |
| 网络 | 创建/删除专用VPC、subnet、IGW、route table、SG；关联路由、设置DNS/子网属性、增删SG规则 | 输入和变更前后均核对标签、VPC拓扑和精确ID；不使用default VPC，不接入生产网络 |
| 身份输入 | 本地生成短期SSH私钥，仅将公钥ImportKeyPair；完成后DeleteKeyPair | 不用CreateKeyPair返回私钥、不上传私钥、不创建IAM角色/profile/PassRole |
| 镜像/恢复 | 启动官方Linux准备机，SSH安装夹具、CreateImage/CreateSnapshot、从本会话AMI/快照恢复；DeregisterImage/DeleteSnapshot | image/snapshot创建时同样打双标签；AMI与snapshot ARN账号段为空，操作时另限制ec2:Owner=本账户；不分享或公开镜像 |
| 管理端/目标 | B01 linux/amd64构建机，M01/M02原生x86/Graviton管理端，T01–T06六目标及S01辅助服务 | B01候选t3.xlarge(4CPU/16GiB)，M01/M02用t3/t4g.medium，目标用small；B01先完成后回收，稳态最多9台，不建20台集群 |
| 证据/回收 | 拉回原始RunReport、Journal脱敏证据与摘要；清理实例、ENI、EBS、AMI/快照、网络和公钥 | 使用本地受限工作区，不需S3/SSM/CloudFormation；不删共享资产 |

所有Linux实机条目保留：2管理端×6目标共12个基础格、六目标36模板六类认证、MIN无Python/Goss/Agent、sha256sum-only/openssl-only/全缺失、双模式逐层noexec/ro回退、身份/提权屏障、真实进程清理、CLI门禁、浏览器与服务委托、故障中断/恢复。功能性单目标故障仅在专用FAULT VM，保留M4-E2E-05；§7负载/压力注入/性能计量完全排除。Windows相关6格、M03/第二账号/Named Pipe Windows11及TODO-03保持待测；F14/TODO-04排除。本轮可完成这些范围内验收，不宣称完整M4全部门槛关闭。

E2E证据按Linux子例编号签署，例如`M4-E2E-04-LINUX-M01/M02`。原包含M03的E2E-01/02/04/05不能记整项PASS；E2E-02先验证M01→M02→M01的Linux导入环，保留跨Windows环待测。签署表区分“Linux子路径已验收”和“原三管理端场景待M03”，不以本轮排除项覆盖原规格。

## 2. 联网准备和正式私网验收

创建独立IPv4 VPC、一个 public-bootstrap subnet 和一个 private-validation subnet，各自创建带标签route table并显式关联。IGW仅连接本次VPC；只给public route table添加外网缺省路由。默认main route table、default SG和default NACL不修改、不补标签；删除VPC时由AWS处理默认资源。私网表仅local路由，无NAT/IGW/peering/TGW/VPN，不创建EIP、NAT Gateway或专用主机。

旧版一律禁止public IP依赖管理员VPN，本版为自主引导作明确调整：M01兼堡垒机以及临时B01/镜像准备机可使用`NetworkClass=public-bootstrap`的临时公网IPv4；SG的SSH入站只允许事先登记的操作者公网IPv4 `/32`（动态出口改变先停止连接并审阅更新），准备目标机入站只允许M01 SG，禁止`0.0.0.0/0`、`::/0`SSH/RDP和开放Web端口。浏览器/测试驱动通过SSH转发访问loopback服务；只绑定回环的服务不直接映射公网。准备期允许依赖下载所需出站，记录公网IPv4计费。

六目标镜像准备完成后，关闭并清理准备机，从记录的BASE/MIN/FULL/FAULT AMI在private-validation subnet新建无公网IP的目标和S01/M02；通过M01的私网SSH跳转访问，不把操作者AWS凭据放入VM。正式执行前停止依赖下载，把M01公网出站与目标隔离，目标无互联网路由，使用内部DNS/NTP/HTTP/TCP/LDAP等服务。B01需要的Node24、浏览器、Go与测试依赖先安装；测试驱动的开发工具依赖不当作ReadySpec运行时依赖。

准备期在M01/B01下载并校验锁定的两个管理端产物、双架构测试驱动/浏览器/系统包及服务夹具，把各架构依赖装入对应准备AMI，附缓存清单/版本/摘要；必要文件经SSH/SCP分发到private M02/S01并校验，不临时给私网新增公网路由取依赖。B01可保留为外部Playwright驱动直到浏览器用例结束；“B01先完成后回收”指结束构建/浏览器驱动职责后回收，不要求提前丢弃唯一驱动。若保留B01，则M01/M02分批管理六目标：B01+单管理端+S01+六目标共9台，另一个管理端保存基线和证据后终止，切换时按清单重建；跨管理端导入只需要两管理端/B01和少量目标，可先收缩目标数。停机实例同样计入9台在册上限。ReadySpec二进制的离线smoke使用不含Go/Node/Python的PATH，浏览器驱动在独立进程/测试机，不借工具存在冒充产品依赖。

SG/路由的CIDR、端口、关联关系是**执行前输入审阅和创建后检查**，当前IAM无法按整条SG规则内容表达“只能允许某个SSH CIDR”，不能声称它是IAM保证。策略限制可修改的SG/路由表均属本会话，但能够更改其规则，必须遵守精确manifest与同VPC引用检查。RunInstances一律新建单个主ENI，无NetworkInterfaceId/额外网卡/launch template/profile/Spot；NetworkClass标签、子网类别、SG类别逐字段比对，不允许用public-bootstrap规避正式目标私网要求。IAM只对对应NetworkClass强制public IP true/false，不检查“该标签是否真的用于某种角色”。启动后再核对实际网卡/subnet/SG/IP/route table，负例未过不扩展。

SG规则不单独请求TagSpecifications，规则变更通过有双标签的父SG授权并在manifest记录rule ID；策略没有授予`security-group-rule/*`的创建标签权限。双标签要求针对本次显式创建的VPC/subnet/route table/SG/IGW/keypair/instance/volume/ENI/image/snapshot，不冒称AWS自动默认资源或SG规则都带标签。新建SG自带的开放出站规则须在启动验收实例前按其类别撤销/收紧，并核对最终规则集。

## 3. 镜像、系统夹具与证据

优先查询Canonical `099720109477`、Debian `136693071363`、Rocky `792107900819`的公开EBS AMI。IAM允许这些发布账号的镜像，**不精确限制发行版版本**；操作前必须核对24.04/13/9、架构、image类型、root大小与完整AMI ID，写入冻结清单。来源参考：[Canonical](https://documentation.ubuntu.com/aws/aws-how-to/instances/find-ubuntu-images)、[Debian](https://wiki.debian.org/Cloud/AmazonEC2Image)、[Rocky发布账号](https://forums.rockylinux.org/t/looking-for-a-rocky-10-ami/18989)。不单凭镜像名称信任。

如果Rocky所在区域只能使用AWS Marketplace分发，模板有**默认不匹配真实资源的可选授权**：`ROCKY_{AMD64,ARM64}_MARKETPLACE_AMI`、`..._PRODUCT_ID`（prod-产品ID）和`..._PRODUCT_CODE`（EC2计费产品代码）是三种不同值，不得混用。由只读DescribeImages/官方产品信息核验后渲染，必须对应Rocky官方两个精确AMI与条款；未填保持禁用，不得放宽为整个Marketplace账号。`Subscribe`用`ForAllValues:StringEquals`配合`Null=false`要求请求产品集合存在、且全部属于两个批准产品，防止混入第三个产品；此为IAM权限，不等于CLI有通用`aws marketplace subscribe`命令。当前CLI的`marketplace-agreement`仅有search-agreements/describe-agreement/get-agreement-terms，策略授予这些只读操作用于核实协议。实际订阅需账户所有者在具体官方产品页/控制台核实零软件费用、报价与条款并接受，再核验订阅状态后启动；遇`OptInRequired`记录并停止该产品启动，完成准确产品确认后才重试，不绕过或伪造订阅。新增零费用订阅单独列入最终清单，取消由用户决定，角色不能取消已有订阅。实例级`ProductCode`存在时必须在这两个代码内；不允许任意付费镜像。若新镜像AMI ID变化重新冻结授权。此订阅/许可确认是账户法律条件，不是Windows测试范围扩大。

通过SSH在专用VM创建本地测试用户/密码、NOPASSWD和受限sudo夹具、独立sshd、内部DNS/HTTPS测试CA、LDAP/NSS、运行时、systemd/journal窗口/轮转/损坏副本。宿主/生产不变，ReadySpec产品不安装依赖、不修改sudoers；这是外部验收夹具准备。BASE/MIN/FULL/FAULT按原矩阵配置，MIN须实测工具清单，不把镜像Minimal标签当证据。为noexec/ro用例在专用VM内创建可恢复挂载命名空间/挂载点；不得把需要权限的测试跳过当通过。

制作AMI前停止服务、刷盘并停止准备实例，默认用CreateImage正常一致性路径，不以NoReboot掩盖未刷盘；image和自动snapshot分别在TagSpecifications写入Project/Session，独立CreateSnapshot也需要目标snapshot标签及源卷双标签。清除源machine-id/SSH host keys/cloud-init状态，保留账户/夹具的受限恢复材料；每次重建实测新的machine-id、boot-id和host key，不能共享模板主机密钥，host-key可信来源为AWS认证控制台输出或独立管理员交付。恢复路径是从标记AMI重新启动；独立snapshot若需要覆盖root block mapping，也必须属于本会话且有完整快照/设备映射记录，不授权AttachVolume到无关实例。

所有root使用显式`alias/aws/ebs`加密gp3，Iops=3000、Throughput=125；IAM每卷≤80GiB。正常目标root≤40GiB、B01≤80GiB，批准单root EBS AMI；超限不自动扩大。这里80GiB是单卷上限不是费用总上限，不支持CMK，组织若强制CMK需另审该key最小使用权限。IAM不授予修改加密默认设置或KMS管理。

## 4. 启用、费用和回收

AWS CLI接入后先只读核对account/region/网络CIDR冲突/配额/AMI/价格及IAM生效结果；尚未创建资源。JSON主参数只需ACCOUNT_ID、REGION、SESSION_ID（建议24位小写十六进制唯一值），不再依赖已有VPC/subnet/SG/keypair或预制私有镜像。可选Marketplace参数若未查实不得猜。专用角色的trust/签发/附加策略由账户管理员设置，本角色不管理IAM，不请求AdministratorAccess。实例属性读取（可能含user-data）只授予本会话实例；全局协议/定价与区域基础EC2元数据的可见范围已明确，不声称只读权限全部按标签隔离。

策略为单个IAM **inline role policy**，离线计数必须≤10,240个去空白字符；若>6,144不得直接作为单个customer managed policy附加，需要管理员拆分。策略包含请求标签与资源标签的分阶段授权：CreateSubnet/RouteTable/SG的新资源须带RequestTag，父VPC另检查ResourceTag；CreateImage源instance、CreateSnapshot源volume检查ResourceTag，新image/snapshot检查RequestTag，ARN账号段留空。CreateTags只允许指定创建API当场触发，禁止事后修改身份标签。额外已有管理员策略会使这些Allow范围失去隔离意义。

实际启动前记录具体小时价、EBS/公网IPv4/快照/传输成本、最大预算、最长运行时间、清理期限和独立回收负责人，经用户批准具体开销；权限模板评审不等于支出授权。无硬预算数值不启动。最多9台同时在册（准备B01/镜像机也计入，稳态9台包含M01/M02/S01/六目标），每次单count1串行创建，ClientToken幂等；镜像变体可快照顺序切换。IAM无全局台数/总费用硬限制，ExpiresAt不自动删除、预算警报不自动断电，凭据过期不关资源；每批核对账本/存量并预留回收费用，独立管理员具备过期兜底权限。

验证步骤：离线JSON/action-resource条件检查 → 渲染后Access Analyzer（无未解释ERROR/SECURITY_WARNING） → 专用角色dry-run/必要的Policy Simulator → 从一VPC/两子网/一IGW/一组路由SG/一公钥及两台canary验证端到端建立和拆除 → 再建完整矩阵。DryRunOperation只代表权限预检，不证明配额/引导/订阅；记录所有AWS返回及真实资源ID。负例覆盖错区域/账号/Session、无标签、新建资源父VPC不属本会话、错误镜像、已有ENI/跨VPC SG引用、公网SSH、超规格/IOPS/磁盘、操作/读取他人实例属性或快照、改标签/分享镜像/IAM/CMK、订阅混入第三产品或缺产品集合。可由IAM强制的必须被IAM拒绝，纯输入控制必须在发请求前拦截，分别记录，不伪称都由IAM保证。

证据回收本地受限目录并脱敏，绑定最终源码SHA/补丁摘要、二进制/Goss/方案摘要、EnvID/RunID/镜像快照/UTC/命令退出码，确认成功归档后依依赖逆序清理：停止任务 → 终止实例并核对DeleteOnTermination → 删除本会话残留未附加ENI/卷 → deregister本会话AMI → 删除不再被任何保留AMI引用的本会话snapshots → 撤销自建SG规则/删除SG → 解除自建route table关联并删自建routes/route tables → 删除subnets → detach/delete IGW → 删除VPC → 删除导入公钥。默认VPC附属资源不接管；共享或归属不确定即停止该删除并交独立负责人，不扩大标签匹配范围。再次Describe核对本会话实例、卷、网卡、AMI、snapshot、SG、route table、subnet、IGW、VPC、keypair均无收费残留；保留异常清单，快照/停机卷仍可能计费。

此策略覆盖非Windows11/非压力验收所需的AWS资源生命周期，不保证地区容量、账户配额、Marketplace法律条款或产品测试本身通过；这些均记录实际证据，不降低原验收要求。当前尚未附加策略、执行云端验证或产生AWS费用。
