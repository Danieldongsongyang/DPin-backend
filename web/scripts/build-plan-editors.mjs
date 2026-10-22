/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

// 与客户端使用同一份真实源码及构建器；服务部署后仅依赖生成的静态页面。
const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const belloBuddyRoot = path.resolve(
  process.env.BELLOBUDDY_ROOT ||
    path.join(webRoot, '../../bosscli/recovery/BelloBuddy')
)
const builderPath = path.join(belloBuddyRoot, 'scripts/build-plan-editors.mjs')
try {
  await fs.access(builderPath)
} catch {
  throw new Error(
    `找不到 BelloBuddy 真实编辑器构建入口：${builderPath}；请设置 BELLOBUDDY_ROOT 指向恢复工程`
  )
}
const { buildPlanEditors } = await import(pathToFileURL(builderPath).href)
const staging = await fs.mkdtemp(path.join(os.tmpdir(), 'dpin-admin-editors-'))
try {
  const sources = await buildPlanEditors(belloBuddyRoot, staging)
  if (!sources.length) throw new Error('未找到可构建的沟通与筛选编辑器源码')
  const pages = await Promise.all(
    ['communication', 'filter'].map(async (kind) => {
      const source = await fs.readFile(
        path.join(staging, 'out/renderer/plan-editors', kind, 'index.html'),
        'utf8'
      )
      // 脚本和样式已由共享构建器内联，保留精确脚本哈希；Web 页面不使用 file: 来源。
      const html = source
        .replace("style-src file: 'unsafe-inline'", "style-src 'unsafe-inline'")
        .replace('img-src file: data:', 'img-src data:')
      return { kind, html }
    })
  )
  // 两页均成功后再替换；生成目录不提交，不从旧安装包复制资源。
  const destination = path.join(webRoot, 'public/plan-editors')
  await fs.mkdir(destination, { recursive: true })
  for (const { kind, html } of pages) {
    const folder = path.join(destination, kind)
    await fs.mkdir(folder, { recursive: true })
    await fs.writeFile(path.join(folder, 'index.html'), html)
  }
  await fs.writeFile(
    path.join(destination, 'sources.json'),
    JSON.stringify({ sources }, null, 2)
  )
  console.log('已从真实源码构建 D-PIN 沟通与筛选编辑器')
} finally {
  await fs.rm(staging, { recursive: true, force: true })
}
