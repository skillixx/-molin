<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CopyDocument, Delete, Edit, Key, Plus, RefreshRight, Remove } from '@element-plus/icons-vue'
import {
  createAIProject,
  getAIResourceLimits,
  issueProjectKey,
  listAIModels,
  listAIProjects,
  listProjectKeys,
  revokeProjectKey,
  rotateProjectKey,
  updateAIProject,
} from '@/api/aiGateway'
import type { AIModelCatalogItem, AIProject, IssuedProjectKey, ProjectKey, UserResourceLimits } from '@/types/aiGateway'
import { formatDateTime } from '@/utils/display'

const route = useRoute()
const router = useRouter()
const loading = ref(false)
const pageError = ref('')
const projects = ref<AIProject[]>([])
const selectedProjectID = ref<number>()
const keys = ref<ProjectKey[]>([])
const models = ref<AIModelCatalogItem[]>([])
const projectDialog = ref(false)
const projectDialogMode = ref<'create' | 'edit'>('create')
const keyDialog = ref(false)
const secretDialog = ref(false)
const saving = ref(false)
const actionKeyID = ref<number>()
const issuedKey = ref<IssuedProjectKey>()
const limits = ref<UserResourceLimits>()
const projectForm = reactive({ name: '', timezone: 'Asia/Shanghai' })
const keyForm = reactive({ name: '', scope_mode: 'allowlist' as 'all' | 'allowlist', model_codes: [] as string[], expires_at: '' })

const selectedProject = computed(() => projects.value.find((item) => item.id === selectedProjectID.value))
const selectedProjectLimit = computed(() => limits.value?.projects.find((item) => item.scope_id === selectedProjectID.value))
function keyLimit(keyID: number) { return limits.value?.api_keys.find((item) => item.scope_id === keyID) }

onMounted(loadPage)

async function loadPage() {
  pageError.value = ''
  try {
    await Promise.all([loadProjects(), loadModels(), loadLimits()])
  } catch {
    pageError.value = 'Project 与 API Key 暂时无法加载，请稍后重试。'
  }
}

async function loadLimits() { limits.value = await getAIResourceLimits() }

async function loadProjects() {
  loading.value = true
  try {
    const loaded: AIProject[] = []
    for (let page = 1; ; page += 1) {
      const result = await listAIProjects({ page, page_size: 100 })
      loaded.push(...result.items)
      if (loaded.length >= result.total) break
    }
    projects.value = loaded
    if (!selectedProjectID.value && projects.value.length) selectedProjectID.value = projects.value.find((item) => item.status === 'active')?.id || projects.value[0].id
    if (selectedProjectID.value) await loadKeys(selectedProjectID.value)
  } finally { loading.value = false }
}

async function loadModels() {
  const loaded: AIModelCatalogItem[] = []
  for (let page = 1; ; page += 1) {
    const result = await listAIModels({ page, page_size: 100 })
    loaded.push(...result.items)
    if (loaded.length >= result.total) break
  }
  models.value = loaded
}

async function selectProject(id: number) {
  selectedProjectID.value = id
  await loadKeys(id)
}

async function loadKeys(projectID: number) {
  const result = await listProjectKeys(projectID)
  keys.value = result.items
}

function openProjectDialog() {
	projectDialogMode.value = 'create'
  projectForm.name = ''
  projectForm.timezone = 'Asia/Shanghai'
  projectDialog.value = true
}

function openProjectEditDialog() {
  if (!selectedProject.value) return
  projectDialogMode.value = 'edit'
  projectForm.name = selectedProject.value.name
  projectForm.timezone = selectedProject.value.timezone
  projectDialog.value = true
}

async function createProject() {
  if (!projectForm.name.trim()) return ElMessage.warning('请填写 Project 名称')
  saving.value = true
  try {
    const project = await createAIProject({ name: projectForm.name.trim(), timezone: projectForm.timezone })
    ElMessage.success('Project 已创建')
    projectDialog.value = false
    await loadProjects()
    await loadLimits()
    await selectProject(project.id)
  } finally { saving.value = false }
}

