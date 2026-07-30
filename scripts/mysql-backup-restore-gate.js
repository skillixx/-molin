/*
 * MySQL Shell 8.4 固定执行器。
 * 该文件只接受父级 PowerShell 包装器注入的进程变量，不读取或输出数据库密码。
 */

function requireEnv(name) {
    const value = os.getenv(name);
    if (value === undefined || value === null || String(value).length === 0) {
        throw new Error("gate_configuration_missing");
    }
    return String(value);
}

function quoteIdentifier(value) {
    return "`" + value.replace(/`/g, "``") + "`";
}

function scalar(sql, parameters) {
    const row = session.runSql(sql, parameters || []).fetchOne();
    return row === null ? null : row[0];
}

function assertSchemaExists(schemaName) {
    const count = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?",
        [schemaName]
    ));
    if (count !== 1) {
        throw new Error("source_schema_unavailable");
    }
}

function assertTargetAbsent(schemaName) {
    const count = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?",
        [schemaName]
    ));
    if (count !== 0) {
        throw new Error("validation_schema_already_exists");
    }
}

const ownershipTableName = "__molin_restore_ownership";

function requireOwnershipToken() {
    const token = requireEnv("MOLIN_GATE_OWNERSHIP_TOKEN");
    if (!/^[0-9a-f]{64}$/.test(token)) {
        throw new Error("ownership_token_invalid");
    }
    return token;
}

function assertOwnershipMarkerNameAvailable(schemaName) {
    const existing = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.tables " +
        "WHERE table_schema = ? AND table_name = ?",
        [schemaName, ownershipTableName]
    ));
    if (existing !== 0) {
        throw new Error("ownership_marker_conflict");
    }
}

function createOwnedValidationSchema(schemaName, token) {
    // CREATE 不使用 IF NOT EXISTS；若只读缺席检查后发生同名竞争，DDL 必须失败且不能取得清理资格。
    assertTargetAbsent(schemaName);
    session.runSql("CREATE SCHEMA " + quoteIdentifier(schemaName));
    session.runSql(
        "CREATE TABLE " + quoteIdentifier(schemaName) + "." + quoteIdentifier(ownershipTableName) +
        " (ownership_token CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY) ENGINE=InnoDB"
    );
    session.runSql(
        "INSERT INTO " + quoteIdentifier(schemaName) + "." + quoteIdentifier(ownershipTableName) +
        " (ownership_token) VALUES (?)",
        [token]
    );
    assertOwnershipMarker(schemaName, token);
}

function assertOwnershipMarker(schemaName, token) {
    const schemaCount = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?",
        [schemaName]
    ));
    const tableCount = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.tables " +
        "WHERE table_schema = ? AND table_name = ? AND table_type = 'BASE TABLE'",
        [schemaName, ownershipTableName]
    ));
    if (schemaCount !== 1 || tableCount !== 1) {
        throw new Error("ownership_marker_missing");
    }
    const totalRows = Number(scalar(
        "SELECT COUNT(*) FROM " + quoteIdentifier(schemaName) + "." + quoteIdentifier(ownershipTableName)
    ));
    const matchingRows = Number(scalar(
        "SELECT COUNT(*) FROM " + quoteIdentifier(schemaName) + "." + quoteIdentifier(ownershipTableName) +
        " WHERE ownership_token = ?",
        [token]
    ));
    if (totalRows !== 1 || matchingRows !== 1) {
        throw new Error("ownership_marker_mismatch");
    }
}

function assertSessionVariablesAdminEvidence() {
    // 只读核对当前连接账号是否获得最小动态权限；SYSTEM_VARIABLES_ADMIN 不作为替代证据。
    const count = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.user_privileges " +
        "WHERE privilege_type = 'SESSION_VARIABLES_ADMIN' " +
        "AND REPLACE(grantee, CHAR(39), '') = CURRENT_USER()"
    ));
    if (count !== 1) {
        throw new Error("restore_session_variables_admin_required");
    }
}

function objectSummary(schemaName, stageSetter) {
    if (typeof stageSetter === "function") {
        stageSetter("preflight_tables_query");
    }
    const tables = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_type = 'BASE TABLE'",
        [schemaName]
    ));
    if (typeof stageSetter === "function") {
        stageSetter("preflight_views_query");
    }
    const views = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.views WHERE table_schema = ?",
        [schemaName]
    ));
    if (typeof stageSetter === "function") {
        stageSetter("preflight_triggers_query");
    }
    const triggers = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = ?",
        [schemaName]
    ));
    if (typeof stageSetter === "function") {
        stageSetter("preflight_routines_query");
    }
    const routines = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema = ?",
        [schemaName]
    ));
    if (typeof stageSetter === "function") {
        stageSetter("preflight_events_query");
    }
    const events = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.events WHERE event_schema = ?",
        [schemaName]
    ));
    if (typeof stageSetter === "function") {
        stageSetter("preflight_engine_query");
    }
    const nonInnoDbTables = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? " +
        "AND table_type = 'BASE TABLE' AND (engine IS NULL OR engine <> 'InnoDB')",
        [schemaName]
    ));
    return {
        tables: tables,
        views: views,
        triggers: triggers,
        routines: routines,
        events: events,
        non_innodb_tables: nonInnoDbTables
    };
}

function assertRemapSafe(summary) {
    // MySQL Shell 不重写对象中的旧 schema 引用，因此存在下列对象时必须失败关闭。
    if (summary.views !== 0 || summary.triggers !== 0 || summary.routines !== 0 || summary.events !== 0 ||
        summary.non_innodb_tables !== 0) {
        throw new Error("schema_remap_unsafe_objects");
    }
}

