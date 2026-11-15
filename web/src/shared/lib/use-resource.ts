import { useEffect, useEffectEvent, useState } from 'react'
import { Resource } from './resource'
import { useStore } from './use-store'

// 挂载时和 key 变化时读取，卸载之后 reload、refresh 和 set 都不再生效。request 失败时抛出 Error，message 是给用户看的原因。
// key 变化的那次渲染就换成新的 Resource，所以不会先显示上一个 key 读到的结果。
export function useResource<T>(request: () => Promise<T>, key?: unknown) {
  const [current, setCurrent] = useState(() => ({ key, resource: new Resource<T>() }))
  let { resource } = current
  if (!Object.is(current.key, key)) {
    resource = new Resource<T>()
    setCurrent({ key, resource })
  }
  const state = useStore(resource, s => s.current)
  const open = useEffectEvent((target: Resource<T>) => target.open(request))
  useEffect(() => {
    open(resource)
    return () => resource.close()
  }, [resource])
  return {
    state,
    reload: () => resource.read(request),
    refresh: () => resource.refresh(request),
    set: (value: T) => resource.set(value),
  }
}
