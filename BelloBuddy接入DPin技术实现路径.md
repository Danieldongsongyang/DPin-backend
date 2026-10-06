# BelloBuddy 接入 DPin 技术实现路径

- 重写日期：2026-10-04（Asia/Shanghai）
- 最近修订：2026-10-05（Asia/Shanghai）；修正方案特殊选项及认证接入路径
- 需求基线：[BelloBuddy 接入 DPin 需求文档 v0.20](/Users/a1/Desktop/DPin-backend-newapi/BelloBuddy接入DPin需求文档.md)，并纳入本轮对方案网页、职位提示词、安装运行方式及刷新机制的确认
- DPin 工程：`/Users/a1/Desktop/DPin-backend-newapi`
- BelloBuddy 工程：`/Users/a1/Desktop/bosscli/recovery/BelloBuddy`
- 文档用途：基于实际代码的完整改造计划；不含伪代码，不表示产品功能已经实现或完成联调
- 命名说明：本文统一使用 DPin；此前沟通中的“低聘”指同一个 DPin 项目

## 1. 实施目标与边界

### 1.1 最终产品行为

用户安装 BelloBuddy 后直接打开程序，使用 DPin 账号登录。BOSS 直聘账号仍在用户电脑的本地浏览器中登录。沟通方案与招聘／筛选方案的字段契约，以 BelloBuddy 当前本地留存的两类真实完整 JSON 为基准。用户通过已经编写好的两个网页自定义方案；网页逐项对应真实 JSON 字段，已有项完成双向适配，缺少的业务配置项补齐控件与交互，再生成完整 JSON 提交 DPin 保存。日常使用时，用户只需在职位配置的下拉框中选择沟通方案与筛选方案，按现有确认方式保存职位选择。

任务真正启动前，BelloBuddy 从 DPin 获取所选方案的完整内容，只放在本次任务内存中。筛选判定、意图识别、固定话术、附件简历处理、联系方式动作以及 BOSS 浏览器操作继续在本地执行。需要 JEV 判定或 MaxKB 生成回复时，通过 DPin 桥接调用。

DPin 复用自己的注册、登录、附加验证、刷新、退出、权限、模型、渠道和额度机制。本次新增的是方案及职位配置能力、用户态 AI 桥接和 BelloBuddy 的客户端适配。

注册并完成认证后不新增人工审批开通环节；具体可用模型、渠道和额度仍服从 DPin 管理配置。未绑定沟通机器人也不妨碍筛选与其他可执行本地流程。

### 1.2 已确认的实施约束

| 事项 | 实施基线 |
| --- | --- |
| 两个方案网页 | 复用用户已有设计，以两类真实 JSON 的字段与语义为准，逐项适配并补齐缺失业务配置项，用于生成、保存和修改完整方案 JSON；不承担招聘任务执行 |
| 网页部署 | 随 BelloBuddy 安装在本地，通过设置入口打开；方案数据从 DPin 读取并保存到 DPin |
| 方案可见性 | 公用方案可见可调用；私人方案属于创建者，仅本人可见可调用。下拉框候选 = 公用 + 本人私人，对所有职位条目一致，职位不改变可见范围 |
| 方案选择 | 职位下拉框选择已保存方案；选择结果保存到 DPin，不要求重复填写表单 |
| 方案正文 | 只在 DPin 持久化；客户端仅在编辑和当前任务执行期间临时持有 |
| 产品认证 | 必须完成 DPin 认证才能运行任务；本地执行模式和 BOSS 已登录都不能代替产品登录 |
| 登录续期 | 遵循 DPin 现有刷新机制，包括凭据轮换、30 秒并发容错与既有异常处理，不改造其刷新策略 |
| 安装运行 | 正式程序默认正常联网，用户双击即可启动，不依赖恢复工程的外层网络沙箱或启动脚本 |
| AI 上游凭据 | 只在 DPin 服务端配置；用户不创建或填写模型 Key，不选择机器人 |
| MaxKB 关联 | 当前一家公司，同一岗位统一关联同一机器人，由管理员维护，不按使用人改变 |
| 本地业务数据 | 候选人资料、简历、BOSS 聊天记录、任务执行记录留在本地；不在 DPin 建招聘业务库 |
| 首期平台 | BOSS 直聘；其他平台后续扩展 |

方案 JSON 保存、职位选择保存和招聘任务执行是三个不同操作。编辑方案不会自动替用户更换职位选择；职位选择只建立方案引用；执行时才将引用解析成完整配置。

## 2. 实际代码基础与改造入口

### 2.1 DPin 已有能力

| 代码位置 | 已核对能力 | 本次使用方式 |
| --- | --- | --- |
| [router/api-router.go](/Users/a1/Desktop/DPin-backend-newapi/router/api-router.go)、[controller/user.go](/Users/a1/Desktop/DPin-backend-newapi/controller/user.go) | 注册、密码登录、附加验证、刷新、退出、当前用户与会话接口 | 保留现有账号体系，客户端适配实际响应 |
| [service/auth_session.go](/Users/a1/Desktop/DPin-backend-newapi/service/auth_session.go)、[model/user_session.go](/Users/a1/Desktop/DPin-backend-newapi/model/user_session.go) | 会话检查、刷新凭据轮换、并发竞争处理、会话撤销 | 直接复用，新增验证聚焦客户端对接 |
| [middleware/auth.go](/Users/a1/Desktop/DPin-backend-newapi/middleware/auth.go) | 用户认证及 `GetSessionAuthIdentity`；普通用户认证也可以识别个人访问 Token | 新业务入口明确要求有效产品登录会话 |
| [middleware/auth_origin.go](/Users/a1/Desktop/DPin-backend-newapi/middleware/auth_origin.go) | 安全 Cookie 模式下刷新、退出的来源检查 | 主进程网络适配遵守来源契约 |
| [router/relay-router.go](/Users/a1/Desktop/DPin-backend-newapi/router/relay-router.go)、[relay/systemone_handler.go](/Users/a1/Desktop/DPin-backend-newapi/relay/systemone_handler.go) | TypeSafe System One 中继及文本用量结算 | 复用 JEV 原生协议链路 |
| [relay/channel/typesafe/adaptor.go](/Users/a1/Desktop/DPin-backend-newapi/relay/channel/typesafe/adaptor.go)、[relaykit/dto/typesafe.go](/Users/a1/Desktop/DPin-backend-newapi/relaykit/dto/typesafe.go) | `state/questions/answers` 协议、原始请求保留、Token 用量校验 | 保留结构化请求，不替换为普通聊天协议 |
| [controller/relay.go](/Users/a1/Desktop/DPin-backend-newapi/controller/relay.go)、[relay/request_billing.go](/Users/a1/Desktop/DPin-backend-newapi/relay/request_billing.go) | 中继请求校验、分发、计费前置处理、重试和失败处理 | 新桥接进入现有调用链，不旁路记账 |
| [controller/playground.go](/Users/a1/Desktop/DPin-backend-newapi/controller/playground.go) | 根据登录用户构造中继上下文的既有入口 | 参考其上下文准备，新增 BelloBuddy 专用入口 |
| [service/authz/registry.go](/Users/a1/Desktop/DPin-backend-newapi/service/authz/registry.go) | Casbin 资源与动作权限体系 | 注册方案管理、职位映射和机器人绑定的管理权限 |
| [model/main.go](/Users/a1/Desktop/DPin-backend-newapi/model/main.go) | 主库与日志库迁移组织 | 新配置模型进入主库 |

当前代码未发现 BelloBuddy 方案、职位关联的完整业务接口，也未发现 MaxKB 专用渠道适配；这些必须新增。DPin 当前没有参考项目中的 `/api/canvas/relay-token` 接口，不能假定已经可用。

### 2.2 BelloBuddy 改造入口

| 代码位置 | 当前行为 | 改造重点 |
| --- | --- | --- |
| [bootstrap.cjs](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/bootstrap.cjs)、[offline-policy.cjs](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/offline-policy.cjs) | 启动时加载离线策略；缺少联网或隔离变量会退出 | 正式安装入口默认正常联网 |
| [index.js](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/index.js)、[loader-OM0zjlIw.js](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/loader-OM0zjlIw.js) | 默认开发认证旁路；本地内核加载依赖离线或旁路分支 | 内核固定随包加载，认证改为 DPin |
| [index-BYEqD--t.js](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/index-BYEqD--t.js) | 旧产品登录、凭据存储、状态检查；`run-boss-beiliao`（约 :2856）与 `create-recruitment-batch`（约 :2912）两个任务启动 IPC 也注册在此文件 | 接入 DPin 登录、恢复、退出与认证代理；两个任务启动入口一并纳入统一启动校验 |
| [index-C9ubNdld.js](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/renderer/assets/index-C9ubNdld.js) | 看板和本地方案列表、搜索、内置对象 | 登录状态控制；云端列表、详情和下拉框 |
| [index-CGQ68GrN.js](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/renderer/assets/index-CGQ68GrN.js) | 批量与单职位选择、方案联动 | 保存 DPin 职位关联，保留既有确认交互 |
| [index-CcSlIXko.js](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/index-CcSlIXko.js) | worker 侧执行文件：`localExecution`／`localPlanPreselected` 凭据分支（约 :677-714）、静态方案分流、JEV gate 接线；批次 IPC 注册不在此文件 | 统一启动校验，按云端配置注入执行器 |
| [local-filter-executor.mjs](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/packages/boss-auto-browse-and-chat/local-filter-executor.mjs)、[local-filter-hook.mjs](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/packages/boss-auto-browse-and-chat/local-filter-hook.mjs) | 按本地登记 ID 查询完整筛选定义，匹配当前职位 | 改为任务内存方案查询，保留判定与职位匹配逻辑 |
| [jev-ai-screen.mjs](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/packages/boss-auto-browse-and-chat/jev-ai-screen.mjs) | 直连上游、读取本机 Key、追加售后条件、错误返回淘汰 | DPin 请求代理、按方案组织筛选要求、故障与判定分离 |
| [local-inbound-screen.mjs](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/packages/boss-auto-browse-and-chat/local-inbound-screen.mjs) | 有 `pass/reject/unknown` 基础；正式入口接入尚未确认 | 接入真实沟通路径并统一故障语义 |
| [local-beiliao-runtime.mjs](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/packages/boss-auto-browse-and-chat/local-beiliao-runtime.mjs) | 本地虚拟接口、附件和联系动作、拒绝意图；可传入 `plans`，默认读取文件 | 注入云端沟通方案，增加必要的 MaxKB 回复决策 |
| [runtime-orchestrator.js](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/packages/pm/runtime-orchestrator.js)、[daemon.js](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/packages/pm/daemon.js) | 批次调度、定时、崩溃恢复；批次及 workerSpec 落盘；spawn stdio 无 'ipc'，但 daemon 已有 unix socket/命名管道 JSONL 控制通道（worker-register、request-graceful-exit、runtime-control 的 pause/resume 等） | 分开持久状态与任务内存；复用并加固既有 socket 通道承载方案传递，补身份鉴权 |
| [preload/index.js](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/preload/index.js) | 通用 IPC 能力 | 为新网页和运行模块提供受控方法 |
| [scripts/build.mjs](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/scripts/build.mjs)、[scripts/package.mjs](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/scripts/package.mjs) | 恢复源码构建、本地内核校验、macOS arm64 打包 | 纳入方案网页和新增模块，验证正式安装入口 |

