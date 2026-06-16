export type OrderStatus = 'pending' | 'paid' | 'cancelled' | 'failed' | 'refunded'
export type OrderType = 'product' | 'recharge'

export interface OrderItem {
  id: number
  order_id: number
  product_id: number
  product_plan_id: number
  quantity: number
  unit_price: string
  total_price: string
  created_at: string
}

export interface Order {
  id: number
  order_no: string
  user_id: number
  order_type: OrderType
  product_id: number | null
  product_plan_id: number | null
  status: OrderStatus
  amount: string
  currency: string
  paid_at: string | null
  cancelled_at: string | null
  failed_at: string | null
  remark: string | null
  created_at: string
  updated_at: string
  items?: OrderItem[]
}

export interface PayOrderResult {
  order_id: number
  status: 'paid'
  wallet_transaction_id: number
  asset_id: number
}
