<template>
  <div class="workbench-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">Agent 工作台配置</h3>
        <p class="page-subtitle">配置官方 Agent、Skill 与插件；Agent / Skill / 插件本身免费，仅模型 Token 调用计费</p>
      </div>
    </div>

    <el-tabs v-model="activeTab" class="config-tabs">
      <el-tab-pane label="Agent" name="agents">
        <div class="toolbar">
          <el-select v-model="agentQuery.category" clearable placeholder="分类" style="width: 140px" @change="fetchAgents">
            <el-option v-for="category in agentCategories" :key="category.code" :label="category.name" :value="category.code" />
          </el-select>
          <el-select v-model="agentQuery.visible_scope" clearable placeholder="可见范围" style="width: 150px" @change="fetchAgents">
            <el-option label="全体可见" value="all" />
            <el-option label="指定分组" value="groups" />
            <el-option label="指定角色" value="roles" />
          </el-select>
          <el-select v-model="agentQuery.status" clearable placeholder="状态" style="width: 140px" @change="fetchAgents">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
          <el-button :icon="Refresh" :loading="agentLoading" @click="fetchAgents">刷新</el-button>
          <el-button type="primary" :icon="Plus" @click="openAgentCreate">新建 Agent</el-button>
        </div>
        <el-table :data="agents" v-loading="agentLoading" border>
          <el-table-column prop="code" label="代码" min-width="140" />
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column label="分类" width="110">
            <template #default="{ row }">{{ row.category_name || '未分类' }}</template>
          </el-table-column>
          <el-table-column prop="default_model_code" label="默认模型" min-width="150" />
          <el-table-column label="可见范围" width="120">
            <template #default="{ row }">{{ visibleScopeLabel(row.visible_scope) }}</template>
          </el-table-column>
          <el-table-column label="绑定" min-width="180">
            <template #default="{ row }">{{ row.skills.length }} 个 Skill / {{ row.plugins.length }} 个插件</template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="row.status === 'active' ? 'success' : 'info'">
                {{ row.status === 'active' ? '启用' : '停用' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="sort_order" label="排序" width="80" />
          <el-table-column label="操作" width="190" fixed="right">
            <template #default="{ row }">
              <el-button type="primary" text size="small" @click="openAgentEdit(row)">编辑</el-button>
              <el-button type="danger" text size="small" @click="deleteAgentRow(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <PaginationBar :pagination="agentPagination" @page="changeAgentPage" @size="changeAgentSize" />
      </el-tab-pane>

      <el-tab-pane label="Skill" name="skills">
        <div class="toolbar">
          <el-select v-model="skillQuery.status" clearable placeholder="状态" style="width: 140px" @change="fetchSkills">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
          <el-input v-model="skillQuery.category" clearable placeholder="分类" style="width: 180px" @keyup.enter="fetchSkills" />
          <el-button :icon="Refresh" :loading="skillLoading" @click="fetchSkills">刷新</el-button>
          <el-button type="primary" :icon="Plus" @click="openSkillCreate">新建 Skill</el-button>
        </div>
        <el-table :data="skills" v-loading="skillLoading" border>
          <el-table-column prop="code" label="代码" min-width="140" />
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column prop="category" label="分类" width="120" />
          <el-table-column prop="handler_key" label="处理器" min-width="160" />
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="row.status === 'active' ? 'success' : 'info'">
                {{ row.status === 'active' ? '启用' : '停用' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="170" fixed="right">
            <template #default="{ row }">
              <el-button type="primary" text size="small" @click="openSkillEdit(row)">编辑</el-button>
              <el-button type="danger" text size="small" @click="deleteSkillRow(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <PaginationBar :pagination="skillPagination" @page="changeSkillPage" @size="changeSkillSize" />
      </el-tab-pane>

      <el-tab-pane label="插件" name="plugins">
        <div class="toolbar">
          <el-select v-model="pluginQuery.status" clearable placeholder="状态" style="width: 140px" @change="fetchPlugins">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
          <el-button :icon="Refresh" :loading="pluginLoading" @click="fetchPlugins">刷新</el-button>
          <el-button type="primary" :icon="Plus" @click="openPluginCreate">新建插件</el-button>
        </div>
        <el-table :data="plugins" v-loading="pluginLoading" border>
          <el-table-column prop="code" label="代码" min-width="130" />
          <el-table-column prop="name" label="名称" min-width="130" />
          <el-table-column prop="endpoint_url" label="Endpoint" min-width="240" show-overflow-tooltip />
          <el-table-column label="鉴权" width="100">
            <template #default="{ row }">
              <el-tag :type="row.has_auth ? 'success' : 'info'">{{ row.has_auth ? '已配置' : '未配置' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="付费" width="90">
            <template #default="{ row }">{{ row.is_paid ? '是' : '否' }}</template>
          </el-table-column>
          <el-table-column prop="daily_limit" label="日限额" width="100" />
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="row.status === 'active' ? 'success' : 'info'">
                {{ row.status === 'active' ? '启用' : '停用' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="170" fixed="right">
            <template #default="{ row }">
              <el-button type="primary" text size="small" @click="openPluginEdit(row)">编辑</el-button>
              <el-button type="danger" text size="small" @click="deletePluginRow(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <PaginationBar :pagination="pluginPagination" @page="changePluginPage" @size="changePluginSize" />
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="agentDialog" :title="agentMode === 'create' ? '新建 Agent' : '编辑 Agent'" width="760px">
      <el-form ref="agentFormRef" :model="agentForm" :rules="agentRules" label-width="110px">
        <el-form-item label="代码" prop="code">
          <el-input v-model="agentForm.code" :disabled="agentMode === 'edit'" placeholder="官方 Agent 唯一代码" />
        </el-form-item>
        <el-form-item label="名称" prop="name"><el-input v-model="agentForm.name" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="agentForm.description" /></el-form-item>
        <el-form-item label="头像地址"><el-input v-model="agentForm.avatar" /></el-form-item>
        <el-form-item label="默认模型" prop="default_model_code">
          <el-select v-model="agentForm.default_model_code" filterable style="width: 100%">
            <el-option v-for="model in models" :key="model.id" :label="model.display_name" :value="model.logical_model_code" />
          </el-select>
        </el-form-item>
        <el-form-item label="分类">
          <el-select v-model="agentForm.category_code" clearable filterable style="width: 100%" placeholder="未分类">
            <el-option v-for="category in agentCategories" :key="category.code" :label="category.name" :value="category.code" />
          </el-select>
        </el-form-item>
        <el-form-item label="人设" prop="system_prompt">
          <el-input v-model="agentForm.system_prompt" type="textarea" :rows="5" />
        </el-form-item>
        <el-form-item label="可见范围" prop="visible_scope">
          <el-radio-group v-model="agentForm.visible_scope">
            <el-radio-button label="all">全体可见</el-radio-button>
            <el-radio-button label="groups">指定分组</el-radio-button>
            <el-radio-button label="roles">指定角色</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="agentForm.visible_scope === 'groups'" label="可见分组">
          <el-select v-model="agentForm.group_ids" multiple filterable style="width: 100%" placeholder="选择分组">
            <el-option v-for="group in groups" :key="group.id" :label="`${group.name}（${group.code}）`" :value="group.id" />
          </el-select>
          <div class="form-tip">指定分组必选；组内角色留空表示该组任意成员可见。</div>
        </el-form-item>
        <el-form-item v-if="agentForm.visible_scope === 'groups'" label="组内角色">
          <el-checkbox-group v-model="agentForm.group_roles">
            <el-checkbox label="admin">组管理员</el-checkbox>
            <el-checkbox label="member">普通成员</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <el-form-item v-if="agentForm.visible_scope === 'roles'" label="全局角色">
          <el-select v-model="agentForm.role_codes" multiple filterable style="width: 100%" placeholder="选择角色">
            <el-option v-for="role in roles" :key="role.code" :label="`${role.name}（${role.code}）`" :value="role.code" />
          </el-select>
        </el-form-item>
        <el-form-item label="Skill">
          <el-select v-model="agentForm.skill_ids" multiple filterable style="width: 100%">
            <el-option v-for="skill in skills" :key="skill.id" :label="`${skill.name}（${skill.code}）`" :value="skill.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="插件">
          <el-select v-model="agentForm.plugin_ids" multiple filterable style="width: 100%">
            <el-option v-for="plugin in plugins" :key="plugin.id" :label="`${plugin.name}（${plugin.code}）`" :value="plugin.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="MCP server">
          <el-checkbox v-model="agentForm.update_mcp_binding">
            本次覆盖 MCP server 绑定
          </el-checkbox>
          <el-select
            v-model="agentForm.mcp_server_ids"
            multiple
            filterable
            style="width: 100%; margin-top: 8px"
            placeholder="选择 MCP server"
            :disabled="!agentForm.update_mcp_binding"
          >
            <el-option v-for="server in mcpServers" :key="server.id" :label="`${server.name}（${server.code}）`" :value="server.id" />
          </el-select>
          <div class="form-tip">MCP 绑定为覆盖语义；勾选后保存会提交当前完整集合，空数组表示全部解绑。</div>
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="agentForm.status" style="width: 100%">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
        </el-form-item>
        <el-form-item label="排序"><el-input-number v-model="agentForm.sort_order" :min="0" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="agentDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveAgent">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="skillDialog" :title="skillMode === 'create' ? '新建 Skill' : '编辑 Skill'" width="760px">
      <el-form ref="skillFormRef" :model="skillForm" :rules="skillRules" label-width="120px">
        <el-form-item label="代码" prop="code"><el-input v-model="skillForm.code" :disabled="skillMode === 'edit'" /></el-form-item>
        <el-form-item label="名称" prop="name"><el-input v-model="skillForm.name" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="skillForm.description" /></el-form-item>
        <el-form-item label="分类"><el-input v-model="skillForm.category" /></el-form-item>
        <el-form-item label="处理器" prop="handler_key"><el-input v-model="skillForm.handler_key" /></el-form-item>
        <el-form-item label="工具 Schema" prop="tool_schema_text">
          <el-input v-model="skillForm.tool_schema_text" type="textarea" :rows="8" />
          <div class="form-tip">请输入合法 JSON，结构按 OpenAI tools/function 定义。</div>
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="skillForm.status" style="width: 100%">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="skillDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveSkill">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="pluginDialog" :title="pluginMode === 'create' ? '新建插件' : '编辑插件'" width="780px">
      <el-form ref="pluginFormRef" :model="pluginForm" :rules="pluginRules" label-width="120px">
        <el-form-item label="代码" prop="code"><el-input v-model="pluginForm.code" :disabled="pluginMode === 'edit'" /></el-form-item>
        <el-form-item label="名称" prop="name"><el-input v-model="pluginForm.name" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="pluginForm.description" /></el-form-item>
        <el-form-item label="Endpoint" prop="endpoint_url">
          <el-input v-model="pluginForm.endpoint_url" placeholder="必须是公网 https 地址" />
        </el-form-item>
        <el-form-item label="鉴权配置">
          <el-input
            v-model="pluginForm.auth_config"
            type="textarea"
            :rows="3"
            :disabled="pluginForm.clear_auth"
            :placeholder="pluginMode === 'edit' ? '已配置时不会回显；留空表示不修改' : '明文只写入，不会在响应中回显'"
          />
          <el-checkbox v-if="pluginMode === 'edit'" v-model="pluginForm.clear_auth">
            清空已配置鉴权
          </el-checkbox>
          <div class="form-tip">后端只返回 has_auth，前端绝不回显凭证明文。</div>
        </el-form-item>
        <el-form-item label="工具 Schema" prop="tool_schema_text">
          <el-input v-model="pluginForm.tool_schema_text" type="textarea" :rows="8" />
        </el-form-item>
        <el-form-item label="超时毫秒"><el-input-number v-model="pluginForm.timeout_ms" :min="1" :max="30000" /></el-form-item>
        <el-form-item label="是否付费"><el-switch v-model="pluginForm.is_paid" /></el-form-item>
        <el-form-item label="每日限额"><el-input-number v-model="pluginForm.daily_limit" :min="0" /></el-form-item>
        <el-form-item label="状态">
          <el-select v-model="pluginForm.status" style="width: 100%">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="pluginDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="savePlugin">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { defineComponent, h, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, ElPagination } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import {
  bindAdminAgentMcpServers,
  bindAdminAgentPlugins,
  bindAdminAgentSkills,
  createAdminAgent,
  createAdminPlugin,
  createAdminSkill,
  deleteAdminAgent,
  deleteAdminPlugin,
  deleteAdminSkill,
  listAdminAgentCategories,
  listAdminAgents,
  listAdminMcpServers,
  listAdminPlugins,
  listAdminSkills,
  listTokenModels,
  updateAdminAgent,
  updateAdminPlugin,
  updateAdminSkill,
} from '@/api/token'
import { listGroups } from '@/api/group'
import { listRoles } from '@/api/role'
import type { Pagination } from '@/types/api'
import type { UserGroup } from '@/types/group'
import type {
  AdminAgent,
  AdminAgentCategory,
  AdminAgentVisibleScope,
  AdminMcpServer,
  AdminPlugin,
  AdminSkill,
  AdminWorkbenchStatus,
  CreateAdminAgentReq,
  CreateAdminPluginReq,
  CreateAdminSkillReq,
  TokenModel,
  UpdateAdminAgentReq,
  UpdateAdminPluginReq,
  UpdateAdminSkillReq,
} from '@/types/token'
import type { Role } from '@/types/user'

const PaginationBar = defineComponent({
  props: { pagination: { type: Object, required: true } },
  emits: ['page', 'size'],
  setup(props, { emit }) {
    return () => {
      const p = props.pagination as Pagination
      if (!p.total) return null
      return h('div', { class: 'list-pagination' }, [
        h(ElPagination, {
          background: true,
          total: p.total,
          currentPage: p.page,
          pageSize: p.page_size,
          pageSizes: [10, 20, 50, 100],
          layout: 'total, sizes, prev, pager, next, jumper',
          onCurrentChange: (page: number) => emit('page', page),
          onSizeChange: (size: number) => emit('size', size),
        }),
      ])
    }
  },
})

const activeTab = ref('agents')
const saving = ref(false)
const models = ref<TokenModel[]>([])
const agentCategories = ref<AdminAgentCategory[]>([])
const groups = ref<UserGroup[]>([])
const roles = ref<Role[]>([])
const mcpServers = ref<AdminMcpServer[]>([])

const agents = ref<AdminAgent[]>([])
const skills = ref<AdminSkill[]>([])
const plugins = ref<AdminPlugin[]>([])
const agentLoading = ref(false)
const skillLoading = ref(false)
const pluginLoading = ref(false)
const agentQuery = reactive<{
  status: AdminWorkbenchStatus | ''
  category: string
  visible_scope: AdminAgentVisibleScope | ''
}>({ status: '', category: '', visible_scope: '' })
const skillQuery = reactive<{ status: AdminWorkbenchStatus | ''; category: string }>({ status: '', category: '' })
const pluginQuery = reactive<{ status: AdminWorkbenchStatus | '' }>({ status: '' })
const agentPagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })
const skillPagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })
const pluginPagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const agentDialog = ref(false)
const skillDialog = ref(false)
const pluginDialog = ref(false)
const agentMode = ref<'create' | 'edit'>('create')
const skillMode = ref<'create' | 'edit'>('create')
const pluginMode = ref<'create' | 'edit'>('create')
const editingAgentId = ref(0)
const editingSkillId = ref(0)
const editingPluginId = ref(0)
const agentFormRef = ref<FormInstance>()
const skillFormRef = ref<FormInstance>()
const pluginFormRef = ref<FormInstance>()

const agentForm = reactive({
  code: '',
  name: '',
  description: '',
  avatar: '',
  system_prompt: '',
  default_model_code: '',
  category_code: '',
  visible_scope: 'all' as AdminAgentVisibleScope,
  group_ids: [] as number[],
  group_roles: [] as Array<'admin' | 'member'>,
  role_codes: [] as string[],
  status: 'active' as AdminWorkbenchStatus,
  sort_order: 0,
  skill_ids: [] as number[],
  plugin_ids: [] as number[],
  update_mcp_binding: false,
  mcp_server_ids: [] as number[],
})
const skillForm = reactive({
  code: '',
  name: '',
  description: '',
  category: '',
  handler_key: '',
  tool_schema_text: '{\n  "type": "function",\n  "function": {\n    "name": "",\n    "description": "",\n    "parameters": { "type": "object", "properties": {} }\n  }\n}',
  status: 'active' as AdminWorkbenchStatus,
})
const pluginForm = reactive({
  code: '',
  name: '',
  description: '',
  endpoint_url: '',
  auth_config: '',
  clear_auth: false,
  tool_schema_text: '{\n  "type": "function",\n  "function": {\n    "name": "",\n    "description": "",\n    "parameters": { "type": "object", "properties": {} }\n  }\n}',
  timeout_ms: 10000,
  is_paid: false,
  daily_limit: 0,
  status: 'active' as AdminWorkbenchStatus,
})

const agentRules: FormRules = {
  code: [{ required: true, message: '请输入代码', trigger: 'blur' }],
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  system_prompt: [{ required: true, message: '请输入人设', trigger: 'blur' }],
  default_model_code: [{ required: true, message: '请选择默认模型', trigger: 'change' }],
}
const skillRules: FormRules = {
  code: [{ required: true, message: '请输入代码', trigger: 'blur' }],
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  handler_key: [{ required: true, message: '请输入处理器', trigger: 'blur' }],
  tool_schema_text: [{ required: true, message: '请输入工具 Schema', trigger: 'blur' }],
}
const pluginRules: FormRules = {
  code: [{ required: true, message: '请输入代码', trigger: 'blur' }],
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  endpoint_url: [{ required: true, message: '请输入 Endpoint', trigger: 'blur' }],
  tool_schema_text: [{ required: true, message: '请输入工具 Schema', trigger: 'blur' }],
}

onMounted(() => {
  fetchModels()
  fetchAgentCategories()
  fetchAudienceOptions()
  fetchMcpServers()
  fetchAgents()
  fetchSkills()
  fetchPlugins()
})

async function fetchModels() {
  const res = await listTokenModels({ page: 1, page_size: 100 })
  models.value = res.items
}

async function fetchAgentCategories() {
  const res = await listAdminAgentCategories()
  agentCategories.value = res.items
}

async function fetchAudienceOptions() {
  const [groupRes, roleRes] = await Promise.all([
    listGroups({ page: 1, page_size: 100 }),
    listRoles({ page: 1, page_size: 100 }),
  ])
  groups.value = groupRes.items
  roles.value = roleRes.items
}

async function fetchMcpServers() {
  const res = await listAdminMcpServers({ page: 1, page_size: 100 })
  mcpServers.value = res.items
}

async function fetchAgents() {
  agentLoading.value = true
  try {
    const res = await listAdminAgents({
      page: agentPagination.page,
      page_size: agentPagination.page_size,
      owner_type: 'official',
      status: agentQuery.status || undefined,
      category: agentQuery.category || undefined,
      visible_scope: agentQuery.visible_scope || undefined,
    })
    agents.value = res.items
    Object.assign(agentPagination, { page: res.page, page_size: res.page_size, total: res.total })
  } finally {
    agentLoading.value = false
  }
}

async function fetchSkills() {
  skillLoading.value = true
  try {
    const res = await listAdminSkills({
      page: skillPagination.page,
      page_size: skillPagination.page_size,
      status: skillQuery.status || undefined,
      category: skillQuery.category || undefined,
    })
    skills.value = res.items
    Object.assign(skillPagination, { page: res.page, page_size: res.page_size, total: res.total })
  } finally {
    skillLoading.value = false
  }
}

async function fetchPlugins() {
  pluginLoading.value = true
  try {
    const res = await listAdminPlugins({
      page: pluginPagination.page,
      page_size: pluginPagination.page_size,
      status: pluginQuery.status || undefined,
    })
    plugins.value = res.items
    Object.assign(pluginPagination, { page: res.page, page_size: res.page_size, total: res.total })
  } finally {
    pluginLoading.value = false
  }
}

function parseJson(text: string) {
  try {
    return JSON.parse(text) as Record<string, unknown>
  } catch {
    ElMessage.error('工具 Schema 必须是合法 JSON')
    return null
  }
}

function openAgentCreate() {
  agentMode.value = 'create'
  editingAgentId.value = 0
  Object.assign(agentForm, {
    code: '',
    name: '',
    description: '',
    avatar: '',
    system_prompt: '',
    default_model_code: models.value[0]?.logical_model_code || '',
    category_code: '',
    visible_scope: 'all',
    group_ids: [],
    group_roles: [],
    role_codes: [],
    status: 'active',
    sort_order: 0,
    skill_ids: [],
    plugin_ids: [],
    update_mcp_binding: false,
    mcp_server_ids: [],
  })
  agentDialog.value = true
}

function openAgentEdit(row: AdminAgent) {
  agentMode.value = 'edit'
  editingAgentId.value = row.id
  Object.assign(agentForm, {
    code: row.code || '',
    name: row.name,
    description: row.description || '',
    avatar: row.avatar || '',
    system_prompt: row.system_prompt,
    default_model_code: row.default_model_code,
    category_code: row.category_code || '',
    visible_scope: row.visible_scope || 'all',
    group_ids: row.target_audience?.group_ids || [],
    group_roles: row.target_audience?.group_roles || [],
    role_codes: row.target_audience?.role_codes || [],
    status: row.status,
    sort_order: row.sort_order,
    skill_ids: row.skills.map((item) => item.id),
    plugin_ids: row.plugins.map((item) => item.id),
    update_mcp_binding: false,
    mcp_server_ids: [],
  })
  agentDialog.value = true
}

async function saveAgent() {
  const valid = await agentFormRef.value?.validate().catch(() => false)
  if (!valid) return
  if (agentForm.visible_scope === 'groups' && agentForm.group_ids.length === 0) {
    ElMessage.error('指定分组可见时必须选择至少一个分组')
    return
  }
  if (agentForm.visible_scope === 'roles' && agentForm.role_codes.length === 0) {
    ElMessage.error('指定角色可见时必须选择至少一个角色')
    return
  }
  saving.value = true
  try {
    const payload: CreateAdminAgentReq | UpdateAdminAgentReq = {
      code: agentForm.code,
      name: agentForm.name,
      description: agentForm.description,
      avatar: agentForm.avatar,
      system_prompt: agentForm.system_prompt,
      default_model_code: agentForm.default_model_code,
      category_code: agentForm.category_code,
      visible_scope: agentForm.visible_scope,
      group_ids: agentForm.visible_scope === 'groups' ? agentForm.group_ids : [],
      group_roles: agentForm.visible_scope === 'groups' ? agentForm.group_roles : [],
      role_codes: agentForm.visible_scope === 'roles' ? agentForm.role_codes : [],
      status: agentForm.status,
      sort_order: agentForm.sort_order,
      skill_ids: agentForm.skill_ids,
      plugin_ids: agentForm.plugin_ids,
    }
    let saved: AdminAgent
    if (agentMode.value === 'create') saved = await createAdminAgent(payload as CreateAdminAgentReq)
    else saved = await updateAdminAgent(editingAgentId.value, payload)
    // 绑定接口是覆盖语义，保存时提交当前完整勾选集，空数组表示全部解绑。
    await bindAdminAgentSkills(saved.id, agentForm.skill_ids)
    await bindAdminAgentPlugins(saved.id, agentForm.plugin_ids)
    if (agentForm.update_mcp_binding) {
      await bindAdminAgentMcpServers(saved.id, agentForm.mcp_server_ids)
    }
    ElMessage.success('Agent 保存成功')
    agentDialog.value = false
    fetchAgents()
  } finally {
    saving.value = false
  }
}

function visibleScopeLabel(scope: AdminAgentVisibleScope) {
  if (scope === 'groups') return '指定分组'
  if (scope === 'roles') return '指定角色'
  return '全体可见'
}

function openSkillCreate() {
  skillMode.value = 'create'
  editingSkillId.value = 0
  Object.assign(skillForm, {
    code: '',
    name: '',
    description: '',
    category: '',
    handler_key: '',
    tool_schema_text: '{\n  "type": "function",\n  "function": {\n    "name": "",\n    "description": "",\n    "parameters": { "type": "object", "properties": {} }\n  }\n}',
    status: 'active',
  })
  skillDialog.value = true
}

function openSkillEdit(row: AdminSkill) {
  skillMode.value = 'edit'
  editingSkillId.value = row.id
  Object.assign(skillForm, {
    code: row.code,
    name: row.name,
    description: row.description || '',
    category: row.category || '',
    handler_key: row.handler_key,
    tool_schema_text: JSON.stringify(row.tool_schema_json, null, 2),
    status: row.status,
  })
  skillDialog.value = true
}

async function saveSkill() {
  const valid = await skillFormRef.value?.validate().catch(() => false)
  if (!valid) return
  const schema = parseJson(skillForm.tool_schema_text)
  if (!schema) return
  saving.value = true
  try {
    const payload: CreateAdminSkillReq | UpdateAdminSkillReq = { ...skillForm, tool_schema_json: schema }
    delete (payload as { tool_schema_text?: string }).tool_schema_text
    if (skillMode.value === 'create') await createAdminSkill(payload as CreateAdminSkillReq)
    else await updateAdminSkill(editingSkillId.value, payload)
    ElMessage.success('Skill 保存成功')
    skillDialog.value = false
    fetchSkills()
  } finally {
    saving.value = false
  }
}

function openPluginCreate() {
  pluginMode.value = 'create'
  editingPluginId.value = 0
  Object.assign(pluginForm, {
    code: '',
    name: '',
    description: '',
    endpoint_url: '',
    auth_config: '',
    clear_auth: false,
    tool_schema_text: '{\n  "type": "function",\n  "function": {\n    "name": "",\n    "description": "",\n    "parameters": { "type": "object", "properties": {} }\n  }\n}',
    timeout_ms: 10000,
    is_paid: false,
    daily_limit: 0,
    status: 'active',
  })
  pluginDialog.value = true
}

function openPluginEdit(row: AdminPlugin) {
  pluginMode.value = 'edit'
  editingPluginId.value = row.id
  Object.assign(pluginForm, {
    code: row.code,
    name: row.name,
    description: row.description || '',
    endpoint_url: row.endpoint_url,
    // 插件鉴权配置只入不出，编辑态必须留空，避免误导为已回显凭证。
    auth_config: '',
    clear_auth: false,
    tool_schema_text: JSON.stringify(row.tool_schema_json, null, 2),
    timeout_ms: row.timeout_ms,
    is_paid: row.is_paid,
    daily_limit: row.daily_limit || 0,
    status: row.status,
  })
  pluginDialog.value = true
}

async function savePlugin() {
  const valid = await pluginFormRef.value?.validate().catch(() => false)
  if (!valid) return
  if (!pluginForm.endpoint_url.startsWith('https://')) {
    ElMessage.error('Endpoint 必须使用 https 公网地址')
    return
  }
  const schema = parseJson(pluginForm.tool_schema_text)
  if (!schema) return
  saving.value = true
  try {
    const payload: CreateAdminPluginReq | UpdateAdminPluginReq = {
      code: pluginForm.code,
      name: pluginForm.name,
      description: pluginForm.description,
      endpoint_url: pluginForm.endpoint_url,
      tool_schema_json: schema,
      timeout_ms: pluginForm.timeout_ms,
      is_paid: pluginForm.is_paid,
      daily_limit: pluginForm.daily_limit || null,
      status: pluginForm.status,
    }
    if (pluginForm.clear_auth) {
      payload.auth_config = ''
    } else if (pluginMode.value === 'create' || pluginForm.auth_config !== '') {
      payload.auth_config = pluginForm.auth_config
    }
    if (pluginMode.value === 'create') await createAdminPlugin(payload as CreateAdminPluginReq)
    else await updateAdminPlugin(editingPluginId.value, payload)
    ElMessage.success('插件保存成功')
    pluginDialog.value = false
    fetchPlugins()
  } finally {
    saving.value = false
  }
}

async function deleteAgentRow(row: AdminAgent) {
  await ElMessageBox.confirm(`确认删除 Agent「${row.name}」吗？`, '删除确认', { type: 'warning' })
  await deleteAdminAgent(row.id)
  ElMessage.success('Agent 已删除')
  fetchAgents()
}

async function deleteSkillRow(row: AdminSkill) {
  await ElMessageBox.confirm(`确认删除 Skill「${row.name}」吗？`, '删除确认', { type: 'warning' })
  await deleteAdminSkill(row.id)
  ElMessage.success('Skill 已删除')
  fetchSkills()
}

async function deletePluginRow(row: AdminPlugin) {
  await ElMessageBox.confirm(`确认删除插件「${row.name}」吗？`, '删除确认', { type: 'warning' })
  await deleteAdminPlugin(row.id)
  ElMessage.success('插件已删除')
  fetchPlugins()
}

function changeAgentPage(page: number) { agentPagination.page = page; fetchAgents() }
function changeAgentSize(size: number) { agentPagination.page = 1; agentPagination.page_size = size; fetchAgents() }
function changeSkillPage(page: number) { skillPagination.page = page; fetchSkills() }
function changeSkillSize(size: number) { skillPagination.page = 1; skillPagination.page_size = size; fetchSkills() }
function changePluginPage(page: number) { pluginPagination.page = page; fetchPlugins() }
function changePluginSize(size: number) { pluginPagination.page = 1; pluginPagination.page_size = size; fetchPlugins() }
</script>

<style scoped>
.workbench-page { padding: 20px; }
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  margin-bottom: 16px;
}
.page-title-text { margin: 0 0 6px; color: var(--mc-text); }
.page-subtitle { margin: 0; color: var(--mc-text-muted); }
.config-tabs {
  padding: 16px;
  border: 1px solid var(--mc-border-soft);
  border-radius: 8px;
  background: var(--mc-surface);
}
.toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-bottom: 14px;
  flex-wrap: wrap;
}
.form-tip {
  margin-top: 6px;
  color: var(--mc-text-muted);
  font-size: 12px;
  line-height: 1.5;
}
:deep(.list-pagination) {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
