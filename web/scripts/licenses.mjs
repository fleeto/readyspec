import { readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const lock = JSON.parse(await readFile(resolve(root, 'package-lock.json'), 'utf8'))
const packages = Object.entries(lock.packages)
  .filter(([path, metadata]) => path && !metadata.dev)
  .map(([path]) => path)
// Tailwind contributes CSS to the distributable even though it is a build dependency.
packages.push('node_modules/tailwindcss')
const allowed = new Set(['MIT', 'ISC', 'Apache-2.0', 'BSD-2-Clause', 'BSD-3-Clause'])
const sections = ['ReadySpec Web UI — bundled dependency licenses\n']
for (const path of [...new Set(packages)].sort()) {
  const directory = resolve(root, path)
  if (!directory.startsWith(`${root}${sep}node_modules${sep}`))
    throw new Error('Invalid dependency path')
  const metadata = JSON.parse(await readFile(resolve(directory, 'package.json'), 'utf8'))
  if (!allowed.has(metadata.license))
    throw new Error(`Unapproved bundled license: ${metadata.name}: ${metadata.license}`)
  const license = await readFile(resolve(directory, 'LICENSE'), 'utf8')
  sections.push(
    `\n${'='.repeat(78)}\n${metadata.name} ${metadata.version} — ${metadata.license}\n${'='.repeat(78)}\n\n${license}`,
  )
}
await writeFile(resolve(root, '../third_party/frontend.LICENSE'), sections.join('\n'))
console.log(`Verified and collected ${packages.length} bundled frontend licenses.`)
