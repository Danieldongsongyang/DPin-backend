# BelloBuddy 接入 DPin 实施进度清单

更新时间：2026-10-06。状态：**按用户要求暂停开发，目标未完成**。

本清单以 [技术实现路径](/Users/a1/Desktop/DPin-backend-newapi/BelloBuddy接入DPin技术实现路径.md) 为主，[需求文档](/Users/a1/Desktop/DPin-backend-newapi/BelloBuddy接入DPin需求文档.md) 为参考。暂停时核对了当前源码、Git 工作区、已有验证日志和本地 DPin 数据库。本次只整理进度，不继续开发、测试、迁移或打包。

标记说明：`[x]` 表示对应范围已经实现并有验证；`[ ]` 表示未完成或尚未获得足够验证。**模块验证通过不代表整个客户端任务链路已经打通。**

### 客户端实际进度更正（2026-10-06）

**BelloBuddy 产品登录接入尚未整体完成。** 更正时的用户工作台截图中，BOSS 直聘账号已登录，但产品账号仍显示“未登录”，工作台仍提示登录倍罗账号。随后用户明确授权先修复左下角账号显示；本次局部修复结果记录如下，其他开发继续暂停。

- [x] 左下角账号区改为读取主进程 DPin 认证代理的用户摘要；重启开发版后通过真实会话续期恢复，界面显示实际用户名及“DPin · 已登录”。
- [x] 左下角未登录点击入口及账号菜单使用 DPin 登录；退出调用同一代理，通知侧栏更新。仅返回用户 ID、用户名、状态和变更序号，不返回认证凭据；迟到状态不覆盖较新的显示。
- [x] 新增账号 IPC 回归覆盖其他窗口、子框架、远程页面及额外参数拒绝，登录恢复、用户摘要字段白名单、退出本机状态与远端撤销未确认、导航后取消交付。现有 54 项测试全部通过；构建成功，423 个输入文件。
- [ ] 主工作台提示、设置中的旧倍罗卡片及全部任务启动检查统一接入 DPin 认证状态。
- [ ] 替换旧 `bello-login`、倍罗凭据检查和退出路径，移除本地模式跳过产品认证的旁路；保留本地 BOSS 账号登录与浏览器会话。
- [ ] 验证登录、重启恢复、退出及失效在主界面和任务入口中的一致行为。

本次验证日志：[54 项测试](/tmp/bellobuddy-dpin-account-full-tests.log)、[构建记录](/tmp/bellobuddy-dpin-account-build.log)、[实际侧栏截图](/tmp/bellobuddy-dpin-account-sidebar.png)。旧 `.app` 未重新打包；当前打开的是更新后的开发构建。本次没有实际退出用户的 DPin 会话，退出与导航取消场景使用隔离夹具验证。

