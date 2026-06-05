// 商品（Week 2 接口就绪后完善）
export interface Product {
  id: number
  name: string
  description: string
  category: string
  icon?: string
  plans: Plan[]
  created_at: string
}

// 套餐
export interface Plan {
  id: number
  product_id: number
  name: string
  description?: string
  prices: Price[]
}

// 价格（按用户角色/会员等级）
export interface Price {
  id: number
  plan_id: number
  amount: number       // 单位：分
  currency: string
  billing_cycle: 'monthly' | 'yearly' | 'once'
  user_role?: string   // 适用用户角色
}
