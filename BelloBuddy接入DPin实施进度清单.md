# BelloBuddy 接入 DPin 实施进度清单

更新时间：2026-10-06（并行开发收口后修订）。状态：**本轮已收口；A／C／D 模块已合入集成分支，M 已独立交付，B 未完成；整体接入与正式发布未完成**。

本轮记录：已保存不改变桌面原分支／暂存区的独立源码基线，创建两个集成 worktree 和 A／B／C／D／M 五个开发 worktree。四个模块完成交付，沟通编辑器保留未完工草稿；E／F 尚未派发。任务范围见 [并行开发计划](BelloBuddy接入DPin并行开发计划.md)，提交、测试和派发结算见 [并行开发执行记录](BelloBuddy接入DPin并行开发执行记录.md)。本次修订只更新清单，不恢复开发、不重启服务、不重新运行业务测试。

本清单以 [技术实现路径](/Users/a1/Desktop/DPin-backend-newapi/BelloBuddy接入DPin技术实现路径.md) 为主，[需求文档](/Users/a1/Desktop/DPin-backend-newapi/BelloBuddy接入DPin需求文档.md) 为参考。明确标注为“历史”的段落仅保留此前证据；其他施工状态按本轮收口更新。本轮新成果位于独立 worktree，尚未覆盖桌面源码或已安装客户端。

标记说明：`[x]` 表示对应范围已经实现并有验证；`[ ]` 表示未完成或尚未获得足够验证。**模块验证通过不代表整个客户端任务链路已经打通。**

### 本轮并行集成补充（2026-10-06）

- [x] 自行创建两个集成 worktree 和 A／B／C／D／M 五个开发 worktree，保留桌面未提交源码作为独立基线，不改原 HEAD／暂存区。
- [x] A 产品认证生命周期与内存任务许可交付并合入：退出、失效、身份切换和宿主关闭撤销旧许可；renderer 只接收版本化显示摘要。
- [x] 集成分支的主工作台状态、设置账号卡片和产品账号页面改用同一 DPin 状态；固定沟通／筛选编辑窗口及五个手动启动／批次创建 IPC 已增加主窗口准入代码。
- [x] A／C／D 模块和共享接线代码已合入 BelloBuddy 集成分支；最新构建成功，437 个输入文件；完整源码测试 110 项通过、0 项失败。M 在独立 MaxKB worktree 交付，20 项用量测试通过。
- [ ] 旧登录／onboarding 路径全部替换、所有调度与实际 worker 生命周期接通；不能仅靠按钮短期准入认定完成。
- [ ] 本轮完整 Electron HTTP／HTTPS 网络与 safeStorage 回归：开发 Electron 40.10.2 被钥匙串授权拒绝（`userCanceledErr`），未放宽加密要求。
- [ ] B 沟通编辑器未完成最终交付，未合入集成分支或总构建；草稿保留在独立 worktree。该终端已被用户接管，停止请求没有关闭进程；本轮受监督派发已终止，不继续协调开发。

### 本轮收口：未完成续接清单（最新状态）

以下仅记录缺口，不表示获准继续开发。详细提交、测试边界和 worker 结算见并行开发执行记录。

| 优先顺序 | 未完成工作 | 完成判据／注意事项 |
| --- | --- | --- |
| 1 | D 的共享恢复入口与真实 worker 接线 | 主程序读取 fd3 能力并登记 host/control；真实 worker 安装云端配置并受许可控制。当前 daemon 已强制协议 12，集成分支仍是 WIP，不可替换现有客户端。 |
| 2 | 全部产品认证入口统一 | 清理旧 onboarding、倍罗登录／凭据 IPC 和本地模式旁路；所有手动、批次、定时、重试、恢复入口验证同一身份生命周期。 |
| 3 | B 沟通编辑器 | 补全字段双向适配、列表／详情／私人保存／复制／版本冲突及内存草稿；交付锁文件、冻结依赖构建和测试，纳入总构建及受控窗口。保留用户接管终端中的草稿，不覆盖。 |
| 4 | C 实际职位页面 | 接账号／职位云端同步、真实双方案下拉框、单职保存与上方确认后的在线批量保存；注入绑定部署 origin 的来源映射，不写死旧方案 ID；验证重开与冲突提示。 |
| 5 | E／F 实际执行与恢复 | 尚未派发。接 JEV／DPin AI、本地规则优先级、故障跳过而非误淘汰、发送前状态许可与幂等回执、暂停／恢复／退避和任务去重；移除静态配置及 renderer 副本兜底。 |
| 6 | M 部署与真实计费闭环 | 同步 DPin 的 MaxKB 重放补丁工具；完整 Django 与真实应用多轮／ROUND 用量验证，核对隐藏重试及并发触发；真实渠道 65、定价、职位绑定和扣费／退款／余额闭环。不得关闭长期记忆来规避用量。 |
| 7 | 系统回归与正式发布 | 解决具备用户系统授权条件下的 safeStorage 回归；三数据库新建／升级及重复启动校验；正式资源打包、签名／公证和干净安装验收。本轮仅重验 SQLite 配置回归，历史验证不算本轮重验。 |

