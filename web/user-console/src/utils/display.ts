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

export function isUnpriced(user_price?: string | null) {
  return user_price === '-1'
}

export function productStatusLabel(status: string) {
  const map: Record<string, string> = {
    active: '已上架',
    inactive: '已下架',
    draft: '草稿',
  }
  return map[status] ?? status
}

export function billingTypeLabel(type: string) {
  const map: Record<string, string> = {
    one_time: '一次性',
    monthly: '月付',
    yearly: '年付',
    usage: '按量',
  }
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
  const map: Record<string, string> = {
    product: '商品订单',
    recharge: '充值订单',
  }
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
  const map: Record<string, string> = {
    in: '入账',
    out: '出账',
  }
  return map[direction] ?? direction
}

export function getErrorCode(error: unknown) {
  const err = error as { response?: { status?: number; data?: { code?: number; message?: string } } }
  return {
    status: err.response?.status,
    code: err.response?.data?.code,
    message: err.response?.data?.message,
  }
}
