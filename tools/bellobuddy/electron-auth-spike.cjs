// 仅由 Go 回归夹具启动；不连接部署实例，不输出或落盘认证凭据。
const { app, net, session, safeStorage } = require('electron')
const { X509Certificate, createHash } = require('node:crypto')

const [origin, certificateHash, profile] = process.argv.slice(2)
const target = new URL(origin)
if (!['127.0.0.1', 'localhost'].includes(target.hostname) || !profile) process.exit(2)
app.setPath('userData', profile)
// 编辑窗口关闭后仍须完成认证回归；夹具结束统一由 app.exit 退出。
app.on('window-all-closed', () => {})
const passed = []
function check(condition, label) {
  if (!condition) throw new Error(label)
  passed.push(label)
}

async function run() {
  await app.whenReady()
  const jar = session.fromPartition('dpin-auth')
  check(!jar.isPersistent(), '专用分区不持久化')
  if (target.protocol === 'https:') {
    // 夹具使用临时自签名证书，只信任 Go 父测试传入的精确 DER 指纹。
    // 该测试策略不进入产品网络栈，也不修改系统信任或关闭证书校验。
    jar.setCertificateVerifyProc(({ hostname, certificate }, callback) => {
      let matches = false
      try {
        const raw = new X509Certificate(certificate.data).raw
        matches = hostname === target.hostname && createHash('sha256').update(raw).digest('hex') === certificateHash
      } catch {}
      callback(matches ? 0 : -2)
    })
  }
  function post(path, sid, access, requestOrigin = origin) {
    return new Promise((resolve, reject) => {
      const request = net.request({ method: 'POST', url: new URL(path, origin).href, session: jar, useSessionCookies: true, redirect: 'manual' })
      const timer = setTimeout(() => { request.abort(); reject(new Error('网络夹具超时')) }, 10000)
      request.setHeader('Content-Type', 'application/json')
      if (requestOrigin) request.setHeader('Origin', requestOrigin)
      if (sid) request.setHeader('X-Auth-Session', sid)
      if (access) request.setHeader('Authorization', `Bearer ${access}`)
      request.on('redirect', () => { clearTimeout(timer); request.abort(); reject(new Error('认证请求发生重定向')) })
      request.on('error', () => { clearTimeout(timer); reject(new Error('认证网络请求失败')) })
      request.on('response', response => {
        let body = ''
        response.on('data', chunk => { body += chunk })
        response.on('error', () => { clearTimeout(timer); reject(new Error('认证响应读取失败')) })
        response.on('end', () => {
          clearTimeout(timer)
          try { resolve({ status: response.statusCode, body: JSON.parse(body) }) }
          catch { reject(new Error('认证响应不是 JSON')) }
        })
      })
      request.end('{}')
    })
  }
  const cookie = async () => (await jar.cookies.get({ url: new URL('/api/user/auth/refresh', origin).href, name: 'new_api_refresh' }))[0]
  const restore = value => jar.cookies.set({ url: new URL('/api/user/auth/refresh', origin).href, name: 'new_api_refresh', value, path: '/api/user/auth', httpOnly: true, sameSite: 'strict', secure: target.protocol === 'https:' })
  const login = await post('/test/login')
  check(login.status === 200 && login.body.success, '夹具签发有效产品会话')
  const sid = login.body.data.session.sid
  const initial = await cookie()
  check(initial && initial.httpOnly && initial.sameSite === 'strict' && initial.path === '/api/user/auth', '真实 Cookie jar 保留 HttpOnly、Strict 和路径')
  check(initial.secure === (target.protocol === 'https:'), 'HTTP 与 HTTPS Secure 标志正确')
  check(safeStorage.isEncryptionAvailable(), '系统安全存储可用')
  let saved = safeStorage.encryptString(initial.value)
  check(safeStorage.decryptString(saved) === initial.value, '安全存储能够恢复刷新凭据')
  if (target.protocol === 'https:') {
    for (const untrustedOrigin of ['', 'https://untrusted.invalid']) {
      const denied = await post('/api/user/auth/refresh', sid, undefined, untrustedOrigin)
      check(denied.status === 403 && denied.body.code === 'AUTH_ORIGIN_FORBIDDEN', 'HTTPS 拒绝缺失或错误 Origin')
    }
  }
  const refreshed = await post('/api/user/auth/refresh', sid)
  check(refreshed.status === 200 && refreshed.body.success, 'Electron net 实际发送 Strict Cookie 并刷新成功')
  const rotated = await cookie()
  check(rotated && rotated.value !== initial.value, '轮换 Set-Cookie 自动更新 jar')
  saved = safeStorage.encryptString(rotated.value)
  check(safeStorage.decryptString(saved) === rotated.value, '轮换凭据安全存储更新')
  await restore(initial.value)
  const race = await post('/api/user/auth/refresh', sid)
  check(race.status === 200 && race.body.data.session.sid === sid && (await cookie()).value === rotated.value, '30 秒内匹配旧凭据返回同轮结果')
  const current = await post('/api/user/auth/refresh', sid)
  check(current.status === 200 && current.body.success, '轮换后的当前凭据可正常使用')
  const currentCookie = await cookie()
  await restore(`${sid}.unknown-refresh-secret`)
  const unknown = await post('/api/user/auth/refresh', sid)
  check(unknown.status === 401 && unknown.body.code === 'AUTH_UNAUTHORIZED', '未知刷新凭据被拒绝')
  await restore(currentCookie.value)
  const recovered = await post('/api/user/auth/refresh', sid)
  check(recovered.status === 200 && recovered.body.success, '未知凭据不会撤销合法会话')
  // 父夹具把窗口时间前移，避免测试等待 30 秒；重放的是已识别的上一轮凭据。
  const previous = currentCookie.value
  await post('/test/expire-grace', sid)
  await restore(previous)
  const replay = await post('/api/user/auth/refresh', sid)
  check(replay.status === 401 && replay.body.code === 'AUTH_SESSION_REVOKED', '超窗重放已识别旧凭据撤销会话')
  check(!(await cookie()), '失效响应清除刷新 Cookie')
  const nextLogin = await post('/test/login')
  const next = nextLogin.body.data
  const logout = await post('/api/user/auth/logout', next.session.sid, next.access_token)
  check(logout.status === 200 && logout.body.success && !(await cookie()), '退出撤销会话并清除 Cookie')
  const afterLogout = await post('/api/user/auth/refresh', next.session.sid)
  check(afterLogout.status === 401, '退出后不能继续刷新')
  if (process.env.BELLOBUDDY_AUTH_PROXY) {
    const fs = require('node:fs/promises')
    const path = require('node:path')
    const { DPinAuthProxy } = require(process.env.BELLOBUDDY_AUTH_PROXY)
    const storageDirectory = path.join(profile, 'auth-backup')
    const proxy = new DPinAuthProxy({ origin, storageDirectory })
    const issued = await post('/test/login')
    const beforeCalls = (await post('/test/refresh-count')).body.data.count
    const states = await Promise.all([proxy.refresh(), proxy.refresh(), proxy.refresh()])
    check(states.every(state => state.state === 'authenticated'), '主进程代理合并并发刷新')
    const afterCalls = (await post('/test/refresh-count')).body.data.count
    check(afterCalls === beforeCalls + 1, '三个并发续期请求只发出一次刷新请求')
    const summary = JSON.stringify(proxy.getPublicState())
    check(!summary.includes('access_token') && !summary.includes('refresh') && !summary.includes(issued.body.data.session.sid), '公开状态不分发访问和刷新凭据')
    const identity = proxy.getIdentityContext()
    check(Object.keys(identity).sort().join(',') === 'generation,origin,userID' && identity.userID === states[0].user.id && identity.origin === origin, '任务身份只包含来源、用户和登录代次')
    identity.userID = -1
    check(proxy.getIdentityContext().userID === states[0].user.id, '任务身份快照不可修改代理身份')
    const business = await proxy.request('GET', '/api/bellobuddy/spike')
    check(business.user_id === states[0].user.id, '业务请求以当前产品会话鉴权')
    if (process.env.BELLOBUDDY_FILTER_EDITOR) {
      const { BrowserWindow, ipcMain } = require('electron')
      const { DPinPlanService } = require(path.join(path.dirname(process.env.BELLOBUDDY_AUTH_PROXY), 'dpin-plan-service.cjs'))
      const { registerDPinPlanIPC } = require(path.join(path.dirname(process.env.BELLOBUDDY_AUTH_PROXY), 'dpin-plan-ipc.cjs'))
      const { DPinPlanDraft } = require(path.join(path.dirname(process.env.BELLOBUDDY_AUTH_PROXY), '../shared/dpin-plan-json.mjs'))
      const service = new DPinPlanService(proxy)
      const bridge = registerDPinPlanIPC({ ipcMain, service })
      const editor = new BrowserWindow({ show: false, width: 1280, height: 900, title: '筛选方案隔离测试', webPreferences: { nodeIntegration: false, contextIsolation: true, sandbox: true, webSecurity: true, preload: path.join(path.dirname(process.env.BELLOBUDDY_AUTH_PROXY), '../preload/dpin-plans.cjs') } })
      bridge.authorizeWindow(editor, process.env.BELLOBUDDY_FILTER_EDITOR)
      try {
        await editor.loadFile(process.env.BELLOBUDDY_FILTER_EDITOR)
        const boundary = await editor.webContents.executeJavaScript('({ methods: Object.keys(window.dpinPlans).sort(), node: typeof window.require, electron: typeof window.electron, storage: Object.keys(localStorage) })')
        check(boundary.methods.join(',') === 'get,list,save' && boundary.node === 'undefined' && boundary.electron === 'undefined' && boundary.storage.length === 0, '真实编辑窗口只有三个方案方法且无通用 IPC 或持久草稿')
        const name = '__electron_plan_spike_precision'
        const content = '{"name":"__electron_plan_spike_precision","schema_defs":["FilterDef"],"disabled":false,"builtin":false,"enable_quick_screening":true,"enable_ai_screening":false,"any_fails":[{"conditions":[{"ages":[{"max":23,"exclusive_max":true}]}],"desc":"年龄条件"}],"any_passes":[],"future":{"big":9007199254740993,"decimal":0.123456789012345678901,"values":[false,0,null]}}'
        const input = { id: 0, kind: 'filter', name, enabled: true, expected_version: 0, filter_plan_id: 0, content_json: content }
        const invoke = (window, method, value) => window.webContents.executeJavaScript(`window.dpinPlans.${method}(${JSON.stringify(value)})`)
        const created = await invoke(editor, 'save', input)
        check(created.ok && created.data.content_json === content && created.data.owner_id === states[0].user.id, '真实 preload 与 Go 服务创建私人方案，保持大整数和精确小数')
        const listed = await invoke(editor, 'list', { kind: 'filter', name, page: 1, page_size: 100 })
        check(listed.ok && listed.data.items.some(item => item.id === created.data.id) && !JSON.stringify(listed).includes('content_json'), '云端保存后的方案进入列表且列表不包含正文')
        const reopened = await invoke(editor, 'get', { kind: 'filter', id: created.data.id })
        check(reopened.ok && reopened.data.content_json === content, '重新读取得到完整云端方案')
        const modified = new DPinPlanDraft(reopened.data.content_json)
        modified.set(['name'], name + '_renamed')
        const changedInput = { ...input, id: created.data.id, expected_version: created.data.version, name: name + '_renamed', content_json: modified.toString() }
        const renamed = await invoke(editor, 'save', changedInput)
        check(renamed.ok && renamed.data.version === created.data.version + 1 && renamed.data.content_json === content.replace(name, name + '_renamed'), '改名只修改对应字段并接受新的并发版本')
        const conflict = await invoke(editor, 'save', changedInput)
        check(!conflict.ok && conflict.error.code === 'VERSION_CONFLICT' && !('data' in conflict), '实际过期版本保存被 Go 服务拒绝')
        const injection = await invoke(editor, 'save', { ...changedInput, user_id: states[0].user.id + 1 })
        check(!injection.ok && injection.error.code === 'INVALID_REQUEST', '实际 IPC 拒绝客户端指定所有者')
        const untrusted = new BrowserWindow({ show: false, webPreferences: { nodeIntegration: false, contextIsolation: true, sandbox: true, webSecurity: true, preload: path.join(path.dirname(process.env.BELLOBUDDY_AUTH_PROXY), '../preload/dpin-plans.cjs') } })
        try {
          await untrusted.loadFile(process.env.BELLOBUDDY_FILTER_EDITOR)
          const denied = await invoke(untrusted, 'get', { kind: 'filter', id: created.data.id })
          check(!denied.ok && denied.error.code === 'AUTH_EDITOR_FORBIDDEN', '未登记的真实 BrowserWindow 不能取得方案正文')
        } finally { untrusted.destroy() }
      } finally { editor.destroy(); bridge.dispose() }
    }
    let upstreamRejected = false
    try { await proxy.request('GET', '/api/bellobuddy/spike/upstream') }
    catch (error) { upstreamRejected = error.code === 'AI_UPSTREAM_REJECTED' && error.source === 'upstream' && error.requestID === 'spike-upstream' }
    check(upstreamRejected && proxy.getPublicState().state === 'authenticated', '上游 401 保留错误来源且不清除产品会话')
    const afterUpstream = await proxy.request('GET', '/api/bellobuddy/spike')
    check(afterUpstream.user_id === states[0].user.id, '上游认证拒绝后产品会话仍可用')
    let quotaRejected = false
    try { await proxy.request('GET', '/api/bellobuddy/spike/quota') }
    catch (error) { quotaRejected = error.code === 'AI_QUOTA_EXHAUSTED' && error.source === 'dpin' && error.requestID === 'spike-quota' }
    check(quotaRejected && proxy.getPublicState().state === 'authenticated', '额度拒绝保留稳定类别和请求标识')
    let arbitraryRejected = false
    try { await proxy.request('GET', 'https://untrusted.invalid/api/bellobuddy/spike') }
    catch (error) { arbitraryRejected = error.code === 'AUTH_REQUEST_PATH_INVALID' }
    check(arbitraryRejected, '代理拒绝任意 URL 转发')
    const activeCookie = await cookie()
    const directories = await fs.readdir(storageDirectory)
    const records = await fs.readdir(path.join(storageDirectory, directories[0]))
    const encryptedRecord = records.find(name => name.endsWith('.bin'))
    const storedPath = path.join(storageDirectory, directories[0], encryptedRecord)
    const bytes = await fs.readFile(storedPath)
    check(!bytes.includes(Buffer.from(activeCookie.value)), '备份文件不含明文刷新凭据')
    check(((await fs.stat(storedPath)).mode & 0o777) === 0o600, '认证备份文件权限为 0600')
    await jar.cookies.remove(origin + '/api/user/auth', 'new_api_refresh')
    const restarted = new DPinAuthProxy({ origin, storageDirectory })
    check(restarted.getPublicState().state === 'signed_out', '重启不凭本地用户摘要判定已登录')
    const restored = await restarted.restore()
    check(restored.state === 'authenticated' && restored.user.id === states[0].user.id, '代理通过加密备份和真实续期恢复身份')
    const signedOut = await restarted.logout()
    check(signedOut.remote_revocation_confirmed && !(await cookie()), '主进程代理退出确认远端撤销')
    check((await fs.readdir(path.dirname(storedPath))).length === 0, '退出清理安全存储备份')
    let identityRejected = false
    try { restarted.getIdentityContext() }
    catch (error) { identityRejected = error.code === 'AUTH_LOGIN_REQUIRED' }
    check(identityRejected, '退出后的任务不能取得登录身份')
    let revokedRejected = false
    try { await proxy.request('GET', '/api/bellobuddy/spike') }
    catch (error) { revokedRejected = error.status === 401 && proxy.getPublicState().state === 'invalid' }
    check(revokedRejected, '另一代理的旧访问凭据不能继续调用已撤销会话')
    await post('/test/login')
    await proxy.refresh()
    check(proxy.getIdentityContext().generation !== identity.generation, '同用户重新登录也切换任务身份代次')
    await post('/test/hold-refresh')
    const pendingRefresh = proxy.refresh().then(() => false, error => error.code === 'AUTH_OPERATION_CANCELLED')
    await post('/test/wait-refresh')
    const cancelled = await proxy.logout()
    await post('/test/release-refresh')
    check(await pendingRefresh, '退出取消正在进行的续期')
    check(cancelled.state === 'signed_out' && proxy.getPublicState().state === 'signed_out' && !(await cookie()), '退出后的旧续期不能恢复身份或 Cookie')
  }
  process.stdout.write(JSON.stringify({ electron: process.versions.electron, protocol: target.protocol, passed }) + '\n')
}

run().then(() => app.exit(0), error => {
  process.stderr.write(`认证网络验证失败：${error.message}\n`)
  app.exit(1)
})
