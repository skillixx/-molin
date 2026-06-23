package service

import (
	"context"
	"errors"
	"strings"

	"molin/server/internal/modules/workbench/dto"
	"molin/server/internal/modules/workbench/model"
	"molin/server/internal/modules/workbench/repository"
)

// ErrAgentCodeExists Agent code 已存在（官方编码唯一冲突，非校验类）。
var ErrAgentCodeExists = errors.New("agent code 已存在")

// AgentService Agent 管理服务：管理端官方 Agent CRUD + 绑定，用户端自建 Agent + 只读列表。
// 依赖 skill/plugin 仓库做绑定合法性校验与详情名称回填。
type AgentService struct {
	repo          *repository.AgentRepository
	skillRepo     *repository.SkillRepository
	pluginRepo    *repository.PluginRepository
	categoryRepo  *repository.AgentCategoryRepository
	groupResolver GroupResolver // 定向可见性：分组归属解析（nil → groups 定向不可见，fail-safe）
	roleResolver  RoleResolver  // 定向可见性：全局角色解析（nil → roles 定向不可见，fail-safe）
}

// NewAgentService 创建 Agent 服务实例。
func NewAgentService(repo *repository.AgentRepository, skillRepo *repository.SkillRepository, pluginRepo *repository.PluginRepository, categoryRepo *repository.AgentCategoryRepository) *AgentService {
	return &AgentService{repo: repo, skillRepo: skillRepo, pluginRepo: pluginRepo, categoryRepo: categoryRepo}
}

// WithResolvers 注入定向可见性所需的分组/角色解析器（bootstrap 装配时调用）。
// 任一为 nil 时对应 scope=groups/roles 的 Agent 一律判为不可见（fail-safe），仅 scope=all 正常返回。
func (s *AgentService) WithResolvers(gr GroupResolver, rr RoleResolver) *AgentService {
	s.groupResolver = gr
	s.roleResolver = rr
	return s
}

// validateCategory 校验 category_code：空=未分类(允许，返回 nil 指针)；非空须存在于字典(否则校验错误)。
// 返回归一化后的 *string，供写库（NULL 或具体 code）。
func (s *AgentService) validateCategory(ctx context.Context, code string) (*string, error) {
	c := strings.TrimSpace(code)
	if c == "" {
		return nil, nil
	}
	exists, err := s.categoryRepo.Exists(ctx, c)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, newValidation("category_code 不存在于分类字典")
	}
	return &c, nil
}

// applyCategoryUpdate 把 category_code 更新写入 updates map（nil=不更新；""=置 NULL 未分类；非空须存在）。
func (s *AgentService) applyCategoryUpdate(ctx context.Context, updates map[string]interface{}, code *string) error {
	if code == nil {
		return nil
	}
	category, err := s.validateCategory(ctx, *code)
	if err != nil {
		return err
	}
	updates["category_code"] = category // *string，nil 写 NULL
	return nil
}

// ——— 管理端（官方 Agent） ———

// AdminCreate 管理端创建官方 Agent（owner_type=official）。
func (s *AgentService) AdminCreate(ctx context.Context, req dto.AdminCreateAgentReq) (*dto.AgentResp, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, newValidation("name 不能为空")
	}
	if strings.TrimSpace(req.SystemPrompt) == "" {
		return nil, newValidation("system_prompt 不能为空")
	}
	if strings.TrimSpace(req.DefaultModelCode) == "" {
		return nil, newValidation("default_model_code 不能为空")
	}
	status := req.Status
	if status == "" {
		status = "active"
	}
	if !validResourceStatus[status] {
		return nil, newValidation("status 只能为 active/inactive")
	}

	// 官方 code 唯一性校验（仅在传了 code 时）。
	var code *string
	if c := strings.TrimSpace(req.Code); c != "" {
		if _, err := s.repo.FindByCode(ctx, c); err == nil {
			return nil, ErrAgentCodeExists
		} else if !isNotFound(err) {
			return nil, err
		}
		code = &c
	}

	// 绑定校验：仅可绑存在的 skill/plugin（管理端不强制 active）。
	if err := s.validateBindables(ctx, req.SkillIDs, req.PluginIDs, false); err != nil {
		return nil, err
	}

	// 分类校验：空=未分类；非空须存在于字典。
	category, err := s.validateCategory(ctx, req.CategoryCode)
	if err != nil {
		return nil, err
	}

	// 定向可见性：空 scope 默认 all（兼容现状）。
	scope := strings.TrimSpace(req.VisibleScope)
	if scope == "" {
		scope = scopeAll
	}
	visScope, audience, err := buildVisibility(ctx, visibilityInput{
		Scope: scope, GroupIDs: req.GroupIDs, GroupRoles: req.GroupRoles, RoleCodes: req.RoleCodes,
	}, s.groupResolver, s.roleResolver)
	if err != nil {
		return nil, err
	}

	a := &model.Agent{
		Code:             code,
		Name:             strings.TrimSpace(req.Name),
		Description:      req.Description,
		Avatar:           req.Avatar,
		OwnerType:        "official",
		SystemPrompt:     req.SystemPrompt,
		DefaultModelCode: strings.TrimSpace(req.DefaultModelCode),
		CategoryCode:     category,
		Status:           status,
		VisibleScope:     visScope,
		TargetAudience:   audience,
		SortOrder:        req.SortOrder,
	}
	if err := s.repo.CreateWithBindings(ctx, a, req.SkillIDs, req.PluginIDs); err != nil {
		return nil, err
	}
	return s.buildResp(ctx, a)
}

