/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** 面向客户公开的墨灵 AI 网关地址；未配置时使用当前站点来源。 */
  readonly VITE_AI_GATEWAY_BASE_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