BelloBuddy 是从编译产物恢复的可编辑 JavaScript 工程。`app/src/renderer/assets` 下的 `index-*.js` 是压缩/半压缩的编译产物，没有对应的前端源码工程；本计划对此采取明确策略，消除“改编译产物”与“可重复构建”的矛盾：

1. 新增界面能力（两个方案网页、云端方案列表、登录状态控制等）一律作为新增独立页面/窗口实现，经 preload 受控方法与主进程通信，不向现有编译产物里塞新逻辑；
2. 现有产物的必要改动（登录跳转控制、下拉框数据源替换、内嵌方案副本移除）做最小定点修改，被修改的产物文件整体纳入版本管理并登记为构建输入，即“产物源码化”——这些文件从此时起被当作可编辑源文件对待；
3. 15.3 的“可重复构建”据此定义为：从版本库状态出发可重复产出 `build/app` 与安装包（含被编辑的产物文件），不声称重建 renderer 源码工程；若后续需要系统性改版 renderer，另行立项恢复源码工程，不在本接入范围内混做。

新增业务模块采用稳定名称，恢复入口只负责接线；实施落在 `app/src`、`packages` 和构建输入，不能只改外部原型、`build/` 或 `.app` 产物。

## 3. 整体架构与职责

### 3.1 模块分工

| 模块 | 所在项目 | 责任 |
| --- | --- | --- |
| 产品认证代理 | BelloBuddy 主进程 | 登录、挑战、Cookie、Token、刷新协调、退出和身份变更 |
| DPin 请求代理 | BelloBuddy 主进程 | 固定服务地址、受控方法、认证注入、错误分类、超时和取消 |
| 两个方案网页 | BelloBuddy 本地页面 | 表单编辑、完整 JSON 双向映射、云端读取/保存、保存结果显示 |
| 职位设置页面 | BelloBuddy | 获取可见方案、下拉框选择、保存职位关联 |
| 本地编排器与 daemon | BelloBuddy | 队列、定时、暂停/恢复、调度状态持久化、真正启动前的校验 |
| 任务内存配置与执行器 | BelloBuddy worker | 持有完整方案、解释规则、招聘决策和 BOSS 操作 |
| 方案及职位配置服务 | DPin | 方案 CRUD、资源归属、职位选择、启动配置、版本控制 |
| 用户态 AI 桥接 | DPin | 产品会话转中继身份，校验资源、权限、模型、渠道和额度 |
| JEV 协议适配 | DPin 既有 TypeSafe 中继 | 转发结构化推理并处理实际用量 |
| MaxKB 协议适配 | DPin 新增渠道能力 | 应用路径、会话字段、回复解析、实际用量和错误转换 |
| 管理端 | DPin | 公用方案管理、同岗位映射、机器人绑定及既有模型/渠道配置 |

### 3.2 三条核心数据链路

**方案编辑：** 已有网页表单 → 完整方案 JSON → BelloBuddy 请求代理 → DPin 校验与保存 → 返回方案 ID、版本和保存结果 → 刷新方案列表。

**方案选择与执行：** 职位下拉框 → 从 DPin 读取可见方案及所选详情 → 保存职位选择 → 任务真正启动 → DPin 返回明确选择模式及必要真实方案的完整内容 → 任务内存 → 本地执行器。

**AI 调用：** 本地判断需要 AI → 认证代理 → DPin 校验用户和职位 → 既有中继及计费链路 → JEV 或职位对应 MaxKB → 本地解释结果 → 本地动作执行器。

### 3.3 存储边界

| 数据 | 持久化位置 | 客户端使用边界 |
| --- | --- | --- |
| 完整筛选/沟通方案 JSON | DPin 主库 | 编辑期间或当前任务内存，不写文件、浏览器存储或本地数据库 |
| 职位与方案的最终关联 | DPin 主库 | 本地可持有 ID 引用和展示信息，不能作为离线权威配置 |
| 同岗位映射和机器人绑定 | DPin 主库 | 客户端得到配置状态或绑定版本，不得到上游凭据 |
| 候选人、简历、BOSS 聊天、执行记录 | BelloBuddy 本地 | 不同步建设 DPin 招聘业务库 |
| 批次、队列、进度、检查点 | BelloBuddy 本地 | 仅调度状态及必要引用，不含完整方案或认证凭据 |
| MaxKB 会话映射 | BelloBuddy 本地 | 按用户/账号/职位/候选人隔离，重启可恢复 |
| 访问 Token | 认证代理内存 | 不交给页面或各 worker 保存 |
| 重启恢复所需刷新凭据 | 系统安全存储 | 只由认证代理管理，不写旧明文凭据文件 |
| 上游 Key | DPin 渠道配置 | 不下发客户端 |

必要简历或消息内容可以随 AI 请求经 DPin 转发；这不意味着新建候选人或聊天数据库。请求体调试、错误打印和消费日志需按现有设置检查留存，避免新增功能隐式保存完整简历或对话。

## 4. DPin 数据模型与保存契约

### 4.1 新增模型

以下为建议的业务模型，具体 Go 类型和文件名称在实现时统一确定。

| 模型 | 必要字段 | 关键约束 |
| --- | --- | --- |
| 方案 | ID、类型、名称、可见范围、owner、启用状态、完整 JSON、Schema 版本、内容版本、来源标识、时间 | 筛选/沟通类型明确；私人 owner 来自会话；公用写入要求管理权限 |
| 用户招聘账号登记 | DPin 用户、客户端稳定 accountKey、平台、显示名称、时间 | 同用户/平台/accountKey 唯一；不保存 BOSS Cookie、密码或验证码 |
| 用户职位配置 | 用户、账号登记、平台职位稳定 ID、最小展示信息、筛选／沟通选择模式、真实方案 ID、招呼限额、配置版本、共享职位关联 | 同用户/账号/平台职位唯一；真实方案引用必须类型正确、可见且有效；特殊选项按第 8.1 节单独保存，不混同缺失配置 |
| 共享职位机器人绑定 | 管理员确认的同岗位关联键、MaxKB 渠道/应用引用、启用状态、绑定版本 | 当前一家公司内共用；不能因不同使用人随机分配机器人 |
| 用户态中继内部凭据关联 | 当前用户、既有 Token 引用及应用标识 | 按现有 Token 体系实现，幂等初始化；不建立共享管理员身份 |

共享职位关联键只是管理员把不同账号中的同一岗位指向同一个机器人的映射，不增加岗位模板、岗位资料库或多租户系统。不能仅凭职位名称自动授予机器人访问权限。

同岗位的识别方式首期定案为管理员手工建立映射：管理界面按平台职位名称与城市提供候选推荐列表辅助定位，由管理员确认后建立关联；不自动绑定、不按展示名称自动授予。新同步的职位默认未绑定，管理端提示补充映射；未绑定职位按“未配置机器人”处理（C22），不阻塞任务。此识别方式已经用户确认。

### 4.2 JSON 完整性

完整方案正文使用跨数据库兼容的 TEXT 保存 JSON。权限元数据与业务 JSON 分开；旧方案中的 `user`、`owned_company`、`visibility` 等字段可以作为兼容内容保留，但不能决定服务器授权。

以当前有效本地沟通方案 JSON 与筛选方案完整对象的字段并集建立唯一字段契约，覆盖类型、嵌套结构、数组、显式零值、布尔值和未知兼容字段。字段契约先于网页适配；页面缺少业务字段时补齐页面，不能据已有控件删减正文。表单、接口、导入器及执行器使用同一契约。方案更新携带内容版本，冲突时明确提示，不静默覆盖。

客户端提交完整 JSON；服务端校验对象结构、大小、Schema、已知业务字段和方案类型。元信息更新不能重置正文。采用能够保留原始字段和数字精度的编解码路径，根 Go 模块使用 `common.*` 包装函数。

方案正文不包含上游密钥。列表返回元信息，详情返回完整内容；列表存在不代表当前用户有权读取详情，所有访问路径均重新执行资源归属检查。

### 4.3 权限与一致性

普通用户可以创建、读取和修改本人私人方案，并读取有效公用方案；不能读取他人私人方案或直接修改公用方案。管理员维护公用方案、同岗位映射和机器人绑定，敏感渠道配置沿用现有渠道权限。

职位绑定、批量保存、启动配置和 AI 桥接共用一致的授权规则，不能只在下拉框隐藏资源。客户端不能指定他人 user ID 或 owner。

同一账号批量更新采用事务，全量验证后提交，任意职位或方案无效则整体失败；跨账号处理明确返回各账号结果。启动读取要返回一致的配置版本，不能把更新前后的半份配置组合成一次任务。

禁用已绑定方案时保留可追溯引用，下次获取配置明确报告无效，由用户重选，不自动改成全部通过。本期优先用内容版本与来源标识，不扩展复杂历史版本库。

## 5. DPin 接口与后端模块

### 5.1 复用接口

复用现有 `/api/user/login`、登录附加验证、`/api/user/auth/refresh`、`/api/user/auth/logout`、`/api/user/self` 等接口，保留实际认证响应及错误代码。注册、找回密码和账号中心使用现有网站。

