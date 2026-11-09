import type { LucideIcon } from 'lucide-react'
import { ArrowDownToLine, ArrowUpToLine, ListChecks, ListPlus, ListX, Trash2, X } from 'lucide-react'
import { IconButton } from '@/shared/ui'

// 窄屏只显示图标，按钮的名称要包含显示的文字。
function BarButton({ icon: Icon, text, label, onClick }: { icon: LucideIcon, text: string, label: string, onClick: () => void }) {
  return (
    <button type="button" className="playlist-bar-button" aria-label={label} onClick={onClick}>
      <Icon size={18} strokeWidth={1.7} aria-hidden="true" />
      <span>{text}</span>
    </button>
  )
}

export function SelectionBar({ count, allSelected, onSave, onTop, onBottom, onRemove, onToggleAll, onClose }: {
  count: number
  allSelected: boolean
  onSave: () => void
  onTop: () => void
  onBottom: () => void
  onRemove: () => void
  onToggleAll: () => void
  onClose: () => void
}) {
  return (
    <div className="playlist-selection-bar" role="toolbar" aria-label="选中的歌">
      <span className="playlist-selection-count text-ui-sm">{`已选 ${count} 首`}</span>
      <BarButton icon={ListPlus} text="保存到歌单" label="把选中的歌保存到歌单" onClick={onSave} />
      <BarButton icon={ArrowUpToLine} text="移到最前" label="把选中的歌移到最前" onClick={onTop} />
      <BarButton icon={ArrowDownToLine} text="移到最后" label="把选中的歌移到最后" onClick={onBottom} />
      <BarButton icon={Trash2} text="移出" label="把选中的歌移出这个歌单" onClick={onRemove} />
      <BarButton icon={allSelected ? ListX : ListChecks} text={allSelected ? '取消全选' : '全选'} label={allSelected ? '取消全选' : '全选'} onClick={onToggleAll} />
      <IconButton icon={X} className="playlist-bar-close" label="退出选择" onClick={onClose} />
    </div>
  )
}
