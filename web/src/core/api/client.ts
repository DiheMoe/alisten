import type { Auth, CreateTicketRequest, CreateTicketResponse, ErrorResponse, HttpRequest } from './http.gen'
import type { ErrorCode, Member } from '@/core/realtime'
import { Emitter } from '@/shared/lib/emitter'
import { deleteMemberToken, loadAdminToken, loadMemberToken, saveMemberToken } from './credentials'
import { httpClient } from './http.gen'

export class ApiError extends Error {
  readonly code: ErrorCode | null
  readonly status: number

  constructor(message: string, code: ErrorCode | null, status: number) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
  }
}

// 被拒绝的请求不会重发，操作没有生效：调用方的提示应说明是哪项操作。
export class IdentityRenewedError extends ApiError {
  constructor() {
    super('刚才的操作没有生效，再试一次。', 'UNAUTHORIZED', 401)
    this.name = 'IdentityRenewedError'
  }
}

// rejected：被拒绝的 token 已删除，新身份尚未取回；renewalFailed：新身份没有取回，之后需要成员身份的请求在没有 token 时会先获取一个。
export type IdentityEvent
  = | { kind: 'rejected' }
    | { kind: 'renewed', member: Member }
    | { kind: 'renewalFailed' }

export const identityEvents = new Emitter<IdentityEvent>()

function isErrorResponse(body: unknown): body is ErrorResponse {
  if (typeof body !== 'object' || body === null || !('error' in body))
    return false
  const { error } = body
  return typeof error === 'object' && error !== null && 'code' in error && 'message' in error && typeof error.message === 'string'
}

async function responseError(response: Response): Promise<ApiError> {
  // 入口或代理自行返回的错误（如服务端不可用时的 502）不带协议定义的错误响应体。
  const body: unknown = await response.json().catch(() => null)
  if (isErrorResponse(body))
    return new ApiError(body.error.message, body.error.code, response.status)
  return new ApiError('服务器暂时不可用，请稍后再试。', null, response.status)
}

async function credential(auth: Auth): Promise<string | null> {
  switch (auth) {
    case 'none':
      return null
    case 'member':
      return memberToken()
    case 'admin':
      return loadAdminToken()
  }
}

async function sendOnce<T>(request: HttpRequest): Promise<T> {
  const headers = new Headers()
  const token = await credential(request.auth)
  if (token)
    headers.set('Authorization', `Bearer ${token}`)
  let body: string | undefined
  if (request.body !== undefined) {
    headers.set('Content-Type', 'application/json')
    body = JSON.stringify(request.body)
  }
  let response: Response
  try {
    response = await fetch(request.path, { method: request.method, headers, body })
  }
  catch {
    throw new ApiError('无法连接服务器，请检查网络后再试。', null, 0)
  }
  if (!response.ok)
    throw await responseError(response)
  if (response.status === 204)
    return undefined as T
  return await response.json() as T
}

function isUnauthorized(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'UNAUTHORIZED'
}

// 被拒绝的请求不重发：它以原来的身份发出，是否以新身份重发应由用户决定。
async function send<T>(request: HttpRequest): Promise<T> {
  // 没有保存的 token 时，请求使用的是刚获取的 token，它被拒绝并不说明原来的身份已经失效。
  const stored = request.auth === 'member' ? loadMemberToken() : null
  try {
    return await sendOnce<T>(request)
  }
  catch (error) {
    if (stored === null || !isUnauthorized(error))
      throw error
    await renewIdentity(stored)
    throw new IdentityRenewedError()
  }
}

const unrenewed = httpClient(sendOnce)
const renewing = httpClient(send)

// 换取票据返回的 UNAUTHORIZED 既可能表示成员 token 无效，也可能表示房间密码错误，需要用 GET /api/me 区分。
// 换取票据的请求本身不更换身份，否则房间密码错误也会触发更换身份。
async function createTicket(id: string, body: CreateTicketRequest): Promise<CreateTicketResponse> {
  const stored = loadMemberToken()
  try {
    return await unrenewed.createTicket(id, body)
  }
  catch (error) {
    if (stored !== null && isUnauthorized(error))
      await confirmMemberToken()
    throw error
  }
}

async function confirmMemberToken(): Promise<void> {
  try {
    await renewing.getMe()
  }
  catch (error) {
    if (error instanceof IdentityRenewedError)
      throw error
  }
}

export const api = {
  ...renewing,
  createTicket,
}

let creatingMember: Promise<string> | null = null

function memberToken(): Promise<string> {
  const stored = loadMemberToken()
  if (stored)
    return Promise.resolve(stored)
  creatingMember ??= api.createMember().then(({ memberToken }) => {
    saveMemberToken(memberToken)
    return memberToken
  }).finally(() => {
    creatingMember = null
  })
  return creatingMember
}

// 同一个 token 只更换一次身份：同时被拒绝的请求共用这一次，更换完成后才到达的拒绝也不再更换。
let renewal: { rejected: string, done: Promise<void> } | null = null

function renewIdentity(rejected: string): Promise<void> {
  if (renewal?.rejected !== rejected)
    renewal = { rejected, done: replaceIdentity(rejected) }
  return renewal.done
}

// 更换过程中用 unrenewed 读取成员：新 token 也被拒绝时不再更换，避免循环。
async function replaceIdentity(rejected: string): Promise<void> {
  // 其他标签页可能已经更换了身份，只删除被拒绝的那个 token。
  if (loadMemberToken() === rejected)
    deleteMemberToken()
  identityEvents.emit({ kind: 'rejected' })
  let member: Member
  try {
    member = (await unrenewed.getMe()).member
  }
  catch (error) {
    identityEvents.emit({ kind: 'renewalFailed' })
    throw error
  }
  identityEvents.emit({ kind: 'renewed', member })
}
