# BelloBuddy 账号登录系统重构计划

> 目标：以 **DPin-backend-newapi（下称 new-api）** 为唯一账号主体系，重构 BelloBuddy 桌面端的账号登录系统。
>
> 编写日期：2026-10-01
>
> 依据：两个项目的实际代码走读（关键文件与行为均已在本文标注出处），未经验证的猜测已标注为「待确认」。

---

## 目录

1. [背景与目标](#1-背景与目标)
2. [现状分析](#2-现状分析)
3. [差异对照](#3-差异对照)
4. [目标架构](#4-目标架构)
5. [分阶段实施计划](#5-分阶段实施计划)
6. [接口与 IPC 映射表](#6-接口与-ipC-映射表)
7. [数据迁移与兼容策略](#7-数据迁移与兼容策略)
8. [安全合规清单](#8-安全合规清单)
9. [风险与开放问题](#9-风险与开放问题)
10. [里程碑与验收标准](#10-里程碑与验收标准)

---

## 1. 背景与目标

### 1.1 背景

BelloBuddy 是一个 Electron 桌面应用（恢复工程，`app/src` 仅有编译产物与部分可编辑 JS，无原始 TS/Vue 源码），当前账号体系依赖 `belloai.com` 云端：

- 登录端点：`user/login`、`user/sign_in`（短信）、`user/desktop_auth_token`（浏览器换票）；
- 凭据：明文 JSON 文件 `bello-credentials.json`（`authorization` + `baseUrl`）；
- 登录与「桌面内核下载授权」（`/api/beiliao/desktop_bundle`）强绑定。

该云端不可控，账号、授权、内核发放均受制于人。new-api 是自有的 Go 网关，具备完整的用户、会话、令牌、2FA、OAuth、审计与配额体系。

### 1.2 目标

1. **单一账号源**：BelloBuddy 的注册、登录、会话、权限、用户资料全部由 new-api 承载，删除对 `belloai.com` 账号接口的一切依赖。
2. **桌面原生体验**：保留桌面端现有登录入口（账号密码、验证码），交互不变或更好。
3. **安全合规**：满足本项目 AGENTS.md 中 OWASP ASVS / Cheat Sheets 的强制要求（服务端强制校验、防枚举、防爆破、会话轮换与失效、审计不含敏感信息）。
4. **可灰度、可回滚**：凭据层重构采用适配层隔离，新旧凭据并存迁移，出问题可一键回退到旧链路（过渡期内）。
5. **授权解耦**：内核/资源下载授权改由 new-api 签发（或本地内置），不再依赖原云端按账号发放。

### 1.3 非目标（本轮不做）

- 不重构 BelloBuddy 的**业务账号**（BOSS 直聘招聘账号管理：`add-account-login-flow`、`one-click-login-flow`、cookie 管理等）——那是产品功能，不是产品账号体系。
- 不改动 new-api 面向 Web 前端（`web/`）的既有行为。
- 不迁移业务数据（方案、职位、候选人记录）的存储位置——那是「断云」计划的另一条工作线（见 `docs/bello-cloud-cutover-boundary.md`）。

---

## 2. 现状分析

### 2.1 BelloBuddy 现有账号体系

> 关键代码：`app/src/main/index-BYEqD--t.js`（IPC 注册区约 5722–5920 行）、`loader-OM0zjlIw.js`（凭据读取与内核加载）、`docs/RECOVERY.md`、`docs/REMOTE_CORE_AND_CLOUD.md`、`docs/bello-cloud-cutover-boundary.md`。

**架构**：渲染进程（Vue 3 + element-plus，编译产物）→ IPC → 主进程（聚合所有云端调用）→ `belloai.com`。

**凭据存储**：`bello-credentials.json`（主进程 `Fe = "bello-credentials.json"`，`readConfigFile` 读写），结构：

```json
{
  "authorization": "<token 字符串>",
  "baseUrl": "https://...",
  "aijobos": { "...": "云端浏览器配置（可选）" },
  "aijobosApiBase": "..."
}
```

**登录方式（IPC handler 一览）**：

| IPC 通道 | 云端接口 | 说明 |
| --- | --- | --- |
| `bello-login` | `POST {baseUrl}/api/user/login` | 手机号或邮箱 + 密码，返回 token |
| `bello-send-sms` | `POST .../api/osr_user/send_sms` | scene=login，发短信验证码 |
| `bello-sms-login` | `POST .../api/user/sign_in` | 手机号 + 短信验证码，返回 token |
| `bello-browser-login` | `user/desktop_auth_token` | 打开浏览器网页登录后用 code 换桌面 token |
| `bello-credentials-status` | `GET /api/beiliao_job?max_results=1` | 用现 token 探活，401 判定过期 |
| `save-bello-credentials` / `clear-bello-credentials` / `fetch-bello-credentials` | — | 凭据读写清 |
| `bello-api-request` | 任意 `/api/*` | 业务 API 统一代理：附 `Authorization` 头，401 时向渲染进程广播 `bello-credentials-expired` |

**令牌行为**：token 为不透明字符串，原样放入 `Authorization` 头；无过期时间元数据、无刷新机制——过期唯一处理是「广播过期事件 + 用户重新登录」。

**登录副作用**：`save-bello-credentials` 成功后立即触发 `ensureCoreEngineLoaded()`（登录即按账号下载/解密桌面内核），内核授权与账号 token 强耦合。

**已知问题（重构动因）**：

1. 凭据**明文落盘** JSON 文件，无 safeStorage 加密；
2. token 无刷新/轮换，过期体验差；
3. 无 2FA、无会话管理（多设备查看/踢出）、无审计；
4. 账号、授权、内核发放完全受制于原云端；
5. 登录协议为原云端私有接口，无标准可依。

### 2.2 new-api 认证体系（重构后的目标体系）

> 关键代码：`router/api-router.go`、`middleware/auth.go`、`middleware/auth_origin.go`、`middleware/rate-limit.go`、`middleware/secure_verification.go`、`controller/user.go`、`service/auth_session.go`、`service/authz/`、`model/user.go`、`model/user_access_token.go`、`oauth/`。

**面板认证（本次对接的部分）**：

| 能力 | 端点 | 说明 |
| --- | --- | --- |
| 密码登录 | `POST /api/user/login` | 用户名+密码；支持密码传输加密（`GET /api/user/login/encryption-key` 取密钥）；Turnstile 可选 |
| 2FA 登录 | `POST /api/user/login/2fa` | TOTP 挑战，登录中态续验 |
| 登录验证 | `POST /api/user/login/verify` | 通用安全验证挑战（`service.StartLoginVerification`） |
| Passkey 登录 | `POST /api/user/passkey/login/begin|finish` | WebAuthn |
| 注册 | `POST /api/user/register` | 可强制邮箱验证码；Turnstile |
| 忘记密码 | `GET /api/reset_password` → `POST /api/user/reset` | 邮件重置 |
| 会话刷新 | `POST /api/user/auth/refresh` | HttpOnly 刷新 cookie（`new_api_refresh`）+ 旋转；`SessionCookieOriginGuard` 校验来源 |
| 登出 | `POST /api/user/auth/logout` | 撤销会话 |
| 会话管理 | `GET /api/user/sessions`、`DELETE /api/user/sessions/:sid`、`POST /api/user/sessions/revoke-others` | 多设备会话查看/踢出 |
| PAT（个人访问令牌） | `/api/user/access_tokens` CRUD + `/catalog` `/scopes` | 前缀 `nap_`，哈希落库，scope 白名单 + 过期 + 访问审计 |
| 2FA 管理 | `/api/user/2fa/*` | TOTP 绑定/解绑/备份码 |
| Passkey 管理 | `/api/user/passkey/*` | 注册/删除/管理员重置 |
| OAuth/OIDC | `GET /api/oauth/:provider`（GitHub、Discord、OIDC、LinuxDO、Telegram）+ 微信 + 自定义 provider（root 管理） | 账号绑定 |
| 安全验证 | `GET /api/verify/methods`、`POST /api/verify` | 敏感操作 step-up |
| 邮箱验证 | `GET /api/verification` | 频率限制 + Turnstile |

**登录成功响应（`AuthBundle`，`controller/user.go:198` `writeLoginResponse`）**：

```json
{
  "success": true,
  "data": {
    "access_token": "...",        // 短期内部令牌，绑定登录会话
    "token_type": "...",
    "access_expires_at": 1234567890,
    "session": { "sid": "...", "login_method": "...", "ip": "...", "created_at": ..., "expires_at": ... },
    "user": { "...": "用户资料" }
  }
}
```

刷新令牌经 `Set-Cookie: new_api_refresh=...`（HttpOnly）下发，`POST /api/user/auth/refresh` 旋转换新。

**鉴权中间件（`middleware/auth.go`）**：`Authorization: Bearer <token>` 双轨：

- **内部会话令牌**（`service.ParseDashboardAccessToken`）：短期、绑定 DB 会话（`UserSession`，含 `auth_version`/`session_version` 失效机制、会话数量与签发频率限制）；
- **PAT**（前缀 `nap_`，`model/user_access_token.go`）：长期、哈希存储、scope 白名单（未声明的路由一律拒绝，fail-closed）、过期、逐请求访问审计。

**角色与权限**：common / admin / root 三级（`RoleCommonUser` 起），叠加 Casbin（`service/authz/`）细粒度 `RequirePermission`。管理写操作自动审计（`beginAdminAudit`）。

**网关 API 鉴权（不属本次对接，但需知晓边界）**：`/v1/*` 等转发流量用独立的 `TokenAuth`（`sk-` 令牌），与面板认证完全分离。

**强制规范**：AGENTS.md 要求任何认证相关改动必须对照最新稳定版 OWASP ASVS 与 Authentication / Session Management Cheat Sheet，服务端强制、防枚举、防爆破、会话轮换/失效、step-up、审计不含敏感信息，且须附回归测试；未验证不得声称合规。

### 2.3 两个体系的本质差异

| 维度 | BelloBuddy 现状 | new-api | 影响 |
| --- | --- | --- | --- |
| 账号标识 | 手机号 / 邮箱 | 用户名（+可选邮箱；OAuth 绑定） | 登录表单字段变化 |
| 凭据形态 | 一个不透明长 token | 短期 access token + 旋转刷新令牌 + 可选 PAT | 客户端需管理刷新 |
| 凭据存储 | 明文 JSON 文件 | —（客户端责任） | 需引入 Electron safeStorage |
| 刷新机制 | 无，401 重登 | `POST /auth/refresh` + cookie 旋转 | 新增逻辑 |
| 2FA / 会话管理 / 审计 | 无 | 全套 | 功能增强 |
| 短信验证码登录 | 有（`user/sign_in`） | **无**（仅邮箱验证码） | 缺口，见 §9 |
| 浏览器换票登录 | 有（`desktop_auth_token`） | **无直接等价物**（有标准 OAuth code flow） | 需决策，见 §9 |
| 登录副作用 | 触发内核下载授权 | 无 | 需重新设计资源发放 |

---

## 3. 差异对照：登录链路映射（旧 → 新）

| BelloBuddy 旧链路 | new-api 新链路 | 改造点 |
| --- | --- | --- |
| `bello-login` → `POST /api/user/login`（mobile/email+password） | `POST /api/user/login`（username+password，可选加密传输） | IPC 参数结构重排；主进程代理不变 |
| `bello-send-sms` + `bello-sms-login` | `GET /api/verification`（邮箱验证码）或保留短信则服务端新增 | **缺口**，见 §9 Q1 |
| `bello-browser-login` → `user/desktop_auth_token` | 方案 a：OAuth 授权码流（`GET /api/oauth/:provider` 体系）；方案 b：保持登录表单单一化，砍掉浏览器换票 | **缺口**，见 §9 Q2 |
| `bello-credentials-status`（探活） | 本地解析 `access_expires_at` + 刷新探测，或 `GET /api/user/self` 轻量探活 | 重写 |
| 401 → `bello-credentials-expired` | 401 → 尝试 refresh → 失败再广播 `auth-session-expired` | 重写并兼容旧事件名（过渡期） |
| `save-bello-credentials`（明文 JSON） | safeStorage 加密存储 + schema v2 | 重写 + 迁移 |
| 登录成功 → 下载内核 | 登录成功 → 按 new-api 授权调用自建资源发放接口（或本地内置） | 见 Phase 5 |

---

## 4. 目标架构

### 4.1 总体原则

1. **BelloBuddy = new-api 的桌面客户端**：所有账号能力调用 new-api 现有端点，new-api 只为桌面场景做**最小增量**（见 Phase 1）。
2. **凭据适配层隔离 + 渲染层零改动**：主进程内新建 `auth-service` 模块，对外**完整保持现有 IPC 语义**（通道名、参数结构、返回结构、事件名均不变），对内封装 new-api 协议并吸收全部差异（字段映射、文案翻译、baseUrl 覆盖）；渲染进程一行不改（硬约束，见 Phase 3）。
3. **安全默认**：safeStorage 加密、令牌不落日志、401 统一处理、所有重试有上限与退避。

### 4.2 凭据模式选型

**方案 A：会话模式（推荐为主）**

- 桌面端走 `POST /api/user/login`（含 2FA/passkey 挑战流程），拿到 `access_token` + 刷新令牌；
- 刷新令牌不依赖浏览器 cookie，**在 Electron 主进程以 safeStorage 加密存储**，到期前调 `POST /api/user/auth/refresh` 旋转；
- 优点：复用会话管理（多设备列表/踢出）、审计、2FA、会话限制等全套服务端能力；与 Web 行为一致。
- 风险：`SessionCookieOriginGuard` 按浏览器 Origin 校验刷新请求，桌面端 fetch 无浏览器 Origin——需要 Phase 1 中的小改造（见下）。

**方案 B：PAT 模式（作为辅助/可选）**

- 用户（或登录后自动）在 new-api 创建 `nap_` PAT（限定 scope、设过期），桌面端保存并直接用作 `Authorization`；
- 优点：实现最简单、无刷新复杂度、天然适合「记住此设备」；
- 缺点：权限静态、无法用 2FA 会话语义、泄露影响窗口长（靠过期+审计兜底）。

**推荐组合**：默认方案 A（登录 → 短期 access token + 刷新令牌）；在「设置」中提供「保持登录（生成本设备专用 PAT）」开关使用方案 B，用于长时间离线后免重登。首版可先只做 A，B 列为二期。

### 4.3 目标认证时序

```text
渲染进程            主进程 auth-service              new-api
   │  bello-login(username,pwd)  │                    │
   │ ───────────────────────────►│ POST /api/user/login │
   │                             │────────────────────►│
   │                             │  ◄── 401 挑战(2FA/verify) 或 AuthBundle
   │  ◄── {ok, user, session}    │ (挑战时回传渲染层收集 TOTP)
   │                             │ safeStorage 保存刷新令牌
   │  bello-api-request(...)     │                    │
   │ ───────────────────────────►│ 带 access_token 转发 │
   │                             │────────────────────►│
   │                             │  ◄── 200 / 401      │
   │                             │ (401: 刷新→重试1次→仍失败广播 auth-session-expired)
```

### 4.4 new-api 侧最小增量（决策依据）

| 增量 | 理由 | 工作量评估 |
| --- | --- | --- |
| 刷新端点兼容桌面客户端（`SessionCookieOriginGuard` 放行携带正确客户端凭据/或为桌面会话签发显式刷新令牌传参模式） | 桌面 fetch 无浏览器 Origin | 小（中间件一个白名单分支 + 测试） |
| （可选）`POST /api/user/device_token`：一次登录换设备绑定 PAT | 支持方案 B / 「保持登录」 | 中 |
| （可选）短信验证码登录 provider 接口 | 若决定保留短信登录 | 中，且引入短信服务商依赖 |

---

## 5. 分阶段实施计划

> 建议按阶段提交，每阶段独立可验收、可回滚。估时为单人有效工作量，供排期参考。

### Phase 0 — 准备与基线（约 0.5 周）

**目标**：冻结边界，建好对照基线。

- [ ] 确认 new-api 部署形态（数据库、Redis、域名、HTTPS），准备一个专用测试实例与测试账号（common/admin/root 各一）。
- [ ] 在 new-api 建立分支 `feat/desktop-auth`；在 BelloBuddy 建立分支 `refactor/auth-newapi`。
- [ ] 对 BelloBuddy 现有 IPC 面（登录相关 10 个 handler）与凭据文件 schema 写快照测试（现状行为基线），供重构后对照。
- [ ] 明确并冻结 §9 的三个开放问题（短信登录、浏览器换票、内核发放）的决策，进入对应 Phase。

**验收**：基线测试绿；决策记录写入本文档 §9。

### Phase 1 — new-api 服务端适配（约 0.5–1 周）

**目标**：new-api 具备承载桌面客户端的最小能力，且不破坏 Web 行为。

- [ ] **刷新链路桌面兼容**：评估 `SessionCookieOriginGuard`（`middleware/auth_origin.go`）对桌面端 refresh 请求的影响；实现方案二选一：
  1. 桌面刷新请求携带登录时签发的设备标识头 + 走同源豁免清单（服务端可配置）；
  2. 或登录响应额外返回显式 `refresh_token`（body），桌面端存 safeStorage、refresh 时以 JSON body 提交（新端点 `POST /api/user/auth/refresh/token`，与 cookie 模式共享旋转逻辑）。
  - 推荐 2（更显式、可测试），并保证 cookie 模式行为完全不变。
- [ ] **PAT 路由 scope 声明核查**：确认桌面端将调用的端点（`/api/user/self`、`/api/user/sessions*` 等）在 `AccessTokenRouteRule` 中已声明（PAT fail-closed 机制要求），二期方案 B 需要时补声明。
- [ ] （可选，依 §9 Q1 决策）短信验证码登录：新增 provider 化的验证码服务（接口对齐现有 `common.VerifyCodeWithKey` 体系）+ `POST /api/user/login/sms`，频控走 `CriticalRateLimit` + 独立 `UserCriticalRateLimit`。
- [ ] （可选，依 §9 Q3 决策）资源发放端点：如内核改为自建发放，设计 `GET /api/resource/desktop_bundle`（root 可配，带版本与校验和），或确定本地内置方案后跳过。
- [ ] 按 AGENTS.md 要求：为以上改动编写 OWASP 对照说明与回归测试（含失败、过期、重放、绕过用例）。

**验收**：新端点 curl/集成测试全绿；Web 前端登录/刷新回归不受影响；测试覆盖含滥用与过期分支。

### Phase 2 — BelloBuddy 凭据与 IPC 层重构（约 1–2 周，核心）

**目标**：主进程完成 new-api 协议接入，IPC 语义平滑过渡。

- [ ] 新建 `app/src/main/auth/` 模块（可编辑 JS 工程风格与现有文件一致）：
  - `credentials-store.js`：凭据 schema v2（`{ version: 2, baseUrl, session: { sid, access_token, access_expires_at, refresh_token }, pat?: {...}, user: {...} }`），读文件→内存缓存→写回均经 **Electron safeStorage** 加密；首次读到 v1（明文 `authorization`）时执行迁移并删除明文。
  - `api-client.js`：封装 new-api 调用（登录、2FA 挑战、refresh 旋转、登出、探活）；统一错误分类（网络/凭据/挑战/限流）；刷新加单飞（in-flight 去重）与退避重试上限。
  - `ipc.js`：注册新 IPC 通道（见 §6 映射表），同时保留旧通道名做**过渡 shim**（`bello-login` 等内部转调新实现），过渡期结束后删除。
- [ ] 401 统一处理：先刷新再重试一次，仍失败广播 `auth-session-expired`（过渡期同时发送旧事件 `bello-credentials-expired` 以免渲染层空白）。
- [ ] 登录成功副作用重构：移除 `save-bello-credentials` 里的 `ensureCoreEngineLoaded` 硬绑定，改为可配置的「登录后动作」钩子（Phase 5 接管）。
- [ ] 移除/禁用旧云端调用：`user/login`、`user/sign_in`、`osr_user/send_sms`、`user/desktop_auth_token`、`beiliao/desktop_bundle` 在主进程的引用全部下线或封存在 `legacy/` 开关后。

**验收**：手动走查 + 冒烟（`npm run smoke`）：密码登录、2FA 账号登录挑战、刷新旋转、登出、踢出会话后下个请求收到过期事件、凭据文件密文落盘、v1 凭据自动迁移。

### Phase 3 — 渲染层零改动验证（约 0.5 周）

> **方向性决策（2026-10-01）：渲染层是编译产物，改动成本高且易碎。确立「渲染层零改动」为硬约束——所有协议差异、字段映射、错误文案翻译全部由主进程 IPC shim 层吸收（Phase 2）。本阶段只做验证，不做 UI 改造。**

**目标**：证明渲染层在不改一行代码的前提下跑通新账号体系。

- [ ] 盘点渲染层实际依赖面（`app/src/renderer/assets/*` 中对 `bello-login` / `bello-send-sms` / `bello-sms-login` / `bello-api-request` / `bello-credentials-expired` 的调用与返回值消费方式），确认 shim 覆盖完整。
- [ ] 逐项核对 shim 吸收点是否闭环：
  - 账号字段：渲染层「手机号/邮箱」输入 → shim 映射为 new-api 用户名（账号开通时用户名与老手机号对齐，见 §7）；
  - baseUrl 输入框：渲染层照旧可填，shim 忽略并覆盖为配置的 new-api 地址；
  - 错误文案：new-api 错误 code 在 shim 层翻译为老文案，渲染层无感；
  - 短信 tab：不隐藏，`bello-send-sms` / `bello-sms-login` shim 返回「功能迁移中」友好错误（依 §9 Q1=b）；
  - 401 过期：继续广播旧事件 `bello-credentials-expired`（Phase 2 已含）。
- [ ] **2FA 约束落地**：老界面无验证码输入步骤，2FA 账号登录会卡在挑战态——运营约束：桌面端账号初期不启用 2FA（服务端按用户控制），shim 对挑战态响应返回明确提示。若未来要求桌面端 2FA，再单独立「最小 UI 补丁」子计划。
- [ ] （二期可选项，默认不做）会话管理入口、浏览器换票入口等 UI 增强——仅在源码工程恢复后考虑。

**验收**：不改渲染层任何文件，真实账号完成「登录→业务请求→过期→重登→登出」全流程；UI 各状态（错误、过期弹窗）表现与迁移前一致。

### Phase 4 — 业务 API 代理切换（约 0.5–1 周）

**目标**：`bello-api-request` 代理目标从原云端切换为 new-api。

- [ ] **前置决策**：原云端的业务接口（`/api/beiliao/messages`、`filter_candidate/filter_by_json` 等）在 new-api 中尚不存在。两种路径：
  1. new-api 逐步自建这些业务端点（属「断云」主线，不在本计划内逐个实现）；
  2. 本阶段仅切换**鉴权头与 baseUrl 机制**，业务端点路径映射表由配置驱动（`endpoint-map.json`），已迁移的走 new-api，未迁移的明确报「功能迁移中」。
- [ ] 推荐 2：本计划只负责「账号与鉴权」，端点映射配置化，后续按业务迁移进度逐条点亮。

**验收**：已映射端点鉴权正常；未映射端点给出明确错误而非 404 迷惑。

### Phase 5 — 登录后资源/内核发放改造（约 0.5–1 周）

**目标**：解除「内核下载授权 = 原云端账号」绑定。

- [ ] 依 §9 Q3 决策执行：
  - **自建发放**：new-api 提供 bundle 清单 + 下载端点（root 配置版本/SHA256），`loader-OM0zjlIw.js` 的凭据读取改为读新凭据存储中的会话令牌；
  - **本地内置**：把已取得的 `vendor/remote-core/core-engine.enc` 随应用分发，`loader` 跳过服务端清单流程（注意 RECOVERY.md 中「备份须保留 `vendor/remote-core/`」的提示）。
- [ ] 移除 loader 中对 `bello-credentials.json` 的直接读取（改走 credentials-store）。

**验收**：登录后内核加载成功（自建或内置路径）；断网冷启动行为符合设计。

### Phase 6 — 数据迁移与兼容清理（约 0.5 周）

- [ ] v1→v2 凭据迁移的批量验证（老用户升级场景）。
- [ ] 过渡 shim 与旧事件名的下线检查表（确认无渲染层引用后移除）。
- [ ] 文档更新：BelloBuddy `docs/` 增补新认证架构说明；本文档收尾归档。

### Phase 7 — 测试、灰度与上线（约 1 周）

- [ ] 安全测试清单执行（§8）。
- [ ] new-api 侧回归：Web 登录/2FA/passkey/OAuth/refresh 全量回归。
- [ ] BelloBuddy 侧冒烟 + 关键路径手测脚本化（扩展现有 `scripts/smoke.mjs`）。
- [ ] 灰度：内部构建（`打开恢复版.command` 通道）先发团队；观察 401/刷新失败率、登录成功率指标（本地埋点，不含敏感字段）。
- [ ] 全量后删除旧协议代码与本文档过渡章节。

---

## 6. 接口与 IPC 映射表

### 6.1 IPC 通道（BelloBuddy 主进程）

| 旧通道 | 处置 | 新通道 / 行为 |
| --- | --- | --- |
| `bello-login` | 保留为 shim | `auth:login`（username/email + password → AuthBundle；2FA 时返回挑战态） |
| `bello-send-sms` | 依 §9 Q1 | `auth:send-code`（邮箱验证码走 `GET /api/verification`）或下线 |
| `bello-sms-login` | 依 §9 Q1 | `auth:login-code` 或下线 |
| `bello-browser-login` / `cancel-bello-browser-login` | 依 §9 Q2 | OAuth 流程或下线 |
| `fetch-bello-credentials` | 改造 | `auth:get-state`（只返回脱敏状态与用户资料，**不返回令牌本体**——渲染层无持币必要） |
| `bello-credentials-status` | 改造 | `auth:status`（本地过期判断 + 必要时 `/api/user/self` 探活） |
| `save-bello-credentials` | 移除 | 令牌仅由主进程凭据存储写入，渲染层不可注入（安全收益） |
| `clear-bello-credentials` | 改造 | `auth:logout`（调 `POST /api/user/auth/logout` + 清 safeStorage） |
| `bello-api-request` | 改造 | `api:request`（内部附当前 access_token；401 自动刷新重试） |
| 事件 `bello-credentials-expired` | 过渡兼容 | 新事件 `auth-session-expired` |

### 6.2 new-api 端点使用清单（桌面端消费）

| 端点 | 用途 |
| --- | --- |
| `GET /api/status` | 启动探活、取服务端配置（注册开关、登录开关等） |
| `GET /api/user/login/encryption-key` | 密码传输加密（启用时） |
| `POST /api/user/login` | 密码登录 |
| `POST /api/user/login/2fa` / `POST /api/user/login/verify` | 登录挑战续验 |
| `POST /api/user/auth/refresh`（或 Phase 1 新端点） | 刷新会话 |
| `POST /api/user/auth/logout` | 登出 |
| `GET /api/user/self` | 用户资料 + 探活 |
| `GET /api/user/sessions` 等 | 会话管理（增强） |
| `GET /api/verification` | 邮箱验证码（注册/找回/换绑） |
| `POST /api/user/reset`（+`GET /api/reset_password`） | 找回密码 |
| （二期）`/api/user/access_tokens` | 设备 PAT |
| （依 Q3）`GET /api/resource/desktop_bundle` | 内核发放 |

---

## 7. 数据迁移与兼容策略

1. **凭据文件**：`bello-credentials.json` v1（明文）→ v2（safeStorage 密文 + 结构升级）。首启检测 v1 → 迁移 → 备份原文件为 `bello-credentials.v1.bak` → 校验登录态有效（`/api/user/self` 或旧探活）→ 失败则引导重登。迁移过程全部 try/catch，失败保留 v1 原文件。
2. **服务端账号**：老用户在 new-api 侧**无账号**，需要开通路径：
   - 方式一：管理员批量预建账号（用户名规则与老手机/邮箱对齐），用户首启走「忘记密码」邮件重置；
   - 方式二：首启引导注册（若 new-api 开放注册）；
   - 方式三：管理员导入老手机号/邮箱为用户名字段，短信/邮箱找回。
   - 推荐方式一 + 找回密码，运维可控。
3. **令牌兼容**：不做旧 token 直通 new-api（两套体系无法互认）；以「迁移窗口内强制重新登录一次」为默认策略，UI 明确提示。
4. **回滚**：过渡期内保留旧登录 shim 分支（编译开关 `LEGACY_AUTH=1`），出问题可回退到旧链路（原云端仍存活期间有效）。

---

## 8. 安全合规清单

依据 AGENTS.md（OWASP ASVS 最新稳定版 + Authentication / Session Management Cheat Sheets），本次改造逐条对照：

| # | 控制 | 落点 | 状态 |
| --- | --- | --- | --- |
| 1 | 凭据传输 | 全链路 HTTPS；启用 new-api 密码传输加密（encryption-key） | 设计内 |
| 2 | 凭据存储 | 刷新令牌/PAT 仅存 Electron safeStorage（Keychain）；废除明文 JSON | Phase 2 |
| 3 | 防爆破 | 复用 new-api `CriticalRateLimit` / `UserCriticalRateLimit`；桌面端禁用无限重试 | 服务端既有 + 客户端上限 |
| 4 | 防枚举 | 登录失败统一「用户名或密码错误」（new-api 既有 `MsgUserUsernameOrPasswordError`） | 既有，客户端不得自行区分 |
| 5 | 会话轮换 | refresh 旋转（`ErrRefreshRace` 防并发重放） | 服务端既有 |
| 6 | 会话失效 | `auth_version`/`session_version` + 踢出/注销全端失效 | 服务端既有 |
| 7 | 2FA / step-up | 登录挑战流 + 敏感操作 `POST /api/verify` | 服务端既有，客户端接 UI |
| 8 | 最小权限 | PAT scope fail-closed；桌面端会话令牌不落地渲染层 | Phase 2/6 |
| 9 | CSRF/Origin | 刷新端点 cookie 模式保留 Origin Guard；桌面模式用显式令牌端点并限频 | Phase 1 |
| 10 | 审计 | 登录/登出/敏感操作走服务端审计；本地日志（含 `console.log`）不得含令牌、密码、验证码 | Phase 2 检查项 |
| 11 | 失效凭据处理 | 401→刷新→重试→过期广播，单飞与上限防风暴 | Phase 2 |
| 12 | 回归测试 | 每个 Phase 附测试；含失败、过期、重放、绕过用例 | 各 Phase 验收 |

**合规声明纪律**：按 AGENTS.md，任一适用控制未落实或未验证前，变更说明不得声称「已合规/已完成」。

---

## 9. 风险与开放问题

| # | 问题 | 影响 | 候选决策 | 建议 |
| --- | --- | --- | --- | --- |
| Q1 | **短信验证码登录**：new-api 无短信能力，老用户习惯手机验证码登录 | 登录方式缺口 | a) new-api 新增短信 provider（+服务商成本）；b) 老用户改用户名密码 + 邮箱验证码/找回；c) 首版 a 后补 | 首版 b（零新增依赖），a 列二期按需 |
| Q2 | **浏览器换票登录**（`desktop_auth_token`）在 new-api 无等价物 | 桌面便捷登录入口 | a) 用 new-api 自定义 OAuth provider 机制搭一个「网页登录→code 换票」流；b) 砍掉，仅保留应用内表单登录 | 首版 b；a 待有真实需求再做（避免新增攻击面） |
| Q3 | **内核发放授权**（`desktop_bundle`）原为按账号云端下发 | 登录后核心功能可用性 | a) new-api 自建发放端点；b) 内核随应用本地内置 | 倾向 b（RECOVERY.md 已验证本地内核可加载），a 仅在需要热更时做 |
| R1 | BelloBuddy 渲染层是**编译产物**，直接改 UI 需改 chunk 或重建源码工程，成本高、易碎 | 已通过「渲染层零改动」约束规避（Phase 3，2026-10-01 决策）：全部适配压入主进程 shim；仅当未来要求桌面端 2FA 等 UI 增强时风险才会回归 | 届时再评估「最小侵入改 chunk」vs「源码恢复」 | 已缓解 |
| R2 | 原云端业务 API 与 new-api 的共存期，`endpoint-map` 未点亮的功能会「看起来登录了但功能不可用」 | 用户困惑 | 配置驱动 + 明确的错误文案与迁移公告 | Phase 4 已含 |
| R3 | new-api 会话数量/签发频率限制（`UserSessionActiveLimit` 等）对桌面端自动重登风暴不友好 | 极端场景登录被限 | 客户端退避 + 服务端限值核对 | Phase 1 顺带核对 |
| R4 | 凭据迁移失败导致用户被登出 | 体验 | 迁移失败保底引导重登，不丢功能 | Phase 6 |

---

## 10. 里程碑与验收标准

| 里程碑 | 内容 | 验收标准 |
| --- | --- | --- |
| M1（Phase 0–1 末） | 服务端就绪 | 桌面模拟客户端脚本（curl/Node）完成「登录→挑战→刷新→登出→踢出」全链路；Web 回归通过 |
| M2（Phase 2 末） | 主进程切换完成 | 冒烟通过；凭据密文落盘；v1 迁移用例通过；旧云端接口零调用 |
| M3（Phase 3–5 末） | 端到端可用 | 真实账号在桌面端完成登录→业务可用→登出全流程；内核加载正常 |
| M4（Phase 6–7 末） | 灰度完成 | 内部灰度指标：登录成功率 ≥99%、刷新失败率 <1%、无凭据明文落盘告警；全量发布 |

---

## 附录 A：本次走读涉及的关键文件

**BelloBuddy**（`/Users/a1/Desktop/bosscli/recovery/BelloBuddy/`）：

- `app/src/main/index-BYEqD--t.js`（登录/凭据/代理 IPC，5722–5920 行核心区）
- `app/src/main/loader-OM0zjlIw.js`（凭据读取 → 内核下载解密加载）
- `app/src/preload/index.js`（IPC 暴露面）
- `docs/RECOVERY.md`、`docs/REMOTE_CORE_AND_CLOUD.md`、`docs/bello-cloud-cutover-boundary.md`（恢复状态与断云边界）
- `packages/boss-auto-browse-and-chat/`（业务插件，含 `beiliao-api.mjs` 等云端调用客户端）

**new-api**（`/Users/a1/Desktop/DPin-backend-newapi/`）：

- `router/api-router.go`（全部认证路由）
- `middleware/auth.go`、`middleware/auth_origin.go`（会话/PAT 双轨鉴权、Origin Guard）
- `controller/user.go`（Login/Register/setupLogin/writeLoginResponse）
- `service/auth_session.go`（AuthBundle、会话创建/旋转/失效）
- `model/user.go`、`model/user_access_token.go`（用户模型、PAT `nap_` 体系）
- `service/authz/`（Casbin）、`oauth/`（OAuth provider 注册）
- `AGENTS.md`（OWASP 强制规范）
