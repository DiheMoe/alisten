import type { SettingSave } from '@/shared/lib/setting-save'
import { Check } from 'lucide-react'

// 外层一直在文档里，读屏软件才能读出它的变化。version 每次保存成功都变，连续保存时“已保存”重新计时淡出。
export function SaveStatus({ status, version = 0, savingText = '保存中…' }: { status: SettingSave<unknown>['status'], version?: number, savingText?: string }) {
  return (
    <span className="save-status text-ui-caption" role="status">
      {status === 'saving' && <span className="save-status-saving">{savingText}</span>}
      {status === 'saved' && (
        <span key={version} className="save-status-saved">
          <Check size={14} strokeWidth={2.2} aria-hidden="true" />
          已保存
        </span>
      )}
    </span>
  )
}