另需用户决策：跨 DPin 账号的本地招聘资料归属、断连阈值／持续上游故障策略及恢复时配置刷新策略。未擅自迁移旧资料或调整应用长期记忆配置。

A／C／D／M 四个已交付 worker 终端已释放；B 为 `abandoned` 派发、`user_owned` 保留终端，**未确认进程停止**。本轮没有覆盖桌面原源码、重启真实服务、安装新 `.app`、推送代码或创建 PR。

以下左下角修复记录为本轮并行开发之前的历史证据；最新模块状态见第 2.6、第三、第四节，最新验证见第五节。源码有新成果不意味着正式安装包和当前运行服务已更新。

### 历史记录：客户端左下角账号修复（2026-10-06，并行开发之前）

**BelloBuddy 产品登录接入尚未整体完成。** 历史更正时的用户工作台截图中，BOSS 直聘账号已登录，但产品账号仍显示“未登录”，工作台仍提示登录倍罗账号。随后用户明确授权先修复左下角账号显示；当时局部修复结果记录如下，不代表本轮最终施工范围。

- [x] 左下角账号区改为读取主进程 DPin 认证代理的用户摘要；重启开发版后通过真实会话续期恢复，界面显示实际用户名及“DPin · 已登录”。
- [x] 左下角未登录点击入口及账号菜单使用 DPin 登录；退出调用同一代理，通知侧栏更新。仅返回用户 ID、用户名、状态和变更序号，不返回认证凭据；迟到状态不覆盖较新的显示。
- [x] 新增账号 IPC 回归覆盖其他窗口、子框架、远程页面及额外参数拒绝，登录恢复、用户摘要字段白名单、退出本机状态与远端撤销未确认、导航后取消交付。当时 54 项测试全部通过；构建成功，423 个输入文件。
- [x] 后续并行集成已将主工作台状态、设置账号卡片及产品账号页面接入 DPin 摘要，并增加五个主窗口任务 IPC 准入检查；成果只在集成 worktree。
- [ ] 全部任务执行入口统一接入 DPin 认证生命周期；五个 IPC 的短期检查不能替代实际 worker 许可。
- [ ] 替换旧 `bello-login`、倍罗凭据检查和退出路径，移除本地模式跳过产品认证的旁路；保留本地 BOSS 账号登录与浏览器会话。
- [ ] 验证登录、重启恢复、退出及失效在主界面和任务入口中的一致行为。

历史验证日志：[54 项测试](/tmp/bellobuddy-dpin-account-full-tests.log)、[构建记录](/tmp/bellobuddy-dpin-account-build.log)、[实际侧栏截图](/tmp/bellobuddy-dpin-account-sidebar.png)。当时打开的是更新后的开发构建，不能据此判断现在的运行程序。旧 `.app` 未重新打包；当时没有实际退出用户的 DPin 会话，退出与导航取消场景使用隔离夹具验证。

