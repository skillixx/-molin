import http from './http'
import type { AccessItem, AdminPlan, AdminProduct, PriceItem } from '@/types/product-admin'
import type { PageResult } from '@/types/api'

export function listAdminProducts(params: {
  keyword?: string
  status?: string
  type?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<AdminProduct>>('/admin/products', { params })
}

export function createProduct(data: {
  product_type: string
  product_code: string
  name: string
  description?: string
  status?: string
  business_ref_id?: number | null
}) {
  return http.post<unknown, AdminProduct>('/admin/products', data)
}

export function getAdminProduct(id: number) {
  return http.get<unknown, AdminProduct>(`/admin/products/${id}`)
}

export function updateProduct(
  id: number,
  data: { name?: string; description?: string | null; business_ref_id?: number | null }
) {
  return http.patch<unknown, { message: string }>(`/admin/products/${id}`, data)
}

export function updateProductStatus(id: number, status: 'active' | 'inactive') {
  return http.patch<unknown, { message: string }>(`/admin/products/${id}/status`, { status })
}

export function listPlans(productId: number) {
  return http.get<unknown, PageResult<AdminPlan>>(`/admin/products/${productId}/plans`)
}

export function createPlan(
  productId: number,
  data: {
    plan_code: string
    name: string
    billing_type: string
    duration_days?: number | null
    quota_json?: string | null
    status?: string
  }
) {
  return http.post<unknown, AdminPlan>(`/admin/products/${productId}/plans`, data)
}

export function updatePlan(
  productId: number,
  planId: number,
  data: {
    name?: string
    billing_type?: string
    duration_days?: number | null
    quota_json?: string | null
    status?: string
  }
) {
  return http.patch<unknown, { message: string }>(`/admin/products/${productId}/plans/${planId}`, data)
}

export function replaceAccess(productId: number, items: AccessItem[]) {
  return http.patch<unknown, { message: string }>(`/admin/products/${productId}/access`, { items })
}

export function replacePrices(productId: number, items: PriceItem[]) {
  return http.patch<unknown, { message: string }>(`/admin/products/${productId}/prices`, { items })
}