产品登录 Token 与模型 API Token 职责不同。不能把登录 Token 直接放到 `/v1/systemone` 就假定完成模型调用；由新增用户态桥接构造标准中继上下文。

### 5.2 拟新增业务接口

建议统一前缀 `/api/bellobuddy`。下表为实施建议，当前未实现；缩写路径均位于该前缀下。

| 方法与路径 | 输入或范围 | 返回与责任 |
| --- | --- | --- |
| `GET /capabilities` | 当前会话 | 用户执行、模型和协议兼容信息；不代替每次 AI 调用的实时校验 |
| `GET /plans` | 类型、名称、分页 | 公用及本人私人方案元信息 |
| `GET /plans/:id` | 方案 ID | 完整 JSON、类型、版本、可见范围和启用状态 |
| `POST /plans` | 方案类型、名称、完整 JSON | 创建本人私人方案，返回真实 ID、版本及规范化结果 |
| `PUT /plans/:id` | 完整 JSON、预期版本 | 修改本人方案，版本冲突明确返回 |
| `POST /accounts/:accountKey/jobs/sync` | 当前账号最小职位资料 | 登记账号与职位，不上传候选人或平台凭据 |
| `GET /accounts/:accountKey/jobs/config` | 当前用户/账号 | 职位最终方案选择及配置版本 |
| `PUT /accounts/:accountKey/jobs/:jobId/config` | 两类方案 ID、预期版本 | 保存单职位选择并返回最终配置 |
| `PUT /accounts/:accountKey/jobs/config/batch` | 明确职位集合、方案 ID、版本 | 批量事务保存，不把上方选择当运行时兜底 |
| `POST /accounts/:accountKey/runtime-config` | 当前账号、本次职位集合、运行器版本 | 每个账号/worker 真正启动前各自获取一次，返回职位的明确选择模式、真实引用所需完整方案、招呼限额、配置与内容版本、获取时间、机器人配置状态及绑定版本；特殊选项不伪造 JSON 正文；多账号不要求跨账号一致版本 |
| `POST /ai/screen` | 职位上下文、结构化 JEV 请求 | 用户态 TypeSafe 中继；返回结构化结果或有来源的错误 |
| `POST /ai/reply` | 职位、必要对话输入、受控会话引用 | 服务端路由到绑定机器人，返回文本与会话引用 |
| 管理接口 `/admin/plans` | 独立管理权限 | 公用方案维护 |
| 管理接口 `/admin/job-bindings` | 独立管理权限 | 同岗位映射与机器人绑定 |

私人方案的禁用操作可并入更新接口；复制方案采用读取后新建的语义，不能改变原方案 owner。删除语义如需开放，要同时处理已绑定引用，不在首期随意补入级联删除。

方案详情和启动配置响应明确禁止缓存。页面可以持有当前内存状态，但不使用 localStorage、IndexedDB、Service Worker 或持久 Query 缓存保存方案正文。

### 5.3 模块落点

路由在 `router/` 注册；`controller/` 负责请求解析和会话入口；`service/` 负责方案授权、版本、职位配置及 AI 业务路由；`model/` 负责 GORM 读写与事务；渠道协议仍位于 `relay/channel/`。新增文件建议使用 `bellobuddy` 业务前缀，避免把全部能力堆进旧用户控制器。

后台管理界面采用 DPin 现有 React 技术栈。公用方案内容继续复用用户已有表单设计，管理入口可以通过权限受控方式调用同一数据契约，不另造一套不兼容表单。后台补充列表、状态、同岗位映射和机器人绑定管理。

实际实现 DPin UI 前遵循 [web/AGENTS.md](/Users/a1/Desktop/DPin-backend-newapi/web/AGENTS.md)，读取项目 `shadcn-ui` 技能并检查现有表格、弹窗、确认和状态组件；使用 Bun、i18n、既有数字与金额格式化能力。

## 6. BelloBuddy 认证与请求代理

### 6.1 登录适配

替换旧 `bello-login`、凭据状态检查和退出实现，使用 DPin 账号。密码原样提交，移除旧逻辑的首尾裁剪，不持久化密码。部署启用密码加密时，先请求现有加密公钥接口，按 DPin 前端契约提交。

认证状态区分未登录、附加验证中、已登录和失效。收到挑战响应不能当作登录完成；只有完整认证成功并确认用户状态后，才允许进入任务运行状态。

TOTP、人机验证和 Passkey 按 DPin 部署启用的机制适配。可信网页或平台能力涉及 Origin/RP ID，不能假设本地 `file://` 可以直接执行全部验证。网页登录若要交接到客户端，需明确安全交接；仅在系统浏览器登录不会自动建立桌面会话。未完成适配的登录方式明确提示不支持，不能绕过附加验证。

登录人机验证是阶段 2 的接入前置条件：DPin 登录链路挂有 `TurnstileCheck`。开工前确认生产部署的启用状态；若启用，采用受信任 BrowserWindow 加载 DPin 部署域名下的现有登录页，完成 Turnstile 及后端要求的附加验证。窗口使用认证代理管理的专用内存 session，优先共享同一 Cookie jar；主进程确认有效会话、用户和认证完成状态后接管后续请求。若确需跨 session 交接，只由主进程读取与写入指定服务的认证 Cookie，并验证交接身份；页面脚本和通用 preload 不接触刷新凭据。具体窗口隔离、凭据交接及联网请求流程在阶段 2 的前置验证中确认，不能假定单纯主进程密码请求能完成浏览器验证。

当前中间件只有全局 Turnstile 启用开关，没有 BelloBuddy 专用豁免配置；本次接入不以关闭全局验证或新增客户端识别豁免作为默认路径。User-Agent、客户端名称或普通请求头不能证明可信身份，也不能据“一家公司使用”推断可以降低认证保护。若部署方另行调整全局验证策略，必须记录影响的全部入口、替代服务端保护和验证结果；这是独立部署决策，不属于现成的客户端专用配置能力。

注册按钮通过系统浏览器打开配置的 DPin 注册页。注册后回客户端登录，本期不增加自动回跳或注册后自动登录要求。认证响应参考 [features/auth/api.ts](/Users/a1/Desktop/DPin-backend-newapi/web/src/features/auth/api.ts) 与 [password-encryption.ts](/Users/a1/Desktop/DPin-backend-newapi/web/src/features/auth/lib/password-encryption.ts)。

### 6.2 复用 DPin 刷新契约

现有访问 Token 有效期 15 分钟，登录会话创建时有效期 30 天；普通刷新不延长原会话到期时间。刷新接口使用 `new_api_refresh` Cookie，路径 `/api/user/auth`，并使用 `SameSite=Strict`；生产按安全 Cookie 配置运行。

BelloBuddy 直接适配现有轮换和 30 秒竞争容错。符合匹配条件的上一轮凭据在窗口内可以取得同一轮续期结果；超出窗口的已识别上一轮凭据重复使用、未知凭据、失效会话按现有后端逻辑处理。不删除竞争窗口，不另行修改 DPin 刷新策略，也不按任何重复请求必须失败的标准验收。

认证代理合并同时发生的续期请求，其他请求等待结果；正确处理新的 Set-Cookie、访问 Token 和会话信息。参考 [web/src/lib/auth-session.ts](/Users/a1/Desktop/DPin-backend-newapi/web/src/lib/auth-session.ts) 中成功、竞争、会话不一致、认证无效和暂时性错误的区别，不把网络失败直接视为退出。

退出、切换用户或切换服务地址后，迟到的旧刷新响应不能覆盖新状态。副作用请求尤其 AI 调用不能因续期或超时无条件重发，需确认服务端未处理或具有明确幂等保证。

限流是既有约束：refresh/logout 与登录共享 IP 级 CriticalRateLimit 配额，且刷新每 15 分钟自动发生。同一公司多个用户共用出口 IP 时会互相挤占配额并可能触发限流。认证代理按用户做客户端节流与退避；部署时核对 DPin 实际配额值，评估“用户数 × 刷新频率”同 IP 并发规模，必要时在部署配置中调整配额（列入第 16 节，不修改服务端限流代码）。

### 6.3 凭据与网络栈

认证代理成为唯一凭据所有者和刷新协调点。访问 Token 在内存中，刷新凭据使用系统安全存储保护，按 DPin 服务与用户分区；不沿用明文 `bello-credentials.json`。

主进程认证网络栈定案：使用 Electron `net` 模块 + 专用 `session.fromPartition('dpin-auth')`（Chromium 网络栈），不用 Node/undici fetch 手工管理认证 Cookie。该组合自带真实 Cookie jar：服务端 `Set-Cookie`（含轮换后的新 `new_api_refresh`）自动写入分区 session 并在后续请求携带（`useSessionCookies`），Cookie 的域、路径、有效期由 Chromium 按 DPin 服务地址统一管理，刷新凭据在安全存储中的备份由认证代理另行维护。

Origin 契约的处理：refresh/logout 请求的 Origin 头设为 DPin 部署地址自身（`scheme://host`），命中 `SessionCookieOriginGuard` 中“Origin 等于请求 scheme://host”的既有放行分支。这不需要修改 DPin 服务端任何代码，符合“只适配现有契约”的约束；浏览器侧 CSRF 防护也不因此削弱（网页无法伪造 Origin 头，能伪造的只有持有 Cookie 的桌面客户端本身）。

阶段 2 先执行认证网络验证（spike），验证通过后才进入依赖该链路的正式接线：确认 `SameSite=Strict` 的 `new_api_refresh` Cookie 是否随 Electron `net` 请求实际发出；确认 `SESSION_COOKIE_SECURE=true` + 生产 HTTPS 下 Secure Cookie 的收发；确认轮换后 Cookie jar 与安全存储更新、当前凭据正常生效、匹配的上一轮凭据在 30 秒竞争窗口内按既有条件取得同一轮结果、超出窗口的已识别上一轮凭据重复使用触发既有撤销、未知凭据被拒绝。客户端合并刷新请求，验证竞争窗口的场景在受控测试中构造，不要求旧凭据轮换后立即被拒绝。开发 HTTP 与生产 HTTPS 分别验证，生产不关闭证书校验。

认证网络验证失败时，按实际失败环节处理，不能把可信来源白名单当作通用备用路径：

