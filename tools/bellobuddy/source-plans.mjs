import fs from 'node:fs/promises'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

// 首批登记的真实来源保持不变；云端运行不再保留 renderer/worker 的本地方案索引。
// 迁移读取历史样例常量及两份完整JSON，不依赖本地实际执行回退，也不扫描目录。
export async function readRegisteredBelloBuddyPlans(sourceRoot) {
  const directory = path.join(sourceRoot, 'packages/boss-auto-browse-and-chat')
  const { LOCAL_FILTER_DEFINITIONS } = await import(pathToFileURL(path.join(directory, 'local-filter-executor.mjs')).href)
  if (!Array.isArray(LOCAL_FILTER_DEFINITIONS)) throw new Error('真实筛选来源未导出完整方案数组')
  const filters = LOCAL_FILTER_DEFINITIONS.filter(plan => plan.id === '6ab3798d5bb2ebb190eb01f7')
  if (filters.length !== 1) throw new Error('首批登记筛选来源缺失或重复')
  const chats = await Promise.all(['local-beiliao-plans.json', 'local-beiliao-after-sales-2.json'].map(async file => JSON.parse(await fs.readFile(path.join(directory, file), 'utf8'))))
  const items = [...filters.map(content => ({ kind: 'filter', content })), ...chats.map(content => ({ kind: 'communication', content }))]
  const seen = new Set()
  const filterIDs = new Set(filters.map(plan => plan.id))
  return items.map(({ kind, content }) => {
    const key = `${kind}:${content.id}`
    const name = kind === 'filter' ? content.name : content.scheme_name
    const marker = kind === 'filter' ? 'FilterDef' : 'BeiliaoConfig'
    if (typeof content.id !== 'string' || !content.id || !name || typeof content.disabled !== 'boolean' || !content.schema_defs?.includes(marker) || seen.has(key)) {
      throw new Error('有效方案的 ID、名称、类型或启用状态无效')
    }
    seen.add(key)
    const filterSourceID = kind === 'communication' ? content.filter_def?.id || '' : ''
    if (filterSourceID && !filterIDs.has(filterSourceID)) throw new Error(`沟通方案引用了未登记筛选方案：${content.id}`)
    return { source_id: content.id, kind, name, enabled: !content.disabled, schema_version: 1, filter_source_id: filterSourceID, content }
  })
}
