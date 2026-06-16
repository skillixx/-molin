export interface ConsumptionRecord {
  id: number
  user_id: number
  product_id: number
  product_plan_id: number | null
  instance_id: number | null
  usage_type: string
  usage_amount: string
  usage_unit: string
  amount: string
  event_id: string
  created_at: string
}