| 失败环节 | 核对与处理 |
| --- | --- |
| Cookie 已发送，但 DPin 返回 `AUTH_ORIGIN_FORBIDDEN` | 核对真实请求 Origin、部署域名与反向代理配置；需要额外可信 HTTPS 来源时，通过现有 `SESSION_COOKIE_TRUSTED_URL` 配置精确来源，不增加任意来源放行 |
| Cookie 未发送或未进入预期 session | 核对请求绑定的 session、Cookie 域／路径／有效期、`useSessionCookies` 及 credentials 配置、Secure 与 SameSite 判定；若主进程上下文无法正确发送，使用绑定认证 session 的受信任 BrowserWindow，在 DPin 部署域名下承载认证请求，不靠白名单改变浏览器发送行为 |
| 登录挑战未完成、凭据过期／撤销或刷新竞争结果异常 | 按 DPin 认证及刷新契约处理，不通过调整 Origin 配置绕过，不更改 30 秒竞争窗口 |

可信来源配置只影响服务端 Origin 校验，不改变 Chromium 是否发送 Cookie。受信任网页登录需要用户交互时使用可见窗口；刷新承载页面可在后台运行，但仍由认证代理统一管理。任何备用路径均需重新验证 Cookie、Origin、轮换和退出，不以配置已填写代替通过验证。

重启时由刷新流程恢复，再读取当前用户；本地用户摘要不能证明已登录。退出时立即禁止新任务并暂停运行任务，调用现有 logout，清理内存和安全存储。断网退出不能确认远端撤销时，如实显示未确认状态，本地立即停止使用凭据。

### 6.4 页面、daemon 与 worker 通信

方案页面通过专用 preload 调用命名明确的方案方法；主进程验证发送窗口、参数和权限。页面不获得任意 URL 转发接口、认证 Token 或上游 Key。

daemon 与 worker 之间已有可复用的通信基础：worker spawn 的 stdio 不含 'ipc'（无 child-process IPC），但 daemon 侧 `net.createServer` 监听随机命名的本地 socket（macOS/Linux 为 `tmpdir()/bellobuddy-d_<uuid>.sock`，Windows 为命名管道），以 JSONL 行协议承载 worker-register、request-graceful-exit、runtime-control（pause/human-control/resume）等控制消息。受控通道优先复用并扩展该 socket，不必新建传输；但当前通道没有身份鉴权，仅靠随机 socket 名保护，承载方案正文前必须增加按运行实例发放的握手凭据，校验 worker 对应的运行 ID、账号和当前身份，限定可调用方法，支持超时、取消与断开处理。完整方案通过该通道进入内存，不通过命令行、环境变量或 workerSpec 传递。

所有后台任务也依赖这一代理。若窗口关闭后需要继续调度，让认证宿主随后台运行持续存在；若认证代理已退出，则暂停或等待恢复，禁止 daemon 自行用旧凭据启动。后台产品行为在发布前确定，但不得将刷新凭据分发给各 worker。

## 7. 两个已有方案网页接入

### 7.1 网页的用途和来源

| 网页 | 现有源文件 | 当前缺口 |
| --- | --- | --- |
| 沟通方案网页 | [communication-plan-editor-clone/src/App.jsx](/Users/a1/Desktop/bosscli/communication-plan-editor-clone/src/App.jsx) | 当前从 `bello-plan-demo` 初始化并写 localStorage，尚未形成真实云端保存链路 |
| 筛选方案网页 | [SearchSettingLocalPreview.vue](/Users/a1/Desktop/bosscli/recovery/vue-frontend-dpin/src/views/SearchSettingLocalPreview.vue) | `saveDraft()` 只更新列表元信息；`openEdit()` 清空规则；完整 JSON 映射未完成 |

这两个网页是用户创建和编辑方案的配置表单。它们的责任是读取已有 JSON、回填表单、把修改后的字段组成完整 JSON、保存到 DPin；不做候选人筛选、不执行沟通动作，也不直接调用 JEV/MaxKB。

### 7.2 以两类真实 JSON 为基准逐项适配网页

#### 7.2.1 字段来源与核对顺序

字段基准是当前本地留存的完整沟通方案与完整筛选方案对象。“两类 JSON”不限定文件数量；必须核对实际登记的全部有效实例，取字段并集，保留不同实例间的缺失字段差异。

| 对象 | 已定位来源 | 核对用途 |
| --- | --- | --- |
| 沟通方案正文 | [local-beiliao-plans.json](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/packages/boss-auto-browse-and-chat/local-beiliao-plans.json)、[local-beiliao-after-sales-2.json](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/packages/boss-auto-browse-and-chat/local-beiliao-after-sales-2.json) | 完整业务字段、话术对象、问题配置、追聊模板及兼容元数据 |
| 筛选方案正文 | [local-filter-executor.mjs](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/packages/boss-auto-browse-and-chat/local-filter-executor.mjs) 的 `LOCAL_FILTER_DEFINITIONS` | 完整 `FilterDef`、规则嵌套结构、AI 指引与判定条件 |
| 有效登记与一致性 | renderer 的 `LOCAL_BEILIAO_DEFINITIONS`／`LOCAL_FILTER_DEFINITIONS`、worker 的 `localBeiliaoPlans`、沟通 runtime 的 `localPlans` | 确认当前可选择且执行端使用的对象，避免把未登记文件当有效方案 |

先列出完整字段路径、真实类型及结构，再对照网页已有控件和执行端消费位置，最后确定双向转换。页面变量名与 JSON 名不同可以映射；枚举、单位、条件组合及关联 ID 必须结合真实消费者确认，不凭中文标签推断。

对应表逐项记录：JSON 路径、类型与缺失规则、业务含义、页面分区／控件、已有或待补、读取与写回规则、执行支持状态、验证场景。业务配置项有 JSON 无控件时必须补网页；系统字段明确只读或服务端管理；未知兼容字段无损保留。网页已有但尚无 JSON 对应的选项列为待核对项，不随意制造新字段。

以下为本轮从现有对象和两个网页核对出的配置分组，作为实施清单的起点。每个分组在实施时展开为逐个完整路径，枚举允许值、参数单位和未填默认值仍须读取实际消费者确认；表中的候选映射不代表已实现。

#### 7.2.2 沟通 JSON 与网页对应项

| 真实 JSON 字段／路径 | 网页已有项 | 适配或补齐要求 |
| --- | --- | --- |
| `scheme_name`、`position_background_info`、`ai_communication_style` | `name`、`background`、`style` | 双向映射名称、背景和风格；背景不自动覆盖 MaxKB 岗位资料 |
| `candidate_unresponsive_timeout`、`skip_delivered_toggle`、`skip_delivered_timeout`、`max_session_rounds` | `days`、`skipDelivered` | 核对时长单位；补投递超时值及最大会话轮数，保留关闭状态下的值 |
| `follow_up_message_enabled`、`follow_up_duration_days`、`daily_follow_up_count`、`follow_up_templates` | `greeting`、`greetingDuration`、`greetingDailyLimit`，追聊表格当前为空 | 数值与“天／次”显示转换；实现模板新增、修改、删除和回显，保留按天及时间条件组织的嵌套对象 |
| `conditions_as_match`、`direct_inquiry_as_match`、`filter_def.id`、`screening_order`、`non_match_action` | `resumeFilter`、`directInquiry`、`filterPlan`、`communicateBeforeResume`、`filterEnd` | 核对开关与筛选顺序的真实含义；关联保存有效方案 ID，名称用于显示；缺失 `filter_def` 与空对象不能自动混同 |
| `standard_response_enabled`、`standard_response_settings.allow_ai_variation`／`text`／`ai_variation_text`／`image_url`／`send_address` | `firstReply`、`firstReplyText`、`firstReplyImageName`、`firstReplyAddress` | 补 AI 改写开关及指引；图片文件名不能代替真实资源引用，接入图片选择与回显；完整话术对象写回 |
| `auto_greeting_enabled`、`auto_initiate_after_response` | 当前无明确独立映射 | 核对执行端后补对应开关或明确组合控件，不能误当作追聊开关合并 |
| `request_attachment_resume`、`request_resume_phrase`、`auto_receive_delivery` | `resumeRequest`、`resumePhrase`；接收附件显示为“固定执行” | 适配索要设置；核对接收策略枚举，补策略配置及回显，不能用页面固定文案覆盖原值 |
| `request_phone_exchange`、`request_wechat_exchange`、`exchange_contact_after_screening_pass` | `phone`、`wechat`、`afterScreening` | 保留请求动作与筛选后交换的条件语义 |
| `wechat_exchange_reply_phrase.allow_ai_variation`／`text`／`ai_variation_text`／`image_url`／`send_address` | `wechatReply`、图片按钮 | 补完整话术子字段及真实图片处理；关闭功能时仍保留原对象 |
| `list_addition_restrictions.must_have_phone_number`／`must_have_attachment_resume`／`must_have_wechat` | `requiredPhone`、`requiredResume`、`requiredWechat` | 逐项布尔映射，明确是结果要求 |
| `additional_candidate_info_needed.switch`／`text`／`questions`／`question_items`／`question_attribute_mapping` | `questions`、`questionSource`、`questionList` | 补问题属性映射和结构化项；文本列表不能覆盖所有子结构；两个样本中部分集合为空，项结构须核对真实定义或消费者 |
| `rejection_keywords`、`skip_inquiry_on_abandoned_intent` | `keywords`、`skipNegativeInquiry` | 数组与开关分别保存，保留关键词内容及顺序 |
| `info_screening_fail_action.mark_unsuitable`／`stop_talk` | “结束对话”中未见这组完整独立配置 | 补沟通筛选失败的动作控制；与在线简历的 `non_match_action` 分别对应 |
| `rejection_response_strategy`、`rejection_phrase.allow_ai_variation`／`text`／`ai_variation_text`／`send_address` | `reject` | 补完整拒绝话术与子字段，枚举转换依据真实执行契约 |
| `mark_unprocessable_sessions`、`new_message_response_strategy`、`end_session_guidance_phrase.allow_ai_variation`／`text`／`ai_variation_text`／`send_address` | `unprocessable`、`newMessage` | 适配已有策略，补会话结束指引话术及其子字段 |

`greetingPhrase` 等页面状态、效果预览及按钮占位也要逐项说明是业务映射、派生显示还是临时 UI 状态。没有确认 JSON 对应的业务选项不能显示已配置成功。

