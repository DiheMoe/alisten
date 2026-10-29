globalThis.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const { clients, registration: { scope } } = globalThis
  // 第一次安装时打开的页面不受 Service Worker 控制，也要找出来。
  event.waitUntil(clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windows) => {
    const app = windows.find(client => client.url.startsWith(scope))
    return app ? app.focus() : clients.openWindow(scope)
  }))
})
