// 游客在保存歌曲或歌单的对话框里点“去账号设置”时打开账号设置。设置页在哪里、怎样打开由应用壳决定，启动时设置。
let open = (): void => {}

export function openAccountSettings(): void {
  open()
}

export function setAccountSettingsOpener(opener: () => void): void {
  open = opener
}