#### 7.2.3 筛选 JSON 与网页对应项

| 真实 JSON 字段／路径 | 网页已有项 | 适配或补齐要求 |
| --- | --- | --- |
| `name`、`enable_quick_screening`、`enable_ai_screening` | `draftName`、`quickEnabled`、`aiEnabled` | 双向映射；编辑从完整对象初始化，不从列表摘要重建正文 |
| `any_fails[]`、`any_passes[]`、各规则的 `conditions[]`、`desc` | `rejectRules`、`qualifyRules`，条件目前为字符串标签 | 改为真实结构化规则，保留组合、顺序及淘汰优先；展示描述与条件对象分开，不能用标签取代条件值 |
| `conditions[].ages[]` 中的 `min`、`max`、`exclusive_min`、`exclusive_max` | 条件选择器有“年龄范围”标签 | 补年龄数值及开闭边界，精确回显原条件；单侧边界缺失不能自动补成零 |
| `conditions[].edu_nature`、`edu_nature_must_all_search_by_relation` | 条件选择器有“教育性质”标签 | 补真实选项值与关系匹配标志，不仅保存标签 |
| `ai_screening_mode`、`ai_screening_guidance` | AI 区当前只有开关、说明及开启按钮 | 补判定模式和每个方案独立的 AI 指引编辑，完整保留原指引 |
| `ai_screening_criteria.levels`、`five_point_scale.threshold`／`condition`、`hundred_point_scale.threshold`／`condition` | 无对应编辑项 | 补等级选择、分数阈值与比较条件；切换模式保留其他模式原配置，执行支持另行核对 |
| `ai_screening_summary_length`、`recruitment_type` | 无对应编辑项 | 补摘要长度与招聘类型配置，允许值依真实契约确定 |
| `source` | 无对应项 | 核对来源语义，明确只读或配置方式，保留已有兼容内容 |

条件选择器还有技能、工作经历、期望薪资等标签，不能凭标签臆造 JSON 字段；继续从真实定义或执行消费者寻找对应项。发现有效 JSON 中的新条件，按相同流程补充参数控件和双向映射。

#### 7.2.4 元数据与完整对象往返

两类对象共有的 `disabled`、`builtin`、`scope`、`scope_value`、`created_at`、`updated_at`、`id`、`schema_defs`、`user.id`、`owned_company.id`、`updated_by.id`、`version`、`snapshot.id`、`visibility` 也要进入对应表。启用／禁用与可见性操作按 DPin 权限契约接入；其他字段明确为服务端元数据、只读显示或旧内容兼容保留。旧 owner 和可见范围不得决定授权，新服务端 ID、版本与旧正文引用需显式映射。

新建使用契约中的明确默认值。编辑从云端完整 JSON 初始化，在完整对象上更新实际修改路径；不开关清空配置，不从有限表单字段重新拼出删减对象。无修改保存、只改名称及复制方案均保持其他业务内容，包括未知字段、显式零值、布尔值、数组顺序、空值与缺失的区别。

字段保留、网页可编辑和执行器支持分别验收。已知业务字段不能长期以“隐藏保留”替代补齐网页；执行器尚不支持的行为明确提示并列出缺口，不能显示已生效。验收以 PRD 的 A34—A37 为准。

当前本地 JSON 是契约与迁移输入；完成接入后仍从 DPin 读取和保存，遵守完整正文仅在编辑及任务内存中临时存在的要求。

### 7.3 保存与下拉框衔接

打开列表或编辑时读取 DPin。页面仅保留当前未提交草稿；取消或关闭不建立持久草稿。保存先生成并校验完整 JSON，再调用 DPin，只有云端成功返回才显示成功并接受新版本。

保存失败保留当前内存草稿供用户重试，不写本地数据冒充保存成功。保存成功后刷新同类型可见列表，新方案进入对应下拉框；其他页面重新打开下拉框时也读取最新云端数据。

新建私人方案不自动替换职位当前选择。日常用户在下拉框选择已保存方案即可，需要调整内容时才进入网页。方案页面与职位选择页面共用 ID、名称、类型和版本契约。

### 7.4 构建和本地入口

保留两套现有设计，建议分别构建本地静态页面，由 BelloBuddy 设置入口打开受信任本地窗口。React/Vue 页面源文件纳入可重复构建输入，资源使用适合安装包的相对路径；不能依赖开发机目录或手工复制 dist。

新增页面通过受控 preload 访问主进程。原型远程发布流程不是桌面程序依赖；移除 localStorage 方案保存以及其他正文持久化路径，关闭网页后的再次编辑重新从 DPin 读取。

## 8. 职位配置与任务启动

### 8.1 职位同步与方案选择

BOSS 职位继续由本地浏览器同步。向 DPin 提交当前用户下账号的稳定职位 ID及最小显示信息，不提交平台凭据。相同显示名称不能成为跨账号授权依据。

每个职位条目是独立的方案选择入口：职位条目各自选择沟通方案与打招呼筛选方案，但职位条目只是选择器，方案本身是账号级资产，不属于任何职位。下拉框按筛选/沟通类型读取有效公用方案及本人私人方案，且该候选范围对所有职位条目一致——公用方案在所有职位条目中均可见可选，本人创建的私人方案在自己的所有职位条目中可见可选，其他用户的私人方案不出现；职位不改变方案的可见范围，方案也不因被某个职位选中而变为该职位专属。职位条目最终保存的只是所选方案 ID 引用，完整正文始终在 DPin，任务启动时按引用解析。

选择真实方案时获取完整内容用于当前展示或校验，不能从旧内置对象补齐。特殊选项继续作为本地下拉框入口提供，选择结果由 DPin 保存并在启动时返回；它们不是方案资产，不创建伪造的完整 JSON，也不能统一压缩成“未启用”或空 ID。

| 选择状态 | 现有标识／行为依据 | 保存与运行语义 |
| --- | --- | --- |
| 使用真实筛选方案 | 有效 `searchSettingId` | 保存明确的真实方案模式与服务端 ID，获取完整筛选 JSON，按配置执行快速筛选及 AI；AI 开关仍由该方案决定 |
| 全部合格 | `pass_all`；筛选执行器返回通过 | 保存独立模式，跳过打招呼前的方案筛选，候选人可进入后续招呼条件判断；仍遵守职位状态、招呼限额、认证及其他执行条件，不绕过沟通阶段另行配置的筛选 |
| 跳过打招呼 | `skip_all`；职位设置将 `greetingLimit` 设为 0，筛选执行器返回不通过 | 保存独立模式并保留限额联动，不执行该职位主动打招呼，不将跳过统计成候选人业务不匹配；已选择真实沟通方案时，符合条件的沟通流程仍按其配置处理 |
| 使用真实沟通方案 | 有效 `chatPlanId`，兼容 `beiliaoPlanId` | 保存明确的真实方案模式与服务端 ID，获取完整沟通 JSON，执行本地规则及符合条件的 MaxKB 调用 |
| 跳过自动沟通 | `jump`；现有 worker 不把它作为沟通方案获取 | 保存独立模式，不执行该职位的自动沟通及其机器人回复；不据此改变另行选择的主动打招呼筛选模式 |
| 未配置或未知值 | 未声明选择模式、真实模式下缺少 ID、未知旧标识 | 返回明确配置错误，不自动转换为 `pass_all`、`skip_all` 或 `jump`；要求该项配置的执行路径不能启动 |

模式的实际接口字段名在阶段 1 契约中确定；类型与模式组合须由服务端校验。特殊模式不携带真实方案 ID，真实模式必须有有效 ID；旧标识到新模式的迁移映射明确且可逆核对。关闭（下线）职位的打招呼筛选仍固定为 `skip_all`，不同状态的保存、重开、启动与执行分别验收。

上方选择未确认只是界面状态，点击确认后事务更新当前账号明确的全部在线职位集合；下方只修改指定职位。两种方式更新同一份 DPin 职位配置，保存失败恢复原显示并提示。

现有沟通方案选择会联动筛选方案和招呼限额，需读取 `index-CGQ68GrN.js` 的对应映射，将必须保留的联动写进契约并一起提交。`searchSettingId`、`chatPlanId` 和兼容 `beiliaoPlanId` 集中归一化，避免 worker 猜关联。

### 8.2 启动配置获取

批次创建可以保存调度意图，但完整方案在每个账号/worker 真正准备启动时获取。排队和定时任务不能使用创建批次时留存的正文或界面内存替代。

启动前完成 DPin 会话、服务连通、执行权限、账号职位归属和两类选择模式校验。仅对真实引用及本次启用流程所需的关联方案校验完整 JSON；明确的特殊选项不是“缺少方案”。机器人未配置不构成启动失败，单独返回配置状态。

`runtime-config` 按账号调用、一次返回该账号本次职位的明确选择模式、招呼限额、真实引用所需完整方案及版本信息；多账号任务中每个账号/worker 各自获取、各自持有本账号的配置版本，不要求跨账号一致版本。两类均选真实方案时，两类完整 JSON 都必须返回；特殊模式不要求对应正文，也不生成空壳方案。沟通方案中启用流程所需的 `filter_def.id` 等关联仍要解析并校验，不能因打招呼模式为特殊选项而漏取。必要真实引用缺失、不可见、禁用或 JSON 不兼容时不启动该账号，不使用历史缓存或默认方案替代。以服务端有效 ID 索引方案；旧正文 ID 保留为兼容字段时明确映射，避免执行器按错 ID 查找。

当前任务固定使用启动时内存配置，运行中编辑不热替换；下一次启动重新读取。所有配置完整注入后才允许执行器进入 BOSS 动作阶段。

### 8.3 所有入口统一校验

覆盖手动运行、组合任务、批次、定时启动、daemon 自动启动、手动恢复、断连恢复和崩溃重启。界面禁用按钮、主进程校验和 worker 边界一致，服务端仍对每次业务/AI 请求执行授权。

移除 `localExecution`、`localPlanPreselected`、开发旁路和占位凭据的认证放行作用。`local-only` 仅可保留为本地动作适配标记，不能表示 DPin 认证成功。旧 Bello 账号不再是前提或备用路径。

重启后的完整方案内存已消失，重新认证、重新向 DPin 获取配置，不能从检查点恢复正文。同进程网络暂停时内存可以保留，恢复是否刷新快照按第 16 节确定。