认证参考：沿用 [OWASP ASVS 5.0.0](https://owasp.org/projects/asvs) 中现有认证代理对应的 `7.2.1/7.2.4`、`7.4.1/7.4.2` 控制范围，并核对[认证备忘单](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)和[会话管理备忘单](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)。本次侧栏依据代理验证后的状态显示，凭据不交付页面，窗口调用由主进程强制校验；未修改服务端认证与刷新策略。整体任务认证、生产部署及完整 ASVS 验收仍未完成。

“设置 → 筛选方案”打开本地编辑器属于已确认需求；历史上该入口触发 DPin 登录窗口只证明编辑器认证接线，不能当作产品登录替换完成的证据。主工作台保留原有产品结构，DPin 承接产品账号认证与云端能力，不以 DPin 管理后台替代工作台。

后续并行开发已扩展认证与任务通道模块，尚未完成真实执行接线；本轮已收口，恢复顺序以文首及第七节为准。

## 一、工程和服务位置

| 对象 | 位置／地址 | 当前用途 |
| --- | --- | --- |
| DPin 项目 | `/Users/a1/Desktop/DPin-backend-newapi` | 配置服务、认证、计费、AI 桥接、管理后台 |
| DPin 后端 | `http://localhost:3000` | 用户指定的本地 Go 服务地址；此前曾重启，本轮未重启或确认当前运行状态 |
| DPin 前端 | `http://localhost:5173` | 既有管理后台及受信任登录网页 |
| DPin 本地数据库 | `/Users/a1/Desktop/DPin-backend-newapi/one-api.db` | 历史部署使用的 SQLite 数据库；本次文档更新未查询 |
| BelloBuddy 恢复工程 | `/Users/a1/Desktop/bosscli/recovery/BelloBuddy` | 实际客户端源码、执行包和构建脚本 |
| 实际客户端前端 | `BelloBuddy/app/src/renderer` | 正式路由改动必须落到这里 |
| 筛选编辑器规范源码 | `BelloBuddy/plan-editors/filter` | 已纳入 BelloBuddy 构建 |
| 原筛选 Vue 原型 | `/Users/a1/Desktop/bosscli/recovery/vue-frontend-dpin` | 相关页面已改为引用规范源码 |
| 原沟通 React 原型 | `/Users/a1/Desktop/bosscli/communication-plan-editor-clone` | 原设计来源；本轮没有修改原型工程 |
| 沟通编辑器施工目录 | `/Users/a1/orca/workspaces/BelloBuddy/bb-communication-editor/plan-editors/communication` | B 的未完工草稿，未交付／未合入；集成已有受控窗口入口代码，但沟通网页未构建 |
| MaxKB 源码 | `/Users/a1/Desktop/MaxKB-2` | 用户授权操作的本机源码工程 |
| MaxKB 管理前端 | `http://127.0.0.1:3010/admin/` | 源码前端；历史曾验证 `/admin/home`，当前状态需恢复时确认 |
| MaxKB 源码后端 | `http://127.0.0.1:8080` | DPin 应连接的本机后端 API 地址 |
| 筛选编辑器开发预览 | `http://127.0.0.1:5185/dpin-filter-editor.html` | 普通浏览器没有 Electron IPC，因此不能代替真实云端联调窗口 |
| BelloBuddy 集成 | `/Users/a1/orca/workspaces/BelloBuddy/bb-dpin-integration` | A／C／D 与共享接线成果；源码锚点 `0172bfe`，交接提交 `208ed67`；仍为 WIP |
| DPin 集成 | `/Users/a1/orca/workspaces/DPin-backend-newapi/dpin-bb-integration` | 本轮隔离配置回归位置；未覆盖桌面原工程 |
| MaxKB 用量交付 | `/Users/a1/orca/workspaces/MaxKB-2/maxkb-complete-usage` | M 的独立交付，提交 `8cef222`；未同步真实服务及 DPin 部署补丁工具 |

用户表示旧 Docker MaxKB 后续自行删除。**本次没有删除旧容器、数据库或数据。** 服务是否仍运行，恢复开发时应重新检查；不能只依赖本文地址或此前的进程号。

## 二、已经完成并验证的工作（注明源码与部署边界）

第 2.1—2.5 节保留本轮之前的后端、认证、方案导入与筛选编辑器证据；本轮新增交付见第 2.6 节。历史成功不代表本轮重验或当前部署已更新。

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

**这些回归使用隔离数据库和受控上游夹具。历史部署快照没有启用 MaxKB 渠道；本轮未配置真实渠道，也没有完成真实招聘职位到 MaxKB 的消费闭环。本次文档更新未查询当前部署。**

主要文件：[AI 桥接服务](/Users/a1/Desktop/DPin-backend-newapi/service/bellobuddy_ai.go)、[AI 控制器](/Users/a1/Desktop/DPin-backend-newapi/controller/bellobuddy_ai.go)、[MaxKB 适配器](/Users/a1/Desktop/DPin-backend-newapi/relay/channel/maxkb)、[回归测试](/Users/a1/Desktop/DPin-backend-newapi/router/bellobuddy_test.go)。

### 2.3 Electron 产品认证代理

- [x] 使用 Electron `net` 和专用内存 Cookie 分区，复用 DPin 产品会话。
- [x] 受信任 DPin 网页登录，页面不向 renderer／worker 分发认证 Token。
- [x] 使用系统 `safeStorage` 加密刷新凭据，备份权限为 `0600`。
- [x] 启动恢复通过真实续期和用户信息校验判断登录有效性。
- [x] 并发续期合并、Cookie 轮换、30 秒竞争契约、未知凭据拒绝及超窗重放撤销。
- [x] 退出取消在途操作，旧异步响应不能恢复登录。
- [x] 历史 Electron 39.2.7 的 HTTP 本机和 HTTPS 夹具分别验证 Cookie、Secure、Origin 及认证网络行为；本轮 Electron 40.10.2 回归未通过，见第五节。
- [x] 已通过真实管理员登录连接当前本机 DPin 并执行方案导入。
- [x] 当前本机 `/api/status` 曾核实 Turnstile 未启用；没有添加客户端豁免。

**侧栏用户摘要及其登录／退出菜单已接入该代理；本轮集成分支已扩展到主工作台、设置账号页及部分任务 IPC。全部执行入口、daemon 和真实 worker 尚未整体接通，旧产品认证／开发旁路没有整体移除，正式安装包没有更新。启用 Turnstile 或附加验证的部署验收仍待进行。**

主要文件：[认证代理](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/dpin-auth-proxy.cjs)、[本机登录／导入工具](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/scripts/dpin-local-connect.cjs)、[Electron 网络回归工具](/Users/a1/Desktop/DPin-backend-newapi/tools/bellobuddy/electron-auth-spike.cjs)。

### 2.4 现有有效方案导入

- [x] 核对并导入当前有效登记的完整方案。
- [x] 核对导入正文及关联筛选方案一致性。
- [x] 重复导入验证通过。
- [x] 历史暂停整理时从 SQLite 只读核实以下 3 个方案、3 条来源映射；本次未重新查询。

| 类型 | 名称 | 新 DPin ID | 版本 | 外层筛选关联 | 快照权限 |
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

### 2.6 本轮并行交付与集成（仅本地开发成果）

- [x] A：产品任务许可绑定服务 origin、用户及身份世代；退出、失效、身份切换和宿主关闭撤销许可；显示摘要以 revision 拒绝迟到更新。
- [x] 共享宿主：同一认证代理、固定隔离方案窗口、主工作台与账号入口、五个主窗口任务 IPC 的调用者及异步准入检查。
- [x] C：云端职位服务与适配器、专用职位 IPC、公共 preload 固定七方法；单职／确认后的在线批量保存、初次未配置拒绝、模式独立、版本冲突与 origin 来源映射模块。
- [x] C 共享接线：职位能力已注册到认证宿主；正常同身份续期不取消请求，退出／身份变化拒绝迟到数据。实际页面和部署来源映射尚未接入。
- [x] D：协议 12 受控通道、fd3 能力、登记校验、内存配置密文封装、配置／初始化前禁止动作、短期单次动作许可、调度持久化白名单与迁移模块。
- [x] M：请求级主回复、问题优化、同步 ROUND 记忆用量归集；缺失／非法／重复用量拒绝可发送回复，非严格路径与应用长期记忆配置保持原状。
- [ ] B：未完成最终交付，沟通页面不在集成构建中；不将契约测试或未提交草稿记为页面完成。
- [ ] C／D／M：真实页面／执行入口／部署与端到端验证未完成，详见第三、第四节。

交接：[A 认证](/Users/a1/orca/workspaces/BelloBuddy/bb-dpin-integration/HANDOFF-A.md)、[C 职位配置](/Users/a1/orca/workspaces/BelloBuddy/bb-dpin-integration/HANDOFF-C.md)、[D 任务通道](/Users/a1/orca/workspaces/BelloBuddy/bb-dpin-integration/HANDOFF-D.md)、[M 完整用量](/Users/a1/orca/workspaces/MaxKB-2/maxkb-complete-usage/HANDOFF-M.md)、[集成记录](/Users/a1/orca/workspaces/BelloBuddy/bb-dpin-integration/HANDOFF-INTEGRATION.md)。

## 三、部分完成，但还不能视为打通

### 3.1 MaxKB 真实用量二次开发

- [x] 历史已在用户授权的 `/Users/a1/Desktop/MaxKB-2` 源码应用严格实际用量补丁；本轮完整归集实现另保存在 M worktree，未覆盖该原目录。
- [x] 从当前响应的 `usage_metadata`／`response_metadata.token_usage` 读取真实数量，不使用共享状态或估算兜底。
- [x] 校验整数、非负、上限、总量及不同来源的一致性。
- [x] 缺失实际用量时返回独立失败，避免伪造成功回复。
- [x] 历史补丁工具、辅助回归及此前的 Django 路径回归已运行；它们不证明本轮请求级归集已完成部署。
- [x] 本轮主回复、问题优化、同步 ROUND 记忆调用用量已归集；20 项独立模块测试通过。
- [x] SCHEDULED、知识向量、流式、工具／MCP／工作流等尚未证实消费者继续严格拒绝，不把主回复用量伪称完整请求用量。
- [ ] 同步 DPin `tools/maxkb` 的补丁应用／验证工具；旧四文件重放工具不代表新版完整用量部署工具。
- [ ] 本轮完整 Django 启动与真实模型验证；当前 20 项测试使用源码 AST、真实响应依赖和模型／数据库夹具。
- [ ] SDK 隐藏重试／缓存及并发 ROUND 触发次数的实际用量边界验证。
- [ ] 当前测试应用的完整真实用量验证。
- [ ] 两轮真实回复、同会话延续、DPin 消费日志及余额变化闭环。

历史已定位测试应用“测试boss”，ID 为 `01a0dbc2-4722-7681-9fbc-4ddf277cbbd6`。此前检查发现草稿长期记忆已开启，发布版配置与草稿不同；记忆提取可能触发额外模型调用。曾观察到主回复的真实 Token 数量，**不构成真实应用的长期记忆等额外调用已经记账的证据**。本轮归集实现尚未部署或用该真实应用验收，本次文档更新没有重新读取应用配置。

恢复后保持应用长期记忆，继续部署并验证完整用量，不以关闭长期记忆规避问题；如需调整应用配置，应先取得用户明确指令。本轮没有修改用户应用配置。

源码补丁涉及 `apps/common/handle/dpin_actual_usage.py`、`apps/application/chat_pipeline/step/chat_step/impl/base_chat_step.py`、`apps/common/handle/impl/response/openai_to_response.py`、`apps/chat/serializers/chat.py`。

待同步工具：[应用补丁](/Users/a1/Desktop/DPin-backend-newapi/tools/maxkb/apply-strict-usage.py)、[真实联调验证器](/Users/a1/Desktop/DPin-backend-newapi/tools/maxkb/verify-strict-usage.py)。本轮实现及边界见 [HANDOFF-M](/Users/a1/orca/workspaces/MaxKB-2/maxkb-complete-usage/HANDOFF-M.md)。MaxKB 的本地环境文件和应用 Key 含秘密，恢复工作时在本机安全读取，不复制到本文、聊天或命令参数。

### 3.2 客户端任务网关与会话存储

- [x] 任务网关模块读取、校验并冻结云端完整配置。
- [x] 任务清单只返回必要元数据，不包含方案正文或认证凭据。
- [x] JEV 故障结果不带 `pass/level`，回复故障不带可发送文本。
- [x] 同会话请求串行；按身份和绑定版本隔离。
- [x] 请求前保存不确定状态，网络异常等情况下阻止盲目重复调用。
- [x] 会话引用／不确定状态使用显式持久化白名单及原子文件保存。
- [x] 模块回归覆盖退出、迟到响应、绑定变化、重启及存储失败。
- [x] 本轮 D 已交付受控 daemon 通道及许可模块，14 项独立测试通过，包含真实隔离 daemon 子进程；源码已合入集成分支。
- [ ] 恢复主程序读取 fd3 能力并登记 host/control，真实 worker 接受、安装和激活云端配置；网关／会话存储的实际执行消费者尚未打通。
- [ ] 当前真实招聘任务仍未证明使用上述云端配置及 AI 网关。

**当前集成分支仍为 WIP，不可直接替换现有 `.app`：daemon 已强制协议 12 登记，主程序与真实 worker 尚未接线。不能为了暂时可运行而放开旧通道。**

主要文件：[任务网关](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/dpin-task-gateway.cjs)、[会话存储](/Users/a1/Desktop/bosscli/recovery/BelloBuddy/app/src/main/dpin-conversation-store.cjs)。

### 3.3 沟通编辑器：未交付草稿

- [x] 阅读原 React 网页及两份真实沟通 JSON，核对技术路径中的字段分组。
- [x] B 已保存契约提交 `b6aeafa`；此前 3 项 session 契约测试通过，但不覆盖完整页面交付。
- [x] 集成负责人已增加固定沟通窗口宿主和客户端路由入口代码；页面未构建，因此不能视为可用功能。
- [ ] `dpin-communication-draft.mjs`、`App.jsx`、`editor-session.mjs`、`styles.css`、`field-catalog.mjs` 存在未提交施工草稿；完整字段适配和页面行为未获得最终交付证据。
- [ ] 依赖锁文件、冻结安装、完整页面构建及测试尚未交付；沟通网页未纳入 `scripts/build.mjs` 和正式包。
- [ ] 列表／详情／新建／复制／私人保存／版本冲突／取消与内存草稿流程尚未完成验收。
- [ ] 用户接管终端中的代码可能继续变化；恢复时先核对实际差异，再评审与补完，不能按旧草稿描述直接覆盖。

草稿保留在 `/Users/a1/orca/workspaces/BelloBuddy/bb-communication-editor`。本轮监督派发已 `abandoned`，终端 `user_owned` 保留，停止请求没有关闭进程，**未确认进程停止**。最新 110 项集成测试及 437 个输入文件构建均不构成该独立草稿页面已完成／可保存的证明；没有合入这些未交付草稿。

### 3.4 职位配置：模块和共享 IPC 已完成，实际页面未接线

- [x] C 新增 20 项测试通过；职位服务、适配器、受控 IPC 和公共 preload 已合入。
- [x] 模块实现单职保存／确认时取在线集合的批量保存、初次双方案必选、模式独立、下线限额、冲突和身份取消。
- [ ] 实际职位页接入云端账号／职位同步、下拉框列表源、单职保存和上方批量确认按钮。
- [ ] 部署来源映射注入与旧方案 ID 迁移／明确重选；云端方案标识不得与本地旧 ID 混淆。
- [ ] 真实页面验证保存后可选、重开回显、初次未配置拒绝、版本冲突提示和多职位行为。
- [ ] 后端当前没有账号级 `expected_version` 契约，无法原子覆盖读取在线集合与提交之间新增的在线职位；不能宣称这一竞态已解决，详见 C 交接。

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
- [x] 受控本地通信／许可／配置封装模块已实现并测试；尚不包含真实恢复主程序和 worker 接线。
- [ ] 认证宿主与 daemon／真实 worker 完成登记、配置安装及生命周期集成。
- [ ] 启动获取完整云端配置，只在任务内存中注入给 worker。
- [ ] 移除正式运行路径的静态沟通 JSON、静态筛选登记及 renderer 内嵌副本。
- [ ] 移除旧本地凭据和开发旁路对产品任务的放行。
- [x] D 调度持久化白名单、schema v2 迁移和配置密文封装的模块测试通过。
- [ ] 对真实执行链路的 workerSpec、环境变量、参数、日志及诊断做完整落盘审查，确认不包含正文或认证凭据。
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

### 5.1 本轮最新验证（并行开发收口版本）

| 验证 | 本轮已观察结果 | 范围／限制 |
| --- | --- | --- |
| BelloBuddy `node --test tests/*.test.mjs` | 110 项通过，0 失败 | 集成分支源码与夹具；不覆盖 B 未交付页面，不证明真实任务打通 |
| BelloBuddy `node scripts/build.mjs` | 成功，437 个输入文件 | 仅筛选网页纳入构建，未产出正式新安装包 |
| C 新增职位模块测试 | 20 项通过，已独立复跑 | 实际职位页面、部署来源映射与真实同步未验收；这些测试包含于集成测试，不额外相加 |
| D 通道模块测试 | 14 项通过，已独立复跑，含真实隔离 daemon | 真实主程序 fd3 登记与 worker 配置消费者尚未接线；包含于集成测试，不额外相加 |
| M `.venv/bin/python -m unittest discover -s tests -p 'test_dpin*usage.py' -v` | 20 项通过，已独立复跑 | MaxKB 独立 worktree；AST、真实响应依赖与模型／数据库夹具，非完整 Django／真实模型验收 |
| DPin `go test ./router -run '^TestBelloBuddyConfiguration$/sqlite$' -count=1 -v` | SQLite 3.50.4 隔离配置回归通过 | 未重验 MySQL／PostgreSQL／最新发布版完整升级矩阵；受控上游不是真实业务闭环 |
| Electron 40.10.2 HTTP／HTTPS 认证脚本 | 两项均未通过：safeStorage 被钥匙串 `userCanceledErr (-128)` 拒绝 | 未绕过系统授权或降为明文；须具备系统授权后重新验证 |

Electron 首次测试错误地调用已安装的 BelloBuddy 可执行文件，旧主程序忽略脚本并读到既有用户目录、启动独立 daemon，测试超时；该次新启动 daemon 及子进程已精确停止，没有执行真实招聘任务。随后开发 Electron 才正确使用临时服务／profile 执行脚本，但被钥匙串拒绝。完整经过见并行执行记录；不能把首次运行称为完全隔离，亦不能宣称本轮系统级认证回归通过。

### 5.2 历史验证（保留证据，不代表本轮重验）

| 验证 | 已观察结果 | 不能推导出的结论 |
| --- | --- | --- |
| `node --test tests/*.test.mjs`，原 BelloBuddy 工程 | 历史 53 项通过；随后左下角修复版本 54 项通过 | 非本轮最新集成测试，不覆盖 B 未交付页面 |
| `node scripts/build.mjs`，原 BelloBuddy 工程 | 历史 422 个输入文件；随后侧栏修复版本 423 个 | 最新集成为 437 个，所有这些构建均不代表正式新安装包 |
| 筛选网页构建 | Vue 类型检查及 Vite 构建通过，由 BelloBuddy 构建脚本执行 | 不证明职位下拉框、worker 或全部业务字段已执行 |
| `go test ./router -run '^TestBelloBuddyConfiguration$' -count=1 -v` | 真实三数据库及 Electron 认证／方案夹具通过 | 不替代完整发布版数据库升级、真实 MaxKB 和真实招聘任务 |
| SQLite | 历史 3.50.4 配置／AI 桥接夹具通过；本轮配置回归见 5.1 | 不代表其他方言已由 SQLite 证明 |
| MySQL | 历史 8.4.11 配置／AI 桥接夹具通过，本轮未重验 | 不代表完成所有最低版本及升级场景 |
| PostgreSQL | 历史 16.15 配置／AI 桥接夹具通过，本轮未重验 | 不代表完成所有最低版本及升级场景 |
| Electron | 39.2.7，HTTP／HTTPS Cookie、认证代理及实际方案 IPC 链路通过 | HTTPS 使用夹具证书，不能替代生产部署验收 |
| DPin 管理前端 | 此前相关测试、类型检查、lint、构建通过 | 暂停整理时没有重跑整套前端检查 |
| DPin 后端构建及相关测试 | 此前已通过受影响检查 | 没有声称整个仓库全部测试均通过 |
| MaxKB | 此前实际用量辅助和 Django 路径回归通过 | 当前测试应用长期记忆场景的完整用量尚未通过 |
| `git diff --check` | 最近 DPin、BelloBuddy 检查通过 | 不证明功能完整或安全符合性 |

三数据库回归使用的本机隔离实例：MySQL 端口 `13316`、PostgreSQL 端口 `15436`。连接配置只用于测试，不是用户部署连接，也不是生产账号。完整命令中的测试 DSN 从本机既有测试环境取得，本文不复制密码。

历史临时日志（系统可能清理，不能当作本轮 110 项测试／437 输入构建的日志）：

- [BelloBuddy 53 项测试](/tmp/bellobuddy-dpin-editor-tests.log)
- [BelloBuddy 历史构建](/tmp/bellobuddy-dpin-host-build.log)
- [三数据库与 Electron 联调](/tmp/dpin-bello-plan-editor-matrix.log)
- [真实云端筛选列表截图](/tmp/bellobuddy-dpin-cloud-filter.png)

认证实现按 [OWASP ASVS 5.0.0](https://owasp.org/projects/asvs)、[认证备忘单](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、[会话管理备忘单](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) 和 [CSRF 备忘单](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html) 的适用控制推进。涉及 ASVS `6.2.8`、`6.3.1/6.3.4`、`6.5.1/6.5.5`、`7.2.1/7.2.4`、`7.4.1/7.4.2`、`8.3.1`；尚有完整任务入口、部署验证及发布缺口，**不宣称已经完成 ASVS 符合性验证**。

## 六、历史部署快照（本轮未重新查询）

2026-10-06 早前整理暂停清单时，曾只读查询桌面 `one-api.db`。以下是当时快照，不是本次文档更新的实时数据：

| 表／配置 | 实际数量／状态 |
| --- | --- |
| `bello_buddy_plans` | 3 |
| `bello_buddy_plan_sources` | 3 |
| `bello_buddy_accounts` | 0 |
| `bello_buddy_jobs` | 0 |
| `bello_buddy_job_bindings` | 0 |
| `bello_buddy_relay_identities` | 0 |
| 已启用渠道类型 | `43`、`64` 各 1 个；没有启用类型 `65` 的 MaxKB 渠道 |

当时真实部署已具备导入方案与部分模块能力，但**没有实际招聘账号／职位／机器人绑定，也没有真实任务生成的托管中继身份**。本轮未进行真实配置部署或业务验收；恢复时须只读核对当前数据，不把历史快照或隔离测试夹具视为部署完成证据。

## 七、恢复开发时的建议顺序

1. 核对现有集成／开发 worktree 与服务；保留用户修改，特别是 B 用户接管终端中的草稿。以 BelloBuddy 源码 `0172bfe`、交接 `208ed67` 和 MaxKB `8cef222` 为本轮续接锚点，不重复创建已存在的 worktree 或重复导入。
2. 优先完成 D 的恢复主程序 fd3 host/control 登记、真实 worker 配置安装／激活，解除当前协议 12 WIP 的实际启动缺口；不放行旧通道。
3. 清理旧 onboarding／倍罗登录／凭据 IPC 和本地模式旁路，统一全部手动、批次、定时、重试、恢复任务入口；保留本地 BOSS 登录，验证 DPin 恢复／退出／失效。
4. 补完 B 的完整沟通页面与字段适配、锁文件和冻结构建，验证后纳入总构建及受控窗口。
5. 接 C 的真实账号／职位同步、双方案下拉框、单职保存、上方确认后的在线批量事务与 origin 来源映射，验证保存后可选、重开和冲突。
6. 开展 E／F 真实执行与恢复：JEV／DPin AI、规则优先级、故障不误淘汰、发送前许可与幂等、暂停／恢复／退避／去重；移除静态配置兜底并做真实落盘审查。
7. 同步 M 的 DPin 部署补丁工具，保持长期记忆，验证完整 Django／真实多轮及 ROUND 用量；按用户授权配置真实渠道、定价和岗位绑定，验证扣费／退款／额度耗尽闭环。
8. 具备系统授权后重跑 Electron safeStorage 回归，完成三数据库新建／升级／重复迁移矩阵、正式资源打包／签名公证和干净安装验收。

本节与文首“本轮收口”清单为同一最新续接顺序。历史桌面源码修改仍保留原状；本轮模块与集成代码已保存为各独立 worktree 的本地提交，未推送、没有创建 PR。**不能把现有 build 或旧 `.app` 当作最终交付。** 本轮已按用户要求结束协调开发；本次仅更新清单，等待用户明确恢复开发。
