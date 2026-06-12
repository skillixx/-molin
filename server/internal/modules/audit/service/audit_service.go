package service

import (
	"context"
	"encoding/json"
	"log"

	"molin/server/internal/modules/audit/model"
	"molin/server/internal/modules/audit/repository"
)

// AuditService 审计日志服务，供 auth/iam/identity 等模块调用写入审计记录。
// 注意：audit 模块不依赖任何业务模块，只被其他模块依赖，避免循环引用。
type AuditService struct {
	repo *repository.AuditLogRepository
}

func NewAuditService(repo *repository.AuditLogRepository) *AuditService {
	return &AuditService{repo: repo}
}

// Record 写入一条审计日志。
// operatorID 为 nil 表示系统操作；targetType/targetID 用于标识被操作对象（如 "user"/"123"）；
// requestSummary 会被序列化为 JSON 字符串存入 request_summary 字段，可为 nil。
// 审计写入失败不应阻断主业务流程：本方法内部会记录警告日志，调用方在业务关键路径上
// 即使收到 error 也不应中断已经成功的主操作。
func (s *AuditService) Record(ctx context.Context, operatorID *uint64, module, action string, targetType, targetID *string, ip string, requestSummary any) error {
	var summary *string
	if requestSummary != nil {
		raw, err := json.Marshal(requestSummary)
		if err != nil {
			log.Printf("[audit] 序列化 request_summary 失败: module=%s action=%s err=%v", module, action, err)
		} else {
			s := string(raw)
			summary = &s
		}
	}

	var ipPtr *string
	if ip != "" {
		ipPtr = &ip
	}

	entry := &model.AuditLog{
		OperatorID:     operatorID,
		Module:         module,
		Action:         action,
		TargetType:     targetType,
		TargetID:       targetID,
		IP:             ipPtr,
		RequestSummary: summary,
	}

	if err := s.repo.Create(ctx, entry); err != nil {
		log.Printf("[audit] 写入审计日志失败: module=%s action=%s err=%v", module, action, err)
		return err
	}
	return nil
}

// ListPaged 分页查询审计日志，支持按 module/action 过滤。
func (s *AuditService) ListPaged(ctx context.Context, module, action string, offset, limit int) ([]model.AuditLog, int64, error) {
	return s.repo.ListPaged(ctx, module, action, offset, limit)
}
