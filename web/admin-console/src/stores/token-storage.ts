type TokenStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>

const ACCESS_TOKEN_KEY = 'access_token'
const LEGACY_REFRESH_TOKEN_KEY = 'refresh_token'

function browserStorage(): TokenStorage | null {
  return typeof window === 'undefined' ? null : window.localStorage
}

/** 页面刷新后只恢复 access token；refresh token 始终由当前 Pinia 实例保存在内存。 */
export function readStoredAccessToken(storage: TokenStorage | null = browserStorage()) {
  return storage?.getItem(ACCESS_TOKEN_KEY) ?? ''
}

/** 浏览器持久层只写 access token，禁止把 refresh token 作为参数传入或落盘。 */
export function writeStoredAccessToken(accessToken: string, storage: TokenStorage | null = browserStorage()) {
  if (!storage) return
  storage.setItem(ACCESS_TOKEN_KEY, accessToken)
}

/** 清理登录态时同时删除旧版本可能遗留的 refresh token，完成一次性安全迁移。 */
export function clearStoredAuthTokens(storage: TokenStorage | null = browserStorage()) {
  if (!storage) return
  storage.removeItem(ACCESS_TOKEN_KEY)
  storage.removeItem(LEGACY_REFRESH_TOKEN_KEY)
}
