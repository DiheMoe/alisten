import type { Plugin } from 'vite'
import { readFileSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { VitePWA } from 'vite-plugin-pwa'

const backend = 'http://localhost:8080'

const licenseFile = '.vite/license.json'

// 这些包被打包进前端，却不在 Vite 生成的许可证清单中：virtual:pwa-register 不归属任何包，workbox 的运行时由 workbox-build
// 内联进 workbox-*.js。补入清单后，随前端分发的许可证才完整。
const unlisted = ['vite-plugin-pwa', 'workbox-core', 'workbox-precaching', 'workbox-routing', 'workbox-strategies']

function listUnlistedLicenses(): Plugin {
  let licensePath = ''
  return {
    name: 'alisten:list-unlisted-licenses',
    apply: 'build',
    enforce: 'post',
    configResolved(config) {
      licensePath = resolve(config.root, config.build.outDir, licenseFile)
    },
    closeBundle() {
      const licenses = JSON.parse(readFileSync(licensePath, 'utf8')) as unknown[]
      for (const name of unlisted) {
        const dir = fileURLToPath(new URL(`./node_modules/${name}/`, import.meta.url))
        const pkg = JSON.parse(readFileSync(`${dir}package.json`, 'utf8')) as { version: string, license: string }
        licenses.push({ name, version: pkg.version, identifier: pkg.license, text: readFileSync(`${dir}LICENSE`, 'utf8') })
      }
      writeFileSync(licensePath, JSON.stringify(licenses, null, 2))
    },
  }
}

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      registerType: 'prompt',
      injectRegister: false,
      includeAssets: ['icon-64x64.png', 'icon-180x180.png'],
      manifest: {
        name: 'Alisten',
        short_name: 'Alisten',
        description: 'Alisten：在线音乐同步播放应用',
        theme_color: '#000000',
        background_color: '#000000',
        display: 'standalone',
        orientation: 'portrait',
        lang: 'zh-CN',
        categories: ['music', 'entertainment', 'multimedia'],
        icons: [
          { src: 'icon-192x192.png', sizes: '192x192', type: 'image/png', purpose: 'any maskable' },
          { src: 'icon-512x512.png', sizes: '512x512', type: 'image/png', purpose: 'any maskable' },
        ],
      },
      workbox: {
        // 与 nginx 一致：只有页面路径返回 index.html，/api、/ws 和 /media 照常走网络。
        // Workbox 拿路径加查询参数来匹配，nginx 只看路径，所以这里的规则末尾允许查询参数。
        navigateFallbackAllowlist: [/^\/(?:admin|settings|history)?(?:\?.*)?$/, /^\/r\/[^/?]+(?:\/(?:queue|chat|search|playlists\/[^/?]+\/[^/?]+))?(?:\?.*)?$/],
        importScripts: ['notification-click.js'],
      },
    }),
    listUnlistedLicenses(),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    proxy: {
      '/api': backend,
      '/media': backend,
      '/ws': { target: backend, ws: true },
    },
  },
  build: {
    outDir: 'dist',
    license: { fileName: licenseFile },
  },
})
