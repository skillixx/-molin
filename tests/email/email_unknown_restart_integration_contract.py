#!/usr/bin/env python3
"""离线检查 Redis unknown 历史夹具精确清理的安全契约。"""

from __future__ import annotations

import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "server/internal/modules/auth/service/email_unknown_restart_integration_test.go"


class ContractError(RuntimeError):
    """表示后端清理实现偏离冻结契约。"""


def require(condition: bool, message: str) -> None:
    """使用显式异常，确保 Python 优化模式不会跳过断言。"""
    if not condition:
        raise ContractError(message)


def extract_function(text: str, name: str) -> str:
    """按花括号层级提取 Go 顶层函数，供顺序和调用次数检查。"""
    match = re.search(rf"(?m)^func\s+{re.escape(name)}\s*\(", text)
    require(match is not None, f"缺少函数:{name}")
    opening = text.find("{", match.start())
    require(opening >= 0, f"函数缺少左花括号:{name}")
    depth = 0
    quote = ""
    escaped = False
    for index in range(opening, len(text)):
        char = text[index]
        if quote:
            if quote == "`":
                if char == "`":
                    quote = ""
            elif escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = ""
            continue
        if char in {'"', "'", "`"}:
            quote = char
        elif char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                return text[match.start() : index + 1]
    raise ContractError(f"函数缺少右花括号:{name}")