function baseTableNames(schemaName) {
    const result = session.runSql(
        "SELECT table_name FROM information_schema.tables " +
        "WHERE table_schema = ? AND table_type = 'BASE TABLE' ORDER BY table_name",
        [schemaName]
    );
    const names = [];
    let row = result.fetchOne();
    while (row !== null) {
        // 隔离库的专用所有权表不属于业务备份覆盖范围。
        if (String(row[0]) !== ownershipTableName) {
            names.push(String(row[0]));
        }
        row = result.fetchOne();
    }
    return names;
}

function assertNoQualifiedSourceReferences(schemaName) {
    const names = baseTableNames(schemaName);
    const qualifiedPrefix = quoteIdentifier(schemaName) + ".";
    for (let index = 0; index < names.length; index += 1) {
        const row = session.runSql(
            "SHOW CREATE TABLE " + quoteIdentifier(schemaName) + "." + quoteIdentifier(names[index])
        ).fetchOne();
        if (row === null || row.length < 2 || String(row[1]).indexOf(qualifiedPrefix) !== -1) {
            // 显式旧 schema 引用会破坏隔离性，即使 MySQL Shell 接受重映射也必须阻断。
            throw new Error("qualified_source_reference_detected");
        }
    }
}

function exactRowCount(schemaName) {
    let total = 0;
    const names = baseTableNames(schemaName);
    for (let index = 0; index < names.length; index += 1) {
        // 先完整消费 information_schema 结果，再逐表计数，避免经典协议存在未读结果集。
        total += Number(scalar("SELECT COUNT(*) FROM " + quoteIdentifier(schemaName) + "." + quoteIdentifier(names[index])));
    }
    return total;
}

function structureFingerprint(schemaName) {
    // 对每张表的完整 SHOW CREATE TABLE 先做摘要，再汇总，覆盖列、索引、约束和表选项且不输出对象名。
    const names = baseTableNames(schemaName);
    const parts = [];
    for (let index = 0; index < names.length; index += 1) {
        const row = session.runSql(
            "SHOW CREATE TABLE " + quoteIdentifier(schemaName) + "." + quoteIdentifier(names[index])
        ).fetchOne();
        if (row === null || row.length < 2) {
            throw new Error("structure_fingerprint_unavailable");
        }
        const ddlHash = String(scalar("SELECT SHA2(?, 256)", [String(row[1])]));
        parts.push(names[index] + ":" + ddlHash);
    }
    return String(scalar("SELECT SHA2(?, 256)", [parts.join("|")]));
}

function verificationAggregate(schemaName) {
    const names = baseTableNames(schemaName);
    return {
        table_count: names.length,
        total_rows: exactRowCount(schemaName),
        structure_fingerprint: structureFingerprint(schemaName)
    };
}

function loadStrictJson(filePath) {
    try {
        const parsed = JSON.parse(os.loadTextFile(filePath));
        if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
            throw new Error("invalid_json_root");
        }
        return parsed;
    } catch (error) {
        throw new Error("dump_checksum_metadata_invalid");
    }
}

function sameStringSet(left, right) {
    if (left.length !== right.length) {
        return false;
    }
    const leftSorted = left.slice().sort();
    const rightSorted = right.slice().sort();
    for (let index = 0; index < leftSorted.length; index += 1) {
        if (leftSorted[index] !== rightSorted[index]) {
            return false;
        }
    }
    return true;
}

function checksumLeaves(node, output) {
    if (node === null || typeof node !== "object") {
        return;
    }
    if (Array.isArray(node)) {
        for (let index = 0; index < node.length; index += 1) {
            checksumLeaves(node[index], output);
        }
        return;
    }
    Object.keys(node).forEach(function (key) {
        if (key === "checksum") {
            output.checksums.push(node[key]);
        } else if (key === "count") {
            output.counts.push(node[key]);
        }
        checksumLeaves(node[key], output);
    });
}

