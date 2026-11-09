# M4 完整 Linux AWS 权限评审记录

用户要求：AWS CLI 身份需能准备、验收和回收除 Windows 11 与压力测试外的全部 M4 Linux 资源；不能继续依赖管理员先建好 VPC、SG、密钥对与镜像。主设计者 Codex；独立评审 Pi 1.5.0 调用 `magpie / kimi-code-cn/k3.1-pro`。旧窄范围计划的通过结论不代替本版评审。

对象：[完整 Linux 计划](m4-aws-full-linux-plan.md)、[完整权限 JSON](aws/m4-full-linux-policy.template.json)，对照 M4 总矩阵与旧版边界。当前状态：**两轮通过，双方达成一致**。首轮无P0/P1，三项P2采纳后第二轮确认关闭，额外权限收紧获确认；主设计者核对后接受。只代表计划/权限模板通过，不代表真实AWS权限或M4实测通过。

## 调用与证据

模型目录确认上述精确 provider/model，未替换 highspeed。禁用工具、MCP、扩展、技能及自动上下文发现，只发送任务文件。提示与原始输出目录：`/tmp/opencode/readyspec-m4-full-aws-review-YpzE4x/`。

```sh
pi --provider magpie --model kimi-code-cn/k3.1-pro --thinking medium \
  --no-extensions --no-mcp --no-skills --no-prompt-templates \
  --no-themes --no-context-files --no-tools --no-session --print \
  @round1-prompt.md @docs/m4-aws-full-linux-plan.md \
  @docs/aws/m4-full-linux-policy.template.json @docs/m4-acceptance-plan.md \
  @docs/m4-aws-execution-plan.md @docs/m4-aws-review-k3.1-pro.md
```

首轮输入 SHA-256：

- 计划：`7811ab22990f9b52aba59d413683d083f39cc5d25b9e67261691186d7565bf14`
- JSON：`d2eb6d727866a70a26b022316ac6e334793144e74b754ddbbb0e6e6a8cbb7c5e`

## 本地核查

JSON 已解析，Sid 唯一。首轮策略20个语句、8,618个去空白字符，可作为角色inline policy（≤10,240），不能作为单份managed policy（上限6,144）。使用测试账号、24字符session和较长区域名示例替换后为9,268字符，实际渲染后仍须复核；没有把示例身份冒充真实账户。所有61个EC2动作名均在官方授权表中找到。

对照 [EC2 IAM授权表](https://docs.aws.amazon.com/service-authorization/latest/reference/list_ec2.html) 核查父VPC/新子资源、源实例卷/新image与snapshot、snapshot/image空account ARN、RunInstances资源与条件。Marketplace授权表确认 [ProductId为ArrayOfString、Subscribe和协议读取动作](https://docs.aws.amazon.com/service-authorization/latest/reference/list_marketplace-agreement.html)。本机 `aws marketplace-agreement help` 仅列 describe-agreement/get-agreement-terms/search-agreements，不假造 CLI subscribe 命令。当前没有调用AWS账户API、附加策略或创建资源。

## 首轮意见与合作修订

Kimi 首轮明确“通过”，P0/P1均无；确认新范围确实包括12个Linux基础格、六目标模板/MIN/FAULT、安全与恢复、全部AWS Linux资源自主准备和回收，且无需管理员预置VPC/镜像/堡垒机。

| 编号 | Kimi建议 | 采纳与修订 |
|---|---|---|
| P2-01 | 精确说明Marketplace实际订阅入口、条款确认和OptInRequired，不假造CLI subscribe命令 | §3明确由账户所有者在官方产品页接受报价/条款，核验协议后启动；OptInRequired停止该产品；增加真实协议读取CLI/IAM动作，新增订阅单独留清单 |
| P2-02 | 更新总矩阵入口，旧版标历史，避免40GiB/预置网络/禁公网旧规则混用 | 总矩阵§3.5指向full-linux，旧计划顶部明确已被新版替代，aws/README区分当前与旧权限文件 |
| P2-03 | 补私网依赖分发和Linux E2E签署粒度 | §1增加Linux子例编号，含M03原场景不能全项PASS；§2说明依赖预置/双架构摘要/SSH分发、B01保留浏览器驱动时仍按最多9台分批调度 |

主设计者另行收紧：订阅的多值ProductId改`ForAllValues:StringEquals`+`Null=false`，禁止混入未批准产品或缺失集合；实例属性读取移入本会话实例标签授权，避免全区域user-data读取。明确SG规则不加单独TagSpecifications，而通过带标签父SG管理并记录规则ID。

第二轮提交SHA-256：

- 计划：`52342936f34100d6a6367d3464711f8f1faee5de420dc827fe213b4796c9c791`
- JSON：`1911fb8e619162725750ba6815e4ac57eca89c99282b04f85321a0d4a788eefb`

第二轮JSON为8,771个去空白字符；仍须在真实替换后重新检查inline role总限制。`DescribeInstanceAttribute`标签条件及Marketplace协议读取动作均已核查官方表。

## 第二轮最终结论

> **评审通过，无未关闭设计阻断；仅完整Linux资源计划与权限模板通过，不代表AWS实际权限或M4实机验收完成**。

Kimi第二轮逐项确认：P2-01/02/03均关闭；多值ProductId的ForAllValues+Null约束正确；实例属性读取收窄正确；SG规则由标签父SG授权及不单独打标签的说明正确。无新增P0/P1及设计阻断。Codex接受结论；之后仅更新计划状态和本记录，不改已评审JSON内容。

最终本地验证：JSON语法、Sid唯一性、61个EC2动作名、关键资源/条件分拆、订阅集合和实例读取范围、文档链接及`git diff --check`通过。用测试账号、24字符会话和较长区域名渲染的示例为9,421/10,240字符；实际身份仍未填入或连接。最终JSON SHA-256与第二轮输入一致：`1911fb8e619162725750ba6815e4ac57eca89c99282b04f85321a0d4a788eefb`。这些离线检查不冒充Access Analyzer或云端实际IAM验证。
