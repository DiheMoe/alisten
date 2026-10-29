import type { ExternalStore } from './store'
import { useCallback, useSyncExternalStore } from 'react'

// selector 只能返回 Store 中已有的值：每次返回新对象会让组件反复渲染。
export function useStore<T, S>(store: ExternalStore<T>, selector: (state: T) => S): S {
  const subscribe = useCallback((notify: () => void) => {
    store.addEventListener('change', notify)
    return () => store.removeEventListener('change', notify)
  }, [store])
  return useSyncExternalStore(subscribe, () => selector(store.state))
}