function dumpChecksumManifest(dumpDirectory, expectedSchema) {
    // 严格解析 dump 与 checksum 元数据，所有 schema/table/checksum 原值只在进程内使用。
    const dumpMetadata = loadStrictJson(os.path.join(dumpDirectory, "@.json"));
    if (!Array.isArray(dumpMetadata.schemas) || dumpMetadata.schemas.length !== 1 ||
        String(dumpMetadata.schemas[0]) !== expectedSchema || dumpMetadata.basenames === null ||
        typeof dumpMetadata.basenames !== "object" || Array.isArray(dumpMetadata.basenames) ||
        Object.keys(dumpMetadata.basenames).length !== 1) {
        throw new Error("dump_checksum_metadata_invalid");
    }
    const schemaBaseName = String(dumpMetadata.basenames[expectedSchema] || "");
    if (!/^[A-Za-z0-9_$@-]{1,255}$/.test(schemaBaseName)) {
        throw new Error("dump_checksum_metadata_invalid");
    }
    const schemaMetadata = loadStrictJson(os.path.join(dumpDirectory, schemaBaseName + ".json"));
    if (!Array.isArray(schemaMetadata.tables) || schemaMetadata.tables.length < 1) {
        throw new Error("dump_checksum_metadata_invalid");
    }
    const metadataTables = schemaMetadata.tables.map(function (table) { return String(table); });
    if ((new Set(metadataTables)).size !== metadataTables.length ||
        metadataTables.some(function (table) { return table.length === 0; })) {
        throw new Error("dump_checksum_metadata_invalid");
    }

    const checksumMetadata = loadStrictJson(os.path.join(dumpDirectory, "@.checksums.json"));
    if (checksumMetadata.config === null || typeof checksumMetadata.config !== "object" ||
        !sameStringSet(Object.keys(checksumMetadata), ["config", "data"]) ||
        !sameStringSet(Object.keys(checksumMetadata.config), ["version", "algorithm", "hash"]) ||
        String(checksumMetadata.config.version) !== "1.0.0" ||
        String(checksumMetadata.config.algorithm) !== "bit_xor" ||
        String(checksumMetadata.config.hash) !== "sha256" ||
        checksumMetadata.data === null || typeof checksumMetadata.data !== "object" ||
        Array.isArray(checksumMetadata.data)) {
        throw new Error("dump_checksum_metadata_invalid");
    }
    const checksumSchemas = Object.keys(checksumMetadata.data);
    if (checksumSchemas.length !== 1 || checksumSchemas[0] !== expectedSchema) {
        throw new Error("coverage_mismatch");
    }
    const checksumTablesNode = checksumMetadata.data[expectedSchema];
    if (checksumTablesNode === null || typeof checksumTablesNode !== "object" || Array.isArray(checksumTablesNode)) {
        throw new Error("dump_checksum_metadata_invalid");
    }
    const checksumTables = Object.keys(checksumTablesNode);
    if (!sameStringSet(metadataTables, checksumTables)) {
        throw new Error("coverage_mismatch");
    }

    const expectedRows = {};
    const checksumParts = [];
    let expectedTotalRows = 0;
    for (let index = 0; index < metadataTables.length; index += 1) {
        const table = metadataTables[index];
        const leaves = {checksums: [], counts: []};
        checksumLeaves(checksumTablesNode[table], leaves);
        if (leaves.checksums.length !== 1 || leaves.counts.length !== 1 ||
            typeof leaves.checksums[0] !== "string" || !/^[0-9A-Fa-f]{64}$/.test(leaves.checksums[0]) ||
            typeof leaves.counts[0] !== "number" || !Number.isSafeInteger(leaves.counts[0]) || leaves.counts[0] < 0) {
            throw new Error("dump_checksum_metadata_invalid");
        }
        const rowCount = leaves.counts[0];
        if (!Number.isSafeInteger(expectedTotalRows + rowCount)) {
            throw new Error("dump_checksum_metadata_invalid");
        }
        expectedRows[table] = rowCount;
        expectedTotalRows += rowCount;
        checksumParts.push(table + ":" + String(leaves.checksums[0]).toLowerCase() + ":" + String(rowCount));
    }
    return {
        tables: metadataTables.slice().sort(),
        expected_rows: expectedRows,
        expected_total_rows: expectedTotalRows,
        dump_checksum_fingerprint: String(scalar("SELECT SHA2(?, 256)", [checksumParts.sort().join("|")]))
    };
}

function assertTableCoverage(schemaName, expectedTables) {
    if (!sameStringSet(baseTableNames(schemaName), expectedTables)) {
        throw new Error("coverage_mismatch");
    }
}

function assertExpectedRows(schemaName, manifest) {
    let total = 0;
    for (let index = 0; index < manifest.tables.length; index += 1) {
        const table = manifest.tables[index];
        const count = Number(scalar(
            "SELECT COUNT(*) FROM " + quoteIdentifier(schemaName) + "." + quoteIdentifier(table)
        ));
        if (!Number.isSafeInteger(count) || count !== manifest.expected_rows[table]) {
            throw new Error("row_count_mismatch");
        }
        total += count;
    }
    if (total !== manifest.expected_total_rows) {
        throw new Error("row_count_mismatch");
    }
}

function checksumTableValue(schemaName, tableName) {
    const row = session.runSql(
        "CHECKSUM TABLE " + quoteIdentifier(schemaName) + "." + quoteIdentifier(tableName)
    ).fetchOne();
    if (row === null || row.length < 2 || row[1] === null || row[1] === undefined ||
        !/^\d+$/.test(String(row[1]))) {
        throw new Error("checksum_unavailable");
    }
    return String(row[1]);
}

function compareSourceTargetChecksums(sourceSchema, targetSchema, tables) {
    const aggregateParts = [];
    for (let index = 0; index < tables.length; index += 1) {
        const table = tables[index];
        const sourceChecksum = checksumTableValue(sourceSchema, table);
        const targetChecksum = checksumTableValue(targetSchema, table);
        if (sourceChecksum !== targetChecksum) {
            throw new Error("source_target_checksum_mismatch");
        }
        aggregateParts.push(table + ":" + sourceChecksum);
    }
    return {
        checked_table_count: tables.length,
        checksum_fingerprint: String(scalar("SELECT SHA2(?, 256)", [aggregateParts.join("|")]))
    };
}

function assertAggregateEqual(sourceAggregate, targetAggregate) {
    // 三项聚合必须逐项一致；任何差异都说明当前源库、dump 与恢复结果不能形成可信证据链。
    if (sourceAggregate.table_count !== targetAggregate.table_count ||
        sourceAggregate.total_rows !== targetAggregate.total_rows ||
        sourceAggregate.structure_fingerprint !== targetAggregate.structure_fingerprint) {
        throw new Error("restore_aggregate_mismatch");
    }
}