async function saveProject() {
  if (projectDialogMode.value === 'create') {
    await createProject()
    return
  }
  if (!selectedProject.value || !projectForm.name.trim()) return ElMessage.warning('请填写 Project 名称')
  saving.value = true
  try {
    const updated = await updateAIProject(selectedProject.value.id, { name: projectForm.name.trim(), timezone: projectForm.timezone })
    projectDialog.value = false
    ElMessage.success('Project 已更新')
    await loadProjects()
    await selectProject(updated.id)
  } finally { saving.value = false }
}

function openKeyDialog() {
  if (!selectedProject.value || selectedProject.value.status !== 'active') return ElMessage.warning('请先选择可用 Project')
  keyForm.name = ''
  keyForm.scope_mode = 'allowlist'
  keyForm.model_codes = route.query.model ? [String(route.query.model)] : []
  keyForm.expires_at = ''
  keyDialog.value = true
}

async function createKey() {
  if (!selectedProjectID.value || !keyForm.name.trim()) return ElMessage.warning('请填写密钥名称')
  if (keyForm.scope_mode === 'allowlist' && keyForm.model_codes.length === 0) return ElMessage.warning('请至少选择一个允许模型')
  saving.value = true
  try {
    issuedKey.value = await issueProjectKey(selectedProjectID.value, {
      name: keyForm.name.trim(), scope_mode: keyForm.scope_mode,
      model_codes: keyForm.scope_mode === 'all' ? [] : keyForm.model_codes,
      expires_at: keyForm.expires_at ? new Date(keyForm.expires_at).toISOString() : undefined,
    })
    keyDialog.value = false
    secretDialog.value = true
    await loadKeys(selectedProjectID.value)
    await loadLimits()
  } finally { saving.value = false }
}

async function copySecret() {
  if (!issuedKey.value) return
  await navigator.clipboard.writeText(issuedKey.value.secret_key)
  ElMessage.success('完整密钥已复制，请立即安全保存')
}

function clearIssuedSecret() {
  issuedKey.value = undefined
  router.replace({ query: {} })
}

function closeSecretDialog() {
  secretDialog.value = false
}

async function rotateKey(row: unknown) {
  const item = row as ProjectKey
  if (!selectedProjectID.value) return
  await ElMessageBox.confirm('轮换会立即吊销旧密钥，确认继续？', '轮换平台 SK', { type: 'warning', confirmButtonText: '确认轮换', cancelButtonText: '取消' })
  actionKeyID.value = item.id
  try {
    issuedKey.value = await rotateProjectKey(selectedProjectID.value, item.id)
    secretDialog.value = true
    await loadKeys(selectedProjectID.value)
    await loadLimits()
  } finally { actionKeyID.value = undefined }
}

async function revokeKey(row: unknown) {
  const item = row as ProjectKey
  if (!selectedProjectID.value) return
  await ElMessageBox.confirm('吊销后该密钥立即失效且不可恢复。', '吊销平台 SK', { type: 'warning', confirmButtonText: '确认吊销', cancelButtonText: '取消' })
  actionKeyID.value = item.id
  try {
    await revokeProjectKey(selectedProjectID.value, item.id)
    ElMessage.success('密钥已吊销')
    await loadKeys(selectedProjectID.value)
    await loadLimits()
  } finally { actionKeyID.value = undefined }
}

async function archiveProject() {
  if (!selectedProject.value) return
  await ElMessageBox.confirm('归档后不能再签发新密钥，已有密钥应先吊销。', '归档 Project', { type: 'warning', confirmButtonText: '确认归档', cancelButtonText: '取消' })
  await updateAIProject(selectedProject.value.id, { status: 'archived' })
  ElMessage.success('Project 已归档')
  await loadProjects()
}
</script>

