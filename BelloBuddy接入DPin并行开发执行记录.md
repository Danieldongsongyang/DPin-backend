# BelloBuddy 接入 DPin 并行开发执行记录

更新时间：2026-10-06（Asia/Shanghai）。用户已批准并行开发计划并授权自行创建 worktree、启动开发；本轮现按用户“记录下来没有完成的东西”的要求收口。当前记录包含第一轮启动、集成验证与任务结算证据，整体接入尚未完成。

## 源码基线

使用独立临时 Git index 保存源码树并创建本地提交；桌面原工程的 HEAD、真实 index 和工作文件保留原状。仅将已核对的接入源码及规划文档纳入基线；未纳入数据库、环境凭据、依赖和 Python 缓存。

- DPin：`92279ac15e2e8d7e622d925c3cfaf26a5edc1e98`，父提交 `77d12a68fb4af4ce5605422e65a09e4d114752fd`。
- BelloBuddy：`011991860e0aa122a3c25e4dda51f4fe17c3423e`，父提交 `6adabb6afed2129412ad11459d08be23363c62ca`。
- 两仓库基线引用：`bb/dpin-baseline-20261006`。
- MaxKB：沿用干净工作区的 `68b81ec`。

基线保留未完工沟通页面，不能视为新增构建或功能验收通过。

## 集成 worktree

| 工程 | 位置 | 分支 |
| --- | --- | --- |
| DPin | `/Users/a1/orca/workspaces/DPin-backend-newapi/dpin-bb-integration` | `Danieldongsongyang/dpin-bb-integration` |
| BelloBuddy | `/Users/a1/orca/workspaces/BelloBuddy/bb-dpin-integration` | `Danieldongsongyang/bb-dpin-integration` |

## 已启动任务

Orca Run：`run_f4602cb65957`。五次启动均返回 `state=ready`、输入接受及 `turn_started`；随后 worker-list 核对五个 agent 均为 `live/working`。采用用户已配置的 Codex 默认模型和推理配置，未覆盖模型参数。

| 任务 | worktree | Task | Dispatch | 启动时状态 |
| --- | --- | --- | --- | --- |
| A 产品认证 | `/Users/a1/orca/workspaces/BelloBuddy/bb-dpin-auth` | `task_d6f349f5d324` | `ctx_b4918e3911ee` | 已开始 |
| B 沟通编辑器 | `/Users/a1/orca/workspaces/BelloBuddy/bb-communication-editor` | `task_e3c4a54cce30` | `ctx_17217eaad36a` | 已开始 |
| C 职位配置 | `/Users/a1/orca/workspaces/BelloBuddy/bb-job-config` | `task_a03451630489` | `ctx_664f37c25df6` | 已开始 |
| D 任务通道 | `/Users/a1/orca/workspaces/BelloBuddy/bb-task-channel` | `task_25e40619a4d4` | `ctx_c2e9501a0b93` | 已开始 |
| M MaxKB 用量 | `/Users/a1/orca/workspaces/MaxKB-2/maxkb-complete-usage` | `task_c55a73455b87` | `ctx_db92471702df` | 已开始 |

启动命令使用 Orca 受监督 worker-start、明确仓库与基线、新子 worktree 和独立终端。跳过通用 setup hook，开发任务按各自实际模块安装依赖，避免使用仓库中不适用的默认 pnpm/npm 总入口。

## 协调边界

- 开发 agent 只写自己的 worktree；公共宿主、恢复产物、preload、总构建／打包由集成负责人修改。
- 不改桌面原工程代码，不重启或改配置当前真实 DPin、MaxKB、数据库和 BOSS 浏览器。
- 不关闭测试应用长期记忆；不发送真实招聘消息。
- 每个任务交付本地 commit、独立测试证据、根目录 HANDOFF 报告及共享文件具体接线要求。
- 启动后继续接收问题、状态与完成报告，合并和实际验证后才更新功能完成状态。

## 第一批集成代码与验证

认证 A 已完成本轮模块任务，提交 `492168a`、`b878f12` 和报告提交 `fd87139`；已合入 BelloBuddy 集成分支。接受匹配 Task／Dispatch 的 worker_done 后，已使用 worker-release 关闭该 worker 终端并保留 worktree 和输出归档。

集成负责人已实现：同一主进程认证宿主与产品任务许可、两个固定的隔离方案编辑窗口、主工作台 DPin 状态与账号页面，以及手动推荐、沟通、自动任务和批次创建入口的短期准入检查。短期检查不替代 D 的实际 worker 生命周期许可，尚未完成所有调度路径接入。

