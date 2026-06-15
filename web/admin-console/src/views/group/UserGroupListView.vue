<template>
  <!-- 用户分组列表页面 -->
  <div class="group-list-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">用户分组</h3>
        <p class="page-subtitle">查看和维护分组基础信息，进入管理页后再配置成员、权限和邀请码</p>
      </div>
      <el-button type="primary" :icon="Plus" @click="openCreateGroup">新建分组</el-button>
    </div>

    <SearchForm :model="searchForm" :loading="loadingGroups" @search="handleSearch" @reset="handleReset">
      <el-form-item label="关键词">
        <el-input v-model="searchForm.keyword" placeholder="分组代码 / 名称" clearable style="width: 220px" />
      </el-form-item>
    </SearchForm>

    <div class="list-card">
      <el-table :data="groups" v-loading="loadingGroups" border>
        <el-table-column prop="name" label="分组名称" min-width="160" />
        <el-table-column prop="code" label="分组代码" min-width="180" />
        <el-table-column label="类型" width="110">
          <template #default="{ row }">{{ groupTypeLabel(row.type) }}</template>
        </el-table-column>
        <el-table-column label="属性" width="110">
          <template #default="{ row }">
            <el-tag :type="row.is_default ? 'success' : 'info'" size="small">
              {{ row.is_default ? '默认' : '普通' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="description" label="描述" min-width="220">
          <template #default="{ row }">{{ row.description || '暂无描述' }}</template>
        </el-table-column>
        <el-table-column prop="created_at" label="创建时间" min-width="170">
          <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="230" fixed="right">
          <template #default="{ row }">
            <el-button size="small" type="primary" text @click="goManage(row)">管理</el-button>
            <el-button size="small" type="primary" text @click="openEditGroup(row)">编辑</el-button>
            <el-button size="small" type="danger" text @click="handleDeleteGroup(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <div v-if="groupPagination.total > 0" class="list-pagination">
        <el-pagination
          :total="groupPagination.total"
          :page-size="groupPagination.page_size"
          :current-page="groupPagination.page"
          :page-sizes="[10, 20, 50, 100]"
          background
          layout="total, sizes, prev, pager, next, jumper"
          @current-change="handleGroupPageChange"
          @size-change="handleGroupPageSizeChange"
        />
      </div>
    </div>

    <el-dialog v-model="groupDialogVisible" :title="groupDialogMode === 'create' ? '新建分组' : '编辑分组'" width="520px">
      <el-form ref="groupFormRef" :model="groupForm" :rules="groupRules" label-width="90px">
        <el-form-item label="分组代码" prop="code">
          <el-input v-model="groupForm.code" :disabled="groupDialogMode === 'edit'" placeholder="如：region-east" />
        </el-form-item>
        <el-form-item label="分组名称" prop="name">
          <el-input v-model="groupForm.name" placeholder="请输入分组名称" />
        </el-form-item>
        <el-form-item label="类型" prop="type">
          <el-select v-model="groupForm.type" style="width: 100%">
            <el-option label="区域" value="region" />
            <el-option label="组织" value="org" />
            <el-option label="自定义" value="custom" />
          </el-select>
        </el-form-item>
        <el-form-item label="默认分组">
          <el-switch v-model="groupForm.is_default" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="groupForm.description" type="textarea" :rows="3" maxlength="200" show-word-limit />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="groupDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingGroup" @click="handleSaveGroup">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import SearchForm from '@/components/common/SearchForm.vue'
import { createGroup, deleteGroup, listGroups, updateGroup } from '@/api/group'
import type { Pagination } from '@/types/api'
import type { UserGroup, UserGroupType } from '@/types/group'

const router = useRouter()
const loadingGroups = ref(false)
const groups = ref<UserGroup[]>([])
const searchForm = reactive({ keyword: '' })
const groupPagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const groupDialogVisible = ref(false)
const groupDialogMode = ref<'create' | 'edit'>('create')
const savingGroup = ref(false)
const editingGroupId = ref(0)
const groupFormRef = ref<FormInstance>()
const groupForm = reactive({
  code: '',
  name: '',
  type: 'custom' as UserGroupType,
  is_default: false,
  description: '',
})
const groupRules: FormRules = {
  code: [{ required: true, message: '请输入分组代码', trigger: 'blur' }],
  name: [{ required: true, message: '请输入分组名称', trigger: 'blur' }],
  type: [{ required: true, message: '请选择类型', trigger: 'change' }],
}

onMounted(fetchGroups)

async function fetchGroups() {
  loadingGroups.value = true
  try {
    const res = await listGroups({
      page: groupPagination.page,
      page_size: groupPagination.page_size,
      keyword: searchForm.keyword || undefined,
    })
    groups.value = res.items
    groupPagination.page = res.page
    groupPagination.page_size = res.page_size
    groupPagination.total = res.total
  } finally {
    loadingGroups.value = false
  }
}

function handleSearch() {
  groupPagination.page = 1
  fetchGroups()
}

function handleReset() {
  searchForm.keyword = ''
  groupPagination.page = 1
  fetchGroups()
}

function handleGroupPageChange(page: number) {
  groupPagination.page = page
  fetchGroups()
}

function handleGroupPageSizeChange(pageSize: number) {
  groupPagination.page = 1
  groupPagination.page_size = pageSize
  fetchGroups()
}

function goManage(group: UserGroup) {
  router.push(`/groups/${group.id}/manage`)
}

function openCreateGroup() {
  groupDialogMode.value = 'create'
  editingGroupId.value = 0
  groupForm.code = ''
  groupForm.name = ''
  groupForm.type = 'custom'
  groupForm.is_default = false
  groupForm.description = ''
  groupDialogVisible.value = true
}

function openEditGroup(group: UserGroup) {
  groupDialogMode.value = 'edit'
  editingGroupId.value = group.id
  groupForm.code = group.code
  groupForm.name = group.name
  groupForm.type = group.type
  groupForm.is_default = group.is_default
  groupForm.description = group.description || ''
  groupDialogVisible.value = true
}

async function handleSaveGroup() {
  const valid = await groupFormRef.value?.validate().catch(() => false)
  if (!valid) return
  savingGroup.value = true
  try {
    if (groupDialogMode.value === 'create') {
      await createGroup({
        code: groupForm.code,
        name: groupForm.name,
        type: groupForm.type,
        is_default: groupForm.is_default,
        description: groupForm.description || null,
      })
      ElMessage.success('分组创建成功')
    } else {
      await updateGroup(editingGroupId.value, {
        name: groupForm.name,
        type: groupForm.type,
        is_default: groupForm.is_default,
        description: groupForm.description || null,
      })
      ElMessage.success('分组更新成功')
    }
    groupDialogVisible.value = false
    await fetchGroups()
  } finally {
    savingGroup.value = false
  }
}

async function handleDeleteGroup(group: UserGroup) {
  try {
    await ElMessageBox.confirm(`确认删除分组「${group.name}」？`, '确认删除', {
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
    await deleteGroup(group.id)
    ElMessage.success('删除成功')
    await fetchGroups()
  } catch {
    // 取消
  }
}

function groupTypeLabel(type: string) {
  const map: Record<string, string> = { region: '区域', org: '组织', custom: '自定义' }
  return map[type] ?? type
}

function formatDate(dateStr: string) {
  if (!dateStr) return '--'
  return new Date(dateStr).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
</script>

<style scoped>
.group-list-page { padding: 0; }
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}
.page-title-text {
  font-size: 18px;
  font-weight: 600;
  color: var(--mc-text);
  margin: 0;
}
.page-subtitle {
  color: var(--mc-text-muted);
  font-size: 12px;
  margin: 6px 0 0;
}
.list-card {
  display: flex;
  flex-direction: column;
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  padding: 16px;
  min-width: 0;
  min-height: calc(100vh - 260px);
}
.list-pagination {
  display: flex;
  justify-content: flex-end;
  margin-top: auto;
  padding-top: 16px;
}
@media (max-width: 760px) {
  .page-header {
    flex-direction: column;
  }
  .list-pagination {
    justify-content: flex-end;
    overflow-x: auto;
  }
}
</style>