function sqlWildcardMatches(pattern, value, partialRevokesEnabled) {
    let expression = "^";
    let escaped = false;
    for (let index = 0; index < pattern.length; index += 1) {
        const character = pattern[index];
        if (escaped) {
            expression += character.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
            escaped = false;
        } else if (character === "\\") {
            escaped = true;
        } else if (character === "%" && !partialRevokesEnabled) {
            expression += ".*";
        } else if (character === "_" && !partialRevokesEnabled) {
            expression += ".";
        } else {
            expression += character.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
        }
    }
    if (escaped) {
        // SHOW GRANTS scope 尾部出现无法解释的转义符时，不猜测授权范围。
        throw new Error("grant_scope_invalid");
    }
    // MySQL 授权表的 Db scope 按大小写敏感规则比较，不能沿用 schema 标识符的平台大小写行为。
    return new RegExp(expression + "$").test(value);
}

function partialRevokesEnabled() {
    // partial_revokes 改变未转义 %/_ 的授权 scope 语义，只接受服务端明确返回的布尔表示。
    const raw = scalar("SELECT @@GLOBAL.partial_revokes");
    if (raw === 1 || raw === "1" || String(raw).toUpperCase() === "ON") {
        return true;
    }
    if (raw === 0 || raw === "0" || String(raw).toUpperCase() === "OFF") {
        return false;
    }
    throw new Error("partial_revokes_state_unknown");
}

function restoreTargetPrivilegeEvidence(schemaName) {
    // 表型 dump 的恢复会创建/调整表和索引、装载数据，门禁随后还要读取校验并精确删除隔离 schema。
    const requiredPrivileges = ["ALTER", "CREATE", "DROP", "INDEX", "INSERT", "REFERENCES", "SELECT"];
    try {
        const partialRevokes = partialRevokesEnabled();
        const currentRoles = String(scalar("SELECT CURRENT_ROLE()") || "NONE");
        const grants = session.runSql(currentRoles === "NONE" ? "SHOW GRANTS" : "SHOW GRANTS USING " + currentRoles);
        const granted = {};
        const revoked = {};
        for (let index = 0; index < requiredPrivileges.length; index += 1) {
            granted[requiredPrivileges[index]] = false;
            revoked[requiredPrivileges[index]] = false;
        }
        let row = grants.fetchOne();
        while (row !== null) {
            const statement = String(row[0]);
            const grantMatch = statement.match(/^GRANT\s+(.+?)\s+ON\s+(.+?)\.\*\s+TO\s+/i);
            const revokeMatch = statement.match(/^REVOKE\s+(.+?)\s+ON\s+(.+?)\.\*\s+FROM\s+/i);
            const match = grantMatch !== null ? grantMatch : revokeMatch;
            if (match !== null) {
                let scope = match[2];
                if (scope.length >= 2 && scope[0] === "`" && scope[scope.length - 1] === "`") {
                    scope = scope.substring(1, scope.length - 1).replace(/``/g, "`");
                }
                if (scope === "*" || sqlWildcardMatches(scope, schemaName, partialRevokes)) {
                    const privilegeText = match[1].toUpperCase();
                    const grantedPrivileges = privilegeText.split(",").map(function (privilege) {
                        return privilege.trim();
                    });
                    // partial_revokes 会把全局授权的目标 schema 例外显示为 REVOKE，撤销证据必须优先于全局 GRANT。
                    const evidenceTarget = revokeMatch !== null ? revoked : granted;
                    for (let index = 0; index < requiredPrivileges.length; index += 1) {
                        const privilege = requiredPrivileges[index];
                        if (privilegeText === "ALL PRIVILEGES" || grantedPrivileges.indexOf(privilege) !== -1) {
                            evidenceTarget[privilege] = true;
                        }
                    }
                }
            }
            row = grants.fetchOne();
        }
        const complete = requiredPrivileges.every(function (privilege) {
            return granted[privilege] === true && revoked[privilege] !== true;
        });
        return {
            overall: complete ? "evidenced" : "not_evidenced",
            create: granted.CREATE && !revoked.CREATE ? "evidenced" : "not_evidenced",
            select: granted.SELECT && !revoked.SELECT ? "evidenced" : "not_evidenced"
        };
    } catch (error) {
        return {overall: "unknown", create: "unknown", select: "unknown"};
    }
}

function assertRestoreTargetPrivileges(schemaName) {
    const evidence = restoreTargetPrivilegeEvidence(schemaName);
    if (evidence.overall !== "evidenced") {
        throw new Error("restore_target_privileges_required");
    }
    return evidence;
}

function runRestoreDryRunProbe(dumpPath, options) {
    try {
        util.loadDump(dumpPath, options);
        return {success: true, reason: "none"};
    } catch (error) {
        const result = {
            success: false,
            reason: classifySafeReason(error, "restore_dry_run", "restore_load_dry_run")
        };
        const diagnosticCode = safeDiagnosticCode(error);
        if (diagnosticCode !== null) {
            result.diagnostic_code = diagnosticCode;
        }
        return result;
    }
}

function printResult(payload) {
    if (payload.status === undefined || payload.status === null || String(payload.status).length === 0) {
        payload.status = "blocked";
    }
    if (payload.reason === undefined || payload.reason === null || String(payload.reason).length === 0) {
        payload.reason = payload.status === "blocked" ? "mysqlsh_action_failed" : "none";
    }
    print("MOLIN_GATE_RESULT " + JSON.stringify(payload));
}

