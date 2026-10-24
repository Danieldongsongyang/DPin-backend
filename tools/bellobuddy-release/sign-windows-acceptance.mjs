#!/usr/bin/env node
// 可信 Windows 验收电脑持有私钥；服务端只配置对应公钥和代码签名发布者。
import { constants, createPrivateKey, sign } from 'node:crypto'
import { readFileSync, statSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const help = `在完成真实 Windows 验收后，生成 D-PIN 可验证的报告（不执行安装器）：
  node sign-windows-acceptance.mjs --installer <完整路径.exe> --version <3.0.29> --minimum-system-version <10.0.19045> --checks-file <验收记录.json> --out <acceptance.json>

环境变量：
  BELLOBUDDY_RELEASE_VERIFIER_PRIVATE_KEY_FILE  受信任验收者的 RSA PEM 私钥路径（不得放入仓库）
  BELLOBUDDY_RELEASE_VERIFIER_PRIVATE_KEY_PASSPHRASE  可选的私钥口令
  BELLOBUDDY_RELEASE_PUBLISHER  Windows 代码签名证书的完整 Subject

验收记录 JSON 须包含 version、installer_sha256 和实际验收时间 verified_at（Unix 秒）。
四个必填项均需真实完成，每项提供 passed: true 和 evidence: "具体结果及证据引用"：
  installation / launch / native_tools / data_preserved
示例结构（请填写真实数据，不可把示例当作验收结果）：
  {"version":"3.0.29","installer_sha256":"完整64位SHA256","verified_at":实际Unix时间,
   "installation":{"passed":true,"evidence":"安装记录引用"},
   "launch":{"passed":true,"evidence":"启动后实际版本及记录引用"},
   "native_tools":{"passed":true,"evidence":"原生模块与浏览器功能结果引用"},
   "data_preserved":{"passed":true,"evidence":"升级前后数据核对记录引用"}}

服务端需配置 BELLOBUDDY_RELEASE_VERIFIER_PUBLIC_KEY_FILE 和同一 BELLOBUDDY_RELEASE_PUBLISHER。
生成的 acceptance.json 是 {payload, signature}，作为管理接口 evidence 提交。
同一签名无法用于不同安装器哈希、应用版本或最低系统版本。不要将私钥、口令或登录凭据写入验收记录。`

if (process.argv.includes('--help') || process.argv.includes('-h')) {
  process.stdout.write(`${help}\n`)
  process.exit(0)
}

try {
  if (process.platform !== 'win32' || process.arch !== 'x64') {
    throw new Error('请在受信任的 Windows x64 验收电脑上运行此工具。')
  }
  const names = new Set(['installer', 'version', 'minimum-system-version', 'checks-file', 'out'])
  const args = new Map()
  for (let i = 2; i < process.argv.length; i += 2) {
    const key = process.argv[i]?.replace(/^--/, '')
    const value = process.argv[i + 1]
    if (!process.argv[i]?.startsWith('--') || !names.has(key) || args.has(key) || !value || value.startsWith('--')) {
      throw new Error('参数无效或重复；使用 --help 查看用法。')
    }
    args.set(key, value)
  }
  if ([...names].some(name => !args.has(name))) throw new Error('缺少必填参数；使用 --help 查看用法。')
  const keyFile = process.env.BELLOBUDDY_RELEASE_VERIFIER_PRIVATE_KEY_FILE
  const publisher = process.env.BELLOBUDDY_RELEASE_PUBLISHER?.trim()
  if (!keyFile || !publisher) throw new Error('缺少私钥文件路径或代码签名发布者环境变量。')
  const keyInfo = statSync(keyFile)
  if (!keyInfo.isFile() || keyInfo.size > 65536) throw new Error('验收私钥文件无效或过大。')
  const privateKey = createPrivateKey({
    key: readFileSync(keyFile),
    format: 'pem',
    passphrase: process.env.BELLOBUDDY_RELEASE_VERIFIER_PRIVATE_KEY_PASSPHRASE,
  })
  const bits = privateKey.asymmetricKeyDetails?.modulusLength ?? 0
  if (privateKey.asymmetricKeyType !== 'rsa' || bits < 2048 || bits > 8192) {
    throw new Error('验收私钥必须为 2048 至 8192 位 RSA 密钥。')
  }
  const script = join(dirname(fileURLToPath(import.meta.url)), 'collect-windows-acceptance.ps1')
  const collected = spawnSync('powershell.exe', [
    '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', script,
    '-Installer', args.get('installer'), '-Version', args.get('version'),
    '-MinimumSystemVersion', args.get('minimum-system-version'), '-ChecksFile', args.get('checks-file'),
    '-ExpectedPublisher', publisher,
  ], { encoding: 'utf8', timeout: 120000, maxBuffer: 128 * 1024, windowsHide: true })
  if (collected.error || collected.status !== 0) {
    // 不打印 PowerShell 的任意输入、路径、私钥内容或潜在敏感验收记录。
    throw new Error('Windows 验收采集失败；请核对安装器的有效签名、证书 Subject、版本和四项验收记录。')
  }
  const payload = Buffer.from(collected.stdout.replace(/^\uFEFF/, '').trim(), 'utf8')
  if (payload.byteLength > 65536) throw new Error('验收报告过大。')
  const parsed = JSON.parse(payload)
  if (parsed.schema_version !== 1 || parsed.product !== 'bellobuddy' || parsed.version !== args.get('version') || parsed.authenticode?.publisher !== publisher) {
    throw new Error('Windows 验收采集结果与请求不符。')
  }
  const signature = sign('RSA-SHA256', payload, { key: privateKey, padding: constants.RSA_PKCS1_PADDING })
  const envelope = { payload: payload.toString('base64'), signature: signature.toString('base64') }
  writeFileSync(args.get('out'), `${JSON.stringify(envelope, null, 2)}\n`, { flag: 'wx', mode: 0o600 })
  process.stdout.write('已保存签名验收报告；验收结果由指定可信验收人员负责，D-PIN 服务端将独立校验签名和制品绑定。\n')
} catch (error) {
  // Node 的密钥解析错误可能包含内部信息，统一输出本工具明确的领域消息。
  const message = error instanceof Error && (error.message.startsWith('请') || error.message.startsWith('参数') || error.message.startsWith('缺少') || error.message.startsWith('验收') || error.message.startsWith('Windows') || error.message.startsWith('已'))
    ? error.message : '生成报告失败，请检查参数、私钥文件格式与输出路径（不能覆盖已有文件）。'
  process.stderr.write(`${message}\n`)
  process.exitCode = 1
}
