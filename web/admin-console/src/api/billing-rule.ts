import http from './http'
import type { BillingRule } from '@/types/billing-rule'
import type { PageResult } from '@/types/api'

export function listBillingRules(params: {
  product_id?: number
  status?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<BillingRule>>('/admin/product-billing-rules', { params })
}

export function createBillingRule(data: {
  product_id: number
  product_plan_id?: number | null
  usage_type: string
  usage_unit: string
  price_amount: string
  currency?: string
  billing_mode: string
  free_quota?: string | null
  status?: string
}) {
  return http.post<unknown, BillingRule>('/admin/product-billing-rules', data)
}

export function updateBillingRule(
  id: number,
  data: Partial<{
    usage_type: string
    usage_unit: string
    price_amount: string
    currency: string
    billing_mode: string
    free_quota: string | null
    status: string
  }>
) {
  return http.patch<unknown, { updated: boolean }>(`/admin/product-billing-rules/${id}`, data)
}
