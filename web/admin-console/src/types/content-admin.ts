export type ContentStatus = 'draft' | 'published' | 'offline'
export type VisibleScope = 'all' | 'roles' | 'members' | 'admins'

export interface AdminAnnouncement {
  id: number
  title: string
  content: string
  visible_scope: VisibleScope
  target_roles_json: string | null
  status: ContentStatus
  start_at: string | null
  end_at: string | null
  sort_order: number
  created_by: number | null
  created_at: string
}

export interface HelpCategory {
  id: number
  name: string
  description: string | null
  sort_order: number
  status: 'active' | 'inactive'
}

export interface HelpArticle {
  id: number
  category_id: number
  title: string
  content: string
  sort_order: number
  status: ContentStatus
  created_by: number | null
  created_at: string
}