### 8.4 编排状态与内存分离

当前 `runtime-orchestrator.js` 的 `persist()` 保存整个 batches 对象，workerSpec 的 args/env 也保留在其中。因此不能把完整方案直接挂到批次、职位条目或 workerSpec 再依赖普通 JSON 保存。

建立明确的持久化字段白名单，只保存任务/用户身份引用、账号、职位、选择模式和方案 ID、招呼限额、必要版本、顺序、状态、进度与检查点。任务内存容器独立持有完整 JSON；保存调度状态时不遍历它。

完整方案及凭据不得出现在运行文件、参数、环境变量、日志、错误详情、诊断附件或浏览器持久存储。worker 启动通过本地通道拿到正文；结束时释放内存。后台恢复保留执行进度，不保留方案正文。

白名单落地必须同时处理既存泄露面：当前 `runtime-orchestration.json` 已在历史版本中全量明文保存 workerSpec 的 args/env（含招聘账号 Key）。升级时执行一次性迁移——读取旧结构、剥离白名单外字段、以新 schema 重写，并对历史文件中的敏感字段做不可恢复清洗；迁移失败不阻塞启动，但必须向用户报告未清洗文件路径。

## 9. 本地执行器改造

### 9.1 筛选执行器

`local-filter-executor` 和 `local-filter-hook` 从当前任务容器查询完整定义，移除运行期模块级静态方案依赖。保留当前快速筛选的年龄、教育性质、规则组合和淘汰优先语义。

多职位使用当前页面/推荐 iframe 的稳定职位信息匹配任务配置。无法识别职位或缺失方案属于配置故障，不把候选人批量判为不匹配。云端保存和读取完整方案，也不能自动保证本地支持所有规则，需做执行能力校验。

### 9.2 沟通执行器

沿用 `installLocalBeiliaoRuntime` 的 `plans` 注入入口，移除默认本地 JSON 读取。任务初始化时注入相关沟通方案和职位关联，本地虚拟接口继续适配现有内核动作。

保留放弃/顾虑判断、附件请求与接收、附件本地保存、联系动作和 action/done 回执。动作只有实际成功才记录完成；生成动作或 AI 文本不代表 BOSS 已发送。

在真实沟通入口接入筛选结果和需要回复的决策分支，增加 MaxKB 返回文本到已有 reply 动作的转换。不能在函数尾部给每条消息统一追加机器人调用。

### 9.3 方案字段执行能力

| 能力 | 当前代码基础 | 实施处理 |
| --- | --- | --- |
| 快速筛选 | 年龄、教育性质及淘汰/合格组合已有实现 | 保留语义，补齐首期使用的其他条件 |
| JEV 等级模式 | `level` 和方案淘汰等级已实现 | 改请求路径、按方案组织筛选要求和结果分类 |
| 放弃/条件顾虑 | 关键词、问句/否定、放弃及重新表达兴趣已有实现 | 维持本地判断，停止/暂停分支不调用机器人 |
| 附件简历 | 已有请求、接收、下载和完成状态 | 复用本地动作，不上传 DPin 建业务库 |
| 电话/微信 | 已有开关到动作映射 | 核对筛选后交换、被动交换等附加条件 |
| 首次回应、追聊、问题采集、轮数及结束控制 | 原型和 JSON 有字段，当前 runtime 不完整支持 | 对首期实际启用项补齐；未支持项明确阻止或提示，不静默忽略 |
| MaxKB 回复 | 当前本地 runtime 未接入 | 新增条件判断、请求、回执和去重 |

实施前交付字段能力清单，将每个网页选项对应到 JSON 与实际执行点。完整保存、完整回显和实际可执行分别验收，不能因为网页有控件就宣称动作已支持。

## 10. DPin 用户态 AI 桥接与计费

### 10.1 从登录身份进入现有中继

参考已阅读的 [MyCanvas auth.ts](/Users/a1/Desktop/不常用项目/my-canvas/web/src/services/api/auth.ts)、其同源请求设计，以及 [mange-backend canvas_relay.go](/Users/a1/Desktop/不常用项目/mange-backend/controller/canvas_relay.go) 的用户态中继初始化。保留设计思路，按 DPin 当前 access_token/刷新 Cookie 契约接入，不复制旧 Cookie 或 `New-Api-User` 登录实现。

新增入口先验证有效产品会话和职位资源，再建立真实用户、用户组、受控内部 Token、模型与渠道上下文。内部 Token 按用户幂等初始化，不返回客户端，不用共享管理员 Key；管理员禁用后不能偷偷重建绕过控制。

利用既有中继完成模型、分组、渠道、额度、预扣、用量及结算。中继格式和请求路径在渠道筛选、RelayMode 推导前确定；JEV 明确走 TypeSafe 格式，MaxKB 明确走专用文本适配，不能让业务路径造成错误分发。

对参考代码的能力边界已核对：[controller/playground.go](/Users/a1/Desktop/DPin-backend-newapi/controller/playground.go) 构造的是内存临时 Token（不落库）、明确拒绝 PAT 登录，且其中继仅支持 OpenAI 格式。它只能作为“产品会话 → 中继上下文”的思路参考；JEV 的 TypeSafe 路径、MaxKB 的专用适配及对应的上下文准备均需新写，工作量按全新实现估计，不因存在参考案例而低估。

### 10.2 计费约束

实施必须遵循 [.agents/rules/billing.md](/Users/a1/Desktop/DPin-backend-newapi/.agents/rules/billing.md)，保持请求校验、额度预扣、结算/退款和消费日志链路。不新增价格、不发明扣费公式、不修改计费表达式系统。

JEV 使用既有 `usage.input_tokens/output_tokens`；MaxKB 使用真实响应中的 `usage.prompt_tokens/completion_tokens`，检查非负、上界和总量一致性。MaxKB 原版响应是否携带 usage 从未联调验证；用户已确认将在 MaxKB 侧二次开发，使响应携带所需用量字段，DPin 直接读取真实用量，不做估算或按次替代。阶段 6 的开工前置相应为：用二开后的授权测试应用抽样真实推理响应，确认字段存在、格式与 billing.md 不变量相符；二开完成并通过抽样前，MaxKB 计费链路不上线。个别请求仍缺失或异常用量时按现有失败路径处理，不得默认按零。

所有成为计费乘数的请求字段沿用已有上界，额度转换使用现有安全函数，不能产生负扣费或溢出返还。成功、拒绝、上游失败、超时、取消及重复请求分别验证，避免双重预扣、二次结算或重复回复。

可选中继标量保留缺省与显式零值区别；根模块编解码用 `common.*`，独立 relaykit 使用 `kitutil.*`。如需要更改 relaykit 公共 DTO，保持模块独立构建。

额度不足时遵循 DPin 现有控制，停止全部 AI 调用，不换 Token、账号或直连上游绕过。整个招聘任务是否仅保留本地动作不在本计划中擅自增加结论。

## 11. JEV 简历筛选

### 11.1 请求与提示词

保留现有简历组织及 System One 的 `state`、`questions.candidate_level` 和 `answers` 结构，改为经认证代理调用 DPin `ai/screen`。DPin 使用受控 JEV 模型和既有 TypeSafe 中继；不下发上游 Key，客户端不再读取本机 Key 文件或环境变量。

请求关系必须是：当前职位 → 关联筛选方案 → JEV 等级 → 该方案的淘汰等级规则。筛选方案 JSON 的全部字段按第 4.2 节统一契约完整保留和使用，不在此单独强调个别字段。

售后主管的经验要求保留在其筛选方案中；公共 `levelQuestion()` 不再对所有职位追加“没有售后经验必须 low”。本次不替用户撰写其他职位提示词，也不新增职位提示词管理系统；用户在各自方案中维护即可。

### 11.2 判定与故障分开

| 返回类别 | 筛选结论 | 执行处理 |
| --- | --- | --- |
| 成功且有效等级，未命中方案淘汰等级 | 通过 | 继续本地后续流程 |
| 成功且有效等级，命中方案淘汰等级 | 不匹配 | 按方案执行不通过处理 |
| 方案未启用 AI | 无需 AI 判定 | 继续既有本地规则 |
| 单次超时、网络失败、空 answers、未知等级或协议异常 | 本次筛选未完成 | 跳过当前依赖 AI 的处理，继续后续候选人 |
| 权限/认证/额度拒绝 | 没有有效判定 | 进入对应运行控制，不能继续重复调用 |

只有调用成功、响应有效且等级属于 `high/medium/low` 才能产生 AI 筛选结论。不能把故障附带的字段当有效等级，不固定假定 high 总是通过，而是依据方案配置。

服务故障不写淘汰记录、不计入不匹配统计、不触发不感兴趣或拒绝话术，也不因未知而发送招呼。日志写明“本次 AI 筛选未完成”和故障类别、请求标识，候选人不因本次服务故障被永久排除。是否重试和何时再次处理按明确控制执行，不额外承诺立即重试或批量补处理。

### 11.3 修改到真实执行边界

当前“故障＝淘汰”是两层折叠的结果：第一层在 `jev-ai-screen.mjs` 模块内——模式不支持、未配置 Key、HTTP 失败、超时、异常、无法识别等级、方案未找到或职位方案无法识别，一律返回 `pass:false` 淘汰（reason 带“为避免误打招呼已淘汰”），服务故障在模块出口处就已被折成业务淘汰语义；第二层在内核 chat-handler——resumeGate 抛出的异常与 gate 超时也被折成 `pass:false`，随后所有未通过统一包装为 `reason: "FILTERED_OUT_BY_RESUME: ..."`，推荐循环按该前缀报告 `filter-fail` 并计入 HUD。因此改造必须同时动两层：JEV 模块先区分“有效判定（等级）”与“本次未完成（故障）”，内核结果分类、推荐循环、HUD、统计和检查点再按两类分别处理，不能只替换日志文案，也不能只在内核侧分类而让模块继续把故障折成淘汰。

沟通阶段的 `local-inbound-screen` 有三态基础，但本次未确认正式入口调用它。实施要接入真实路径，将故障来源的 unknown 转为当前处理跳过，避免被当成放行或拒绝，并区分简历资料不足与服务故障。

内核结果适配优先放在稳定外部模块；必须修改内置内核时，同步维护源文件、构建完整性校验与包内核加载校验，不能通过关闭校验或只改产物完成。

