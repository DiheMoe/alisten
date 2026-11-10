import { openAccountSettings } from './account-settings'

export function AccountPrompt({ message, onClose }: { message: string, onClose: () => void }) {
  return (
    <>
      <p className="text-ui-base muted">{message}</p>
      <div className="dialog-actions">
        <button
          type="button"
          className="button-primary"
          onClick={() => {
            onClose()
            openAccountSettings()
          }}
        >
          去账号设置
        </button>
      </div>
    </>
  )
}