function classifySafeReason(error, action, failureStage) {
    const message = String(error && error.message ? error.message : error);
    const errorType = String(error && (error.type || error.name) ? (error.type || error.name) : "").toLowerCase();
    if (message.indexOf("dump_checksum_metadata_invalid") !== -1) {
        return "dump_checksum_metadata_invalid";
    }
    if (message.indexOf("coverage_mismatch") !== -1) {
        return "coverage_mismatch";
    }
    if (message.indexOf("row_count_mismatch") !== -1) {
        return "row_count_mismatch";
    }
    if (message.indexOf("source_target_checksum_mismatch") !== -1) {
        return "source_target_checksum_mismatch";
    }
    if (message.indexOf("checksum_unavailable") !== -1) {
        return "checksum_unavailable";
    }
    if (message.indexOf("source_schema_unavailable") !== -1) {
        return "source_schema_unavailable";
    }
    if (message.indexOf("schema_remap_unsafe_objects") !== -1) {
        return "unsafe_objects";
    }
    if (message.indexOf("qualified_source_reference_detected") !== -1) {
        return "qualified_reference_check_failed";
    }
    if (message.indexOf("restore_aggregate_mismatch") !== -1) {
        return "restore_aggregate_mismatch";
    }
    if (message.indexOf("validation_schema_") !== -1 || message.indexOf("cleanup_target_rejected") !== -1) {
        return "validation_target_rejected";
    }
    if (message.indexOf("gate_configuration_missing") !== -1) {
        return "configuration_invalid";
    }
    if (message.indexOf("restore_session_variables_admin_required") !== -1) {
        return "restore_session_variables_admin_required";
    }
    if (failureStage === "dump_utility") {
        if (/1227|access denied; you need|missing required privileges?|does not have.*privilege|\bprivileges?\b/i.test(message)) {
            return "dump_missing_privileges";
        }
        if (/lock instance for backup|flush tables with read lock|backup lock|consistent snapshot/i.test(message)) {
            return "dump_consistency_lock_failed";
        }
        if (/output directory.*(exists|not empty)|target.*(exists|not empty)|directory already exists/i.test(message)) {
            return "dump_target_exists";
        }
        if (/invalid option|unknown option|option.*not supported/i.test(message) ||
            errorType.indexOf("argument") !== -1 || errorType.indexOf("typeerror") !== -1) {
            return "dump_option_invalid";
        }
        if (/unsupported.*(server|version)|server version.*not supported|requires mysql server/i.test(message)) {
            return "dump_server_unsupported";
        }
        return "dump_utility_failed";
    }
    if (failureStage === "restore_load_dry_run" || failureStage === "restore_load") {
        const diagnosticReason = restoreReasonFromDiagnosticCode(safeDiagnosticCode(error));
        if (diagnosticReason !== null) {
            return diagnosticReason;
        }
        // loadDump 原始异常只在内存中按受控词汇归类，结果不包含路径、对象名或异常原文。
        if (/3948|local_infile\s*(?:=|is)?\s*(?:off|0|disabled)|loading local data is disabled/i.test(message)) {
            return "local_infile_off";
        }
        if (/1044|1045|1142|1227|access denied|command denied|missing required privileges?|does not have.*privilege|\bprivileges?\b/i.test(message)) {
            return "restore_missing_privileges";
        }
        if (/schema.*(?:remap|mapping).*(?:unsupported|not supported)|(?:unsupported|not supported).*schema.*(?:remap|mapping)|schema.*option.*(?:only|single.schema|unsupported|not supported)|only one schema|cannot.*target schema/i.test(message)) {
            return "restore_schema_remap_unsupported";
        }
        if (/dump.*metadata.*(?:invalid|missing|corrupt)|metadata.*(?:invalid|missing|corrupt)|@\.done\.json|@\.json|invalid dump|not a dump/i.test(message)) {
            return "restore_dump_metadata_invalid";
        }
        if (/sql_require_primary_key|primary key.*(?:required|enabled)/i.test(message)) {
            return "restore_primary_key_policy_blocked";
        }
        if (/duplicate objects?|already exists in (?:the )?(?:target|destination)/i.test(message)) {
            return "restore_duplicate_objects";
        }
        if (/unsupported dump (?:version|capabilities)|mysql version mismatch|server version.*(?:unsupported|mismatch)/i.test(message)) {
            return "restore_version_incompatible";
        }
        if (/error splitting ddl|ddl.*(?:parse|splitting).*failed/i.test(message)) {
            return "restore_ddl_parse_failed";
        }
        if (/1062|duplicate entry|duplicate key/i.test(message)) {
            return "restore_duplicate_key";
        }
        if (/1451|1452|foreign key constraint|cannot add or update a child row|cannot delete or update a parent row/i.test(message)) {
            return "restore_data_constraint_failed";
        }
        if (/1264|1265|1366|data truncated|incorrect .* value|out of range value/i.test(message)) {
            return "restore_data_value_invalid";
        }
        if (/1153|max_allowed_packet|packet.*too large|packet bigger than/i.test(message)) {
            return "restore_packet_too_large";
        }
        if (/1197|max_binlog_cache_size|transaction.*too large/i.test(message)) {
            return "restore_transaction_too_large";
        }
        if (/1114|table.*full|no space left|disk.*full/i.test(message)) {
            return "restore_storage_exhausted";
        }
        if (/1205|1213|lock wait timeout|deadlock/i.test(message)) {
            return "restore_lock_failed";
        }
        if (/invalid option|unknown option|option.*not supported/i.test(message) ||
            errorType.indexOf("argument") !== -1 || errorType.indexOf("typeerror") !== -1) {
            return "restore_option_invalid";
        }
        return failureStage === "restore_load" ? "restore_data_load_failed" : "restore_dry_run_failed";
    }
    const restoreDryRunStageReasons = {
        restore_target_privileges_check: "restore_target_privileges_required",
        restore_source_schema_check: "restore_source_schema_check_failed",
        restore_validation_target_check: "restore_validation_target_check_failed",
        restore_object_inventory: "restore_object_inventory_failed",
        restore_qualified_reference_check: "restore_qualified_reference_check_failed",
        restore_source_aggregate: "restore_source_aggregate_failed",
        restore_target_aggregate: "restore_target_aggregate_failed",
        restore_aggregate_compare: "restore_aggregate_mismatch"
    };
    if (restoreDryRunStageReasons[failureStage] !== undefined) {
        return restoreDryRunStageReasons[failureStage];
    }
    if (/1227|privilege|access denied; you need/i.test(message)) {
        return "insufficient_privileges";
    }
    if (/1045|2002|2003|2005|2013|can't connect|connection (refused|lost)|unknown mysql server host/i.test(message)) {
        return "connection_failed";
    }
    if (failureStage === "qualified_reference_check") {
        return "qualified_reference_check_failed";
    }
    const preflightStageReasons = {
        preflight_schema_query: "preflight_schema_query_failed",
        preflight_session_variables_admin: "restore_session_variables_admin_required",
        preflight_tables_query: "preflight_tables_query_failed",
        preflight_engine_query: "preflight_engine_query_failed",
        preflight_views_query: "preflight_views_query_failed",
        preflight_triggers_query: "preflight_triggers_query_failed",
        preflight_routines_query: "preflight_routines_query_failed",
        preflight_events_query: "preflight_events_query_failed"
    };
    if (preflightStageReasons[failureStage] !== undefined) {
        return preflightStageReasons[failureStage];
    }
    if (action === "backup" || action === "backup_dry_run") {
        return "dump_utility_failed";
    }
    if (action === "preflight") {
        return "preflight_query_failed";
    }
    if (action === "restore_dry_run") {
        return "restore_dry_run_failed";
    }
    if (action === "restore") {
        return "restore_failed";
    }
    if (action === "cleanup") {
        return "cleanup_failed";
    }
    return "mysqlsh_action_failed";
}

