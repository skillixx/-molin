<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Edit, Plus, Refresh, Search } from '@element-plus/icons-vue'
import {
  createMembershipBenefit,
  createMembershipLevel,
  grantUserMembership,
  listMembershipBenefits,
  listMembershipLevels,
  listUserMemberships,
  updateMembershipBenefit,
  updateMembershipLevel,
  updateUserMembership,
} from '@/api/membership-admin'
import type { AdminUserMembership, MembershipBenefit, MembershipLevel } from '@/types/membership-admin'
import type { Pagination } from '@/types/api'
import { formatDateTime, membershipStatusLabel } from '@/utils/display'

const activeTab = ref('levels')
const levelLoading = ref(false)
const benefitLoading = ref(false)
const userMembershipLoading = ref(false)
const levels = ref<MembershipLevel[]>([])
const benefits = ref<MembershipBenefit[]>([])
const userMemberships = ref<AdminUserMembership[]>([])
const selectedLevelId = ref<number>()
const userFilter = reactive({ user_id: undefined as number | undefined })
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const levelDialogVisible = ref(false)
const editingLevel = ref<MembershipLevel | null>(null)
const levelSaving = ref(false)
const levelForm = reactive({
  level_code: '',
  name: '',
  description: '',
  sort_order: 0,
  status: 'active',
})

const benefitDialogVisible = ref(false)
const editingBenefit = ref<MembershipBenefit | null>(null)
const benefitSaving = ref(false)
const benefitForm = reactive({
  level_id: undefined as number | undefined,
  benefit_type: '',
  benefit_value: '',
  status: 'active',
})

const grantDialogVisible = ref(false)
const granting = ref(false)
const grantForm = reactive({
  user_id: undefined as number | undefined,
  level_id: undefined as number | undefined,
  permanent: true,
  duration_days: undefined as number | undefined,
})

const expireDialogVisible = ref(false)
const expiring = ref(false)
const selectedMembership = ref<AdminUserMembership | null>(null)
const expireForm = reactive({ expires_at: '' })

const levelOptions = computed(() => levels.value.map(level => ({ label: `${level.name}（${level.level_code}）`, value: level.id })))

onMounted(async () => {
  await Promise.all([fetchLevels(), fetchUserMemberships()])
})

async function fetchLevels() {
  levelLoading.value = true
  try {
    // 会员等级是不分页列表，首次加载后默认选中第一个等级，方便权益页直接查询。
    const res = await listMembershipLevels()
    levels.value = res.items
    if (!selectedLevelId.value && levels.value[0]) selectedLevelId.value = levels.value[0].id
    await fetchBenefits()
  } finally {
    levelLoading.value = false
  }
}

async function fetchBenefits() {
  benefitLoading.value = true
  try {
    // 权益接口按 level_id 过滤，未选等级时让后端返回默认范围，避免前端伪造规则。
    const res = await listMembershipBenefits({ level_id: selectedLevelId.value })
    benefits.value = res.items
  } finally {
    benefitLoading.value = false
  }
}

async function fetchUserMemberships() {
  userMembershipLoading.value = true
  try {
    // 用户会员记录是分页接口，后端直接返回 level_code/level_name，前端不再自行关联等级表。
    const res = await listUserMemberships({
      user_id: userFilter.user_id,
      page: pagination.page,
      page_size: pagination.page_size,
    })
    userMemberships.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    userMembershipLoading.value = false
  }
}

function openCreateLevel() {
  editingLevel.value = null
  levelForm.level_code = ''
  levelForm.name = ''
  levelForm.description = ''
  levelForm.sort_order = 0
  levelForm.status = 'active'
  levelDialogVisible.value = true
}

function openEditLevel(level: MembershipLevel) {
  editingLevel.value = level
  levelForm.level_code = level.level_code
  levelForm.name = level.name
  levelForm.description = level.description || ''
  levelForm.sort_order = level.sort_order
  levelForm.status = level.status
  levelDialogVisible.value = true
}

