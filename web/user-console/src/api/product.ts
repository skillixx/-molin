import http from './http'
import type { Product, ProductPlan, PurchaseResult } from '@/types/product'
import type { PageResult } from '@/types/api'

export function listProducts(params: {
  product_type?: string
  keyword?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResult<Product>>('/products', { params })
}

export function getProduct(id: number) {
  return http.get<unknown, { product: Product; plans: ProductPlan[] }>(`/products/${id}`)
}

export function getProductPlans(id: number) {
  return http.get<unknown, PageResult<ProductPlan>>(`/products/${id}/plans`)
}

export function purchaseProduct(
  id: number,
  body: { plan_id: number; quantity: number; remark?: string },
  idempotencyKey: string,
) {
  return http.post<unknown, PurchaseResult>(`/products/${id}/purchase`, body, {
    headers: { 'Idempotency-Key': idempotencyKey },
  })
}
