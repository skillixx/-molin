<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { ChatDotRound, Edit, Plus, Refresh } from '@element-plus/icons-vue'
import {
  createAgent,
  deleteAgent,
  listAgents,
  listModels,
  listPlugins,
  listSkills,
  updateAgent,
} from '@/api/token'
import type {
  AgentItem,
  CreateAgentReq,
  PluginItem,
  SkillItem,
  TokenModel,
  UpdateAgentReq,
} from '@/types/token'

const router = useRouter()

const loading = ref(false)
const agents = ref<AgentItem[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

const models = ref<TokenModel[]>([])
const skills = ref<SkillItem[]>([])
const plugins = ref<PluginItem[]>([])
const abilityLoading = ref(false)

const dialogVisible = ref(false)
const dialogMode = ref<'create' | 'edit'>('create')
const saving = ref(false)
const editingId = ref(0)
const formRef = ref<FormInstance>()
const form = reactive({
  name: '',
  description: '',
  avatar: '',
  system_prompt: '',
  default_model_code: '',
  skill_ids: [] as number[],
  plugin_ids: [] as number[],
})

const rules: FormRules = {
  name: [{ required: true, message: '请输入 Agent 名称', trigger: 'blur' }],
  system_prompt: [{ required: true, message: '请输入人设提示词', trigger: 'blur' }],
  default_model_code: [{ required: true, message: '请选择默认模型', trigger: 'change' }],
}

const officialAgents = computed(() => agents.value.filter((item) => item.owner_type === 'official'))
const myAgents = computed(() => agents.value.filter((item) => item.owner_type === 'user'))

onMounted(() => {
  fetchAgents()
  fetchAbilities()
})

async function fetchAgents() {
  loading.value = true
  try {
    const res = await listAgents({ page: page.value, page_size: pageSize.value })
    agents.value = res.items
    page.value = res.page
    pageSize.value = res.page_size
    total.value = res.total
  } finally {
    loading.value = false
  }
}

async function fetchAbilities() {
  abilityLoading.value = true
  try {
    const [modelRes, skillRes, pluginRes] = await Promise.all([
      listModels(),
      listSkills({ page: 1, page_size: 100 }),
      listPlugins({ page: 1, page_size: 100 }),
    ])
    models.value = modelRes.items.filter((item) => item.modality === 'chat')
    skills.value = skillRes.items
    plugins.value = pluginRes.items
  } finally {
    abilityLoading.value = false
  }
}

function openCreate() {
  dialogMode.value = 'create'
  editingId.value = 0
  resetForm()
  form.default_model_code = models.value[0]?.logical_model_code || ''
  dialogVisible.value = true
}

function openEdit(agent: AgentItem) {
  if (agent.owner_type !== 'user') return
  dialogMode.value = 'edit'
  editingId.value = agent.id
  form.name = agent.name
  form.description = agent.description || ''
  form.avatar = agent.avatar || ''
  form.system_prompt = agent.system_prompt
  form.default_model_code = agent.default_model_code
  form.skill_ids = agent.skills.map((item) => item.id)
  form.plugin_ids = agent.plugins.map((item) => item.id)
  dialogVisible.value = true
}

function resetForm() {
  form.name = ''
  form.description = ''
  form.avatar = ''
  form.system_prompt = ''
  form.default_model_code = ''
  form.skill_ids = []
  form.plugin_ids = []
  formRef.value?.clearValidate()
}

async function handleSave() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  saving.value = true
  try {
    const payload: CreateAgentReq | UpdateAgentReq = {
      name: form.name,
      description: form.description,
      avatar: form.avatar,
      system_prompt: form.system_prompt,
      default_model_code: form.default_model_code,
      // skill_ids / plugin_ids 是覆盖语义，传空数组表示清空绑定。
      skill_ids: form.skill_ids,
      plugin_ids: form.plugin_ids,
    }
    if (dialogMode.value === 'create') {
      await createAgent(payload as CreateAgentReq)
      ElMessage.success('Agent 创建成功')
    } else {
      await updateAgent(editingId.value, payload)
      ElMessage.success('Agent 更新成功')
    }
    dialogVisible.value = false
    fetchAgents()
  } finally {
    saving.value = false
  }
}

async function handleDelete(agent: AgentItem) {
  if (agent.owner_type !== 'user') return
  await ElMessageBox.confirm(`确认删除「${agent.name}」吗？删除后不可恢复。`, '删除 Agent', {
    type: 'warning',
    confirmButtonText: '删除',
    cancelButtonText: '取消',
  })
  await deleteAgent(agent.id)
  ElMessage.success('Agent 已删除')
  fetchAgents()
}

function enterChat(agent: AgentItem) {
  router.push(`/agents/${agent.id}/chat`)
}

function handlePageChange(nextPage: number) {
  page.value = nextPage
  fetchAgents()
}

function handlePageSizeChange(nextSize: number) {
  page.value = 1
  pageSize.value = nextSize
  fetchAgents()
}
</script>