认证参考：沿用 [OWASP ASVS 5.0.0](https://owasp.org/projects/asvs) 中现有认证代理对应的 `7.2.1/7.2.4`、`7.4.1/7.4.2` 控制范围，并核对[认证备忘单](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)和[会话管理备忘单](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)。本次侧栏依据代理验证后的状态显示，凭据不交付页面，窗口调用由主进程强制校验；未修改服务端认证与刷新策略。整体任务认证、生产部署及完整 ASVS 验收仍未完成。

“设置 → 筛选方案”打开本地编辑器属于已确认需求；该入口目前触发的 DPin 登录窗口，只证明编辑器认证接线，不能当作产品登录替换完成的证据。主工作台保留原有产品结构，DPin 承接产品账号认证与云端能力，不以 DPin 管理后台替代工作台。

恢复整体开发时应先完成上述客户端产品登录接入，再推进沟通编辑器等后续工作。本次只实施用户明确指定的左下角账号修复，整体目标仍保持暂停。

## 一、工程和服务位置

| 对象 | 位置／地址 | 当前用途 |
| --- | --- | --- |
| DPin 项目 | `/Users/a1/Desktop/DPin-backend-newapi` | 配置服务、认证、计费、AI 桥接、管理后台 |
| DPin 后端 | `http://localhost:3000` | 用户指定的本地 Go 服务，已由 subagent 重启加载后端变更 |
| DPin 前端 | `http://localhost:5173` | 既有管理后台及受信任登录网页 |
| DPin 本地数据库 | `/Users/a1/Desktop/DPin-backend-newapi/one-api.db` | 当前实际运行的 SQLite 数据库 |
| BelloBuddy 恢复工程 | `/Users/a1/Desktop/bosscli/recovery/BelloBuddy` | 实际客户端源码、执行包和构建脚本 |
| 实际客户端前端 | `BelloBuddy/app/src/renderer` | 正式路由改动必须落到这里 |
| 筛选编辑器规范源码 | `BelloBuddy/plan-editors/filter` | 已纳入 BelloBuddy 构建 |
| 原筛选 Vue 原型 | `/Users/a1/Desktop/bosscli/recovery/vue-frontend-dpin` | 相关页面已改为引用规范源码 |
| 原沟通 React 原型 | `/Users/a1/Desktop/bosscli/communication-plan-editor-clone` | 原设计来源；本轮没有修改原型工程 |
| 沟通编辑器施工目录 | `BelloBuddy/plan-editors/communication` | 刚建立，未完成、未纳入构建或客户端入口 |
| MaxKB 源码 | `/Users/a1/Desktop/MaxKB-2` | 用户授权操作的本机源码工程 |
| MaxKB 管理前端 | `http://127.0.0.1:3010/admin/` | 源码前端；浏览器访问 `/admin/home` 可用 |
| MaxKB 源码后端 | `http://127.0.0.1:8080` | DPin 应连接的本机后端 API 地址 |
| 筛选编辑器开发预览 | `http://127.0.0.1:5185/dpin-filter-editor.html` | 普通浏览器没有 Electron IPC，因此不能代替真实云端联调窗口 |

用户表示旧 Docker MaxKB 后续自行删除。**本次没有删除旧容器、数据库或数据。** 服务是否仍运行，恢复开发时应重新检查；不能只依赖本文地址或此前的进程号。

## 二、已经完成并验证的工作

### 2.1 DPin 配置服务和管理端

- [x] 新增方案、来源映射、招聘账号、职位配置、岗位机器人绑定及应用中继身份模型。
- [x] 完整方案以 TEXT JSON 保存；保留业务正文，权限元数据单独管理。
- [x] 公用方案及本人私人方案的读取、创建、修改和权限隔离。
- [x] 方案版本校验；过期版本返回冲突，避免静默覆盖。
- [x] 招聘账号／职位同步接口、职位选择保存及批量事务接口。
- [x] 区分真实筛选方案、`pass_all`、`skip_all`，以及沟通方案、`jump` 等模式。
- [x] 任务 `runtime-config` 接口返回完整配置，并提供执行端需要的兼容字段。
- [x] 来源映射幂等导入接口；重复导入不重复创建、不覆盖后续编辑。
- [x] 管理权限及 Casbin 资源接入。
- [x] DPin 管理前端的 BelloBuddy 功能页面、方案管理及机器人绑定管理代码。
- [x] 管理前端复用已有组件、处理版本冲突，并接入七种语言翻译。

以上接口和管理页面的实现／回归已完成，**客户端账号与职位尚未实际同步到当前部署**，因此不能视为职位端到端验收通过。

主要文件：[配置服务](/Users/a1/Desktop/DPin-backend-newapi/service/bellobuddy.go)、[配置模型](/Users/a1/Desktop/DPin-backend-newapi/model/bellobuddy.go)、[路由](/Users/a1/Desktop/DPin-backend-newapi/router/bellobuddy-router.go)、[管理前端](/Users/a1/Desktop/DPin-backend-newapi/web/src/features/bellobuddy)。

### 2.2 DPin 用户态 AI 桥接

- [x] 增加用户态筛选／回复桥接接口，复用原生中继、预扣、结算、退款和消费日志。
- [x] JEV／TypeSafe 筛选入口的请求约束和上游实际用量处理。
- [x] MaxKB 渠道适配器，渠道类型为 `65`。
- [x] MaxKB 应用 UUID 映射、完整助手回复校验和实际 Token 用量校验。
- [x] 缺失／非法用量时拒绝返回可发送回复；显式合法零用量有独立记录。
- [x] 固定岗位机器人路由仍校验用户组、模型、渠道能力及绑定版本。
- [x] 内部托管 Token 的生成和受控使用；禁止普通接口导出／修改其 Key。
- [x] 已撤销的内部凭据不自动重新创建以绕过撤销。
- [x] 签名／加密会话引用，绑定用户、账号、职位、候选人、绑定版本和渠道。
- [x] 拒绝裸上游 `chat_id`、跨用户／跨职位引用、过期及绑定变化等情况。
- [x] 错误保留稳定类别、来源和请求标识；上游 401 不清除 DPin 产品登录。
- [x] 回归验证正确扣费、失败退款、零用量、额度不足及会话隔离等行为。

**这些回归使用隔离数据库和受控上游夹具。当前部署还没有配置启用 MaxKB 渠道，也没有完成真实招聘职位到 MaxKB 的消费闭环。**

主要文件：[AI 桥接服务](/Users/a1/Desktop/DPin-backend-newapi/service/bellobuddy_ai.go)、[AI 控制器](/Users/a1/Desktop/DPin-backend-newapi/controller/bellobuddy_ai.go)、[MaxKB 适配器](/Users/a1/Desktop/DPin-backend-newapi/relay/channel/maxkb)、[回归测试](/Users/a1/Desktop/DPin-backend-newapi/router/bellobuddy_test.go)。

### 2.3 Electron 产品认证代理

- [x] 使用 Electron `net` 和专用内存 Cookie 分区，复用 DPin 产品会话。
- [x] 受信任 DPin 网页登录，页面不向 renderer／worker 分发认证 Token。
- [x] 使用系统 `safeStorage` 加密刷新凭据，备份权限为 `0600`。
- [x] 启动恢复通过真实续期和用户信息校验判断登录有效性。
- [x] 并发续期合并、Cookie 轮换、30 秒竞争契约、未知凭据拒绝及超窗重放撤销。
- [x] 退出取消在途操作，旧异步响应不能恢复登录。
- [x] HTTP 本机和 HTTPS 夹具分别验证 Cookie、Secure、Origin 及认证网络行为。
- [x] 已通过真实管理员登录连接当前本机 DPin 并执行方案导入。
- [x] 当前本机 `/api/status` 曾核实 Turnstile 未启用；没有添加客户端豁免。

**侧栏用户摘要及其登录／退出菜单已接入该代理；主工作台和设置中的旧产品状态判断、全部任务入口、daemon 和 worker 尚未整体接入，旧产品认证／开发旁路还没有整体移除。启用 Turnstile 或附加验证的部署验收仍待进行。**

主要文件：[认证代理](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/dpin-auth-proxy.cjs)、[本机登录／导入工具](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/scripts/dpin-local-connect.cjs)、[Electron 网络回归工具](/Users/a1/Desktop/DPin-backend-newapi/tools/bellobuddy/electron-auth-spike.cjs)。

### 2.4 现有有效方案导入

- [x] 核对并导入当前有效登记的完整方案。
- [x] 核对导入正文及关联筛选方案一致性。
- [x] 重复导入验证通过。
- [x] 暂停时从当前 SQLite 只读核实仍有以下 3 个方案、3 条来源映射。

| 类型 | 名称 | 新 DPin ID | 版本 | 外层筛选关联 | 当前权限 |
| --- | --- | --- | --- | --- | --- |
| 筛选 | 售后主管 | 1 | 1 | 无 | 公用 |
| 沟通 | 售后方案1 | 2 | 1 | 筛选方案 1 | 公用 |
| 沟通 | 售后主管2 | 3 | 1 | 无 | 公用 |

导入命名空间为 `bellobuddy-local-v1`。旧正文中的 ID、owner、visibility 等兼容字段不决定新服务端授权。

### 2.5 筛选方案编辑器

- [x] 规范源码迁入 BelloBuddy 工程，使用原有 Vue 设计及样式。
- [x] 通过固定方案 IPC 读取真实云端列表和完整详情，无本地样例兜底。
- [x] 公用方案只读；复制后作为私人草稿保存；私人方案新建、编辑、启用／禁用。
- [x] 年龄范围、开闭边界、多范围、规则组合、教育性质及关联搜索标志回显和编辑。
- [x] AI 指引、等级、分数条件、摘要长度、招聘类型等已有 JSON 字段回显和编辑。
- [x] 不因关闭开关而清空原配置；未知条件保留，并明确未支持编辑的类型。
- [x] 完整 JSON 无损编辑：保留未知字段、大整数、精确小数、显式零值、空值及数组顺序。
- [x] 保存失败／版本冲突保留草稿；取消释放草稿。
- [x] 专用 preload 只有 `list/get/save`，拒绝其他窗口、子框架及远程页面调用。
- [x] 登录／加载期间主窗口关闭或导航后，不交付迟到页面／正文。
- [x] 客户端实际 `SearchSetting` 路由已接入打开编辑窗口的入口代码。
- [x] 构建脚本自动构建并打包筛选网页，manifest 记录源码输入。
- [x] 真实 Electron 窗口读取当前云端“售后主管”，核对原始规则及售后经验指引。
- [x] 实际界面验证查看、复制、改名、关闭快筛后保留规则和取消草稿。

界面验证没有保存测试草稿。真实保存／重开／版本冲突／窗口权限链路在 Electron + Go 隔离夹具中验证。**完整正式客户端安装后的入口验收，以及保存后进入职位下拉框，仍未完成。**

主要文件：[筛选网页](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/plan-editors/filter/src/SearchSettingLocalPreview.vue)、[无损 JSON 模块](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/shared/dpin-plan-json.mjs)、[方案服务](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/dpin-plan-service.cjs)、[方案 IPC](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/dpin-plan-ipc.cjs)、[客户端宿主](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/dpin-app-host.cjs)。

## 三、部分完成，但还不能视为打通

### 3.1 MaxKB 真实用量二次开发

- [x] 在用户授权的 `/Users/a1/Desktop/MaxKB-2` 源码应用严格实际用量补丁。
- [x] 从当前响应的 `usage_metadata`／`response_metadata.token_usage` 读取真实数量，不使用共享状态或估算兜底。
- [x] 校验整数、非负、上限、总量及不同来源的一致性。
- [x] 缺失实际用量时返回独立失败，避免伪造成功回复。
- [x] 补丁工具、辅助回归及此前的 Django 路径回归已运行。
- [x] 对额外模型调用配置增加严格拒绝，防止把主回复用量误称为完整请求用量。
- [ ] 当前测试应用的完整真实用量验证。
- [ ] 两轮真实回复、同会话延续、DPin 消费日志及余额变化闭环。

已定位测试应用“测试boss”，ID 为 `01a0dbc2-4722-7681-9fbc-4ddf277cbbd6`。此前检查发现草稿长期记忆已开启，发布版配置与草稿不同；记忆提取可能触发额外模型调用。当前严格接口／验证器会拒绝不满足完整用量条件的配置。曾观察到主回复的真实 Token 数量，**不构成长期记忆等额外调用已经记账的证据**。

恢复后需明确处理：保持长期记忆并补齐额外调用用量，或经用户同意调整测试应用配置再验收。此前没有擅自关闭应用长期记忆。

源码补丁涉及 `apps/common/handle/dpin_actual_usage.py`、`apps/application/chat_pipeline/step/chat_step/impl/base_chat_step.py`、`apps/common/handle/impl/response/openai_to_response.py`、`apps/chat/serializers/chat.py`。

工具：[应用补丁](/Users/a1/Desktop/DPin-backend-newapi/tools/maxkb/apply-strict-usage.py)、[真实联调验证器](/Users/a1/Desktop/DPin-backend-newapi/tools/maxkb/verify-strict-usage.py)。MaxKB 的本地环境文件和应用 Key 含秘密，恢复工作时在本机安全读取，不复制到本文、聊天或命令参数。

### 3.2 客户端任务网关与会话存储

- [x] 任务网关模块读取、校验并冻结云端完整配置。
- [x] 任务清单只返回必要元数据，不包含方案正文或认证凭据。
- [x] JEV 故障结果不带 `pass/level`，回复故障不带可发送文本。
- [x] 同会话请求串行；按身份和绑定版本隔离。
- [x] 请求前保存不确定状态，网络异常等情况下阻止盲目重复调用。
- [x] 会话引用／不确定状态使用显式持久化白名单及原子文件保存。
- [x] 模块回归覆盖退出、迟到响应、绑定变化、重启及存储失败。
- [ ] 正式 IPC、daemon 通道及真实 worker 尚未接入这些模块。
- [ ] 当前真实招聘任务仍未证明使用上述云端配置及 AI 网关。

主要文件：[任务网关](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/dpin-task-gateway.cjs)、[会话存储](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/dpin-conversation-store.cjs)。

### 3.3 停工前刚开始的沟通编辑器：未验证施工代码

- [x] 阅读原 React 网页及两份真实沟通 JSON，核对技术路径中的字段分组。
- [ ] 新增 `app/src/shared/dpin-communication-draft.mjs`，包含字段路径映射、无损草稿、外层筛选关联和 JSON 子结构处理；**未测试**。
- [ ] 新建 `plan-editors/communication`，复制原 React／CSS 输入，新增 package、Vite 配置和 HTML；**未安装依赖、未构建**。
- [ ] 新目录的 `src/App.jsx` 已移除原演示初始化及原 `App` 导出，新的云端应用入口尚未写完；**目前不是可运行页面**。
- [ ] 新目录的 `src/main.jsx` 仍来自原型，依赖待完成的 `App` 导出。
- [ ] 沟通目录没有接入 `scripts/build.mjs`、`DPinAppHost` 或客户端路由。
- [ ] 原沟通原型工程没有在本轮修改，仍保留原有 localStorage 演示逻辑。

暂停时保留上述施工源码，不删除、不冒充成品。**此前的 53 项测试和 422 个输入文件构建不覆盖这批停工前新增代码。** 恢复时应先阅读、评审并补完，不能直接宣称它已经通过构建或可保存。

## 四、尚未完成的完整任务清单

### 4.1 两个网页与职位配置

- [ ] 沟通网页完成真实列表、详情、新建、复制、私人保存、版本冲突、失败保留及取消。
- [ ] 完成沟通字段双向映射，特别是追聊模板、完整话术对象、图片资源、问题结构及属性映射、接收附件策略、失败动作和结束指引。
- [ ] 核对真实枚举、单位、默认值和执行消费者；原型中无契约依据的选项不能伪称已配置。
- [ ] 两类方案形成完整字段路径／网页控件／执行支持对应清单，补齐所有已知业务字段。
- [ ] 两个网页都纳入可重复构建、主进程受控入口及正式包。
- [ ] 「账号与职位」两类下拉框改为真实 DPin 元数据来源。
- [ ] 职位同步、旧 ID 到服务端 ID 映射、关联选择保存和重新打开回显。
- [ ] 上方确认批量事务、下方单职修改，以及特殊模式的完整界面验收。
- [ ] 云端保存后可选、启动读取一致；不得以接口成功替代下拉框验收。

### 4.2 全部任务入口及执行器

- [ ] 手动、批次、定时、daemon、直接 IPC、恢复和重启全部强制经过统一产品认证。
- [ ] 建立认证宿主与 daemon／worker 的受控本地通信和生命周期。
- [ ] 启动获取完整云端配置，只在任务内存中注入给 worker。
- [ ] 移除正式运行路径的静态沟通 JSON、静态筛选登记及 renderer 内嵌副本。
- [ ] 移除旧本地凭据和开发旁路对产品任务的放行。
- [ ] 调度持久化、workerSpec、环境变量、参数、日志及诊断中不包含正文或认证凭据。
- [ ] 真实多职位、重启重新获取配置及运行来源一致性验收。
- [ ] 补齐本地执行器对沟通字段的实际行为支持，而非仅保存字段。
- [ ] `skip_all` 不主动招呼、不记业务不匹配；`jump` 不自动沟通；未知配置不自动放行。

### 4.3 真实 AI 和动作闭环

- [ ] 正式 worker 的 JEV 调用切换到 DPin 网关，并使用当前方案自己的指引。
- [ ] 移除旧全局售后指引和直接上游 Key 路径，防止跨方案污染。
- [ ] JEV 网络失败、非法结果及 gate 超时跳过当前处理，不写淘汰结果。
- [ ] 当前部署配置启用 MaxKB 渠道、明确管理员定价／逻辑模型和岗位机器人绑定。
- [ ] 当前实际账号／职位建立云端记录和有权限的绑定。
- [ ] 真实验证同岗位路由、多用户／多候选人／多职位隔离及重启会话续接。
- [ ] 先执行本地放弃、暂停、固定话术、附件与联系动作，再决定是否调用机器人。
- [ ] 机器人未配置时只跳过生成回复，其他流程继续。
- [ ] 发送前检查任务状态，按消息／职位／候选人／动作去重，以发送回执确认完成。
- [ ] 真实 JEV／MaxKB 消费日志、余额扣费、失败退款及额度耗尽控制。

### 4.4 暂停、恢复和权限变化

- [ ] 接入稳定错误类别和 HUD／任务状态，不根据中文消息猜控制行为。
- [ ] 区分偶发 AI 故障、持续 DPin 断连、上游单路故障、产品登录失效和权限／额度拒绝。
- [ ] 复用真实 pause/resume 和 worker 安全检查点，暂停后阻止新动作及排队发送。
- [ ] 定期探测及退避；恢复必须校验会话与权限，不能只检查 `/api/status`。
- [ ] 用户主动暂停、停止、切换账号后，旧探测不重启任务。
- [ ] 确认断连阈值、上游持续故障策略和同进程恢复时的配置刷新行为。

### 4.5 安装包及最终发布

- [ ] 正式入口默认正常联网，无特殊环境变量、`.command` 或外层网络沙箱要求。
- [ ] 清理旧强制更新／云端正常调用及开发旁路，保留必要防护拦截。
- [ ] 固定加载内置内核并保持完整性校验；缺失／损坏失败，不能下载旧内核兜底。
- [ ] 正式入口移除为旧外层沙箱设置的 `no-sandbox`，验证兼容性。
- [ ] 将两个网页、执行器、工具和资源完整打包，不依赖源码或旧应用目录。
- [ ] 本轮完整 `.app` 重新打包及资源签名检查。
- [ ] Developer ID 签名、Apple 公证及正式分发验收；此前 ad-hoc 签名不代替公证。
- [ ] 干净系统用户／机器双击安装，验证注册、登录恢复、网页保存、下拉框、启动、AI 和故障恢复。
- [ ] 其他平台如需发布，准备相应原生依赖和独立安装验证。

### 4.6 验证范围缺口

- [ ] 最新发布版创建的代表性完整数据库升级验证；已有测试不能替代完整升级。
- [ ] 完整约束、索引、唯一性、数据保留和两次迁移的交接记录。
- [ ] 如受影响，补 ClickHouse 独立日志路径；目前 AI 桥接夹具使用独立 SQLite 日志库。
- [ ] 部署启用 Turnstile／附加验证时的受信任窗口验收。
- [ ] 完整真实 worker 集成、安全检查点、执行器能力和落盘审查。
- [ ] 技术路径全部验收项的逐项证据及正式安装验收。

## 五、已经观察到的验证结果及边界

| 验证 | 已观察结果 | 不能推导出的结论 |
| --- | --- | --- |
| `node --test tests/*.test.mjs`，BelloBuddy 工程 | 最近已验证版本 53 项通过 | 不覆盖停工前新增的沟通草稿／网页源码 |
| `node scripts/build.mjs`，BelloBuddy 工程 | 最近已验证构建成功，422 个输入文件 | 沟通网页未纳入构建；没有因此产生正式新安装包 |
| 筛选网页构建 | Vue 类型检查及 Vite 构建通过，由 BelloBuddy 构建脚本执行 | 不证明职位下拉框、worker 或全部业务字段已执行 |
| `go test ./router -run '^TestBelloBuddyConfiguration$' -count=1 -v` | 真实三数据库及 Electron 认证／方案夹具通过 | 不替代完整发布版数据库升级、真实 MaxKB 和真实招聘任务 |
| SQLite | 3.50.4，通过当前配置／AI 桥接夹具 | 不代表其他方言已由 SQLite 证明 |
| MySQL | 8.4.11，通过当前配置／AI 桥接夹具 | 不代表完成所有最低版本及升级场景 |
| PostgreSQL | 16.15，通过当前配置／AI 桥接夹具 | 不代表完成所有最低版本及升级场景 |
| Electron | 39.2.7，HTTP／HTTPS Cookie、认证代理及实际方案 IPC 链路通过 | HTTPS 使用夹具证书，不能替代生产部署验收 |
| DPin 管理前端 | 此前相关测试、类型检查、lint、构建通过 | 暂停整理时没有重跑整套前端检查 |
| DPin 后端构建及相关测试 | 此前已通过受影响检查 | 没有声称整个仓库全部测试均通过 |
| MaxKB | 此前实际用量辅助和 Django 路径回归通过 | 当前测试应用长期记忆场景的完整用量尚未通过 |
| `git diff --check` | 最近 DPin、BelloBuddy 检查通过 | 不证明功能完整或安全符合性 |

三数据库回归使用的本机隔离实例：MySQL 端口 `13316`、PostgreSQL 端口 `15436`。连接配置只用于测试，不是用户部署连接，也不是生产账号。完整命令中的测试 DSN 从本机既有测试环境取得，本文不复制密码。

临时日志（系统可能清理，后续应重新验证）：

- [BelloBuddy 53 项测试](/tmp/bellobuddy-dpin-editor-tests.log)
- [BelloBuddy 最近构建](/tmp/bellobuddy-dpin-host-build.log)
- [三数据库与 Electron 联调](/tmp/dpin-bello-plan-editor-matrix.log)
- [真实云端筛选列表截图](/tmp/bellobuddy-dpin-cloud-filter.png)

认证实现按 [OWASP ASVS 5.0.0](https://owasp.org/projects/asvs)、[认证备忘单](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、[会话管理备忘单](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 和 [CSRF 备忘单](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html) 的适用控制推进。涉及 ASVS `6.2.8`、`6.3.1/6.3.4`、`6.5.1/6.5.5`、`7.2.1/7.2.4`、`7.4.1/7.4.2`、`8.3.1`；尚有完整任务入口、部署验证及发布缺口，**不宣称已经完成 ASVS 符合性验证**。

## 六、暂停时实际部署数据

2026-10-06 整理清单时，只读查询当前 `one-api.db`：

| 表／配置 | 实际数量／状态 |
| --- | --- |
| `bello_buddy_plans` | 3 |
| `bello_buddy_plan_sources` | 3 |
| `bello_buddy_accounts` | 0 |
| `bello_buddy_jobs` | 0 |
| `bello_buddy_job_bindings` | 0 |
| `bello_buddy_relay_identities` | 0 |
| 已启用渠道类型 | `43`、`64` 各 1 个；没有启用类型 `65` 的 MaxKB 渠道 |

因此当前真实部署已具备导入方案与部分模块能力，**没有实际招聘账号／职位／机器人绑定，也没有真实任务生成的托管中继身份**。不要把隔离测试夹具的数据视为当前部署已配置完成。

## 七、恢复开发时的建议顺序

1. 重新核对三处工作区、服务和本文施工状态；保留用户原有修改。不要重复导入或重新询问已经完成的管理员登录。
2. 先完成客户端产品登录替换，使主工作台、侧栏、设置与任务启动使用同一 DPin 认证状态，保留本地 BOSS 登录；验证恢复、退出和失效，不再把编辑器登录当作整体产品登录完成。
3. 补完 `plan-editors/communication` 的 App 和完整字段适配，复用既有无损 JSON、方案服务及 IPC；验证后再纳入构建和受控入口。
4. 完成账号／职位同步和云端选择，验证两个网页保存后可以在真实下拉框选中。
5. 整体接入统一认证宿主、daemon／worker 和任务启动配置；与移除静态兜底、旧认证旁路一起验收。
6. 完成本地执行器能力及 JEV 故障处理，确保故障不会误淘汰或继续发送。
7. 处理 MaxKB 长期记忆／额外调用用量问题，再配置真实渠道、定价和岗位绑定，验证会话与计费闭环。
8. 接入完整暂停／恢复、去重、落盘审查和数据库升级验证，再打包与安装验收。

目前变更保留在各工作区，**尚未提交、没有创建 PR，不能把现有 build 或旧 `.app` 当作最终交付**。本清单生成后不再继续目标工作，等待用户明确恢复。
