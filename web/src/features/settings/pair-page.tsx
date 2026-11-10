import { useStore } from '@/shared/lib/use-store'
import { PairingCodeForm } from './pairing-dialog'
import { profileStore, switchIdentity } from './profile-store'
import './settings.css'

// 扫描配对二维码打开的页面。要用户点一下才兑换：打开链接就自动登录的话，别人发来的链接能把你登进他的账号。
// onSignedIn 在换身份、重新加载页面之前调用，由应用壳决定登录之后停在哪里。
export function PairPage({ code, onSignedIn }: { code: string, onSignedIn: () => void }) {
  const account = useStore(profileStore, s => s.account)
  return (
    <div className="page-content settings-pair-page">
      <div className="page-title">
        <h1 className="text-ui-xl">用配对码登录</h1>
      </div>
      {account
        ? <p className="text-ui-base muted">这台设备已经登录了账号。要换成其他账号，先在设置里退出登录。</p>
        : (
            <PairingCodeForm
              initialCode={code}
              onSignedIn={(memberToken) => {
                onSignedIn()
                switchIdentity(memberToken)
              }}
            />
          )}
    </div>
  )
}
