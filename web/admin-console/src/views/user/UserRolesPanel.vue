<template>
  <!-- 用户授权管理面板（嵌入用户管理对话框中使用） -->
  <div class="roles-panel">
    <el-tabs v-model="activeTab" @tab-change="handleTabChange">
      <el-tab-pane label="用户角色" name="roles">
        <div class="section">
          <div class="section-header">
            <span class="section-title">已分配角色</span>
            <el-button size="small" type="primary" :icon="Plus" @click="showAssignDialog = true">
              分配角色
            </el-button>
          </div>

          <el-table :data="userRoles" v-loading="loadingRoles" size="small" border>
            <el-table-column label="角色名称">
              <template #default="{ row }">{{ getRoleName(row) }}</template>
            </el-table-column>
            <el-table-column label="角色代码" width="150">
              <template #default="{ row }">{{ getRoleCode(row) }}</template>
            </el-table-column>
            <el-table-column prop="reason" label="备注" min-width="120" />
            <el-table-column prop="created_at" label="分配时间" width="160">
              <template #default="{ row }">
                {{ formatDate(row.created_at) }}
              </template>
            </el-table-column>
            <el-table-column label="操作" width="80">
              <template #default="{ row }">
                <el-button size="small" type="danger" text @click="handleRevokeRole(row)">
                  撤销
                </el-button>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </el-tab-pane>

      <el-tab-pane label="权限覆盖" name="overrides">
        <div class="section">
          <div class="section-header">
            <span class="section-title">用户权限覆盖</span>
            <el-button size="small" type="primary" :icon="Plus" @click="openOverrideDialog">
              新增覆盖
            </el-button>
          </div>

          <el-table :data="permissionOverrides" v-loading="loadingOverrides" size="small" border>
            <el-table-column label="权限代码" min-width="180">
              <template #default="{ row }">{{ getOverrideCode(row) }}</template>
            </el-table-column>
            <el-table-column label="效果" width="100">
              <template #default="{ row }">
                <el-tag :type="row.effect === 'allow' ? 'success' : 'danger'" size="small">
                  {{ row.effect === 'allow' ? '允许' : '拒绝' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="reason" label="原因" min-width="140" />
            <el-table-column label="过期时间" width="160">
              <template #default="{ row }">{{ row.expires_at ? formatDate(row.expires_at) : '长期有效' }}</template>
            </el-table-column>
            <el-table-column label="操作" width="80">
              <template #default="{ row }">
                <el-button size="small" type="danger" text @click="handleDeleteOverride(row)">
                  删除
                </el-button>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </el-tab-pane>

      <el-tab-pane label="生效权限" name="effective">
        <div class="section">
          <div class="section-header">
            <span class="section-title">最终生效权限</span>
            <el-button size="small" :icon="Refresh" @click="fetchEffectivePermissions">刷新</el-button>
          </div>

          <div v-loading="loadingEffective" class="effective-panel">
            <div class="permission-tags">
              <el-tag
                v-for="code in effectiveCodes"
                :key="code"
                size="small"
                type="info"
              >
                {{ code }}
              </el-tag>
              <el-empty v-if="effectiveCodes.length === 0 && !loadingEffective" description="暂无生效权限" :image-size="72" />
            </div>

            <el-divider content-position="left">覆盖明细</el-divider>
            <el-table :data="effectiveOverrides" size="small" border>
              <el-table-column label="权限代码" min-width="180">
                <template #default="{ row }">{{ row.permission_code || row.code || '--' }}</template>
              </el-table-column>
              <el-table-column label="效果" width="100">
                <template #default="{ row }">
                  <el-tag :type="row.effect === 'allow' ? 'success' : 'danger'" size="small">
                    {{ row.effect === 'allow' ? '允许' : '拒绝' }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="source" label="来源" min-width="140" />
            </el-table>
          </div>
        </div>
      </el-tab-pane>
    </el-tabs>

    <!-- 分配角色对话框 -->
    <el-dialog
      v-model="showAssignDialog"
      title="分配角色"
      width="460px"
      append-to-body
    >
      <el-form :model="assignForm" label-width="80px">
        <el-form-item label="选择角色" required>
          <el-select
            v-model="assignForm.role_id"
            placeholder="请选择角色"
            filterable
            style="width: 100%"
          >
            <el-option
              v-for="role in allRoles"
              :key="role.id"
              :label="role.name"
              :value="role.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="分配原因">
          <el-input
            v-model="assignForm.reason"
            placeholder="请填写分配原因（可选）"
            maxlength="200"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showAssignDialog = false">取消</el-button>
        <el-button type="primary" :loading="assigning" @click="handleAssignRole">确认分配</el-button>
      </template>
    </el-dialog>

    <!-- 新增权限覆盖对话框 -->
    <el-dialog
      v-model="showOverrideDialog"
      title="新增权限覆盖"
      width="520px"
      append-to-body
    >
      <el-form :model="overrideForm" label-width="90px">
        <el-form-item label="权限代码" required>
          <el-select
            v-model="overrideForm.permission_id"
            placeholder="请选择权限"
            filterable
            style="width: 100%"
          >
            <el-option
              v-for="permission in allPermissions"
              :key="permission.id"
              :label="`${permission.name}（${permission.code}）`"
              :value="permission.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="覆盖效果" required>
          <el-radio-group v-model="overrideForm.effect">
            <el-radio-button label="allow">允许</el-radio-button>
            <el-radio-button label="deny">拒绝</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="原因">
          <el-input
            v-model="overrideForm.reason"
            type="textarea"
            :rows="3"
            placeholder="请填写授权或禁用原因"
            maxlength="200"
            show-word-limit
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showOverrideDialog = false">取消</el-button>
        <el-button type="primary" :loading="savingOverride" @click="handleSetOverride">
          确认保存
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessageBox, ElMessage } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import {
  assignRoleToUser,
  deletePermissionOverride,
  getUserEffectivePermissions,
  listPermissionOverrides,
  listPermissions,
  listRoles,
  listUserRoles,
  revokeUserRole,
  setPermissionOverride,
} from '@/api/role'
import type { EffectivePermission, Permission, PermissionOverride, Role, UserRole } from '@/types/user'

const props = defineProps<{ userId: number }>()

const activeTab = ref('roles')
const loadingRoles = ref(false)
const userRoles = ref<UserRole[]>([])
const allRoles = ref<Role[]>([])
const allPermissions = ref<Permission[]>([])
const showAssignDialog = ref(false)
const assigning = ref(false)
const assignForm = reactive({ role_id: 0, reason: '' })
const loadingOverrides = ref(false)
const permissionOverrides = ref<PermissionOverride[]>([])
const showOverrideDialog = ref(false)
const savingOverride = ref(false)
const overrideForm = reactive({
  permission_id: 0,
  effect: 'allow' as 'allow' | 'deny',
  reason: '',
})
const loadingEffective = ref(false)
const effectiveCodes = ref<string[]>([])
const effectiveOverrides = ref<EffectivePermission['overrides']>([])

onMounted(async () => {
  await Promise.all([fetchUserRoles(), fetchAllRoles(), fetchAllPermissions()])
})

async function handleTabChange(name: string | number) {
  if (name === 'overrides') {
    await fetchPermissionOverrides()
  }
  if (name === 'effective') {
    await fetchEffectivePermissions()
  }
}

async function fetchUserRoles() {
  loadingRoles.value = true
  try {
    const res = await listUserRoles(props.userId, { page: 1, page_size: 100 })
    userRoles.value = res.items
  } finally {
    loadingRoles.value = false
  }
}

async function fetchAllRoles() {
  const res = await listRoles({ page: 1, page_size: 100 })
  allRoles.value = res.items
}

async function fetchAllPermissions() {
  const res = await listPermissions({ page: 1, page_size: 500 })
  allPermissions.value = res.items
}

async function fetchPermissionOverrides() {
  loadingOverrides.value = true
  try {
    const res = await listPermissionOverrides(props.userId, { page: 1, page_size: 100 })
    permissionOverrides.value = res.items
  } finally {
    loadingOverrides.value = false
  }
}

async function fetchEffectivePermissions() {
  loadingEffective.value = true
  try {
    const res = await getUserEffectivePermissions(props.userId)
    effectiveCodes.value = res.permissions ?? res.codes ?? []
    effectiveOverrides.value = res.overrides ?? []
  } finally {
    loadingEffective.value = false
  }
}

async function handleAssignRole() {
  if (!assignForm.role_id) {
    ElMessage.warning('请选择角色')
    return
  }
  assigning.value = true
  try {
    await assignRoleToUser(props.userId, {
      role_id: assignForm.role_id,
      reason: assignForm.reason || '管理员手动分配',
    })
    ElMessage.success('角色分配成功')
    showAssignDialog.value = false
    assignForm.role_id = 0
    assignForm.reason = ''
    await fetchUserRoles()
  } finally {
    assigning.value = false
  }
}

function openOverrideDialog() {
  overrideForm.permission_id = 0
  overrideForm.effect = 'allow'
  overrideForm.reason = ''
  showOverrideDialog.value = true
}

async function handleSetOverride() {
  if (!overrideForm.permission_id) {
    ElMessage.warning('请选择权限')
    return
  }
  savingOverride.value = true
  try {
    await setPermissionOverride(props.userId, {
      permission_id: overrideForm.permission_id,
      effect: overrideForm.effect,
      reason: overrideForm.reason || '管理员手动设置',
    })
    ElMessage.success('权限覆盖已保存')
    showOverrideDialog.value = false
    await fetchPermissionOverrides()
  } finally {
    savingOverride.value = false
  }
}

async function handleDeleteOverride(override: PermissionOverride) {
  try {
    await ElMessageBox.confirm(
      `确认删除权限覆盖「${getOverrideCode(override)}」？`,
      '确认删除',
      { confirmButtonText: '确认删除', cancelButtonText: '取消', type: 'warning' }
    )
    await deletePermissionOverride(props.userId, override.id)
    ElMessage.success('删除成功')
    await fetchPermissionOverrides()
  } catch {
    // 取消
  }
}

async function handleRevokeRole(userRole: UserRole) {
  try {
    await ElMessageBox.confirm(
      `确认撤销角色「${getRoleName(userRole)}」？`,
      '确认撤销',
      { confirmButtonText: '确认撤销', cancelButtonText: '取消', type: 'warning' }
    )
    await revokeUserRole(props.userId, userRole.role_id ?? userRole.id)
    ElMessage.success('撤销成功')
    await fetchUserRoles()
  } catch {
    // 取消
  }
}

function getRoleName(userRole: UserRole) {
  return userRole.role?.name || userRole.role_name || userRole.name || '--'
}

function getRoleCode(userRole: UserRole) {
  return userRole.role?.code || userRole.role_code || userRole.code || '--'
}

function getOverrideCode(override: PermissionOverride) {
  return override.permission?.code || override.permission_code || '--'
}

function formatDate(dateStr: string) {
  if (!dateStr) return '--'
  return new Date(dateStr).toLocaleString('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  })
}
</script>

<style scoped>
.roles-panel {
  min-height: 320px;
}

.section {
  margin-bottom: 24px;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.section-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--mc-text);
}

.effective-panel {
  min-height: 180px;
}

.permission-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  min-height: 60px;
}
</style>