async function saveLevel() {
  if (!levelForm.name || (!editingLevel.value && !levelForm.level_code)) {
    ElMessage.warning('请填写等级代码和名称')
    return
  }
  levelSaving.value = true
  try {
    if (editingLevel.value) {
      await updateMembershipLevel(editingLevel.value.id, {
        name: levelForm.name,
        description: levelForm.description || null,
        sort_order: levelForm.sort_order,
        status: levelForm.status,
      })
      ElMessage.success('会员等级已更新')
    } else {
      await createMembershipLevel({
        level_code: levelForm.level_code,
        name: levelForm.name,
        description: levelForm.description || null,
        sort_order: levelForm.sort_order,
        status: levelForm.status,
      })
      ElMessage.success('会员等级已创建')
    }
    levelDialogVisible.value = false
    await fetchLevels()
  } finally {
    levelSaving.value = false
  }
}

function openCreateBenefit() {
  editingBenefit.value = null
  benefitForm.level_id = selectedLevelId.value
  benefitForm.benefit_type = ''
  benefitForm.benefit_value = ''
  benefitForm.status = 'active'
  benefitDialogVisible.value = true
}

function openEditBenefit(benefit: MembershipBenefit) {
  editingBenefit.value = benefit
  benefitForm.level_id = benefit.level_id
  benefitForm.benefit_type = benefit.benefit_type
  benefitForm.benefit_value = benefit.benefit_value
  benefitForm.status = benefit.status
  benefitDialogVisible.value = true
}

function validateJsonText(value: string, fieldName: string) {
  try {
    // benefit_value 是后端存储的 JSON 字符串，提交前只校验合法性，不转换字段形态。
    JSON.parse(value)
    return true
  } catch {
    ElMessage.warning(`${fieldName} 必须是合法 JSON 字符串`)
    return false
  }
}

async function saveBenefit() {
  if (!benefitForm.level_id || !benefitForm.benefit_type || !benefitForm.benefit_value) {
    ElMessage.warning('请填写权益必填项')
    return
  }
  if (!validateJsonText(benefitForm.benefit_value, '权益值')) return
  benefitSaving.value = true
  try {
    if (editingBenefit.value) {
      await updateMembershipBenefit(editingBenefit.value.id, {
        benefit_type: benefitForm.benefit_type,
        benefit_value: benefitForm.benefit_value,
        status: benefitForm.status,
      })
      ElMessage.success('会员权益已更新')
    } else {
      await createMembershipBenefit({
        level_id: benefitForm.level_id,
        benefit_type: benefitForm.benefit_type,
        benefit_value: benefitForm.benefit_value,
        status: benefitForm.status,
      })
      ElMessage.success('会员权益已创建')
    }
    benefitDialogVisible.value = false
    await fetchBenefits()
  } finally {
    benefitSaving.value = false
  }
}

function handleUserSearch() {
  pagination.page = 1
  fetchUserMemberships()
}

function openGrantDialog() {
  grantForm.user_id = undefined
  grantForm.level_id = levels.value[0]?.id
  grantForm.permanent = true
  grantForm.duration_days = undefined
  grantDialogVisible.value = true
}

async function submitGrant() {
  if (!grantForm.user_id || !grantForm.level_id) {
    ElMessage.warning('请填写用户和会员等级')
    return
  }
  if (!grantForm.permanent && !grantForm.duration_days) {
    ElMessage.warning('非永久会员需要填写有效天数')
    return
  }
  granting.value = true
  try {
    // 后端约定 duration_days=null 表示永久会员，不能传 0，避免被误解为立即过期。
    await grantUserMembership({
      user_id: grantForm.user_id,
      level_id: grantForm.level_id,
      duration_days: grantForm.permanent ? null : grantForm.duration_days!,
    })
    ElMessage.success('会员已开通')
    grantDialogVisible.value = false
    await fetchUserMemberships()
  } finally {
    granting.value = false
  }
}

async function cancelMembership(row: AdminUserMembership) {
  await ElMessageBox.confirm(`确认取消用户 ${row.user_id} 的「${row.level_name}」会员？`, '确认取消', { type: 'warning' })
  // 取消会员使用 action=cancel；调整到期时间走另一个 PATCH body，两个分支不能混传。
  await updateUserMembership(row.id, { action: 'cancel' })
  ElMessage.success('会员已取消')
  await fetchUserMemberships()
}

function openExpireDialog(row: AdminUserMembership) {
  selectedMembership.value = row
  expireForm.expires_at = row.expires_at || ''
  expireDialogVisible.value = true
}

