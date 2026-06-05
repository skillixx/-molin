// 商品、套餐、价格相关类型定义

/** 商品状态 */
export type ProductStatus = 'active' | 'inactive' | 'draft'

/** 商品信息 */
export interface Product {
  id: number
  name: string
  description: string
  status: ProductStatus
  created_at: string
  updated_at: string
}

/** 套餐信息 */
export interface Plan {
  id: number
  product_id: number
  name: string
  description: string
  status: ProductStatus
  created_at: string
  updated_at: string
}

/** 价格分层 */
export interface Price {
  id: number
  plan_id: number
  amount: number
  currency: string
  billing_cycle: 'monthly' | 'quarterly' | 'yearly' | 'once'
  created_at: string
}
