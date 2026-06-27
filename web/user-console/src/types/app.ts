export interface MarketplaceApp {
  id: number
  code: string
  name: string
  type: string
  description: string | null
  icon_url: string | null
  access_url: string | null
  status: string
  created_at: string
}
