<template>
  <!-- 单个用户分组管理页面 -->
  <div class="group-manage-page">
    <div class="page-header">
      <div class="header-main">
        <el-button :icon="ArrowLeft" text @click="router.push('/groups')">返回列表</el-button>
        <div>
          <h3 class="page-title-text">分组管理</h3>
          <p class="page-subtitle">维护当前分组的成员、权限和邀请码</p>
        </div>
      </div>
      <el-button v-if="selectedGroup" type="primary" @click="openEditGroup">编辑分组</el-button>
    </div>

    <el-empty v-if="!selectedGroup && !loadingGroup" description="分组不存在或已被删除" :image-size="96" />

    <template v-else>
      <div v-loading="loadingGroup" class="group-info-card">
        <div class="section-header">
          <div>
            <div class="section-title">分组信息</div>
            <div class="section-subtitle">当前分组的基础资料</div>
          </div>
          <el-tag v-if="selectedGroup" :type="selectedGroup.is_default ? 'success' : 'info'" size="small">
            {{ selectedGroup.is_default ? '默认分组' : '普通分组' }}
          </el-tag>
        </div>

        <div v-if="selectedGroup" class="info-grid">
          <div class="info-item info-item--wide">
            <span class="info-label">分组名称</span>
            <span class="info-value info-value--strong">{{ selectedGroup.name }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">分组代码</span>
            <span class="info-value">{{ selectedGroup.code }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">分组类型</span>
            <span class="info-value">{{ groupTypeLabel(selectedGroup.type) }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">创建时间</span>
            <span class="info-value">{{ formatDate(selectedGroup.created_at) }}</span>
          </div>
          <div class="info-item info-item--full">
            <span class="info-label">分组描述</span>
            <span class="info-value">{{ selectedGroup.description || '暂无描述' }}</span>
          </div>
        </div>
      </div>

      <div v-if="selectedGroup" class="manage-card">
        <el-tabs v-model="activeTab" @tab-change="handleTabChange">
          <el-tab-pane label="成员管理" name="members">
            <div class="tab-toolbar">
              <el-button type="primary" size="small" :icon="Plus" @click="openAddMember">添加成员</el-button>
            </div>
            <el-table :data="members" v-loading="loadingMembers" size="small" border>
              <el-table-column prop="user_id" label="用户 ID" width="120" />
              <el-table-column label="组内角色" width="140">
                <template #default="{ row }">
                  <el-tag :type="row.group_role === 'admin' ? 'warning' : 'info'" size="small">
                    {{ groupRoleLabel(row.group_role) }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="created_at" label="加入时间" min-width="180">
                <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
              </el-table-column>
              <el-table-column label="操作" width="180" fixed="right">
                <template #default="{ row }">
                  <el-button size="small" type="primary" text @click="openEditMember(row)">改角色</el-button>
                  <el-button size="small" type="danger" text @click="handleRemoveMember(row)">移除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>

          <el-tab-pane label="组权限" name="permissions">
            <div class="tab-toolbar">
              <el-button type="primary" size="small" :icon="Plus" @click="openAddPermission">添加权限</el-button>
            </div>
            <el-table :data="groupPermissions" v-loading="loadingPermissions" size="small" border>
              <el-table-column prop="permission_code" label="权限代码" min-width="220" />
              <el-table-column prop="created_at" label="添加时间" min-width="180">
                <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
              </el-table-column>
              <el-table-column label="操作" width="100" fixed="right">
                <template #default="{ row }">
                  <el-button size="small" type="danger" text @click="handleRemovePermission(row)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>

          <el-tab-pane label="邀请码" name="invites">
            <div class="tab-toolbar">
              <el-button type="primary" size="small" :icon="Plus" @click="openCreateInvite">生成邀请码</el-button>
            </div>
            <el-table :data="inviteCodes" v-loading="loadingInvites" size="small" border>
              <el-table-column prop="code" label="邀请码" min-width="150" />
              <el-table-column label="默认角色" width="120">
                <template #default="{ row }">{{ groupRoleLabel(row.default_group_role) }}</template>
              </el-table-column>
              <el-table-column label="使用次数" width="120">
                <template #default="{ row }">{{ row.used_count }} / {{ row.max_uses || '不限' }}</template>
              </el-table-column>
              <el-table-column label="状态" width="100">
                <template #default="{ row }">
                  <el-tag :type="row.status === 'active' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="过期时间" min-width="170">
                <template #default="{ row }">{{ row.expires_at ? formatDate(row.expires_at) : '永不过期' }}</template>
              </el-table-column>
              <el-table-column label="操作" width="100" fixed="right">
                <template #default="{ row }">
                  <el-button
                    size="small"
                    type="danger"
                    text
                    :disabled="row.status !== 'active'"
                    @click="handleDisableInvite(row)"
                  >
                    停用
                  </el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>
        </el-tabs>
      </div>
    </template>

    <el-dialog v-model="groupDialogVisible" title="编辑分组" width="520px">
      <el-form ref="groupFormRef" :model="groupForm" :rules="groupRules" label-width="90px">
        <el-form-item label="分组代码" prop="code">
          <el-input v-model="groupForm.code" disabled />
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

    <el-dialog v-model="memberDialogVisible" :title="memberDialogMode === 'create' ? '添加成员' : '修改成员角色'" width="420px">
      <el-form :model="memberForm" label-width="90px">
        <el-form-item label="用户 ID" required>
          <el-input-number v-model="memberForm.user_id" :disabled="memberDialogMode === 'edit'" :min="1" style="width: 100%" />
        </el-form-item>
        <el-form-item label="组内角色" required>
          <el-select v-model="memberForm.group_role" style="width: 100%">
            <el-option label="管理员" value="admin" />
            <el-option label="成员" value="member" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="memberDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingMember" @click="handleSaveMember">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="permissionDialogVisible" title="添加组权限" width="460px">
      <el-form :model="permissionForm" label-width="90px">
        <el-form-item label="权限代码" required>
          <el-select v-model="permissionForm.permission_code" filterable placeholder="请选择权限" style="width: 100%">
            <el-option v-for="item in allPermissions" :key="item.code" :label="`${item.name}（${item.code}）`" :value="item.code" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="permissionDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingPermission" @click="handleAddPermission">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="inviteDialogVisible" title="生成邀请码" width="520px">
      <el-form :model="inviteForm" label-width="100px">
        <el-form-item label="邀请码">
          <el-input v-model="inviteForm.code" placeholder="留空由系统生成" />
        </el-form-item>
        <el-form-item label="默认角色">
          <el-select v-model="inviteForm.default_group_role" style="width: 100%">
            <el-option label="管理员" value="admin" />
            <el-option label="成员" value="member" />
          </el-select>
        </el-form-item>
        <el-form-item label="最大使用次数">
          <el-input-number v-model="inviteForm.max_uses" :min="0" style="width: 100%" />
        </el-form-item>
        <el-form-item label="过期时间">
          <el-date-picker
            v-model="inviteForm.expires_at"
            type="datetime"
            value-format="YYYY-MM-DDTHH:mm:ssZ"
            placeholder="留空永不过期"
            style="width: 100%"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="inviteDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingInvite" @click="handleCreateInvite">生成</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { ArrowLeft, Plus } from '@element-plus/icons-vue'
import {
  addGroupMember,
  addGroupPermission,
  createInviteCode,
  disableInviteCode,
  getGroup,
  listGroupMembers,
  listGroupPermissions,
  listInviteCodes,
  removeGroupMember,
  removeGroupPermission,
  updateGroup,
  updateGroupMemberRole,
} from '@/api/group'
import { listPermissions } from '@/api/role'
import type { Permission } from '@/types/user'
import type { GroupInviteCode, GroupMember, GroupPermission, GroupRole, UserGroup, UserGroupType } from '@/types/group'

const route = useRoute()
const router = useRouter()
const groupId = Number(route.params.id)

const selectedGroup = ref<UserGroup | null>(null)
const loadingGroup = ref(false)
const activeTab = ref('members')
const members = ref<GroupMember[]>([])
const groupPermissions = ref<GroupPermission[]>([])
const inviteCodes = ref<GroupInviteCode[]>([])
const allPermissions = ref<Permission[]>([])
const loadingMembers = ref(false)
const loadingPermissions = ref(false)
const loadingInvites = ref(false)

const groupDialogVisible = ref(false)
const savingGroup = ref(false)
const groupFormRef = ref<FormInstance>()
const groupForm = reactive({
  code: '',
  name: '',
  type: 'custom' as UserGroupType,
  is_default: false,
  description: '',
})
const groupRules: FormRules = {
  name: [{ required: true, message: '请输入分组名称', trigger: 'blur' }],
  type: [{ required: true, message: '请选择类型', trigger: 'change' }],
}

const memberDialogVisible = ref(false)
const memberDialogMode = ref<'create' | 'edit'>('create')
const savingMember = ref(false)
const memberForm = reactive({ user_id: 1, group_role: 'member' as GroupRole })

const permissionDialogVisible = ref(false)
const savingPermission = ref(false)
const permissionForm = reactive({ permission_code: '' })

const inviteDialogVisible = ref(false)
const savingInvite = ref(false)
const inviteForm = reactive({
  code: '',
  default_group_role: 'member' as GroupRole,
  max_uses: 0,
  expires_at: '',
})

onMounted(async () => {
  await Promise.all([fetchGroup(), fetchAllPermissions()])
  await fetchActiveTab()
})

async function fetchGroup() {
  if (!groupId) return
  loadingGroup.value = true
  try {
    selectedGroup.value = await getGroup(groupId)
  } finally {
    loadingGroup.value = false
  }
}

async function fetchAllPermissions() {
  const res = await listPermissions({ page: 1, page_size: 500 })
  allPermissions.value = res.items
}

async function fetchActiveTab() {
  if (!selectedGroup.value) return
  if (activeTab.value === 'members') await fetchMembers()
  if (activeTab.value === 'permissions') await fetchGroupPermissions()
  if (activeTab.value === 'invites') await fetchInviteCodes()
}

async function handleTabChange() {
  await fetchActiveTab()
}

async function fetchMembers() {
  loadingMembers.value = true
  try {
    const res = await listGroupMembers(groupId, { page: 1, page_size: 100 })
    members.value = res.items
  } finally {
    loadingMembers.value = false
  }
}

async function fetchGroupPermissions() {
  loadingPermissions.value = true
  try {
    const res = await listGroupPermissions(groupId, { page: 1, page_size: 100 })
    groupPermissions.value = res.items
  } finally {
    loadingPermissions.value = false
  }
}

async function fetchInviteCodes() {
  loadingInvites.value = true
  try {
    const res = await listInviteCodes(groupId, { page: 1, page_size: 100 })
    inviteCodes.value = res.items
  } finally {
    loadingInvites.value = false
  }
}

function openEditGroup() {
  if (!selectedGroup.value) return
  groupForm.code = selectedGroup.value.code
  groupForm.name = selectedGroup.value.name
  groupForm.type = selectedGroup.value.type
  groupForm.is_default = selectedGroup.value.is_default
  groupForm.description = selectedGroup.value.description || ''
  groupDialogVisible.value = true
}

async function handleSaveGroup() {
  const valid = await groupFormRef.value?.validate().catch(() => false)
  if (!valid) return
  savingGroup.value = true
  try {
    selectedGroup.value = await updateGroup(groupId, {
      name: groupForm.name,
      type: groupForm.type,
      is_default: groupForm.is_default,
      description: groupForm.description || null,
    })
    ElMessage.success('分组更新成功')
    groupDialogVisible.value = false
  } finally {
    savingGroup.value = false
  }
}

function openAddMember() {
  memberDialogMode.value = 'create'
  memberForm.user_id = 1
  memberForm.group_role = 'member'
  memberDialogVisible.value = true
}

function openEditMember(member: GroupMember) {
  memberDialogMode.value = 'edit'
  memberForm.user_id = member.user_id
  memberForm.group_role = member.group_role
  memberDialogVisible.value = true
}

async function handleSaveMember() {
  savingMember.value = true
  try {
    if (memberDialogMode.value === 'create') {
      await addGroupMember(groupId, {
        user_id: memberForm.user_id,
        group_role: memberForm.group_role,
      })
      ElMessage.success('成员已添加')
    } else {
      await updateGroupMemberRole(groupId, memberForm.user_id, {
        group_role: memberForm.group_role,
      })
      ElMessage.success('成员角色已更新')
    }
    memberDialogVisible.value = false
    await fetchMembers()
  } finally {
    savingMember.value = false
  }
}

async function handleRemoveMember(member: GroupMember) {
  try {
    await ElMessageBox.confirm(`确认移除用户 ID ${member.user_id}？`, '确认移除', {
      confirmButtonText: '确认移除',
      cancelButtonText: '取消',
      type: 'warning',
    })
    await removeGroupMember(groupId, member.user_id)
    ElMessage.success('成员已移除')
    await fetchMembers()
  } catch {
    // 取消
  }
}

function openAddPermission() {
  permissionForm.permission_code = ''
  permissionDialogVisible.value = true
}

async function handleAddPermission() {
  if (!permissionForm.permission_code) {
    ElMessage.warning('请选择权限')
    return
  }
  savingPermission.value = true
  try {
    await addGroupPermission(groupId, {
      permission_code: permissionForm.permission_code,
    })
    ElMessage.success('组权限已添加')
    permissionDialogVisible.value = false
    await fetchGroupPermissions()
  } finally {
    savingPermission.value = false
  }
}

async function handleRemovePermission(item: GroupPermission) {
  try {
    await ElMessageBox.confirm(`确认删除权限「${item.permission_code}」？`, '确认删除', {
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
    await removeGroupPermission(groupId, item.permission_code)
    ElMessage.success('组权限已删除')
    await fetchGroupPermissions()
  } catch {
    // 取消
  }
}

function openCreateInvite() {
  inviteForm.code = ''
  inviteForm.default_group_role = 'member'
  inviteForm.max_uses = 0
  inviteForm.expires_at = ''
  inviteDialogVisible.value = true
}

async function handleCreateInvite() {
  savingInvite.value = true
  try {
    await createInviteCode(groupId, {
      code: inviteForm.code || undefined,
      default_group_role: inviteForm.default_group_role,
      max_uses: inviteForm.max_uses,
      expires_at: inviteForm.expires_at || null,
    })
    ElMessage.success('邀请码已生成')
    inviteDialogVisible.value = false
    await fetchInviteCodes()
  } finally {
    savingInvite.value = false
  }
}

async function handleDisableInvite(item: GroupInviteCode) {
  await disableInviteCode(groupId, item.id)
  ElMessage.success('邀请码已停用')
  await fetchInviteCodes()
}

function groupTypeLabel(type: string) {
  const map: Record<string, string> = { region: '区域', org: '组织', custom: '自定义' }
  return map[type] ?? type
}

function groupRoleLabel(role: string) {
  const map: Record<string, string> = { admin: '管理员', member: '成员' }
  return map[role] ?? role
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
.group-manage-page { padding: 0; }
.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}
.header-main {
  display: flex;
  align-items: center;
  gap: 12px;
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
  margin: 4px 0 0;
}
.group-info-card,
.manage-card {
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  padding: 16px;
  min-width: 0;
}
.manage-card {
  margin-top: 16px;
}
.section-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
}
.section-title {
  color: var(--mc-text);
  font-size: 16px;
  font-weight: 700;
}
.section-subtitle {
  color: var(--mc-text-muted);
  font-size: 12px;
  margin-top: 4px;
}
.info-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}
.info-item {
  min-height: 72px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  background: rgba(15, 23, 42, 0.42);
  border-radius: 8px;
  padding: 12px;
}
.info-item--wide {
  grid-column: span 2;
}
.info-item--full {
  grid-column: 1 / -1;
}
.info-label {
  display: block;
  color: var(--mc-text-muted);
  font-size: 12px;
  line-height: 1.3;
  margin-bottom: 8px;
}
.info-value {
  display: block;
  color: var(--mc-text);
  font-size: 13px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}
.info-value--strong {
  font-size: 15px;
  font-weight: 700;
}
.tab-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;
}
@media (max-width: 760px) {
  .page-header,
  .header-main,
  .section-header {
    align-items: stretch;
    flex-direction: column;
  }
  .info-grid {
    grid-template-columns: 1fr;
  }
  .info-item--wide {
    grid-column: span 1;
  }
}
</style>