- `node scripts/build.mjs`：最新合入 A／C／D 及共享职位 IPC 后成功，437 个输入文件（当前仍仅构建筛选编辑器）。
- `node --test tests/*.test.mjs`：最新重新构建后 110 项通过、0 项失败。
- 包含新增主窗口任务准入测试：2 项通过，覆盖其他窗口、子框架、远程导航、等待期间退出／宿主关闭和后台身份存续。
- DPin `go test ./router -run '^TestBelloBuddyConfiguration$/sqlite$' -count=1 -v`：真实 SQLite 3.50.4 隔离数据库回归通过。未重验 MySQL、PostgreSQL 或版本升级矩阵。

### 系统级 Electron 回归未通过

首次错误地将 `/Applications/BelloBuddy.app/Contents/MacOS/BelloBuddy` 作为测试运行时，已安装程序忽略脚本而启动了自己的旧主程序，测试超时。它读到了既有用户目录并拉起独立 daemon；已核对启动时间与测试输出，向此次新建的 daemon PID 93852 发送 TERM，确认其及子进程退出。没有执行真实招聘任务，但不能把这次运行称为完全隔离。

随后从已有 Electron 40.10.2 缓存解压至随机 `/tmp/bb-dpin-electron.QDuyDw`，正确执行本地临时服务与临时 profile 的 HTTP／HTTPS 脚本；两项均因 macOS 钥匙串 `userCanceledErr (-128)` 在 safeStorage 可用性检查处失败。没有绕过系统授权、改用明文备份或修改产品加密要求。此项真实网络／安全存储整体回归仍未通过，不能据此声明编辑窗口真实网络验收完成。

本记录尚不包含真实 AI、三数据库升级、正式安装包或发布验收结论。

## 其他模块已复验的成果

### M MaxKB 请求级用量

本轮模块任务已交付；提交 `625fa18`、`d89b8df`、`8cef222` 位于 `/Users/a1/orca/workspaces/MaxKB-2/maxkb-complete-usage`。协调者独立运行 `.venv/bin/python -m unittest discover -s tests -p 'test_dpin*usage.py' -v`，20 项全部通过；实际测试使用源码 AST 与真实响应依赖、模型／数据库夹具，不是完整 Django 或实际模型端到端测试。接受匹配 worker_done 后已释放终端，保留 worktree 与输出归档。

主回复、问题优化、同步 ROUND 记忆已归集；SCHEDULED、知识向量、工具／MCP、工作流及流式等未证实消费者仍严格拒绝，原应用配置及非严格路径保留。尚未合到桌面原源码、同步 DPin 重放补丁工具或重启真实服务。SDK 隐藏重试、并发 ROUND 触发次数、完整 Django 与真实账务闭环仍待验证，参见该目录 `HANDOFF-M.md`。

### D 受控任务通道

本轮模块任务已交付，提交 `9deea37`、`a573e1f`、`71495ee` 已合入集成分支；接受 worker_done 后释放终端并保留 worktree／归档。协调者在 D worktree 独立运行 `node --test tests/dpin-task-channel.test.mjs`，14 项通过，包含真实隔离 daemon 子进程。覆盖配置获取前禁止 spawn、配置交付及初始化前禁止动作、身份失效、持久化剥离与配置密文绑定。共享恢复入口尚未接线；测试模块可用不代表实际招聘 worker 已使用这些控制。

**集成分支当前为开发中版本，不可直接替换现有 `.app`。** daemon 协议已升至 12，其受控登记要求已实现；恢复主入口尚未读 fd3 capability／登记 host 与 control，实际 worker 也尚未从 fd3 接受并安装云端配置。因此源码和模块测试通过不等于当前集成程序可正式启动招聘任务，必须先完成 `HANDOFF-D.md` 的共享接线。

### C 职位配置

本轮模块任务已交付，提交 `a67083f`、`2844d4f`、`ca31c3f` 已合入集成分支；接受 worker_done 后释放终端并保留 worktree／归档。协调者独立复跑新增 20 项测试，全部通过。覆盖云端有效候选、完整详情与关联筛选、单职／在线批量／重开、初次未配置、模式与下线限额、冲突、身份取消、旧 ID 映射和窗口权限。