// AdminUpdate 管理端更新官方 Agent。
func (s *AgentService) AdminUpdate(ctx context.Context, id uint64, req dto.AdminUpdateAgentReq) (*dto.AgentResp, error) {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.OwnerType != "official" {
		return nil, newValidation("仅可通过管理端编辑官方 Agent")
	}

	updates, err := buildAgentScalarUpdates(req.Name, req.Description, req.Avatar, req.SystemPrompt, req.DefaultModelCode, req.Status, req.SortOrder)
	if err != nil {
		return nil, err
	}
	if err := s.applyCategoryUpdate(ctx, updates, req.CategoryCode); err != nil {
		return nil, err
	}
	// 定向可见性：visible_scope 非 nil 时整体覆盖（连同 group_ids/group_roles/role_codes）。
	if req.VisibleScope != nil {
		visScope, audience, vErr := buildVisibility(ctx, visibilityInput{
			Scope: *req.VisibleScope, GroupIDs: req.GroupIDs, GroupRoles: req.GroupRoles, RoleCodes: req.RoleCodes,
		}, s.groupResolver, s.roleResolver)
		if vErr != nil {
			return nil, vErr
		}
		updates["visible_scope"] = visScope
		updates["target_audience_json"] = audience // *string，scope=all 时 nil 写 NULL
	}
	if err := s.validateBindables(ctx, req.SkillIDs, req.PluginIDs, false); err != nil {
		return nil, err
	}

	if err := s.repo.UpdateWithBindings(ctx, id, updates, req.SkillIDs, req.PluginIDs, req.SkillIDs != nil, req.PluginIDs != nil); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// SetVisibility 独立设置官方 Agent 的定向可见性（PUT /api/admin/agents/{id}/visibility，覆盖语义）。
func (s *AgentService) SetVisibility(ctx context.Context, id uint64, req dto.SetVisibilityReq) (*dto.AgentResp, error) {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.OwnerType != "official" {
		return nil, newValidation("仅可对官方 Agent 设置定向可见性")
	}
	visScope, audience, err := buildVisibility(ctx, visibilityInput{
		Scope: req.VisibleScope, GroupIDs: req.GroupIDs, GroupRoles: req.GroupRoles, RoleCodes: req.RoleCodes,
	}, s.groupResolver, s.roleResolver)
	if err != nil {
		return nil, err
	}
	updates := map[string]interface{}{
		"visible_scope":        visScope,
		"target_audience_json": audience,
	}
	if err := s.repo.Update(ctx, id, updates); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// AdminList 管理端分页列出 Agent（默认 owner_type=official，可传空查全部）；category/visibleScope 非空按对应维度过滤。
func (s *AgentService) AdminList(ctx context.Context, ownerType, status, category, visibleScope string, offset, limit int) ([]dto.AgentResp, int64, error) {
	items, total, err := s.repo.ListAdminPaged(ctx, ownerType, status, category, visibleScope, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	return s.buildRespList(ctx, items, total)
}

// AdminDelete 管理端删除官方 Agent。
func (s *AgentService) AdminDelete(ctx context.Context, id uint64) error {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if a.OwnerType != "official" {
		return newValidation("仅可通过管理端删除官方 Agent")
	}
	return s.repo.Delete(ctx, id)
}

// BindSkills 管理端覆盖式设置 Agent 的 skill 绑定（空列表=全部解绑）。
func (s *AgentService) BindSkills(ctx context.Context, agentID uint64, skillIDs []uint64) (*dto.AgentResp, error) {
	if _, err := s.repo.FindByID(ctx, agentID); err != nil {
		return nil, err
	}
	if err := s.validateBindables(ctx, skillIDs, nil, false); err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceSkillBindings(ctx, agentID, skillIDs); err != nil {
		return nil, err
	}
	return s.Get(ctx, agentID)
}

// BindPlugins 管理端覆盖式设置 Agent 的 plugin 绑定（空列表=全部解绑）。
func (s *AgentService) BindPlugins(ctx context.Context, agentID uint64, pluginIDs []uint64) (*dto.AgentResp, error) {
	if _, err := s.repo.FindByID(ctx, agentID); err != nil {
		return nil, err
	}
	if err := s.validateBindables(ctx, nil, pluginIDs, false); err != nil {
		return nil, err
	}
	if err := s.repo.ReplacePluginBindings(ctx, agentID, pluginIDs); err != nil {
		return nil, err
	}
	return s.Get(ctx, agentID)
}

// ——— 用户端（自建 Agent） ———

// UserCreate 用户自建 Agent（owner_type=user；仅可绑 active 官方 skill/plugin）。
func (s *AgentService) UserCreate(ctx context.Context, userID uint64, req dto.UserCreateAgentReq) (*dto.AgentResp, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, newValidation("name 不能为空")
	}
	if strings.TrimSpace(req.SystemPrompt) == "" {
		return nil, newValidation("system_prompt 不能为空")
	}
	if strings.TrimSpace(req.DefaultModelCode) == "" {
		return nil, newValidation("default_model_code 不能为空")
	}
	// 自建 Agent 只能绑 active 官方 skill/plugin（契约 §3.2）。
	if err := s.validateBindables(ctx, req.SkillIDs, req.PluginIDs, true); err != nil {
		return nil, err
	}
	// 分类校验：契约 §5 允许自建选分类；空=未分类。
	category, err := s.validateCategory(ctx, req.CategoryCode)
	if err != nil {
		return nil, err
	}

	uid := userID
	a := &model.Agent{
		Name:             strings.TrimSpace(req.Name),
		Description:      req.Description,
		Avatar:           req.Avatar,
		OwnerType:        "user",
		OwnerUserID:      &uid,
		SystemPrompt:     req.SystemPrompt,
		DefaultModelCode: strings.TrimSpace(req.DefaultModelCode),
		CategoryCode:     category,
		Status:           "active",
	}
	if err := s.repo.CreateWithBindings(ctx, a, req.SkillIDs, req.PluginIDs); err != nil {
		return nil, err
	}
	return s.buildResp(ctx, a)
}

// UserUpdate 用户更新自建 Agent（越权返回 ErrForbidden）。
func (s *AgentService) UserUpdate(ctx context.Context, userID, id uint64, req dto.UserUpdateAgentReq) (*dto.AgentResp, error) {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !s.ownedBy(a, userID) {
		return nil, ErrForbidden
	}

	updates, err := buildAgentScalarUpdates(req.Name, req.Description, req.Avatar, req.SystemPrompt, req.DefaultModelCode, req.Status, nil)
	if err != nil {
		return nil, err
	}
	if err := s.applyCategoryUpdate(ctx, updates, req.CategoryCode); err != nil {
		return nil, err
	}
	if err := s.validateBindables(ctx, req.SkillIDs, req.PluginIDs, true); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateWithBindings(ctx, id, updates, req.SkillIDs, req.PluginIDs, req.SkillIDs != nil, req.PluginIDs != nil); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// UserDelete 用户删除自建 Agent（越权返回 ErrForbidden）。
func (s *AgentService) UserDelete(ctx context.Context, userID, id uint64) error {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if !s.ownedBy(a, userID) {
		return ErrForbidden
	}
	return s.repo.Delete(ctx, id)
}

// UserList 用户端列出可见 Agent：官方 active（按定向可见性过滤）+ 本人自建；category 非空按分类过滤。
// 官方 Agent 数量少，全量加载候选后在应用层做可见性过滤，再分页（保证分页 total 准确）。
func (s *AgentService) UserList(ctx context.Context, userID uint64, category string, offset, limit int) ([]dto.AgentResp, int64, error) {
	candidates, err := s.repo.ListVisibleCandidates(ctx, userID, category)
	if err != nil {
		return nil, 0, err
	}
	visible := make([]model.Agent, 0, len(candidates))
	for i := range candidates {
		if s.visibleToUser(ctx, &candidates[i], userID) {
			visible = append(visible, candidates[i])
		}
	}
	total := int64(len(visible))
	// 应用层分页（candidates 已按 sort_order ASC, id ASC 排序）。
	start := offset
	if start > len(visible) {
		start = len(visible)
	}
	end := start + limit
	if limit <= 0 || end > len(visible) {
		end = len(visible)
	}
	return s.buildRespList(ctx, visible[start:end], total)
}

// UserGet 用户端查看 Agent 详情：本人自建或对其可见的官方 active，否则越权/不存在。
func (s *AgentService) UserGet(ctx context.Context, userID, id uint64) (*dto.AgentResp, error) {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !s.visibleToUser(ctx, a, userID) {
		return nil, ErrForbidden
	}
	return s.buildResp(ctx, a)
}

// visibleToUser 统一可见性判定：本人自建恒可见；官方须 active 且通过定向 scope 判定。
// 供 UserList / UserGet 复用；与 ChatService 中编排端点判定语义一致。
func (s *AgentService) visibleToUser(ctx context.Context, a *model.Agent, userID uint64) bool {
	if s.ownedBy(a, userID) {
		return true
	}
	if a.OwnerType != "official" || a.Status != "active" {
		return false
	}
	return agentVisibleTo(ctx, a, userID, s.groupResolver, s.roleResolver)
}

// ——— 公共 ———

// Get 按 ID 查询 Agent 详情（含绑定名称）。
func (s *AgentService) Get(ctx context.Context, id uint64) (*dto.AgentResp, error) {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.buildResp(ctx, a)
}

// ownedBy 判断 Agent 是否为某用户自建。
func (s *AgentService) ownedBy(a *model.Agent, userID uint64) bool {
	return a.OwnerType == "user" && a.OwnerUserID != nil && *a.OwnerUserID == userID
}

// validateBindables 校验待绑定的 skill/plugin 均存在；requireActive 时要求 status=active。
// nil 切片表示该类不处理（保留原绑定）；空非 nil 切片表示清空，无需校验。
func (s *AgentService) validateBindables(ctx context.Context, skillIDs, pluginIDs []uint64, requireActive bool) error {
	if len(skillIDs) > 0 {
		found, err := s.skillRepo.FindByIDs(ctx, dedup(skillIDs))
		if err != nil {
			return err
		}
		if err := checkAllPresentActive(len(dedup(skillIDs)), found, requireActive, "skill"); err != nil {
			return err
		}
	}
	if len(pluginIDs) > 0 {
		found, err := s.pluginRepo.FindByIDs(ctx, dedup(pluginIDs))
		if err != nil {
			return err
		}
		if err := checkPluginsPresentActive(len(dedup(pluginIDs)), found, requireActive); err != nil {
			return err
		}
	}
	return nil
}

// buildResp 把 Agent 实体组装为含绑定名称的响应 DTO（单个，自行联字典取分类名称）。
func (s *AgentService) buildResp(ctx context.Context, a *model.Agent) (*dto.AgentResp, error) {
	categoryName := ""
	if a.CategoryCode != nil && *a.CategoryCode != "" {
		if c, err := s.categoryRepo.FindByCode(ctx, *a.CategoryCode); err == nil {
			categoryName = c.Name
		} else if !isNotFound(err) {
			return nil, err
		}
	}
	return s.buildRespWithCategoryName(ctx, a, categoryName)
}

// buildRespWithCategoryName 组装响应 DTO，分类名称由调用方提供（列表批量回填用，避免 N+1）。
func (s *AgentService) buildRespWithCategoryName(ctx context.Context, a *model.Agent, categoryName string) (*dto.AgentResp, error) {
	skillIDs, err := s.repo.ListSkillIDs(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	pluginIDs, err := s.repo.ListPluginIDs(ctx, a.ID)
	if err != nil {
		return nil, err
	}

	skills := make([]dto.BoundResource, 0, len(skillIDs))
	if len(skillIDs) > 0 {
		found, err := s.skillRepo.FindByIDs(ctx, skillIDs)
		if err != nil {
			return nil, err
		}
		for i := range found {
			skills = append(skills, dto.BoundResource{ID: found[i].ID, Code: found[i].Code, Name: found[i].Name})
		}
	}
	plugins := make([]dto.BoundResource, 0, len(pluginIDs))
	if len(pluginIDs) > 0 {
		found, err := s.pluginRepo.FindByIDs(ctx, pluginIDs)
		if err != nil {
			return nil, err
		}
		for i := range found {
			plugins = append(plugins, dto.BoundResource{ID: found[i].ID, Code: found[i].Code, Name: found[i].Name})
		}
	}

	scope := a.VisibleScope
	if scope == "" {
		scope = scopeAll
	}
	var audience *dto.TargetAudienceResp
	if ta := audienceForResp(scope, a.TargetAudience); ta != nil {
		audience = &dto.TargetAudienceResp{
			GroupIDs:   ta.GroupIDs,
			GroupRoles: ta.GroupRoles,
			RoleCodes:  ta.RoleCodes,
		}
	}

	return &dto.AgentResp{
		ID:               a.ID,
		Code:             a.Code,
		Name:             a.Name,
		Description:      a.Description,
		Avatar:           a.Avatar,
		OwnerType:        a.OwnerType,
		OwnerUserID:      a.OwnerUserID,
		SystemPrompt:     a.SystemPrompt,
		DefaultModelCode: a.DefaultModelCode,
		CategoryCode:     a.CategoryCode,
		CategoryName:     categoryName,
		Status:           a.Status,
		VisibleScope:     scope,
		TargetAudience:   audience,
		SortOrder:        a.SortOrder,
		Skills:           skills,
		Plugins:          plugins,
		CreatedAt:        a.CreatedAt,
		UpdatedAt:        a.UpdatedAt,
	}, nil
}

// buildRespList 批量组装响应（逐个回填绑定）；分类名称用一次批量字典查询回填，避免 N+1。
func (s *AgentService) buildRespList(ctx context.Context, items []model.Agent, total int64) ([]dto.AgentResp, int64, error) {
	// 收集出现的分类 code，批量取名称。
	codeSet := make(map[string]struct{}, len(items))
	for i := range items {
		if items[i].CategoryCode != nil && *items[i].CategoryCode != "" {
			codeSet[*items[i].CategoryCode] = struct{}{}
		}
	}
	codes := make([]string, 0, len(codeSet))
	for c := range codeSet {
		codes = append(codes, c)
	}
	nameByCode, err := s.categoryRepo.MapByCodes(ctx, codes)
	if err != nil {
		return nil, 0, err
	}

	resp := make([]dto.AgentResp, 0, len(items))
	for i := range items {
		name := ""
		if items[i].CategoryCode != nil {
			name = nameByCode[*items[i].CategoryCode]
		}
		r, err := s.buildRespWithCategoryName(ctx, &items[i], name)
		if err != nil {
			return nil, 0, err
		}
		resp = append(resp, *r)
	}
	return resp, total, nil
}

// buildAgentScalarUpdates 把可空标量字段汇总为 updates map（统一供管理端/用户端复用）。
func buildAgentScalarUpdates(name, description, avatar, systemPrompt, defaultModelCode, status *string, sortOrder *int) (map[string]interface{}, error) {
	updates := map[string]interface{}{}
	if name != nil {
		if strings.TrimSpace(*name) == "" {
			return nil, newValidation("name 不能为空")
		}
		updates["name"] = strings.TrimSpace(*name)
	}
	if description != nil {
		updates["description"] = *description
	}
	if avatar != nil {
		updates["avatar"] = *avatar
	}
	if systemPrompt != nil {
		if strings.TrimSpace(*systemPrompt) == "" {
			return nil, newValidation("system_prompt 不能为空")
		}
		updates["system_prompt"] = *systemPrompt
	}
	if defaultModelCode != nil {
		if strings.TrimSpace(*defaultModelCode) == "" {
			return nil, newValidation("default_model_code 不能为空")
		}
		updates["default_model_code"] = strings.TrimSpace(*defaultModelCode)
	}
	if status != nil {
		if !validResourceStatus[*status] {
			return nil, newValidation("status 只能为 active/inactive")
		}
		updates["status"] = *status
	}
	if sortOrder != nil {
		updates["sort_order"] = *sortOrder
	}
	return updates, nil
}

// checkAllPresentActive 校验 skill 集合全部命中（且按需 active）。
func checkAllPresentActive(want int, found []model.Skill, requireActive bool, kind string) error {
	if len(found) != want {
		return newValidation(kind + " 含不存在的 ID")
	}
	if requireActive {
		for i := range found {
			if found[i].Status != "active" {
				return newValidation(kind + " 含未上架（非 active）项，不可绑定")
			}
		}
	}
	return nil
}

// checkPluginsPresentActive 校验 plugin 集合全部命中（且按需 active）。
func checkPluginsPresentActive(want int, found []model.Plugin, requireActive bool) error {
	if len(found) != want {
		return newValidation("plugin 含不存在的 ID")
	}
	if requireActive {
		for i := range found {
			if found[i].Status != "active" {
				return newValidation("plugin 含未上架（非 active）项，不可绑定")
			}
		}
	}
	return nil
}

// dedup 去重 uint64 切片，保持稳定。
func dedup(ids []uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(ids))
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
