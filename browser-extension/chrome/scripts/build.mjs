import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { build } from 'esbuild';

const currentDir = path.dirname(fileURLToPath(import.meta.url));
const projectDir = path.resolve(currentDir, '..');
const distDir = path.join(projectDir, 'dist');

await rm(distDir, { force: true, recursive: true });
await mkdir(distDir, { recursive: true });

await Promise.all([
  build({
    entryPoints: [path.join(projectDir, 'src', 'background.ts')],
    outfile: path.join(distDir, 'background.js'),
    bundle: true,
    format: 'esm',
    platform: 'browser',
    target: 'chrome120',
    sourcemap: false,
  }),
  build({
    entryPoints: [path.join(projectDir, 'src', 'content.ts')],
    outfile: path.join(distDir, 'content.js'),
    bundle: true,
    format: 'iife',
    platform: 'browser',
    target: 'chrome120',
    sourcemap: false,
  }),
  build({
    entryPoints: [path.join(projectDir, 'src', 'page_bridge.ts')],
    outfile: path.join(distDir, 'page_bridge.js'),
    bundle: true,
    format: 'iife',
    platform: 'browser',
    target: 'chrome120',
    sourcemap: false,
  }),
]);

const manifest = await readFile(path.join(projectDir, 'manifest.json'), 'utf8');
await writeFile(path.join(distDir, 'manifest.json'), manifest);