async function submitExpire() {
  if (!selectedMembership.value || !expireForm.expires_at) {
    ElMessage.warning('请填写新的到期时间')
    return
  }
  expiring.value = true
  try {
    // 到期时间按后端要求的字符串透传，避免前端本地时区格式化造成时间漂移。
    await updateUserMembership(selectedMembership.value.id, { expires_at: expireForm.expires_at })
    ElMessage.success('到期时间已调整')
    expireDialogVisible.value = false
    await fetchUserMemberships()
  } finally {
    expiring.value = false
  }
}

function statusTagType(status: string) {
  if (status === 'active') return 'success'
  if (status === 'inactive') return 'warning'
  if (status === 'cancelled') return 'danger'
  return 'info'
}
</script>

<template>
  <div class="admin-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">会员管理</h3>
        <p class="page-subtitle">维护会员等级、权益配置，并为指定用户开通或调整会员</p>
      </div>
    </div>

    <el-tabs v-model="activeTab" class="admin-tabs">
      <el-tab-pane label="等级管理" name="levels">
        <div class="toolbar">
          <el-button type="primary" :icon="Plus" @click="openCreateLevel">新建等级</el-button>
          <el-button :icon="Refresh" @click="fetchLevels">刷新</el-button>
        </div>
        <el-table :data="levels" v-loading="levelLoading" border>
          <el-table-column prop="id" label="ID" width="80" />
          <el-table-column prop="level_code" label="等级代码" min-width="140" />
          <el-table-column prop="name" label="等级名称" min-width="140" />
          <el-table-column prop="description" label="说明" min-width="220" show-overflow-tooltip />
          <el-table-column prop="sort_order" label="排序" width="90" />
          <el-table-column label="状态" width="100">
            <template #default="{ row }"><el-tag :type="statusTagType(row.status)">{{ membershipStatusLabel(row.status) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="110" fixed="right">
            <template #default="{ row }"><el-button text :icon="Edit" @click="openEditLevel(row)">编辑</el-button></template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="权益配置" name="benefits">
        <div class="toolbar">
          <el-select v-model="selectedLevelId" placeholder="选择等级" @change="fetchBenefits">
            <el-option v-for="level in levelOptions" :key="level.value" :label="level.label" :value="level.value" />
          </el-select>
          <el-button type="primary" :icon="Plus" @click="openCreateBenefit">新建权益</el-button>
          <el-button :icon="Refresh" @click="fetchBenefits">刷新</el-button>
        </div>
        <el-table :data="benefits" v-loading="benefitLoading" border>
          <el-table-column prop="id" label="ID" width="80" />
          <el-table-column prop="level_id" label="等级 ID" width="100" />
          <el-table-column prop="benefit_type" label="权益类型" min-width="140" />
          <el-table-column prop="benefit_value" label="权益值 JSON" min-width="260" show-overflow-tooltip />
          <el-table-column label="状态" width="100">
            <template #default="{ row }"><el-tag :type="statusTagType(row.status)">{{ membershipStatusLabel(row.status) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="110" fixed="right">
            <template #default="{ row }"><el-button text :icon="Edit" @click="openEditBenefit(row)">编辑</el-button></template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="用户会员" name="users">
        <div class="toolbar">
          <el-input-number v-model="userFilter.user_id" :min="1" :precision="0" controls-position="right" placeholder="用户 ID" />
          <el-button type="primary" :icon="Search" :loading="userMembershipLoading" @click="handleUserSearch">查询</el-button>
          <el-button @click="userFilter.user_id = undefined; handleUserSearch()">重置</el-button>
          <el-button type="primary" :icon="Plus" @click="openGrantDialog">开通会员</el-button>
        </div>
        <el-table :data="userMemberships" v-loading="userMembershipLoading" border>
          <el-table-column prop="id" label="ID" width="80" />
          <el-table-column prop="user_id" label="用户 ID" width="100" />
          <el-table-column prop="level_name" label="等级名称" min-width="140" />
          <el-table-column prop="level_code" label="等级代码" min-width="120" />
          <el-table-column prop="asset_id" label="资产 ID" width="100" />
          <el-table-column label="状态" width="100">
            <template #default="{ row }"><el-tag :type="statusTagType(row.status)">{{ membershipStatusLabel(row.status) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="开始时间" min-width="170"><template #default="{ row }">{{ formatDateTime(row.started_at) }}</template></el-table-column>
          <el-table-column label="到期时间" min-width="170"><template #default="{ row }">{{ formatDateTime(row.expires_at) }}</template></el-table-column>
          <el-table-column label="操作" width="210" fixed="right">
            <template #default="{ row }">
              <el-button text @click="openExpireDialog(row)">调整到期</el-button>
              <el-button v-if="row.status === 'active'" type="danger" text @click="cancelMembership(row)">取消</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div class="pagination-row">
          <el-pagination background layout="prev, pager, next, total" :current-page="pagination.page" :page-size="pagination.page_size" :total="pagination.total" @current-change="(page: number) => { pagination.page = page; fetchUserMemberships() }" />
        </div>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="levelDialogVisible" :title="editingLevel ? '编辑会员等级' : '新建会员等级'" width="560px">
      <el-form label-width="100px">
        <el-form-item label="等级代码"><el-input v-model="levelForm.level_code" :disabled="!!editingLevel" /></el-form-item>
        <el-form-item label="等级名称"><el-input v-model="levelForm.name" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="levelForm.description" type="textarea" :rows="3" /></el-form-item>
        <el-form-item label="排序"><el-input-number v-model="levelForm.sort_order" :precision="0" /></el-form-item>
        <el-form-item label="状态">
          <el-select v-model="levelForm.status"><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="levelDialogVisible = false">取消</el-button><el-button type="primary" :loading="levelSaving" @click="saveLevel">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="benefitDialogVisible" :title="editingBenefit ? '编辑会员权益' : '新建会员权益'" width="620px">
      <el-form label-width="110px">
        <el-form-item label="会员等级"><el-select v-model="benefitForm.level_id"><el-option v-for="level in levelOptions" :key="level.value" :label="level.label" :value="level.value" /></el-select></el-form-item>
        <el-form-item label="权益类型"><el-input v-model="benefitForm.benefit_type" placeholder="如 quota_discount / storage_quota" /></el-form-item>
        <el-form-item label="权益值 JSON"><el-input v-model="benefitForm.benefit_value" type="textarea" :rows="5" placeholder='{"discount":"0.9"}' /></el-form-item>
        <el-form-item label="状态"><el-select v-model="benefitForm.status"><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item>
      </el-form>
      <template #footer><el-button @click="benefitDialogVisible = false">取消</el-button><el-button type="primary" :loading="benefitSaving" @click="saveBenefit">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="grantDialogVisible" title="开通用户会员" width="520px">
      <el-form label-width="100px">
        <el-form-item label="用户 ID"><el-input-number v-model="grantForm.user_id" :min="1" :precision="0" /></el-form-item>
        <el-form-item label="会员等级"><el-select v-model="grantForm.level_id"><el-option v-for="level in levelOptions" :key="level.value" :label="level.label" :value="level.value" /></el-select></el-form-item>
        <el-form-item label="永久会员"><el-switch v-model="grantForm.permanent" /></el-form-item>
        <el-form-item v-if="!grantForm.permanent" label="有效天数"><el-input-number v-model="grantForm.duration_days" :min="1" :precision="0" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="grantDialogVisible = false">取消</el-button><el-button type="primary" :loading="granting" @click="submitGrant">确认开通</el-button></template>
    </el-dialog>

    <el-dialog v-model="expireDialogVisible" title="调整会员到期时间" width="520px">
      <el-form label-width="110px">
        <el-form-item label="会员记录">{{ selectedMembership?.id }}</el-form-item>
        <el-form-item label="到期时间"><el-input v-model="expireForm.expires_at" placeholder="例如 2026-12-31T00:00:00Z" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="expireDialogVisible = false">取消</el-button><el-button type="primary" :loading="expiring" @click="submitExpire">保存</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.admin-page { display: flex; flex-direction: column; gap: 16px; }
.page-header, .admin-tabs { padding: 16px; background: var(--mc-surface); border: 1px solid var(--mc-border-soft); border-radius: 8px; }
.page-title-text { margin: 0 0 6px; font-size: 18px; }
.page-subtitle { margin: 0; color: var(--mc-text-muted); }
.toolbar { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 14px; }
.pagination-row { display: flex; justify-content: flex-end; margin-top: 14px; }
</style>