## 12. MaxKB 招聘回复

### 12.1 协议与职位路由

已有只读源码核对确认应用路径为 `/chat/api/<application_id>/chat/completions`，支持 `chat_id`，主要读取 `messages` 最后一项；非流式回复在 `choices[0].message.content`，会话 ID在 `choices[0].chat_id`。顶层 `id` 是聊天记录 ID，不能代替会话标识。实际应用推理、内容和用量仍需联调验证。

新增 MaxKB 专用 channel adaptor，首期采用非流式。处理应用 URL、凭据、会话字段、回复扩展与实际用量，复用 DPin 文本中继结算。响应中模型为空时保留 DPin 已选择的逻辑模型，确保消费记录可追溯。

管理员在 DPin 将招聘职位映射到共享岗位机器人绑定。服务端根据当前用户有权使用的职位确定渠道/应用；请求重试不能换到其他岗位机器人，普通客户端不能覆盖 channel ID、上游 URL 或 Key。固定渠道仍要校验用户组、模型和渠道可用性。

开发 MaxKB 的 `localhost:8080` 只表示开发服务所在机器；部署为 DPin 服务端可达地址，不要求用户电脑安装 MaxKB。薪资、地点、时间等岗位资料在机器人内配置，沟通方案背景字段保留但不自动覆盖机器人资料。

### 12.2 本地先决策，再生成回复

本地先执行放弃、条件顾虑、方案限制、必要筛选、固定话术、附件与联系动作判断。只有流程明确需要生成回复时调用 MaxKB；停止和暂停分支不调用。

MaxKB 返回建议文本，本地转换为 reply 动作，发送前再检查任务状态并由既有执行器发送。机器人不输出浏览器指令。按消息 ID、职位、候选人和动作类型去重，发送回执决定是否完成。

职位未配置机器人时，返回独立的 `BOT_NOT_CONFIGURED`：任务仍能启动、筛选和执行本地规则，生成回复步骤跳过并提示；不暂停整个任务，不退回其他机器人或旧 Bello 服务。

### 12.3 会话延续与上下文

同候选人同职位继续使用原 MaxKB 会话；不同候选人、同候选人不同职位分别隔离。映射键至少包含 DPin 服务与用户、招聘账号、稳定职位 ID、候选人 ID和机器人绑定版本，本地保存，重启后恢复。

建议 DPin 返回绑定用户/账号/职位/候选人/机器人版本的签名会话引用，客户端持久化该引用，后续服务端验证后使用上游 chat_id；不能接受任意裸 chat_id 访问他人会话。该方法无需建设 DPin 聊天业务库。

首轮必要上下文形成可被 MaxKB 实际读取的一轮输入，后续依赖会话历史。不能假设完整 OpenAI messages 数组都被使用，也不据此增加完整聊天同步系统。同一会话 AI 请求串行，上游可能已处理的超时不盲目重发。

机器人绑定变化、原会话丢失或失效时返回明确状态，不能静默新建并声称保留历史。新建与历史补充规则在发布前核对实际能力。

## 13. 故障控制、暂停与恢复

### 13.1 分类处理

| 场景 | 控制行为 |
| --- | --- |
| 未完成登录/附加验证 | 禁止运行任务 |
| 启动时 DPin 断连或配置获取失败 | 不启动，提示重试，不使用本地方案 |
| 单次网络或 AI 故障 | 跳过当前对应处理，继续后续候选人，不写不匹配 |
| DPin 持续完全无法连接 | 暂停整个相关任务，记录网络暂停原因 |
| 登录失效且无法续期、账号禁用 | 禁止新任务并暂停运行任务，等待有效认证 |
| 模型/资源权限拒绝 | 停止被拒绝能力，展示原因并重新校验；不绕过 |
| 额度不足 | 遵循 DPin，停止全部 AI 调用 |
| 未配置 MaxKB | 只跳过生成回复，其他可执行流程继续 |
| DPin 可达、单路上游持续故障 | 与网关断连区分，最终持续故障策略在发布前确定 |

桥接保留稳定错误类别、错误来源、request ID及必要重试信息，不靠中文消息推断。上游 401 不等于 DPin 用户未登录；429 不一定是余额不足；单个上游错误也不等于 DPin 整体断连。

### 13.2 暂停与自动继续

复用编排器现有 pause/resume 与 worker 安全检查点，新增暂停原因和操作代次。收到暂停后阻止新动作，在下一安全边界停下；实际发送前再次检查，避免排队消息继续发送。

DPin 断连后定期探测，后端恢复且登录、权限有效时自动继续原任务，无需用户点击继续。用户主动暂停、停止、切换账号或任务已终止时，旧探测不能重新开跑。

连通监测与 AI 故障独立记录，检测超时、连续失败阈值、退避及恢复成功条件配置化并通过联调定值。恢复判断不能仅依据 `/api/status` 成功，还要验证当前会话及所需权限。

暂停期间同进程保留的配置如何刷新不扩展为已确认产品行为；进程重启必定重新获取云端方案。MaxKB 会话引用可从本地恢复，但仍要重新通过任务认证和配置校验。

## 14. 初始导入与正式发布

### 14.1 现有有效方案导入

已核对的候选是筛选方案“售后主管”、沟通方案“售后方案1”和“售后主管2”。目录存在的其他 JSON 不自动等于已登记或当前可用方案。

实施时对实际使用版本的下拉框、worker、本地登记与完整内容建立清单。保留业务字段和原有语义，旧 ID映射到新的服务端 ID，迁移职位选择引用。售后经验要求在对应筛选方案中完整保留。

导入通过有权限的工具或管理接口执行，按类型与来源标识幂等；再次执行不能生成重复方案或覆盖管理员后续修改。导入后验证详情、网页回显、下拉框选择和启动获取一致。

历史文件可作为迁移输入归档，但运行时不读它们，正式包不包含可被当作备用方案库的历史正文。须移除的内置来源逐项明确：`packages/boss-auto-browse-and-chat` 下沟通方案 JSON（`local-beiliao-plans.json`、`local-beiliao-after-sales-2.json`）的运行时读取与 `local-filter-executor.mjs` 的静态筛选登记，以及 renderer 产物内嵌的 `LOCAL_BEILIAO_DEFINITIONS`／`LOCAL_FILTER_DEFINITIONS` 副本（`index-C9ubNdld.js` 等）——当前方案数据在 packages 与 renderer 是双份存放，云端切换后两处都必须停用，防止其一成为残留备用来源。renderer 内嵌副本的改动落在编译产物上，按第 2.2 节的产物源码化策略处理。初始有效方案必须先导入再切换云端来源。

### 14.2 默认联网与内置内核

正式 `.app` 直接启动，不通过 `sandbox-exec`、`.command` 或特殊环境变量。移除离线策略中无变量即退出的门槛，允许正常访问 DPin、BOSS 及受控注册网址。开发离线入口可保留，不能决定用户安装后的行为。

内核固定加载随包 `core-engine.cjs`，保留完整性校验；缺失或损坏停止启动并提示，不下载旧远程内核。去掉 `index.js` 默认开发旁路，内核加载和产品认证分别管理。

处理旧最低版本检查及更新请求，避免删除旁路后恢复旧强制更新。首期没有新更新服务时使用完整包升级；不把旧云端作为备用。移除正常链路中的旧服务调用，残留访问拦截保留作防护。

外层网络沙箱与 Electron/Chromium 自带隔离不同。正式入口移除为外层嵌套沙箱设置的 no-sandbox 开关，并验证页面/preload 兼容性，正常联网无需全局关闭浏览器安全隔离。

### 14.3 可重复构建与安装验收

构建纳入新增主进程模块、通信层、两个网页、资源和内核，manifest 记录输入。修改源文件后重新生成包，不直接改 build 或安装产物交付。

当前实际打包脚本仅支持 macOS arm64，使用 ad-hoc 本地签名。正式外部分发完成 Developer ID签名、公证及资源签名验证；Windows/Intel Mac 需准备对应原生依赖与独立构建，不能从旧 electron-builder 配置推断已经支持。

目标电脑无需源码、开发目录或旧应用资源。浏览器、配套工具、页面和内核从安装包定位；可写数据放用户目录。构建期间对旧应用资源的借用应整理成明确构建输入，不变成用户运行依赖。

在干净机器或新系统用户中，通过系统界面直接安装并双击，验证注册跳转、登录恢复、网页保存、下拉框、任务启动和 AI。无旧凭据、无联网环境变量、无恢复工程时也必须可用。

## 15. 实施顺序、交付物与验收

### 15.1 分阶段交付

| 阶段 | 实施内容 | 交付物 | 进入下一阶段的标准 |
| --- | --- | --- | --- |
| 1. 契约与安装基础 | 两类真实 JSON 字段并集、逐项网页对应与缺项清单、资源标识、拟新增接口、正常联网和内置内核 | 字段对应/接口/执行能力清单，能直接启动的开发安装包 | 每个字段有明确归属，已有网页项与待补项确定，方案契约明确 |
| 2. 认证与通信 | DPin 登录、验证、既有刷新、退出、请求代理、本地通道、各任务入口校验 | 主进程代理及 daemon/worker 接线 | 前置验证：部署启用的 Turnstile／附加验证流程、所选网络路径的 Cookie／Origin 与 30 秒竞争契约；通过后正式接线，收尾时所有入口认证有效、凭据不分发落盘 |
| 3. DPin 配置服务 | 模型迁移、方案 CRUD、授权、职位同步/选择、runtime-config、管理权限 | 云端完整数据与接口 | 三数据库验证，私人隔离、版本及批量事务成立 |
| 4. 导入与两个网页 | 有效方案导入、已有项双向适配、JSON 业务缺项补齐、云端保存/回显、设置入口、下拉框 | 字段齐全、可编辑和选择的真实云端方案 | A34—A37 验证；无修改及只改名称不丢字段，补充项可编辑回显，保存后可选择，无本地正文存储 |
| 5. 本地执行器 | 内存注入、静态依赖移除、字段支持、调度持久化白名单 | 真实方案驱动的 worker | 多职位使用正确方案，重启重新获取，存储审查通过 |
| 6. AI 桥接 | 用户态中继、JEV、MaxKB、职位机器人路由、会话与计费 | 可验证的筛选和回复链路 | 权限/额度有效，故障不误淘汰，会话隔离和记账成立 |
| 7. 故障恢复与发布 | 分类暂停、定期恢复、操作去重、完整打包、签名及安装验证 | 可分发安装包与验收记录 | 完整用户链路及失败路径通过，未决事项有明确结论 |