def cleanup_contract_errors(text: str) -> list[str]:
    """返回清理实现偏离冻结安全契约的稳定分类。"""
    errors: list[str] = []

    required_tokens = (
        "RUN_EMAIL_UNKNOWN_RESTART_INTEGRATION",
        "EMAIL_UNKNOWN_RESTART_ACK",
        "RUN_EMAIL_UNKNOWN_RESTART_CLEANUP",
        "EMAIL_UNKNOWN_RESTART_CLEANUP_ACK",
        'os.Getenv("APP_ENV")',
        'os.Getenv("EMAIL_ADAPTER")',
        'os.Getenv("EMAIL_UNKNOWN_RESTART_NONCE")',
        "SELECT version, dirty FROM schema_migrations",
        "version != 57 || dirty",
        'UnexpectedSendLogID uint64 `json:"unexpected_send_log_id,omitempty"`',
        "cleanup_gate_denied",
        "redis_key_absent=true",
        "state_removed=true",
        "cleanup_verified",
        "verified_cleanup_unexpected_log_present",
        "cleanup_phase1",
        "phase1_cleanup_unexpected_log_present",
    )
    for token in required_tokens:
        if token not in text:
            errors.append(f"缺少冻结入口:{token}")

    forbidden = (
        ".FlushDB(",
        ".FlushAll(",
        ".Keys(",
        "client.Scan(",
        ".Del(",
        "ProductionDirectMailAdapter",
        'BusinessRequestNo string `json:',
        'Email string `json:',
        'OldKey string `json:',
        'NewKey string `json:',
        "adapter_calls_nonzero=true",
    )
    for token in forbidden:
        if token in text:
            errors.append(f"禁止能力或敏感字段:{token}")

    try:
        state_body = extract_function(text, "validateEmailUnknownRestartCleanupState")
    except ContractError as exc:
        errors.append(str(exc))
        state_body = ""
    state_tokens = (
        'state.Version != 1 || state.Phase != "phase1_created"',
        "emailUnknownRestartNoncePattern.MatchString(state.Nonce)",
        "emailUnknownRestartRunIDPattern.MatchString(state.RedisRunID)",
        "state.OperatorID == 0",
        "state.TemplateID == 0",
        "state.AllowlistID == 0",
        "state.SendLogID == 0",
        "state.UnexpectedSendLogID == 0",
        "state.SendLogID == state.UnexpectedSendLogID",
    )
    if any(token not in state_body for token in state_tokens):
        errors.append("cleanup状态未冻结phase1及五个正主键或双日志互异")

    try:
        verified_state_body = extract_function(text, "validateEmailUnknownRestartVerifiedCleanupState")
    except ContractError as exc:
        errors.append(str(exc))
        verified_state_body = ""
    verified_state_tokens = (
        'state.Version != 1 || state.Phase != "phase2_verified"',
        "emailUnknownRestartNoncePattern.MatchString(state.Nonce)",
        "emailUnknownRestartRunIDPattern.MatchString(state.RedisRunID)",
        "state.OperatorID == 0",
        "state.TemplateID == 0",
        "state.AllowlistID == 0",
        "state.SendLogID == 0",
        "state.UnexpectedSendLogID != 0",
    )
    if any(token not in verified_state_body for token in verified_state_tokens):
        errors.append("成功周期cleanup状态未冻结phase2_verified单日志形态")

    try:
        phase1_state_body = extract_function(text, "validateEmailUnknownRestartPhase1CleanupState")
    except ContractError as exc:
        errors.append(str(exc))
        phase1_state_body = ""
    phase1_state_tokens = (
        'state.Version != 1 || state.Phase != "phase1_created"',
        "emailUnknownRestartNoncePattern.MatchString(state.Nonce)",
        "emailUnknownRestartRunIDPattern.MatchString(state.RedisRunID)",
        "state.OperatorID == 0",
        "state.TemplateID == 0",
        "state.AllowlistID == 0",
        "state.SendLogID == 0",
        "state.UnexpectedSendLogID != 0",
    )
    if any(token not in phase1_state_body for token in phase1_state_tokens):
        errors.append("phase1_single_log_cleanup_state_incomplete")

    try:
        main_body = extract_function(text, "TestEmailUnknownTombstoneSurvivesRedisRestart")
    except ContractError as exc:
        errors.append(str(exc))
        main_body = ""
    db_open = main_body.find("openEmailUnknownRestartDB(t)")
    redis_open = main_body.find("openEmailUnknownRestartRedis(t)")
    cleanup_gate = main_body.find('if (phase == "cleanup" || phase == "cleanup_verified" || phase == "cleanup_phase1") &&')
    prepare_call = main_body.find("prepareEmailUnknownRestartStateBeforeConnect(")
    first_external = min(position for position in (db_open, redis_open) if position >= 0) if db_open >= 0 and redis_open >= 0 else -1
    if not (0 <= cleanup_gate < prepare_call < first_external):
        errors.append("cleanup非法状态未在Redis或数据库访问前失败关闭")
    phase1_branch = main_body.find('if phase == "phase1"')
    nonce_read = main_body.find('strings.TrimSpace(os.Getenv("EMAIL_UNKNOWN_RESTART_NONCE"))', phase1_branch)
    nonce_gate = main_body.find("emailUnknownRestartNoncePattern.MatchString(nonce)", nonce_read)
    state_create = main_body.find("writeEmailUnknownRestartState(statePath, state)", nonce_gate)
    fixture_create = main_body.find("db.WithContext(ctx).Create(&tpl)", nonce_gate)
    if not (0 <= phase1_branch < nonce_read < nonce_gate < state_create < fixture_create):
        errors.append("phase1_nonce_binding_order_invalid")
    if "randomNonce()" in main_body[phase1_branch:state_create]:
        errors.append("phase1_nonce_must_not_be_regenerated")
    try:
        prepare_body = extract_function(text, "prepareEmailUnknownRestartStateBeforeConnect")
    except ContractError as exc:
        errors.append(str(exc))
        prepare_body = ""
    prepare_tokens = (
        'case "phase2", "cleanup", "cleanup_verified", "cleanup_phase1":',
        "ops.readState(statePath)",
        "state.OperatorID != operatorID",
        'if phase == "cleanup"',
        "validateEmailUnknownRestartCleanupState(state)",
        'if phase == "cleanup_verified"',
        "validateEmailUnknownRestartVerifiedCleanupState(state)",
        'if phase == "cleanup_phase1"',
        "validateEmailUnknownRestartPhase1CleanupState(state)",
    )
    if any(token not in prepare_body for token in prepare_tokens):
        errors.append("连接前cleanup状态读取、操作员或完整字段校验缺失")
    if not (
        0 <= prepare_body.find("ops.readState(statePath)")
        < prepare_body.find("state.OperatorID != operatorID")
        < prepare_body.find("validateEmailUnknownRestartCleanupState(state)")
    ):
        errors.append("连接前cleanup状态校验顺序无效")

    state_reader_tokens = (
        "os.Lstat",
        "info.Mode()&os.ModeSymlink != 0",
        "info.Mode().IsRegular()",
        "info.Mode().Perm() != 0o600",
        "ownerMatches(info)",
    )
    if any(token not in text for token in state_reader_tokens):
        errors.append("状态文件未冻结为当前用户独占普通600非符号链接")
    try:
        state_reader_body = extract_function(text, "readEmailUnknownRestartStateWithOps")
        state_decoder_body = extract_function(text, "decodeEmailUnknownRestartState")
    except ContractError as exc:
        errors.append(str(exc))
        state_reader_body = ""
        state_decoder_body = ""
    if any(token not in state_reader_body for token in (
        "ops.lstat(path)", "info.Mode()&os.ModeSymlink != 0", "!info.Mode().IsRegular()",
        "info.Mode().Perm() != 0o600", "!ops.ownerMatches(info)",
    )):
        errors.append("状态文件Lstat、类型、0600或属主门禁不完整")
    if any(token not in state_decoder_body for token in (
        "state_duplicate_field", "state_unknown_field", "state_trailing_content",
        "seen[key] = struct{}{}", "if _, duplicate := seen[key]; duplicate {",
        'return emailUnknownRestartState{}, errors.New("state_unknown_field")',
        "if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {",
    )):
        errors.append("状态JSON未严格拒绝重复键、未知字段或尾随内容")

    try:
        runtime_body = extract_function(text, "executeEmailUnknownRestartCleanup")
    except ContractError as exc:
        errors.append(str(exc))
        runtime_body = ""
    if runtime_body.count("runtime.redisExists(ctx)") != 2:
        errors.append("Redis必须且只能执行前后两次EXISTS")
    if not (
        runtime_body.find("runtime.redisExists(ctx)")
        < runtime_body.find("runtime.cleanupDB(ctx)")
        < runtime_body.rfind("runtime.redisExists(ctx)")
    ):
        errors.append("Redis前置EXISTS、数据库事务、后置EXISTS顺序无效")
    if "exists != 0" not in runtime_body or runtime_body.count("exists != 0") != 2:
        errors.append("Redis前后EXISTS未严格要求为0")

    try:
        verified_runtime_body = extract_function(text, "executeEmailUnknownRestartVerifiedCleanup")
    except ContractError as exc:
        errors.append(str(exc))
        verified_runtime_body = ""
    if verified_runtime_body.count("runtime.redisExists(ctx)") != 2:
        errors.append("成功周期Redis必须且只能执行前后两次EXISTS")
    if not (
        verified_runtime_body.find("runtime.redisExists(ctx)")
        < verified_runtime_body.find("runtime.cleanupDB(ctx)")
        < verified_runtime_body.rfind("runtime.redisExists(ctx)")
    ):
        errors.append("成功周期Redis前置EXISTS、数据库事务、后置EXISTS顺序无效")
    if verified_runtime_body.count("exists != 0") != 2:
        errors.append("成功周期Redis前后EXISTS未严格要求为0")

    # 模板变量归属必须是恰好 Code 与 ExpireMinutes 两项，不能回退为 JSON 字面相等或放宽数量。
    template_variable_length = "JSON_LENGTH(variables_json) = 2"
    template_variable_code = "JSON_CONTAINS(variables_json, JSON_QUOTE('Code'))"
    template_variable_expire = "JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes'))"
    template_variable_clause = f"{template_variable_length} AND {template_variable_code} AND {template_variable_expire}"
    predicate_requirements = {
        "emailUnknownRestartLogPredicate": (
            "id = ?", "template_id = ?", "provider_template_id = ?", "provider = ?",
            "verification_code_id IS NULL", "scene = ?", "purpose = ?", "recipient_hmac = ?",
            "idempotency_scope = ?", "idempotency_key_hash = ?", "request_fingerprint = ?",
            "status = ?", "failure_reason = ?",
        ),
        "emailUnknownRestartAllowlistPredicate": (
            "id = ?", "email_hmac = ?", "email_masked = ?", "status = ?", "version = ?",
            "created_by = ?", "updated_by = ?", "revoked_at IS NULL",
        ),
        "emailUnknownRestartTemplatePredicate": (
            "id = ?", "provider = ?", "provider_template_id = ?", "name = ?", "subject = ?",
            "sender_nickname IS NULL", "template_text = ?", template_variable_length, template_variable_code,
            template_variable_expire, "content_sha256 = ?",
            "provider_status = ?", "review_comment IS NULL", "variables_complete = ?", "local_enabled = ?",
            "missing = ?", "missing_since IS NULL", "provider_created_at IS NULL", "version = ?",
        ),
    }
    for constant, tokens in predicate_requirements.items():
        line = next((item for item in text.splitlines() if constant in item and "=" in item), "")
        if any(token not in line for token in tokens):
            errors.append(f"完整归属谓词缺字段:{constant}")
        if constant == "emailUnknownRestartTemplatePredicate" and (
            line.count(template_variable_clause) != 1
            or line.count("JSON_LENGTH(variables_json)") != 1
            or line.count("JSON_CONTAINS(variables_json") != 2
            or "variables_json = ?" in line
        ):
            errors.append("模板变量归属谓词未冻结为Code和ExpireMinutes两项")

    try:
        database_body = extract_function(text, "cleanupEmailUnknownRestartDatabase")
    except ContractError as exc:
        errors.append(str(exc))
        database_body = ""
    database_tokens = (
        ".Transaction(func(tx *gorm.DB) error",
        'Clauses(clause.Locking{Strength: "UPDATE"})',
        'Where("idempotency_scope = ?", scope)',
        "len(rows) != 2",
        "rows[0].ID == state.SendLogID",
        "rows[1].ID == state.UnexpectedSendLogID",
    )
    if any(token not in database_body for token in database_tokens):
        errors.append("事务内scope两日志FOR UPDATE门禁不完整")
    if database_body.count('Clauses(clause.Locking{Strength: "UPDATE"})') != 2:
        errors.append("scope锁与完整归属锁未同时使用FOR UPDATE")
    expected_query_counts = {
        "emailUnknownRestartLogQuery(": 4,
        "emailUnknownRestartAllowlistQuery(": 2,
        "emailUnknownRestartTemplateQuery(": 2,
    }
    for query, expected in expected_query_counts.items():
        if database_body.count(query) != expected:
            errors.append(f"锁定与删除未复用同一谓词:{query}")
    if database_body.count(".Delete(") != 4:
        errors.append("数据库删除数量不是2日志+1白名单+1模板")

    try:
        verified_database_body = extract_function(text, "cleanupEmailUnknownRestartVerifiedDatabase")
    except ContractError as exc:
        errors.append(str(exc))
        verified_database_body = ""
    verified_database_tokens = (
        ".Transaction(func(tx *gorm.DB) error",
        'Clauses(clause.Locking{Strength: "UPDATE"})',
        'Where("idempotency_scope = ?", scope)',
        "len(rows) != 1",
        "rows[0].ID != state.SendLogID",
    )
    if any(token not in verified_database_body for token in verified_database_tokens):
        errors.append("成功周期事务内scope单日志FOR UPDATE门禁不完整")
    if verified_database_body.count('Clauses(clause.Locking{Strength: "UPDATE"})') != 2:
        errors.append("成功周期scope锁与完整归属锁未同时使用FOR UPDATE")
    for query, expected in {
        "emailUnknownRestartLogQuery(": 2,
        "emailUnknownRestartAllowlistQuery(": 2,
        "emailUnknownRestartTemplateQuery(": 2,
    }.items():
        if verified_database_body.count(query) != expected:
            errors.append(f"成功周期锁定与删除未复用同一谓词:{query}")
    if verified_database_body.count(".Delete(") != 3:
        errors.append("成功周期数据库删除数量不是1日志+1白名单+1模板")

    try:
        rows_body = extract_function(text, "executeEmailUnknownRestartCleanupRows")
    except ContractError as exc:
        errors.append(str(exc))
        rows_body = ""
    for lock_name in ("lockScope", "lockUnexpected", "lockPrimary", "lockAllowlist", "lockTemplate"):
        if lock_name not in rows_body:
            errors.append(f"缺少事务锁定步骤:{lock_name}")
    for delete_name in ("deleteUnexpected", "deletePrimary", "deleteAllowlist", "deleteTemplate"):
        if delete_name not in rows_body:
            errors.append(f"缺少精确删除步骤:{delete_name}")
    if rows_body.count("rows != 1") != 4:
        errors.append("四项删除未逐项要求RowsAffected等于1")
    first_delete = min((rows_body.find(name) for name in ("ops.deleteUnexpected()", "ops.deletePrimary()", "ops.deleteAllowlist()", "ops.deleteTemplate()") if rows_body.find(name) >= 0), default=-1)
    last_lock = max((rows_body.find(name) for name in ("ops.lockScope", "ops.lockUnexpected", "ops.lockPrimary", "ops.lockAllowlist", "ops.lockTemplate")), default=-1)
    if first_delete < 0 or last_lock < 0 or last_lock > first_delete:
        errors.append("归属锁定未全部先于任何删除")

    try:
        verified_rows_body = extract_function(text, "executeEmailUnknownRestartVerifiedCleanupRows")
    except ContractError as exc:
        errors.append(str(exc))
        verified_rows_body = ""
    for lock_name in ("lockScope", "lockPrimary", "lockAllowlist", "lockTemplate"):
        if lock_name not in verified_rows_body:
            errors.append(f"成功周期缺少事务锁定步骤:{lock_name}")
    for delete_name in ("deletePrimary", "deleteAllowlist", "deleteTemplate"):
        if delete_name not in verified_rows_body:
            errors.append(f"成功周期缺少精确删除步骤:{delete_name}")
    if verified_rows_body.count("rows != 1") != 1:
        errors.append("成功周期三项删除未统一逐项要求RowsAffected等于1")
    verified_first_delete = verified_rows_body.find("for _, deletion := range deletes")
    verified_last_lock = verified_rows_body.find("for _, lock := range locks")
    if verified_first_delete < 0 or verified_last_lock < 0 or verified_last_lock > verified_first_delete:
        errors.append("成功周期归属锁定未全部先于任何删除")

    try:
        complete_body = extract_function(text, "completeEmailUnknownRestartCleanup")
    except ContractError as exc:
        errors.append(str(exc))
        complete_body = ""
    if not (0 <= complete_body.find("cleanup()") < complete_body.find("removeState()")):
        errors.append("状态文件删除未固定在全部清理后验成功之后")
    if text.count("os.Remove(statePath)") != 3:
        errors.append("历史与成功周期状态文件未各自唯一一次精确Remove")

    required_fault_tests = (
        "TestEmailUnknownRestartStateReaderRejectsSymlinkBeforeRead",
        "TestEmailUnknownRestartStateReaderRejectsOwnerMismatchBeforeRead",
        "TestEmailUnknownRestartStateDecoderRejectsDuplicateAndUnknownFields",
        "TestEmailUnknownRestartCleanupInvalidStateBlocksConnections",
        "TestEmailUnknownRestartCleanupRejectsInvalidStateBeforeExternalAccess",
        "TestEmailUnknownRestartCleanupRejectsExistingRedisKeyWithoutDatabaseWrite",
        "TestEmailUnknownRestartCleanupRejectsOwnershipDriftBeforeDelete",
        "TestEmailUnknownRestartCleanupRejectsEveryMissingDeleteRow",
        "TestEmailUnknownRestartCleanupLaterFailureRollsBackLogicalTransaction",
        "TestEmailUnknownRestartCleanupPostflightFailureRetainsState",
        "TestEmailUnknownRestartCleanupFailureRetainsState",
        "TestEmailUnknownRestartCleanupSuccessRemovesStateOnce",
        "TestEmailUnknownRestartCleanupPredicatesCoverFrozenOwnership",
        "TestEmailUnknownRestartVerifiedCleanupStateIsStrict",
        "TestEmailUnknownRestartPhase1CleanupStateIsStrict",
        "TestEmailUnknownRestartVerifiedCleanupRowsRequireExactOwnershipAndDeletes",
        "TestEmailUnknownRestartVerifiedCleanupRedisGateRetainsState",
    )
    for test_name in required_fault_tests:
        if f"func {test_name}(" not in text:
            errors.append(f"缺少后端故障注入:{test_name}")
    return errors


