package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestAILedgerTableNamesAndOrthogonalStates(t *testing.T) {
	if (AIProject{}).TableName() != "ai_projects" ||
		(AIRequest{}).TableName() != "ai_requests" ||
		(AIUsageItem{}).TableName() != "ai_usage_items" ||
		(AIExecutionAttempt{}).TableName() != "ai_execution_attempts" {
		t.Fatal("AI 网关 G1 领域模型必须与 000060 Expand Migration 表名一致")
	}

	moderation := []string{AIModerationPending, AIModerationPassed, AIModerationRejected, AIModerationError}
	execution := []string{AIExecutionPending, AIExecutionRunning, AIExecutionSucceeded, AIExecutionFailed, AIExecutionCancelled, AIExecutionUnknown}
	billing := []string{AIBillingUnquoted, AIBillingHeld, AIBillingSettlementPending, AIBillingSettled, AIBillingReleased, AIBillingException}
	assertUniqueStates(t, "moderation", moderation, 4)
	assertUniqueStates(t, "execution", execution, 6)
	assertUniqueStates(t, "billing", billing, 6)
}

func TestAILedgerGORMColumnsMatchFrozenContract(t *testing.T) {
	models := []struct {
		value    interface{}
		required []string
	}{
		{value: &AIProject{}, required: []string{"id", "user_id", "name", "status", "monthly_budget", "budget_mode", "timezone"}},
		{value: &AIRequest{}, required: []string{"request_id", "idempotency_key", "request_fingerprint", "user_id", "project_id", "api_key_id", "logical_model_code", "execution_model_code", "moderation_status", "execution_status", "billing_status", "version_no"}},
		{value: &AIUsageItem{}, required: []string{"request_id", "meter_type", "source", "sequence_no", "quantity", "unit_price", "amount"}},
		{value: &AIExecutionAttempt{}, required: []string{"request_id", "attempt_no", "execution_driver", "provider_code", "endpoint_code", "execution_model_code", "upstream_request_id", "status", "result_unknown", "latency_ms"}},
	}

	for _, item := range models {
		parsed, err := schema.Parse(item.value, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("解析 GORM Schema 失败: %v", err)
		}
		for _, column := range item.required {
			if parsed.FieldsByDBName[column] == nil {
				t.Fatalf("Go 模型 %s 缺少冻结列 %s", parsed.Table, column)
			}
		}
	}
}

func TestAIProjectGORMCompositeOwnerIndex(t *testing.T) {
	parsed, err := schema.Parse(&AIProject{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("解析 AIProject GORM Schema 失败: %v", err)
	}
	var index *schema.Index
	for _, candidate := range parsed.ParseIndexes() {
		if candidate.Name == "uk_ai_projects_id_user" {
			index = candidate
			break
		}
	}
	if index == nil || index.Class != "UNIQUE" || len(index.Fields) != 2 || index.Fields[0].DBName != "id" || index.Fields[1].DBName != "user_id" {
		t.Fatalf("AIProject GORM 复合归属索引与 Migration 不一致: %+v", index)
	}
}

func TestAILedgerJSONDoesNotExposeInternalTopology(t *testing.T) {
	executionModel := "bailian/qwen-turbo"
	requestRaw, err := json.Marshal(AIRequest{RequestID: "req-public", ExecutionModelCode: &executionModel})
	if err != nil {
		t.Fatalf("序列化 AIRequest 失败: %v", err)
	}
	attemptRaw, err := json.Marshal(AIExecutionAttempt{ProviderCode: "bailian", EndpointCode: &executionModel, ExecutionModelCode: executionModel, UpstreamRequestID: &executionModel})
	if err != nil {
		t.Fatalf("序列化 AIExecutionAttempt 失败: %v", err)
	}
	for _, raw := range [][]byte{requestRaw, attemptRaw} {
		for _, forbidden := range []string{"bailian", "execution_model_code", "provider_code", "endpoint_code", "upstream_request_id"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("账本 JSON 不得暴露内部拓扑字段 %s: %s", forbidden, raw)
			}
		}
	}
}

