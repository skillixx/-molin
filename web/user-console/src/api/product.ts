/**
 * 商品相关 API（Week 2 接口就绪后完善）
 * TODO: Week 2 接入 GET /api/products
 */
import http from './http'
import type { Product } from '@/types/product'
import type { PageResponse } from '@/types/api'

// 获取商品列表（Week 2 后端 B 实现）
export function listProducts(params?: { page?: number; page_size?: number; category?: string }) {
  return http.get<unknown, PageResponse<Product>>('/products', { params })
}

// 获取商品详情
export function getProduct(id: number) {
  return http.get<unknown, Product>(`/products/${id}`)
}