共享宿主与公共 preload 的七方法职位能力已接入；新增实际共享接口测试覆盖白名单及退出取消、正常续期不误取消请求。原实际职位页的下拉框、单职保存、批量按钮及列表源仍未接线，部署来源映射未注入；参见 `HANDOFF-C.md`。这部分不能勾选为真实客户端职位链路完成。

## 本轮收口与资源结算

用户要求记录未完成项后，不再新增开发。A／C／D／M 已交付成果和测试证据保留，四个 worker 终端已释放，worktree 与归档未删除。最终查询本 Run 的 `terminal-state=reclaimable` 列表为空，资源计数为 `released:4`、`retained:1`。

B 沟通编辑器未交付最终实现／锁文件／构建证据，未合入集成分支。停止请求 `worker-stop --dispatch ctx_17217eaad36a` 返回 `stop_unknown`、`processAction=none`，原因是终端 `user_owned`；未强行关闭用户接管终端。随后已发送收口通知并执行 `worker-abandon`，派发状态为 `abandoned`，任务状态为 `blocked`、无交付结果；返回警告明确可能仍活跃的资源被保留，没有停止或删除进程。**派发终止不等于该终端进程已经停止。**

B 保留位置为 `/Users/a1/orca/workspaces/BelloBuddy/bb-communication-editor`，终端为 `term_1bc99051-9fa3-4747-bf00-3044922b79e1`。已提交契约 `b6aeafa`；收口采样中存在未提交的 `app/src/shared/dpin-communication-draft.mjs`、`plan-editors/communication/src/App.jsx`、`editor-session.mjs`、`styles.css` 和新文件 `field-catalog.mjs`。它们可能随用户接管终端继续变化，协调者没有提交、覆盖或合入这些草稿。后续接续应先检查当前差异，不按本文采样覆盖。

## 未完成项与下一次续接位置

| 模块 | 本轮交付范围 | 尚未完成 |
| --- | --- | --- |
| A／共享认证 | 生命周期、产品摘要、工作台／账号页面及部分主窗口入口准入 | 旧 onboarding／倍罗凭据 IPC／本地模式旁路替换，所有真实执行入口验证。 |
| B 沟通编辑器 | 契约及未完工草稿，未合入 | 完整交互及业务字段映射、保存／复制／冲突、锁文件与构建、总构建和真实编辑窗口验收。 |
| C 职位配置 | 服务、适配器、七方法 IPC 与许可生命周期 | 真实账号／职位同步、实际下拉框和单职／在线批量按钮、部署来源映射、重开和冲突界面。 |
| D 任务通道 | 协议 12、配置封装、许可与调度持久化模块 | 主程序 fd3 host/control 登记、真实 worker 配置安装／激活、所有调度入口和实际 BOSS 动作的共同准入。当前集成版本为 WIP。 |
| E／F 执行与恢复 | 本轮未派发 | 真实 JEV／DPin AI、规则优先级、故障语义、发送许可／幂等、暂停／恢复／退避／去重和静态兜底清理。 |
| M 用量与部署 | 请求级主回复／问题优化／同步 ROUND 归集及严格拒绝 | DPin 补丁工具同步、完整 Django／真实模型多轮、隐藏重试／并发 ROUND、真实渠道／定价／绑定／扣费退款闭环。 |
| 验证／发布 | 110 项源码测试、437 输入构建、SQLite 配置回归；M 20 项模块测试 | Electron safeStorage 系统授权回归、最新三数据库升级矩阵、正式资源打包签名公证／安装及完整端到端验收。 |

建议下次先接 D 的共享恢复入口并补齐旧认证入口，再完成 B／C 真实页面，随后推进 E／F 与 M 真实部署联调，最后做系统验证和正式发布。跨账号本地资料归属、断连阈值／持续上游故障策略及恢复配置刷新仍需用户决策。

续接代码锚点：BelloBuddy 集成源码提交 `0172bfe13fe137036d7731b4175908872e1b398c`（其后交接文档提交不改变此源码锚点）；MaxKB 交付提交 `8cef2220ee63b1a848429122466df7d1ff1ef31a`。共享接线细节见集成根目录的 `HANDOFF-A.md`、`HANDOFF-C.md`、`HANDOFF-D.md`、`HANDOFF-INTEGRATION.md` 及 MaxKB 的 `HANDOFF-M.md`。

本轮没有覆盖桌面原工程、重启／改配真实服务、替换安装包、推送代码或创建 PR。测试通过与模块交付不能等同于整个产品接入完成。