func TestAIGatewayMigration000060Contract(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位 AI 账本契约测试文件")
	}
	migrationsDir := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "migrations"))
	up := readMigrationForTest(t, filepath.Join(migrationsDir, "000060_create_ai_gateway_ledger_expand.up.sql"))
	down := readMigrationForTest(t, filepath.Join(migrationsDir, "000060_create_ai_gateway_ledger_expand.down.sql"))

	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS ai_projects",
		"CREATE TABLE IF NOT EXISTS ai_requests",
		"CREATE TABLE IF NOT EXISTS ai_usage_items",
		"CREATE TABLE IF NOT EXISTS ai_execution_attempts",
		"UNIQUE KEY uk_ai_requests_request_id",
		"UNIQUE KEY uk_ai_requests_user_idempotency",
		"UNIQUE KEY uk_ai_projects_id_user",
		"ADD UNIQUE KEY uk_api_keys_id_user",
		"information_schema.statistics",
		"PREPARE ai_gateway_add_api_key_owner_index_stmt",
		"indexed_columns = 'id,user_id'",
		"non_unique = 0",
		"UNIQUE KEY uk_ai_usage_request_meter_source_seq",
		"UNIQUE KEY uk_ai_attempts_request_no",
		"moderation_status",
		"execution_status",
		"billing_status",
		"project_id",
		"api_key_id",
		"logical_model_code",
		"execution_model_code",
		"monthly_budget",
		"budget_mode",
		"budget_mode = 'disabled' AND monthly_budget IS NULL",
		"budget_mode IN ('soft', 'hard') AND monthly_budget IS NOT NULL AND monthly_budget > 0",
		"timezone",
		"endpoint_code",
		"FOREIGN KEY (project_id, user_id)",
		"FOREIGN KEY (api_key_id, user_id)",
		"DECIMAL(30,10)",
		"DECIMAL(20,8)",
		"version_no",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("000060 Expand Migration 缺少契约片段: %s", required)
		}
	}

	lowerUp := strings.ToLower(up)
	for _, forbidden := range []string{"prompt_text", "prompt_content", "response_text", "response_content", "api_key_plaintext"} {
		if strings.Contains(lowerUp, forbidden) {
			t.Fatalf("AI 商业账本禁止保存对话或明文密钥字段: %s", forbidden)
		}
	}

	lowerDown := strings.ToLower(down)
	for _, destructive := range []string{"drop table", "delete from", "truncate table"} {
		if strings.Contains(lowerDown, destructive) {
			t.Fatalf("000060 回滚必须保留审计账本，禁止破坏性语句: %s", destructive)
		}
	}
	if !strings.Contains(down, "ai_gateway_expand_schema_retained") {
		t.Fatal("000060 down 必须显式声明保留 Expand Schema")
	}
}

func TestAIGatewayMigration000061G2Contract(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位 G2 Migration 契约测试文件")
	}
	migrationsDir := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "migrations"))
	up := readMigrationForTest(t, filepath.Join(migrationsDir, "000061_add_ai_gateway_g2_projects_keys.up.sql"))
	down := readMigrationForTest(t, filepath.Join(migrationsDir, "000061_add_ai_gateway_g2_projects_keys.down.sql"))

	for _, required := range []string{
		"ADD COLUMN project_id", "ADD COLUMN scope_mode", "ADD COLUMN expires_at", "ADD COLUMN rotated_from_id",
		"chk_api_keys_scope_mode", "UPDATE api_keys SET scope_mode = 'legacy_all'",
		"CREATE TABLE IF NOT EXISTS api_key_model_scopes", "UNIQUE KEY uk_api_key_model_scope",
		"UNIQUE KEY uk_api_keys_id_project_user", "FOREIGN KEY (project_id, user_id)",
		"FOREIGN KEY (api_key_id, project_id, user_id)", "fk_ai_requests_api_key_project_owner",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("000061 G2 Migration 缺少契约片段: %s", required)
		}
	}
	lowerUp := strings.ToLower(up)
	for _, forbidden := range []string{"api_key_plaintext", "secret_key", "wallet_holds", "quoted_amount =", "billing_status = 'held'"} {
		if strings.Contains(lowerUp, forbidden) {
			t.Fatalf("000061 不得写入密钥明文或进入 G3 计费事实: %s", forbidden)
		}
	}
	lowerDown := strings.ToLower(down)
	for _, destructive := range []string{"drop table", "drop column", "delete from", "truncate table"} {
		if strings.Contains(lowerDown, destructive) {
			t.Fatalf("000061 down 必须保留审计和权限事实: %s", destructive)
		}
	}
	if !strings.Contains(down, "ai_gateway_g2_expand_schema_retained") {
		t.Fatal("000061 down 必须声明保留 G2 Expand Schema")
	}
}

func TestAIGatewayMigration000063DoesNotImpersonateUser(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位 G4 Migration 契约测试文件")
	}
	migrationsDir := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "migrations"))
	up := strings.ToLower(readMigrationForTest(t, filepath.Join(migrationsDir, "000063_create_ai_gateway_g4_governance.up.sql")))
	for _, forbidden := range []string{"from users", "limit 1", "insert into ai_safety_policy_versions"} {
		if strings.Contains(up, forbidden) {
			t.Fatalf("000063 不得冒用普通用户身份自动发布安全策略: %s", forbidden)
		}
	}
}

func assertUniqueStates(t *testing.T, dimension string, states []string, expected int) {
	t.Helper()
	seen := make(map[string]struct{}, len(states))
	for _, state := range states {
		if state == "" {
			t.Fatalf("%s 状态不能为空", dimension)
		}
		if _, exists := seen[state]; exists {
			t.Fatalf("%s 状态重复: %s", dimension, state)
		}
		seen[state] = struct{}{}
	}
	if len(seen) != expected {
		t.Fatalf("%s 状态数量错误: got=%d want=%d", dimension, len(seen), expected)
	}
}

func readMigrationForTest(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 Migration 失败 %s: %v", path, err)
	}
	return string(raw)
}