两端可在接口确定后并行实施，但真实云端方案导入必须先于运行来源切换，认证代理和启动获取必须先于 AI 开跑。阶段结束依赖实际验证，不以文件存在或按钮可点击代替功能完成。

### 15.2 功能与安全验收矩阵

| 验收主题 | 必测内容 |
| --- | --- |
| 产品登录 | 密码原样提交、挑战未完成不能运行、部署启用的 Turnstile／附加验证正常完成、受信任网页登录身份交接、注册回客户端登录，无客户端标识豁免 |
| DPin 既有刷新适配 | Cookie 发送与 Origin 校验分别验证；Cookie／安全存储更新、并发合并、新凭据生效、匹配旧凭据的 30 秒竞争结果、超窗已识别旧凭据撤销、未知凭据拒绝、退出/切换后的迟到响应不能覆盖 |
| 方案保存与回显 | 两类有效 JSON 字段逐项有对应处理；业务缺项已补控件；无修改/只改名称往返、补充项编辑、未知字段、嵌套结构、数组顺序、缺失/空值、布尔/零值、保存失败、版本冲突 |
| 方案权限 | 公用及本人可见，猜 ID无法读取他人方案，不能跨用户修改/绑定/执行 |
| 职位选择 | 上方未确认不保存、确认批量事务、下方只改一职；真实方案、pass_all、skip_all、jump 各自保存回显及启动；skip_all 无主动招呼且不记业务不匹配、jump 无自动沟通，两类模式独立、未知配置不自动放行，关联联动与跨账号隔离正确 |
| 任务入口 | 手动、批次、定时、daemon、直接 IPC、恢复和重启全部校验；旧凭据和本地模式不放行 |
| 内存与落盘 | 方案不进编排、workerSpec、环境变量、参数、浏览器存储、日志和诊断；重启重新获取 |
| JEV | 不同方案筛选要求独立、售后条件不扩散、有效等级按方案判断、异常和 gate 超时跳过而非淘汰 |
| MaxKB | 同岗位跨用户机器人一致，不同职位目标正确；多候选人/多职位会话隔离，重启延续，不接受他人会话引用 |
| 本地动作 | 放弃/暂停不调用机器人，附件和联系动作保持，发送回执和去重有效 |
| 权限与额度 | 模型/渠道/用户组限制有效，额度不足停止全部 AI，拒绝不当作筛选等级或回复 |
| 恢复 | 偶发故障继续、持续 DPin 断连暂停、认证和权限恢复才自动继续、用户主动停止不被恢复 |
| 安装包 | 干净机器双击、无特殊变量/旧凭据/源码目录，页面/内核/工具齐全，旧云端无正常回退 |

测试以真实可观察行为为准，优先扩展现有 `local-filter`、`local-inbound-screen`、`local-beiliao-plan`、`local-rejection-intent` 和附件测试，以及 DPin 的 auth_session、auth_flow、systemone 测试。新增后端小功能避免分散重复测试，大幅重写测试遵循项目 testify 规范。

测试运行的现状约束：BelloBuddy 的 `node --test` 目前逐文件运行共 32 例全部通过，整目录一次性运行会失败。实施时先查明整目录失败原因（共享 fixture 或模块状态冲突）并修复；在修复前，验证与 CI 以逐文件运行为基线，不以整目录失败为由跳过或删除任何测试。

### 15.3 数据库、构建与集成验证

所有新增数据库行为在真实 SQLite、MySQL、PostgreSQL 上验证，覆盖新库、最新发布版代表性数据升级、至少两次启动/迁移，以及数据、索引、唯一性和事务回滚。遵守最低支持版本与 GORM 方言集合约束，方案 TEXT、字段默认值和版本更新不得依赖单一方言。

AI 消费路径验证主库与独立日志库；受影响的 ClickHouse 日志路径也覆盖。记录实际数据库版本、运行命令和结果，mock、构建或一种数据库测试不能替代三库验证。

| 对象 | 实施时执行的检查 |
| --- | --- |
| DPin 后端 | 受影响 Go 测试与根模块构建；真实三数据库迁移/授权/事务验证 |
| relaykit 如有修改 | 在独立模块执行 `GOWORK=off go build ./...` |
| DPin 管理前端 | 相关测试、`bun run typecheck`、lint 和 `bun run build` |
| 两个方案网页 | 各自实际构建命令、字段往返与保存/回显/下拉框联动测试 |
| BelloBuddy | 受影响 `node --test`、`node scripts/build.mjs` 和真实 worker 集成 |
| 正式产物 | `node scripts/package.mjs`、签名/公证检查、干净机器真实入口验收 |
| 存储审查 | 对比用户目录、编排/检查点、日志、浏览器存储和包内容，确认正文没有隐式持久化 |

认证实施遵循仓库要求的 [OWASP ASVS 5.0.0](https://owasp.org/projects/asvs) 及适用指南，保留 [认证指南](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、[会话管理指南](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 和 [CSRF 指南](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html) 的服务端控制。重点涉及 v5.0.0-6.2.8、v5.0.0-6.3.1/6.3.4、v5.0.0-6.5.1/6.5.5、v5.0.0-7.2.1/7.2.4、v5.0.0-7.4.1/7.4.2、v5.0.0-8.3.1；实施交接记录适用控制、实际验证及缺口，不声称本计划已完成符合性认证。

## 16. 发布前需定值或验证的细节

以下不改变已确认架构，也不要求重新讨论 DPin 刷新策略；与其无依赖的工作可先实施。

| 事项 | 已确认基线 | 实施时补齐 |
| --- | --- | --- |
| 部署地址 | 访问当前 DPin 项目的部署实例，生产 HTTPS | API、注册网站、固定可信来源及 MaxKB 服务端可达地址 |
| 登录验证范围 | 保留 DPin 部署启用的验证，当前无 BelloBuddy 专用豁免配置 | 启用时验证部署域名下的受信任 BrowserWindow 登录、附加验证及认证代理交接（阶段 2 前置） |
| 主进程认证网络栈 | Electron `net` + 专用内存 session，按失败环节选择受信任页面备用路径 | Cookie 发送、Secure 模式、服务端 Origin 校验分别留证；验证新凭据生效、30 秒竞争、超窗重放及未知凭据拒绝，白名单不替代 Cookie 发送验证 |
| IP 级限流配额 | 复用 DPin 既有 CriticalRateLimit，不改服务端代码 | 同出口 IP 多用户刷新并发的配额评估与部署配置调整 |
| 认证宿主生命周期 | 后台启动必须经统一代理 | 窗口关闭后的后台行为及代理存活安排 |
| 网关断连检测 | 持续完全断连暂停，恢复后自动继续 | 超时、失败阈值、退避与恢复判定 |
| 同岗位识别方式 | 管理员手工映射，管理界面按名称/城市提供候选推荐 | 映射建立与维护流程的用户确认（阶段 3 前置） |
| 同进程恢复快照 | 启动配置固定，进程重启重新获取 | 网络暂停恢复时是否刷新方案和机器人绑定 |
| 机器人会话变化 | 同候选人同职位延续原会话 | 绑定改变/会话失效的新建、提示和历史补充 |
| 单路上游持续故障 | 与 DPin 整体断连区分 | 持续故障运行控制与提示，不擅自套用全任务断连策略 |
| JSON 与网页字段覆盖 | 两类真实 JSON 为基准，业务缺项必须补网页，保存、编辑回显与执行分别验收 | 完整路径对应表、枚举/单位/默认值核对、原型多余选项映射及执行缺口明确处理 |
| MaxKB 实际响应 | MaxKB 侧二次开发补齐用量字段（用户已确认） | 二开后以授权测试应用验证真实推理、用量格式及服务留存 |
| 产品发布平台 | 当前 macOS arm64 打包已具基础 | 正式签名/公证；其他平台对应依赖与安装验证 |

### 16.1 本轮文档修正与验证边界

2026-10-05 根据技术路径审查修正四项：区分真实方案、`pass_all`、`skip_all`、`jump` 与未配置状态，并衔接保存、启动及执行；将 Cookie 发送失败与服务端 Origin 拒绝分开处理；移除把 BelloBuddy 专用 Turnstile 豁免视为现成配置的路径，保留已启用验证；将刷新验收改为现有 30 秒竞争契约。

本轮只修正文档，依据现有代码与上轮核对的 OWASP 认证、会话管理及 CSRF 指南。认证适配沿用 ASVS 5.0.0 的 v5.0.0-6.3.1／6.3.4、v5.0.0-7.2.1／7.2.4 和 v5.0.0-7.4.1／7.4.2 等适用约束；尚未执行客户端认证网络验证、安全回归测试或符合性验证。第 7.2 节完整字段路径清单及 MaxKB 二开后的真实用量验证仍是实施前置工作，不能因文档修正而视为已通过。

## 17. 交付完成的判定

完成标准是：用户安装并登录 DPin，通过两个已有网页创建和修改私人方案，云端保存后在下拉框选中，职位选择成功保存，任务启动读取完整配置并在本地执行；JEV 使用对应方案指引，MaxKB 使用对应岗位机器人，权限与额度遵循 DPin，故障和恢复符合已确认规则，完整方案无本地持久化路径。

交付包含 DPin 后端及管理端变更、BelloBuddy 源码及构建集成、有效方案迁移与 ID映射记录、正式安装包、字段能力清单，以及真实测试/数据库/安装验收记录。只完成接口、网页或下拉框，不等于整体接入完成。

本次交付为技术路径文档重写，未执行程序改造、数据迁移、真实 AI 推理、三数据库测试、打包或安装验收；后续实施按上述阶段推进，并在交接时列明真实完成项。

2026-10-04 按需求文档 v0.19 补充两类 JSON 的字段对应与网页缺项清单；2026-10-05 同步需求文档 v0.20，修正第 6、8、15、16 节的特殊选项和认证契约。两次修订均仅修改指定文档，网页、执行器及安装包尚未据此改造。
