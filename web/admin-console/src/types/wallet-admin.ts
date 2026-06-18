export interface Wallet {
  wallet_id: number
  user_id: number
  balance_amount: string
  frozen_amount: string
  currency: string
}

export type TxType = 'recharge' | 'consume' | 'refund' | 'freeze' | 'unfreeze'
export type TxDirection = 'in' | 'out'

export interface WalletTransaction {
  id: number
  wallet_id: number
  user_id: number
  type: TxType
  direction: TxDirection
  amount: string
  balance_after: string
  related_order_id: number | null
  remark: string
  created_at: string
}

export interface PaymentCallback {
  id: number
  order_id: number
  provider: string
  provider_trade_no: string
  status: string
  processed_at: string | null
  created_at: string
  updated_at: string
}
