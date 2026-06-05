// 订单、钱包流水相关类型定义

/** 订单状态 */
export type OrderStatus = 'pending' | 'paid' | 'cancelled' | 'refunded'

/** 订单信息 */
export interface Order {
  id: number
  user_id: number
  plan_id: number
  amount: number
  currency: string
  status: OrderStatus
  paid_at?: string
  created_at: string
  updated_at: string
}

/** 钱包交易类型 */
export type TransactionType = 'recharge' | 'consume' | 'refund' | 'withdraw'

/** 钱包流水 */
export interface WalletTransaction {
  id: number
  user_id: number
  amount: number
  type: TransactionType
  description: string
  balance_after: number
  created_at: string
}