function restoreReasonFromDiagnosticCode(code) {
    if (code === null) {
        return null;
    }
    if (code === 53002) {
        return "restore_ddl_parse_failed";
    }
    if (code === 53004) {
        return "restore_missing_privileges";
    }
    if (code === 53005) {
        return "restore_worker_failed";
    }
    if (code === 53006 || code === 53007 || code === 53009 || code === 53010 || code === 53011 || code === 53019) {
        return "restore_version_incompatible";
    }
    if (code === 53008) {
        return "restore_dump_incomplete";
    }
    if (code === 53020) {
        return "restore_primary_key_policy_blocked";
    }
    if (code === 53021) {
        return "restore_duplicate_objects";
    }
    if (code === 53023 || code === 53024 || code === 53029 || code === 53030) {
        return "restore_dump_metadata_invalid";
    }
    if (code === 53025) {
        return "local_infile_off";
    }
    if (code === 53026 || code === 53027) {
        return "restore_progress_state_invalid";
    }
    if (code === 53031) {
        return "restore_checksum_failed";
    }
    if (code >= 54000 && code <= 54511) {
        return "restore_connection_failed";
    }
    return null;
}

function safeDiagnosticCode(error) {
    const candidates = [];
    if (error) {
        candidates.push(Number(error.code));
        if (error.cause) {
            candidates.push(Number(error.cause.code));
        }
        const message = String(error.message || error);
        const match = message.match(/\b(?:MYSQLSH|MySQL Error|Error(?: code)?)\s*[:#]?\s*(\d{3,6})\b/i);
        if (match !== null) {
            candidates.push(Number(match[1]));
        }
    }
    for (let index = 0; index < candidates.length; index += 1) {
        if (Number.isInteger(candidates[index]) && candidates[index] >= 1 && candidates[index] <= 999999) {
            return candidates[index];
        }
    }
    return null;
}

function safeFailureStage(stage) {
    // 失败阶段只能从固定枚举返回，未知内部值统一收敛，禁止透传任意文本。
    const allowed = {
        initialization: true,
        configuration: true,
        preflight: true,
        preflight_query: true,
        preflight_session_variables_admin: true,
        preflight_schema_query: true,
        preflight_tables_query: true,
        preflight_engine_query: true,
        preflight_views_query: true,
        preflight_triggers_query: true,
        preflight_routines_query: true,
        preflight_events_query: true,
        qualified_reference_check: true,
        dump_utility: true,
        restore_source_schema_check: true,
        restore_validation_target_check: true,
        restore_object_inventory: true,
        restore_qualified_reference_check: true,
        restore_target_privileges_check: true,
        restore_source_aggregate: true,
        restore_load_dry_run: true,
        restore_load: true,
        restore_target_aggregate: true,
        restore_aggregate_compare: true,
        restore_ownership_marker: true,
        cleanup: true,
        unknown: true
    };
    return allowed[String(stage)] === true ? String(stage) : "unknown";
}

let action = "initialization";
let failureStage = "initialization";
let ownershipConfirmed = false;
try {
failureStage = "configuration";
const sourceSchema = requireEnv("MOLIN_GATE_SOURCE_SCHEMA");
const validationSchema = requireEnv("MOLIN_GATE_VALIDATION_SCHEMA");
const dumpPath = requireEnv("MOLIN_GATE_DUMP_PATH");
const progressFile = requireEnv("MOLIN_GATE_PROGRESS_FILE");
const ownershipToken = requireOwnershipToken();
action = requireEnv("MOLIN_GATE_ACTION");

if (sourceSchema === validationSchema || validationSchema.indexOf("molin_restore_verify_") !== 0) {
    throw new Error("validation_schema_invalid");
}

if (action === "preflight") {
    failureStage = "preflight_session_variables_admin";
    assertSessionVariablesAdminEvidence();
    failureStage = "restore_target_privileges_check";
    assertRestoreTargetPrivileges(validationSchema);
    failureStage = "preflight_schema_query";
    assertSchemaExists(sourceSchema);
    const summary = objectSummary(sourceSchema, function (stage) {
        failureStage = stage;
    });
    assertRemapSafe(summary);
    failureStage = "qualified_reference_check";
    assertNoQualifiedSourceReferences(sourceSchema);
    printResult({status: "preflight_complete", table_count: summary.tables});
} else if (action === "backup_dry_run") {
    failureStage = "preflight_query";
    assertSchemaExists(sourceSchema);
    const summary = objectSummary(sourceSchema);
    assertRemapSafe(summary);
    failureStage = "qualified_reference_check";
    assertNoQualifiedSourceReferences(sourceSchema);
    failureStage = "dump_utility";
    util.dumpSchemas([sourceSchema], dumpPath, {
        dryRun: true,
        consistent: true,
        checksum: true,
        threads: 2,
        showProgress: false
    });
    printResult({status: "backup_dry_run_complete", table_count: summary.tables});
} else if (action === "backup") {
    failureStage = "preflight_query";
    assertSchemaExists(sourceSchema);
    const summary = objectSummary(sourceSchema);
    // 在产生含敏感数据的备份前先确认它具备安全隔离恢复条件。
    assertRemapSafe(summary);
    failureStage = "qualified_reference_check";
    assertNoQualifiedSourceReferences(sourceSchema);
    failureStage = "dump_utility";
    util.dumpSchemas([sourceSchema], dumpPath, {
        consistent: true,
        checksum: true,
        threads: 2,
        showProgress: false
    });
    printResult({status: "backup_complete", table_count: summary.tables});
} else if (action === "restore_dry_run") {
    failureStage = "restore_target_privileges_check";
    assertRestoreTargetPrivileges(validationSchema);
    failureStage = "restore_source_schema_check";
    assertSchemaExists(sourceSchema);
    failureStage = "restore_validation_target_check";
    assertTargetAbsent(validationSchema);
    failureStage = "restore_object_inventory";
    const summary = objectSummary(sourceSchema);
    assertRemapSafe(summary);
    failureStage = "restore_qualified_reference_check";
    assertNoQualifiedSourceReferences(sourceSchema);
    failureStage = "restore_load_dry_run";
    util.loadDump(dumpPath, {
        schema: validationSchema,
        dryRun: true,
        // 测试数据量较小，固定单线程以减少并行装载带来的非确定性。
        threads: 1,
        showProgress: false,
        progressFile: ""
    });
    printResult({status: "restore_dry_run_complete", table_count: summary.tables});
} else if (action === "restore_diagnostic") {
    // 所有探针都强制 dryRun 且禁用进度文件，只返回布尔值、固定原因和可选数字诊断码。
    failureStage = "restore_target_privileges_check";
    const targetPrivilegeEvidence = assertRestoreTargetPrivileges(validationSchema);
    failureStage = "restore_source_schema_check";
    assertSchemaExists(sourceSchema);
    failureStage = "restore_validation_target_check";
    assertTargetAbsent(validationSchema);
    failureStage = "restore_object_inventory";
    const summary = objectSummary(sourceSchema);
    assertRemapSafe(summary);
    failureStage = "restore_qualified_reference_check";
    assertNoQualifiedSourceReferences(sourceSchema);

    const commonOptions = {dryRun: true, threads: 1, showProgress: false, progressFile: ""};
    const sourceCollision = runRestoreDryRunProbe(dumpPath, Object.assign({}, commonOptions, {
        checksum: false
    }));
    const schemaNoChecksum = runRestoreDryRunProbe(dumpPath, Object.assign({}, commonOptions, {
        schema: validationSchema, checksum: false
    }));
    const ddlOnly = runRestoreDryRunProbe(dumpPath, Object.assign({}, commonOptions, {
        schema: validationSchema, checksum: false, loadDdl: true, loadData: false
    }));
    const dataOnly = runRestoreDryRunProbe(dumpPath, Object.assign({}, commonOptions, {
        schema: validationSchema, checksum: false, loadDdl: false, loadData: true
    }));
    const schemaWithChecksum = runRestoreDryRunProbe(dumpPath, Object.assign({}, commonOptions, {
        schema: validationSchema, checksum: true
    }));
    printResult({
        status: "restore_diagnostic_complete",
        source_schema_exists: true,
        target_schema_absent: true,
        restore_target_capability: targetPrivilegeEvidence.overall,
        create_capability: targetPrivilegeEvidence.create,
        select_capability: targetPrivilegeEvidence.select,
        source_collision_probe: sourceCollision,
        schema_no_checksum_probe: schemaNoChecksum,
        ddl_only_probe: ddlOnly,
        data_only_probe: dataOnly,
        schema_with_checksum_probe: schemaWithChecksum,
        remote_write: false
    });
} else if (action === "restore") {
    failureStage = "restore_target_privileges_check";
    assertRestoreTargetPrivileges(validationSchema);
    failureStage = "restore_source_schema_check";
    assertSchemaExists(sourceSchema);
    failureStage = "restore_validation_target_check";
    assertTargetAbsent(validationSchema);
    failureStage = "restore_object_inventory";
    assertRemapSafe(objectSummary(sourceSchema));
    assertOwnershipMarkerNameAvailable(sourceSchema);
    failureStage = "restore_qualified_reference_check";
    assertNoQualifiedSourceReferences(sourceSchema);
    // 先把 dump checksum count 固定为不可变快照，再确认业务源总行数没有偏离备份时点。
    failureStage = "restore_source_aggregate";
    const checksumManifest = dumpChecksumManifest(dumpPath, sourceSchema);
    const sourceAggregate = verificationAggregate(sourceSchema);
    assertTableCoverage(sourceSchema, checksumManifest.tables);
    if (sourceAggregate.total_rows !== checksumManifest.expected_total_rows) {
        throw new Error("row_count_mismatch");
    }
    failureStage = "restore_ownership_marker";
    createOwnedValidationSchema(validationSchema, ownershipToken);
    ownershipConfirmed = true;
    failureStage = "restore_load";
    util.loadDump(dumpPath, {
        schema: validationSchema,
        // MySQL Shell 8.4.10 在 schema remap + checksum: true 完成装载后仍异常退出；改由下方补偿校验闭环。
        checksum: false,
        // 隔离恢复优先保证稳定与可诊断性，禁止并行数据装载。
        threads: 1,
        showProgress: false,
        progressFile: progressFile,
        // 隔离 schema 和专用 marker 已由本连接创建；只忽略这两个已确认对象，后续覆盖率校验仍拒绝额外对象。
        ignoreExistingObjects: true
    });
    failureStage = "restore_target_aggregate";
    const targetAggregate = verificationAggregate(validationSchema);
    assertTableCoverage(validationSchema, checksumManifest.tables);
    assertExpectedRows(validationSchema, checksumManifest);
    const checksumComparison = compareSourceTargetChecksums(
        sourceSchema, validationSchema, checksumManifest.tables
    );
    failureStage = "restore_aggregate_compare";
    assertAggregateEqual(sourceAggregate, targetAggregate);
    failureStage = "restore_ownership_marker";
    assertOwnershipMarker(validationSchema, ownershipToken);
    printResult({
        status: "restore_verified",
        ownership_confirmed: true,
        table_count: targetAggregate.table_count,
        total_rows: targetAggregate.total_rows,
        structure_fingerprint: targetAggregate.structure_fingerprint,
        checked_table_count: checksumComparison.checked_table_count,
        checksum_fingerprint: checksumComparison.checksum_fingerprint
    });
} else if (action === "validation_status") {
    // 只返回精确隔离 schema 的存在状态和对象计数，不输出 schema 或对象名称。
    const exists = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?",
        [validationSchema]
    )) === 1;
    let tableCount = 0;
    let viewCount = 0;
    let triggerCount = 0;
    let routineCount = 0;
    let eventCount = 0;
    if (exists) {
        tableCount = Number(scalar("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_type = 'BASE TABLE'", [validationSchema]));
        viewCount = Number(scalar("SELECT COUNT(*) FROM information_schema.views WHERE table_schema = ?", [validationSchema]));
        triggerCount = Number(scalar("SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = ?", [validationSchema]));
        routineCount = Number(scalar("SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema = ?", [validationSchema]));
        eventCount = Number(scalar("SELECT COUNT(*) FROM information_schema.events WHERE event_schema = ?", [validationSchema]));
    }
    printResult({
        status: "validation_status_complete",
        exists: exists,
        table_count: tableCount,
        view_count: viewCount,
        trigger_count: triggerCount,
        routine_count: routineCount,
        event_count: eventCount
    });
} else if (action === "restore_variable_status") {
    // 只读查询当前会话的主键生成策略，结果严格收敛为 on/off/absent 枚举。
    const rows = session.runSql(
        "SHOW SESSION VARIABLES WHERE Variable_name IN ('sql_generate_invisible_primary_key', 'sql_require_primary_key')"
    ).fetchAll();
    const values = {};
    rows.forEach(function (row) {
        values[String(row[0]).toLowerCase()] = String(row[1]).toLowerCase() === "on" ? "on" : "off";
    });
    printResult({
        status: "restore_variable_status_complete",
        sql_generate_invisible_primary_key: values.sql_generate_invisible_primary_key || "absent",
        sql_require_primary_key: values.sql_require_primary_key || "absent"
    });
} else if (action === "cleanup") {
    failureStage = "cleanup";
    // DROP 前必须同时满足精确名称、固定前缀和数据库内随机 marker 三重门禁。
    if (validationSchema !== requireEnv("MOLIN_GATE_EXPECTED_CLEANUP_SCHEMA") || validationSchema.indexOf("molin_restore_verify_") !== 0) {
        throw new Error("cleanup_target_rejected");
    }
    assertOwnershipMarker(validationSchema, ownershipToken);
    session.runSql("DROP SCHEMA " + quoteIdentifier(validationSchema));
    const remaining = Number(scalar(
        "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?",
        [validationSchema]
    ));
    if (remaining !== 0) {
        throw new Error("cleanup_verification_failed");
    }
    printResult({status: "cleanup_complete", validation_schema_absent: true});
} else {
    throw new Error("gate_action_invalid");
}
} catch (error) {
    // 无论 mysqlsh 最终退出码如何，先提供唯一固定错误标记；原始异常只留给包装器内存处理。
    const blockedResult = {
        status: "blocked",
        reason: classifySafeReason(error, action, failureStage),
        failure_stage: safeFailureStage(failureStage)
    };
    if (ownershipConfirmed) {
        // 只返回清理资格布尔值，不返回 marker 或 schema 名称。
        blockedResult.ownership_confirmed = true;
    }
    const diagnosticCode = safeDiagnosticCode(error);
    if (diagnosticCode !== null) {
        blockedResult.diagnostic_code = diagnosticCode;
    }
    printResult(blockedResult);
    throw error;
}
