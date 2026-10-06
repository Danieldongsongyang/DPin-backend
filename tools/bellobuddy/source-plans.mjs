import fs from 'node:fs/promises'
import path from 'node:path'
import vm from 'node:vm'
import { isDeepStrictEqual } from 'node:util'

function registeredArray(source, declaration, nextMarker) {
  const start = source.indexOf(declaration)
  const begin = start < 0 ? -1 : source.indexOf('=', start) + 1
  const end = begin < 0 ? -1 : source.indexOf(nextMarker, begin)
  if (begin <= 0 || end < 0) throw new Error(`未找到方案登记边界：${declaration}`)
  const literal = source.slice(begin, end).trim().replace(/[;,]\s*$/, '')
  const value = vm.runInNewContext(`(${literal})`, Object.create(null), { timeout: 1000 })
  if (!Array.isArray(value)) throw new Error(`方案登记不是数组：${declaration}`)
  return JSON.parse(JSON.stringify(value))
}

function registeredChatFiles(source, declaration, nextMarker, prefix) {
  const start = source.indexOf(declaration)
  const end = start < 0 ? -1 : source.indexOf(nextMarker, start)
  if (start < 0 || end < 0) throw new Error('未找到沟通方案文件登记表')
  const files = [...source.slice(start, end).matchAll(/['"]([^'"]+\.json)['"]/g)].map(match => match[1].replace(prefix, ''))
  if (!files.length || new Set(files).size !== files.length || files.some(file => !/^[\w.-]+\.json$/.test(file))) {
    throw new Error('沟通方案文件登记为空、重复或包含非法路径')
  }
  return files
}

// 迁移只读取实际登记且前端、worker、执行器一致的对象；不扫描目录猜测有效方案。
export async function readRegisteredBelloBuddyPlans(sourceRoot) {
  const read = relative => fs.readFile(path.join(sourceRoot, relative), 'utf8')
  const [renderer, worker, runtime, filterRuntime] = await Promise.all([
    read('app/src/renderer/assets/index-C9ubNdld.js'),
    read('app/src/main/index-CcSlIXko.js'),
    read('packages/boss-auto-browse-and-chat/local-beiliao-runtime.mjs'),
    read('packages/boss-auto-browse-and-chat/local-filter-executor.mjs')
  ])
  const filters = registeredArray(filterRuntime, 'export const LOCAL_FILTER_DEFINITIONS = [', 'const definitionsById =')
  const rendererFilters = registeredArray(renderer, 'const LOCAL_FILTER_DEFINITIONS = [', 'LOCAL_FILTER_BY_ID =')
  const rendererChats = registeredArray(renderer, 'const LOCAL_BEILIAO_DEFINITIONS = [', 'const LOCAL_BEILIAO_BY_ID =')
  const workerFiles = registeredChatFiles(worker, 'const localBeiliaoPlans = [', 'const localBeiliaoPlanIds', '@bellobuddy/boss-auto-browse-and-chat/')
  const runtimeFiles = registeredChatFiles(runtime, 'const localPlans = [', '].map((file)', './')
  if (!isDeepStrictEqual(workerFiles, runtimeFiles)) throw new Error('worker 与沟通执行器的登记文件不一致')
  const chats = await Promise.all(workerFiles.map(async file => JSON.parse(await read(`packages/boss-auto-browse-and-chat/${file}`))))
  if (!isDeepStrictEqual(filters, rendererFilters) || !isDeepStrictEqual(chats, rendererChats)) {
    throw new Error('前端与执行端完整方案不一致，停止导入')
  }
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