<template>
  <section class="keys-page">
    <header class="page-header">
      <div><p class="eyebrow">AI 服务</p><h1>Project 与 API Key</h1><p>按 Project 隔离模型权限和消费。完整 SK 只在签发或轮换成功时显示一次。</p></div>
      <div class="header-actions"><el-button :icon="Plus" @click="openProjectDialog">创建 Project</el-button><el-button type="primary" :icon="Key" @click="openKeyDialog">创建 API Key</el-button></div>
    </header>

    <el-alert type="warning" show-icon :closable="false" title="不要把完整 SK 写入网页、源码、日志或聊天记录。请使用服务器环境变量或密钥管理服务。" />
    <el-alert v-if="pageError" class="page-error" type="error" show-icon :closable="false" :title="pageError"><template #default><el-button link type="primary" @click="loadPage">重新加载</el-button></template></el-alert>

    <div v-loading="loading" class="workspace">
      <aside class="project-list" aria-label="Project 列表">
        <button v-for="project in projects" :key="project.id" type="button" class="project-item" :class="{ active: project.id === selectedProjectID }" @click="selectProject(project.id)">
          <span class="project-name">{{ project.name }}</span>
          <el-tag :type="project.status === 'active' ? 'success' : 'info'" size="small" effect="plain">{{ project.status === 'active' ? '可用' : '已归档' }}</el-tag>
          <small>{{ limits?.projects.find((item) => item.scope_id === project.id)?.budget_mode === 'disabled' ? '预算未启用' : `月预算 ¥${limits?.projects.find((item) => item.scope_id === project.id)?.monthly_budget || '未设置'}` }}</small>
        </button>
        <el-empty v-if="projects.length === 0" description="还没有 Project" :image-size="72" />
      </aside>

      <main class="key-content">
        <template v-if="selectedProject">
          <div class="project-summary">
            <div><h2>{{ selectedProject.name }}</h2><p>时区 {{ selectedProject.timezone }} · Project 有效限制：并发 {{ selectedProjectLimit?.concurrency || '—' }}，RPM {{ selectedProjectLimit?.rpm || '—' }}，TPM {{ selectedProjectLimit?.tpm || '—' }}</p></div>
            <div class="project-actions"><el-button text :icon="Edit" @click="openProjectEditDialog">编辑</el-button><el-button v-if="selectedProject.status === 'active'" text type="warning" :icon="Delete" @click="archiveProject">归档</el-button></div>
          </div>

          <div class="desktop-table">
            <el-table :data="keys" border empty-text="当前 Project 还没有 API Key">
              <el-table-column prop="name" label="名称" min-width="140" />
              <el-table-column prop="key_prefix" label="密钥前缀" min-width="150" />
              <el-table-column label="模型范围" min-width="210"><template #default="{ row }"><span v-if="row.scope_mode === 'all'">全部已授权模型</span><span v-else>{{ row.model_codes.join('、') || '拒绝全部' }}</span></template></el-table-column>
              <el-table-column label="状态" width="90"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'" size="small">{{ row.status === 'active' ? '可用' : '已吊销' }}</el-tag></template></el-table-column>
              <el-table-column label="最近使用" min-width="155"><template #default="{ row }">{{ row.last_used_at ? formatDateTime(row.last_used_at) : '尚未使用' }}</template></el-table-column>
              <el-table-column label="创建时间" min-width="155"><template #default="{ row }">{{ formatDateTime(row.created_at) }}</template></el-table-column>
              <el-table-column label="有效限制" min-width="170"><template #default="{ row }"><span v-if="row.status !== 'active'">已停止执行</span><span v-else-if="keyLimit(row.id)">并发 {{ keyLimit(row.id)?.concurrency }} · RPM {{ keyLimit(row.id)?.rpm }} · TPM {{ keyLimit(row.id)?.tpm }}</span><span v-else>加载中</span></template></el-table-column>
              <el-table-column label="操作" width="156" fixed="right"><template #default="{ row }"><el-tooltip content="轮换密钥"><el-button text circle :icon="RefreshRight" aria-label="轮换 API Key" :loading="actionKeyID === row.id" :disabled="row.status !== 'active'" @click="rotateKey(row)" /></el-tooltip><el-tooltip content="吊销密钥"><el-button text circle type="danger" :icon="Remove" aria-label="吊销 API Key" :loading="actionKeyID === row.id" :disabled="row.status !== 'active'" @click="revokeKey(row)" /></el-tooltip></template></el-table-column>
            </el-table>
          </div>

          <div class="mobile-keys">
            <article v-for="item in keys" :key="item.id" class="key-card"><div><strong>{{ item.name }}</strong><el-tag :type="item.status === 'active' ? 'success' : 'info'" size="small">{{ item.status === 'active' ? '可用' : '已吊销' }}</el-tag></div><code>{{ item.key_prefix }}</code><p>{{ item.scope_mode === 'all' ? '全部已授权模型' : (item.model_codes.join('、') || '拒绝全部') }}</p><p>创建于 {{ formatDateTime(item.created_at) }}</p><p v-if="item.status !== 'active'">已停止执行</p><p v-else-if="keyLimit(item.id)">有效限制：并发 {{ keyLimit(item.id)?.concurrency }} · RPM {{ keyLimit(item.id)?.rpm }} · TPM {{ keyLimit(item.id)?.tpm }}</p><div class="card-actions"><el-button :icon="RefreshRight" :disabled="item.status !== 'active'" @click="rotateKey(item)">轮换</el-button><el-button type="danger" plain :icon="Remove" :disabled="item.status !== 'active'" @click="revokeKey(item)">吊销</el-button></div></article>
            <el-empty v-if="keys.length === 0" description="当前 Project 还没有 API Key" />
          </div>
        </template>
        <el-empty v-else description="创建 Project 后即可签发 API Key"><el-button type="primary" @click="openProjectDialog">创建 Project</el-button></el-empty>
      </main>
    </div>

    <el-dialog v-model="projectDialog" :title="projectDialogMode === 'create' ? '创建 Project' : '编辑 Project'" width="min(480px, 92vw)"><el-form label-position="top"><el-form-item label="名称" required><el-input v-model="projectForm.name" maxlength="80" show-word-limit placeholder="例如：客户服务生产环境" /></el-form-item><el-form-item label="账单时区"><el-select v-model="projectForm.timezone"><el-option label="中国标准时间" value="Asia/Shanghai" /><el-option label="UTC" value="UTC" /></el-select></el-form-item></el-form><template #footer><el-button @click="projectDialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveProject">{{ projectDialogMode === 'create' ? '创建' : '保存' }}</el-button></template></el-dialog>

    <el-dialog v-model="keyDialog" title="创建平台 SK" width="min(560px, 94vw)"><el-form label-position="top"><el-form-item label="密钥名称" required><el-input v-model="keyForm.name" maxlength="80" placeholder="例如：订单系统" /></el-form-item><el-form-item label="模型权限"><el-radio-group v-model="keyForm.scope_mode"><el-radio-button value="allowlist">指定模型</el-radio-button><el-radio-button value="all">全部模型</el-radio-button></el-radio-group></el-form-item><el-form-item v-if="keyForm.scope_mode === 'allowlist'" label="允许模型" required><el-select v-model="keyForm.model_codes" multiple filterable collapse-tags><el-option v-for="item in models" :key="item.logical_model_code" :label="`${item.display_name} · ${item.logical_model_code}`" :value="item.logical_model_code" /></el-select></el-form-item><el-form-item label="过期时间（可选）"><el-date-picker v-model="keyForm.expires_at" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" placeholder="长期有效" /></el-form-item></el-form><template #footer><el-button @click="keyDialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="createKey">签发密钥</el-button></template></el-dialog>

    <el-dialog v-model="secretDialog" title="立即保存完整密钥" width="min(620px, 94vw)" :close-on-click-modal="false" destroy-on-close @closed="clearIssuedSecret"><el-alert type="warning" show-icon :closable="false" title="关闭后无法再次查看，只能重新轮换。" /><div v-if="issuedKey" class="secret-box"><code>{{ issuedKey.secret_key }}</code><el-button :icon="CopyDocument" @click="copySecret">复制</el-button></div><template #footer><el-button type="primary" @click="closeSecretDialog">我已安全保存</el-button></template></el-dialog>
  </section>
