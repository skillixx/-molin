export function formatDateTime(value?: string | null) {
  if (!value) return '--'
  return new Date(value).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function displayAmount(value?: string | null, currency = 'CNY') {
  const symbol = currency === 'CNY' ? '¥' : currency
  return `${symbol} ${value ?? '0'}`
}

export function isPositiveAmount(value: string) {
  if (!/^\d+(\.\d{1,6})?$/.test(value)) return false
  return value.replace('.', '').replace(/^0+/, '') !== ''
}

export function productStatusLabel(status: string) {
  const map: Record<string, string> = { draft: '草稿', active: '已上架', inactive: '已下架' }
  return map[status] ?? status
}

export function billingTypeLabel(type: string) {
  const map: Record<string, string> = { one_time: '一次性', monthly: '月付', yearly: '年付', usage: '按量' }
  return map[type] ?? type
}

export function orderStatusLabel(status: string) {
  const map: Record<string, string> = {
    pending: '待支付',
    paid: '已支付',
    cancelled: '已取消',
    failed: '支付失败',
    refunded: '已退款',
  }
  return map[status] ?? status
}

export function orderTypeLabel(type: string) {
  const map: Record<string, string> = { product: '商品订单', recharge: '充值订单' }
  return map[type] ?? type
}

export function txTypeLabel(type: string) {
  const map: Record<string, string> = {
    recharge: '充值',
    consume: '消费',
    refund: '退款',
    freeze: '冻结',
    unfreeze: '解冻',
  }
  return map[type] ?? type
}

export function txDirectionLabel(direction: string) {
  const map: Record<string, string> = { in: '入账', out: '出账' }
  return map[direction] ?? direction
}

export function assetStatusLabel(status: string) {
  const map: Record<string, string> = {
    active: '生效中',
    suspended: '已冻结',
    expired: '已过期',
    cancelled: '已取消',
  }
  return map[status] ?? status
}

export function membershipStatusLabel(status: string) {
  const map: Record<string, string> = {
    active: '生效中',
    inactive: '已停用',
    cancelled: '已取消',
    expired: '已过期',
  }
  return map[status] ?? status
}

export function contentStatusLabel(status: string) {
  const map: Record<string, string> = {
    draft: '草稿',
    published: '已发布',
    offline: '已下线',
    active: '启用',
    inactive: '停用',
  }
  return map[status] ?? status
}

export function visibleScopeLabel(scope: string) {
  const map: Record<string, string> = {
    all: '全部用户',
    roles: '指定角色',
    members: '会员用户',
    admins: '管理员',
  }
  return map[scope] ?? scope
}

export function appStatusLabel(status: string) {
  const map: Record<string, string> = {
    draft: '草稿',
    active: '启用',
    inactive: '停用',
    archived: '已归档',
  }
  return map[status] ?? status
}
