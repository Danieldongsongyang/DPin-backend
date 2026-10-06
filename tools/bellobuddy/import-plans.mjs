#!/usr/bin/env node
import fs from 'node:fs/promises'
import path from 'node:path'
import { parseArgs } from 'node:util'
import { readRegisteredBelloBuddyPlans } from './source-plans.mjs'

try {
  const { values } = parseArgs({ options: {
    'source-root': { type: 'string' },
    'base-url': { type: 'string' },
    namespace: { type: 'string', default: 'bellobuddy-local-v1' },
    'mapping-output': { type: 'string' },
    apply: { type: 'boolean', default: false }
  } })
  if (!values['source-root']) throw new Error('请通过 --source-root 指定 BelloBuddy 恢复工程目录')
  const plans = await readRegisteredBelloBuddyPlans(path.resolve(values['source-root']))
  const summary = plans.map(({ kind, source_id, name, enabled, filter_source_id }) => ({ kind, source_id, name, enabled, filter_source_id }))
  if (!values.apply) {
    console.log(JSON.stringify({ mode: 'check', namespace: values.namespace, plans: summary }, null, 2))
  } else {
    if (!values['base-url'] || !process.env.DPIN_ACCESS_TOKEN) throw new Error('实际导入需要 --base-url 和环境变量 DPIN_ACCESS_TOKEN（有效管理员产品会话）')
    const base = new URL(values['base-url'])
    const local = ['localhost', '127.0.0.1', '[::1]'].includes(base.hostname)
    if (base.username || base.password || base.search || base.hash || !['', '/'].includes(base.pathname) || (base.protocol !== 'https:' && !(base.protocol === 'http:' && local))) {
      throw new Error('服务地址必须是 HTTPS Origin；本机开发可使用 localhost HTTP')
    }
    const response = await fetch(new URL('/api/bellobuddy/admin/plans/import', base), {
      method: 'POST', redirect: 'error', signal: AbortSignal.timeout(30000),
      headers: { Authorization: `Bearer ${process.env.DPIN_ACCESS_TOKEN}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ namespace: values.namespace, plans })
    })
    const result = await response.json()
    if (!response.ok || result.success !== true || !Array.isArray(result.data)) {
      throw new Error(`DPin 导入失败：HTTP ${response.status}，${result.code || 'INVALID_RESPONSE'}`)
    }
    const expected = new Set(plans.map(plan => `${plan.kind}:${plan.source_id}`))
    const seen = new Set()
    for (const entry of result.data) {
      const key = `${entry.kind}:${entry.source_id}`
      if (!expected.has(key) || seen.has(key) || entry.namespace !== values.namespace || !Number.isSafeInteger(entry.plan_id) || entry.plan_id <= 0 || !Number.isSafeInteger(entry.version) || entry.version <= 0 || typeof entry.created !== 'boolean') {
        throw new Error('DPin 返回的来源映射无效，未生成迁移结果文件')
      }
      seen.add(key)
    }
    if (seen.size !== expected.size) throw new Error('DPin 返回的来源映射不完整')
    // 只保存迁移 ID 和版本，不输出或持久化完整正文、认证凭据。
    const mapping = { namespace: values.namespace, imported_at: new Date().toISOString(), plans: result.data }
    if (values['mapping-output']) await fs.writeFile(path.resolve(values['mapping-output']), JSON.stringify(mapping, null, 2) + '\n', { mode: 0o600 })
    console.log(JSON.stringify(mapping, null, 2))
  }
} catch (error) {
  console.error(error instanceof Error ? error.message : '方案迁移失败')
  process.exitCode = 1
}