def replace_once(text: str, old: str, new: str) -> str:
    """攻击模型要求目标必须存在且只替换一次，避免空变异假通过。"""
    require(old in text, f"攻击目标不存在:{old}")
    return text.replace(old, new, 1)


def replace_in_function(text: str, function_name: str, old: str, new: str) -> str:
    """只变异指定 Go 函数，避免同名测试夹具或注释先被替换。"""
    body = extract_function(text, function_name)
    require(old in body, f"函数攻击目标不存在:{function_name}:{old}")
    mutated_body = body.replace(old, new, 1)
    return text.replace(body, mutated_body, 1)


def run_attack_models(text: str) -> int:
    """对关键安全边界逐一做单点破坏，并要求静态契约失败关闭。"""
    attacks = (
        ("移除控制器nonce绑定", "TestEmailUnknownTombstoneSurvivesRedisRestart", 'strings.TrimSpace(os.Getenv("EMAIL_UNKNOWN_RESTART_NONCE"))', 'strings.Repeat("a", 32)'),
        ("阶段降级", "validateEmailUnknownRestartCleanupState", 'state.Version != 1 || state.Phase != "phase1_created"', "state.Version != 1"),
        ("遗漏意外日志主键", "validateEmailUnknownRestartCleanupState", " || state.UnexpectedSendLogID == 0", ""),
        ("允许日志主键重复", "validateEmailUnknownRestartCleanupState", "if state.SendLogID == state.UnexpectedSendLogID {", "if false {"),
        ("移除连接前完整状态校验", "prepareEmailUnknownRestartStateBeforeConnect", "validateEmailUnknownRestartCleanupState(state)", "errors.New(\"validation_removed\")"),
        ("允许状态符号链接", "readEmailUnknownRestartStateWithOps", " || info.Mode()&os.ModeSymlink != 0", ""),
        ("忽略状态文件属主", "readEmailUnknownRestartStateWithOps", "if !ops.ownerMatches(info) {", "if false {"),
        ("允许重复JSON键", "decodeEmailUnknownRestartState", "if _, duplicate := seen[key]; duplicate {", "if false {"),
        ("允许未知JSON字段", "decodeEmailUnknownRestartState", 'return emailUnknownRestartState{}, errors.New("state_unknown_field")', "decodeErr = decoder.Decode(&struct{}{})"),
        ("允许JSON尾随内容", "decodeEmailUnknownRestartState", "if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {", "if false {"),
        ("移除Redis前置检查", "executeEmailUnknownRestartCleanup", "exists, err := runtime.redisExists(ctx)", "exists, err := int64(0), error(nil)"),
        ("移除Redis后置检查", "executeEmailUnknownRestartCleanup", "exists, err = runtime.redisExists(ctx)", "exists, err = int64(0), error(nil)"),
        ("注入Redis删除", "cleanupEmailUnknownRestartFixture", "return client.Exists(checkCtx, lockKey).Result()", "client.Del(checkCtx, lockKey); return client.Exists(checkCtx, lockKey).Result()"),
        ("移除数据库事务", "cleanupEmailUnknownRestartDatabase", "return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {", "return func(tx *gorm.DB) error {"),
        ("移除FOR UPDATE", "cleanupEmailUnknownRestartDatabase", 'Clauses(clause.Locking{Strength: "UPDATE"})', ""),
        ("scope允许单日志", "cleanupEmailUnknownRestartDatabase", "len(rows) != 2", "len(rows) != 1"),
        ("删除不复用完整日志谓词", "cleanupEmailUnknownRestartDatabase", "emailUnknownRestartLogQuery(tx, state, state.UnexpectedSendLogID, providerTemplateID, recipientHMAC, scope, newKeyHash, fingerprint).Delete", 'tx.Where("id = ?", state.UnexpectedSendLogID).Delete'),
        ("删除行数不要求1", "executeEmailUnknownRestartCleanupRows", "rows != 1", "rows < 0"),
        ("状态删除提前", "completeEmailUnknownRestartCleanup", "if err := cleanup(); err != nil {", "if err := removeState(); err != nil {"),
        ("成功周期阶段降级", "validateEmailUnknownRestartVerifiedCleanupState", 'state.Version != 1 || state.Phase != "phase2_verified"', "state.Version != 1"),
        ("成功周期允许意外日志", "validateEmailUnknownRestartVerifiedCleanupState", "if state.UnexpectedSendLogID != 0 {", "if false {"),
        ("成功周期移除Redis前置检查", "executeEmailUnknownRestartVerifiedCleanup", "exists, err := runtime.redisExists(ctx)", "exists, err := int64(0), error(nil)"),
        ("成功周期移除Redis后置检查", "executeEmailUnknownRestartVerifiedCleanup", "exists, err = runtime.redisExists(ctx)", "exists, err = int64(0), error(nil)"),
        ("成功周期移除数据库事务", "cleanupEmailUnknownRestartVerifiedDatabase", "return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {", "return func(tx *gorm.DB) error {"),
        ("成功周期scope允许双日志", "cleanupEmailUnknownRestartVerifiedDatabase", "len(rows) != 1", "len(rows) != 2"),
        ("成功周期删除行数不要求1", "executeEmailUnknownRestartVerifiedCleanupRows", "rows != 1", "rows < 0"),
    )
    for name, function_name, old, new in attacks:
        mutated = replace_in_function(text, function_name, old, new)
        require(bool(cleanup_contract_errors(mutated)), f"攻击未被拒绝:{name}")
    global_attacks = (
        ("日志谓词缺供应商", " AND provider = ?", ""),
        ("状态文件重复删除", "func() error { return os.Remove(statePath) },", "func() error { _ = os.Remove(statePath); return os.Remove(statePath) },"),
        (
            "模板变量回退旧字面相等",
            "JSON_LENGTH(variables_json) = 2 AND JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes'))",
            "variables_json = ?",
        ),
        (
            "模板变量缺失Code",
            "JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND ",
            "",
        ),
        (
            "模板变量允许额外项",
            "JSON_LENGTH(variables_json) = 2",
            "JSON_LENGTH(variables_json) >= 2",
        ),
    )
    for name, old, new in global_attacks:
        mutated = replace_once(text, old, new)
        require(bool(cleanup_contract_errors(mutated)), f"攻击未被拒绝:{name}")
    return len(attacks) + len(global_attacks)


def main() -> int:
    """只读取 Go 源码，不访问网络、数据库、Redis，也不执行 cleanup。"""
    text = SOURCE.read_text(encoding="utf-8")
    errors = cleanup_contract_errors(text)
    if errors:
        print("[FAIL] mode=email_unknown_restart_static classification=contract_mismatch")
        print("external_access=false database=false redis=false cleanup=false")
        print("findings=" + str(len(errors)))
        return 1
    attack_cases = run_attack_models(text)
    print("[PASS] mode=email_unknown_restart_static classification=contract_verified")
    print("external_access=false database=false redis=false cleanup=false")
    print(f"attack_cases={attack_cases}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