</template>

<style scoped>
.keys-page { width: min(1440px, calc(100% - 48px)); margin: 0 auto; padding: 34px 0 56px; color: var(--color-text); }
.page-error { margin-top: 12px; }
.page-header { display: flex; justify-content: space-between; align-items: end; gap: 20px; margin-bottom: 20px; }.eyebrow { margin: 0 0 5px; color: var(--color-primary); font-size: 12px; font-weight: 700; }h1 { margin: 0; font-size: 24px; letter-spacing: 0; }.page-header p:last-child { margin: 8px 0 0; color: var(--color-text-muted); }.header-actions { display: flex; gap: 8px; flex-shrink: 0; }
.workspace { display: grid; grid-template-columns: 250px minmax(0, 1fr); min-height: 420px; margin-top: 18px; border-top: 1px solid var(--color-border); }.project-list { padding: 16px 16px 16px 0; border-right: 1px solid var(--color-border); }.project-item { width: 100%; display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 7px; padding: 13px; margin-bottom: 8px; text-align: left; color: var(--color-text); border: 1px solid transparent; border-radius: 7px; background: transparent; cursor: pointer; }.project-item:hover, .project-item.active { background: rgba(34, 211, 238, .07); border-color: rgba(34, 211, 238, .28); }.project-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }.project-item small { grid-column: 1 / -1; color: var(--color-text-muted); }
.key-content { min-width: 0; padding: 18px 0 18px 24px; }.project-summary { display: flex; align-items: start; justify-content: space-between; gap: 16px; margin-bottom: 18px; }.project-summary h2 { margin: 0; font-size: 18px; }.project-summary p { margin: 7px 0 0; color: var(--color-text-muted); font-size: 13px; }.mobile-keys { display: none; }.secret-box { display: flex; align-items: center; gap: 10px; margin-top: 16px; padding: 14px; border: 1px solid var(--color-border); border-radius: 8px; background: rgba(0,0,0,.24); }.secret-box code { flex: 1; min-width: 0; overflow-wrap: anywhere; color: var(--color-primary); }.card-actions { display: flex; gap: 8px; }.card-actions :deep(.el-button) { flex: 1; min-height: 44px; }
.project-actions { display: flex; gap: 4px; flex-shrink: 0; }
@media (max-width: 900px) { .keys-page { width: calc(100% - 40px); }.workspace { grid-template-columns: 1fr; }.project-list { display: flex; gap: 8px; overflow-x: auto; border-right: 0; border-bottom: 1px solid var(--color-border); padding-right: 0; }.project-item { min-width: 220px; }.key-content { padding-left: 0; }.desktop-table { display: none; }.mobile-keys { display: grid; gap: 10px; }.key-card { padding: 15px 0; border-bottom: 1px solid var(--color-border); }.key-card > div:first-child { display: flex; align-items: center; justify-content: space-between; gap: 10px; }.key-card code { display: block; margin-top: 10px; color: var(--color-primary); }.key-card p { color: var(--color-text-muted); overflow-wrap: anywhere; } }
@media (max-width: 560px) { .keys-page { width: calc(100% - 32px); padding-top: 24px; }.page-header { align-items: stretch; flex-direction: column; }.header-actions { display: grid; grid-template-columns: 1fr 1fr; }.header-actions :deep(.el-button) { margin: 0; min-height: 44px; }.secret-box { align-items: stretch; flex-direction: column; } }
</style>