<template>
  <div class="agent-page">
    <div class="page-container">
      <section class="agent-header glass-card">
        <div>
          <span class="page-kicker">多模型工作台</span>
          <h2>Agent 工作台</h2>
          <p>选择官方 Agent 或创建自己的 Agent，绑定可用 Skill 与插件后进入对话。</p>
        </div>
        <div class="header-actions">
          <el-button :icon="Refresh" :loading="loading" @click="fetchAgents">刷新</el-button>
          <el-button type="primary" :icon="Plus" :loading="abilityLoading" @click="openCreate">
            自建 Agent
          </el-button>
        </div>
      </section>

      <section v-loading="loading" class="agent-sections">
        <div class="section-title">官方 Agent</div>
        <div class="agent-grid">
          <article v-for="agent in officialAgents" :key="agent.id" class="agent-card glass-card">
            <div class="agent-card-head">
              <div class="agent-avatar">{{ agent.name.slice(0, 1) }}</div>
              <el-tag type="success">官方</el-tag>
            </div>
            <h3>{{ agent.name }}</h3>
            <p>{{ agent.description || '暂无描述' }}</p>
            <div class="ability-line">
              <span>{{ agent.default_model_code }}</span>
              <span>{{ agent.skills.length }} 个 Skill</span>
              <span>{{ agent.plugins.length }} 个插件</span>
            </div>
            <el-button type="primary" :icon="ChatDotRound" @click="enterChat(agent)">进入对话</el-button>
          </article>
        </div>

        <div class="section-title">我的 Agent</div>
        <div class="agent-grid">
          <article v-for="agent in myAgents" :key="agent.id" class="agent-card glass-card">
            <div class="agent-card-head">
              <div class="agent-avatar mine">{{ agent.name.slice(0, 1) }}</div>
              <el-tag>我的</el-tag>
            </div>
            <h3>{{ agent.name }}</h3>
            <p>{{ agent.description || '暂无描述' }}</p>
            <div class="ability-line">
              <span>{{ agent.default_model_code }}</span>
              <span>{{ agent.skills.length }} 个 Skill</span>
              <span>{{ agent.plugins.length }} 个插件</span>
            </div>
            <div class="card-actions">
              <el-button type="primary" :icon="ChatDotRound" @click="enterChat(agent)">对话</el-button>
              <el-button :icon="Edit" @click="openEdit(agent)">编辑</el-button>
              <el-button type="danger" text @click="handleDelete(agent)">删除</el-button>
            </div>
          </article>
          <div v-if="myAgents.length === 0 && !loading" class="empty-card glass-card">
            还没有自建 Agent
          </div>
        </div>
      </section>

      <div v-if="total > 0" class="pagination-wrap">
        <el-pagination
          background
          layout="total, sizes, prev, pager, next, jumper"
          :total="total"
          :current-page="page"
          :page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          @current-change="handlePageChange"
          @size-change="handlePageSizeChange"
        />
      </div>
    </div>

    <el-dialog
      v-model="dialogVisible"
      :title="dialogMode === 'create' ? '自建 Agent' : '编辑 Agent'"
      width="720px"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="110px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" maxlength="40" placeholder="请输入 Agent 名称" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" maxlength="120" placeholder="简短说明用途" />
        </el-form-item>
        <el-form-item label="头像地址">
          <el-input v-model="form.avatar" placeholder="可选，图片 URL" />
        </el-form-item>
        <el-form-item label="默认模型" prop="default_model_code">
          <el-select v-model="form.default_model_code" filterable style="width: 100%">
            <el-option
              v-for="model in models"
              :key="model.logical_model_code"
              :label="model.display_name || model.logical_model_code"
              :value="model.logical_model_code"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="人设" prop="system_prompt">
          <el-input
            v-model="form.system_prompt"
            type="textarea"
            :rows="5"
            maxlength="2000"
            show-word-limit
            placeholder="描述这个 Agent 的角色、边界和回答风格"
          />
        </el-form-item>
        <el-form-item label="Skill">
          <el-select v-model="form.skill_ids" multiple filterable style="width: 100%" placeholder="选择可绑定 Skill">
            <el-option
              v-for="skill in skills"
              :key="skill.id"
              :label="`${skill.name}（${skill.code}）`"
              :value="skill.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="插件">
          <el-select v-model="form.plugin_ids" multiple filterable style="width: 100%" placeholder="选择可绑定插件">
            <el-option
              v-for="plugin in plugins"
              :key="plugin.id"
              :label="`${plugin.name}（${plugin.code}${plugin.is_paid ? '，付费' : ''}）`"
              :value="plugin.id"
            />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.agent-page { padding: 34px 0 42px; }
.agent-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 18px;
  padding: 24px;
}
.page-kicker { color: var(--color-accent); font-size: 13px; font-weight: 700; }
.agent-header h2 { margin: 8px 0 6px; }
.agent-header p,
.agent-card p,
.ability-line,
.empty-card { color: var(--color-text-muted); }
.header-actions,
.card-actions { display: flex; gap: 10px; flex-wrap: wrap; }
.agent-sections { margin-top: 20px; min-height: 240px; }
.section-title { margin: 22px 0 12px; font-weight: 800; font-size: 17px; }
.agent-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(270px, 1fr));
  gap: 16px;
}
.agent-card { padding: 20px; }
.agent-card-head { display: flex; justify-content: space-between; align-items: center; }
.agent-avatar {
  width: 42px;
  height: 42px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  color: #06111f;
  font-weight: 800;
  background: linear-gradient(135deg, #38bdf8, #818cf8);
}
.agent-avatar.mine { background: linear-gradient(135deg, #34d399, #38bdf8); }
.agent-card h3 { margin: 16px 0 8px; }
.agent-card p { min-height: 44px; line-height: 1.6; }
.ability-line {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 16px 0;
  font-size: 12px;
}
.ability-line span {
  padding: 4px 8px;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  background: rgba(15, 23, 42, 0.48);
}
.empty-card { padding: 24px; }
.pagination-wrap { margin-top: 20px; display: flex; justify-content: flex-end; }
@media (max-width: 800px) {
  .agent-header { flex-direction: column; align-items: stretch; }
}
</style>
