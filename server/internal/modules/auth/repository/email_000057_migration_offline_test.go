package repository

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func readEmail000057Migration(t *testing.T, suffix string) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位 000057 离线迁移测试文件")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../../migrations/000057_fix_email_datetime_utc_seconds."+suffix+".sql"))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 000057 %s 迁移失败: %v", suffix, err)
	}
	return string(content)
}

func compactMigrationSQL(sql string) string {
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(sql, " "))
}

func requireMigrationOrder(t *testing.T, sql string, fragments ...string) {
	t.Helper()
	statements, err := parseEmail000057Statements(sql)
	if err != nil {
		t.Fatalf("解析迁移顺序失败: %v", err)
	}
	position := -1
	for _, fragment := range fragments {
		next := -1
		for index := position + 1; index < len(statements); index++ {
			if strings.Contains(statements[index], fragment) {
				next = index
				break
			}
		}
		if next == -1 {
			t.Fatalf("迁移缺少有序片段: %s", fragment)
		}
		position = next
	}
}

func email000057StatementDigest(statement string) string {
	digest := sha256.Sum256([]byte(statement))
	return fmt.Sprintf("%x", digest[:])
}

// email000057FrozenStatementDigests 冻结去注释、字符串感知解析并规范化空白后的完整语句及顺序。
// 任一断言名称、聚合、来源表、列、WHERE、默认值、精度、ON UPDATE 或 LIMIT 改动都会使摘要不匹配。
var email000057FrozenStatementDigests = map[string][]string{
	"up": {
		"5146d0bfee4e954490e7983816f36fe1c61496527addca22f81bb16abfba559b",
		"c084e3a25fd987bbf1fd1b612b4dfd831f7aeed06a6cca78fa6222062672d129",
		"eb0b70fced24c00027544908551e45244790a48f0b1948f6fa3212858135fe10",
		"72871f52c8c722229c88d8a99c6d12639345268f26c03b57d35978c6fe24de00",
		"c79507b222444a5f2d5bb02c7caecb889db5cc10d3e1a5af8cd6a7e69657f677",
		"30ceee122944b76cb6d9b2a30725b1b61cab8f62bd1164bdcde044570b85858f",
		"5646f913efa377418d3efbd1da79aae3a2144cb2de33c515e7c9676ce655bd07",
		"766f727b14d980d1a641d503b744c46135232fa1217806f9c477e0525e5d84f2",
		"4857dc6c8feabd54ddb6d8ddc2f2af04706a180b7a89ea037ea09a81089bf26b",
		"bf98f2b74eb2f78947d9c34c75976ac5b01d20868d918e2e5d37f5e9fa5b4d12",
		"5d46d24c8c2940ea1eaf12d0ceeeabbd191330600beb4c8ff13d4be7d1de27e2",
		"9376071ef2e6802c92486be651ee93f0f6a6d60c1927712473294d9f3c8c6e32",
		"bda46fbe45e1c11f138ee1f9d66265c3be7930f8d2567d676c6d73eb6957a631",
		"9b91fa4559cff377b8739e600ce43ed6d95bd981c4d6e0ff795b7ae67401f2bc",
		"5565af813a5890e5df5b63f419af55b02499e5fe6e86ac4e52b2005612349cea",
		"4377bbfd085bdf343731d08662fe5d41eaba69b836bea50c4d3dd9bde923fb84",
	},
	"down": {
		"5146d0bfee4e954490e7983816f36fe1c61496527addca22f81bb16abfba559b",
		"5d33b8defa621ae34891e615538442d0d0c09fff9096061b9ae28b9808853f60",
		"f1d7e1891e705e519df474f0428a1ead356a5884bb111b81706d5f565a308efb",
		"2265d8400ce29fcd9cd9e97f7fe057bd2f142e8e4a0367941c7e23289551a977",
		"7c6375efd760f8f8b5f35567fd9fd9e492f8080617b27a01f38023376ab4e9af",
		"de2a39ad7753ac6f28055b238e2061ecba792b779a8bd66b3e1c6fd19b21da59",
		"a3a2fdc8644ab735af33c955a1dd65b7cabf699358e25701daedfeeb08b381a1",
		"29a903dd01bba1fe8743545927794ae8b66b886adedc6a3c6366b261e19a3ba7",
		"589badd4b1cae94c3ffc3494ecaff613ffed5ea4f7931a45bc1674ffb57579ca",
		"752093f6a3ed974b53f0c9125c4a28ff94b328dba026f5f3c58d0561acba2ca5",
		"3af78e173ac2a729e9bae51a3c3769b6a17ddbbb3f9c3f9f22af7f6ed515b2f7",
		"0e4574e1f786efc2453b754632d9fc339bd8ae45f2fd5bb73566b27f6ca5fdcf",
		"d1a067ff3686d261b5d766f890724abe62127bd7cfba9688a588f2bc03ad8967",
		"4366aa15e1f5912b50cc48ff1602ab4495e1a58da0183834baf74fd475590687",
		"4377bbfd085bdf343731d08662fe5d41eaba69b836bea50c4d3dd9bde923fb84",
	},
}

// parseEmail000057Statements 在不执行 SQL 的前提下识别字符串、行注释和块注释，再按顶层分号切分语句。
// 该解析器只用于迁移资产结构门禁，不声称验证 MySQL 方言或运行时语义。
func parseEmail000057Statements(sql string) ([]string, error) {
	var statements []string
	var current strings.Builder
	inString := false
	inLineComment := false
	inBlockComment := false
	depth := 0
	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				current.WriteByte(' ')
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(sql) && sql[i+1] == '/' {
				inBlockComment = false
				i++
				current.WriteByte(' ')
			}
			continue
		}
		if !inString && ch == '-' && i+1 < len(sql) && sql[i+1] == '-' && (i+2 >= len(sql) || sql[i+2] <= ' ') {
			inLineComment = true
			i++
			continue
		}
		if !inString && ch == '#' {
			inLineComment = true
			continue
		}
		if !inString && ch == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			if i+2 < len(sql) && sql[i+2] == '!' {
				return nil, fmt.Errorf("拒绝可能由 MySQL 执行的版本化注释")
			}
			inBlockComment = true
			i++
			continue
		}
		if ch == '\'' {
			current.WriteByte(ch)
			if inString && i+1 < len(sql) && sql[i+1] == '\'' {
				current.WriteByte(sql[i+1])
				i++
				continue
			}
			inString = !inString
			continue
		}
		if inString {
			current.WriteByte(ch)
			if ch == '\\' && i+1 < len(sql) {
				current.WriteByte(sql[i+1])
				i++
			}
			continue
		}
		if ch == '`' || ch == '"' {
			return nil, fmt.Errorf("严格资产不允许反引号或双引号标识符")
		}
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("出现未配对的右括号")
			}
		case ';':
			if depth == 0 {
				statement := compactMigrationSQL(current.String())
				if statement == "" {
					return nil, fmt.Errorf("包含空语句")
				}
				statements = append(statements, statement)
				current.Reset()
				continue
			}
		}
		current.WriteByte(ch)
	}
	if inString || inBlockComment || depth != 0 {
		return nil, fmt.Errorf("引号、块注释或括号未闭合: string=%t block_comment=%t depth=%d", inString, inBlockComment, depth)
	}
	if strings.TrimSpace(current.String()) != "" {
		return nil, fmt.Errorf("最后一条语句缺少分号")
	}
	return statements, nil
}

// email000057SQLCodeMask 保留 SQL 结构并遮蔽字符串内容，防止断言文案或注释中的关键字造成误判。
func email000057SQLCodeMask(statement string) (string, error) {
	var masked strings.Builder
	inString := false
	for i := 0; i < len(statement); i++ {
		ch := statement[i]
		if ch == '\'' {
			if inString && i+1 < len(statement) && statement[i+1] == '\'' {
				i++
				continue
			}
			inString = !inString
			masked.WriteString(" '?'")
			continue
		}
		if inString {
			if ch == '\\' && i+1 < len(statement) {
				i++
			}
			continue
		}
		masked.WriteByte(ch)
	}
	if inString {
		return "", fmt.Errorf("字符串未闭合")
	}
	return compactMigrationSQL(masked.String()), nil
}

func validateEmail000057AssertionInsert(statement string) error {
	masked, err := email000057SQLCodeMask(statement)
	if err != nil {
		return err
	}
	upper := strings.ToUpper(masked)
	if !strings.HasPrefix(upper, "INSERT INTO MIGRATION_000057_ASSERTIONS (ASSERTION_NAME, PASSED) SELECT ") {
		return fmt.Errorf("断言写入目标或形态不符合白名单")
	}
	forbidden := regexp.MustCompile(`(?i)\b(INSERT\s+IGNORE|REPLACE\s+INTO|UPDATE|DELETE|TRUNCATE|DROP|ALTER|CREATE|RENAME|LOAD\s+DATA|CALL|HANDLER|INTO\s+OUTFILE|INTO\s+DUMPFILE)\b`)
	if match := forbidden.FindString(masked); match != "" {
		return fmt.Errorf("断言 SELECT 含禁止操作: %s", match)
	}
	tablePattern := regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([a-z0-9_.]+)`)
	allowedSources := map[string]bool{
		"information_schema.columns":                 true,
		"information_schema.tables":                  true,
		"information_schema.statistics":              true,
		"information_schema.table_constraints":       true,
		"information_schema.check_constraints":       true,
		"email_admin_verify_bootstrap_receipts":      true,
		"migration_000057_email_receipt_time_backup": true,
	}
	for _, match := range tablePattern.FindAllStringSubmatch(masked, -1) {
		if !allowedSources[strings.ToLower(match[1])] {
			return fmt.Errorf("断言读取了白名单外表: %s", match[1])
		}
	}
	if len(tablePattern.FindAllStringSubmatch(masked, -1)) == 0 {
		return fmt.Errorf("断言 SELECT 缺少受控来源表")
	}
	return nil
}

func validateEmail000057SQLAllowlist(sql, direction string) error {
	statements, err := parseEmail000057Statements(sql)
	if err != nil {
		return err
	}
	expectedCount := 16
	expectedKinds := []string{"temp_create", "assertion", "assertion", "assertion", "backup_create", "backup_insert", "backup_insert", "assertion", "receipt_update", "assertion", "alter", "alter", "assertion", "assertion", "assertion", "temp_drop"}
	if direction == "down" {
		expectedCount = 15
		expectedKinds = []string{"temp_create", "assertion", "assertion", "assertion", "assertion", "alter", "receipt_update", "assertion", "alter", "alter", "assertion", "assertion", "assertion", "backup_drop", "temp_drop"}
	} else if direction != "up" {
		return fmt.Errorf("未知迁移方向: %s", direction)
	}
	if len(statements) != expectedCount {
		return fmt.Errorf("%s 语句数量异常: %d", direction, len(statements))
	}
	frozenDigests := email000057FrozenStatementDigests[direction]
	if len(frozenDigests) != len(statements) {
		return fmt.Errorf("%s 冻结语句数量与迁移不一致", direction)
	}
	for index, statement := range statements {
		if actual := email000057StatementDigest(statement); actual != frozenDigests[index] {
			return fmt.Errorf("第 %d 条规范化完整语句偏离冻结契约: got=%s want=%s", index+1, actual, frozenDigests[index])
		}
	}

	createAssertion := compactMigrationSQL(`CREATE TEMPORARY TABLE migration_000057_assertions (
  assertion_name VARCHAR(191) NOT NULL,
  passed TINYINT(1) NOT NULL,
  PRIMARY KEY (assertion_name),
  CONSTRAINT chk_migration_000057_assertion CHECK (passed = 1)
) ENGINE=InnoDB`)
	dropAssertion := "DROP TEMPORARY TABLE migration_000057_assertions"
	upSceneAlter := "ALTER TABLE email_scene_bindings MODIFY COLUMN created_at DATETIME NOT NULL, MODIFY COLUMN updated_at DATETIME NOT NULL"
	upReceiptAlter := "ALTER TABLE email_admin_verify_bootstrap_receipts MODIFY COLUMN created_at DATETIME NOT NULL"
	downSceneAlter := "ALTER TABLE email_scene_bindings MODIFY COLUMN created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, MODIFY COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP"
	downReceiptExpand := "ALTER TABLE email_admin_verify_bootstrap_receipts MODIFY COLUMN created_at DATETIME(3) NOT NULL"
	downReceiptAlter := "ALTER TABLE email_admin_verify_bootstrap_receipts MODIFY COLUMN created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)"
	allowedAlters := map[string]bool{upSceneAlter: true, upReceiptAlter: true}
	if direction == "down" {
		allowedAlters = map[string]bool{downReceiptExpand: true, downSceneAlter: true, downReceiptAlter: true}
	}

	for index, statement := range statements {
		switch expectedKinds[index] {
		case "temp_create":
			if statement != createAssertion {
				return fmt.Errorf("第 %d 条临时断言表创建不精确", index+1)
			}
		case "temp_drop":
			if statement != dropAssertion {
				return fmt.Errorf("第 %d 条临时断言表删除不精确", index+1)
			}
		case "assertion":
			if err := validateEmail000057AssertionInsert(statement); err != nil {
				return fmt.Errorf("第 %d 条语句失败: %w", index+1, err)
			}
		case "backup_create":
			if !strings.HasPrefix(statement, "CREATE TABLE migration_000057_email_receipt_time_backup (") {
				return fmt.Errorf("第 %d 条备份表创建目标不精确", index+1)
			}
		case "backup_insert":
			if !strings.HasPrefix(statement, "INSERT INTO migration_000057_email_receipt_time_backup (") {
				return fmt.Errorf("第 %d 条备份写入目标不精确", index+1)
			}
		case "receipt_update":
			if !strings.HasPrefix(statement, "UPDATE email_admin_verify_bootstrap_receipts r JOIN migration_000057_email_receipt_time_backup b ") {
				return fmt.Errorf("第 %d 条 receipt 恢复或归一语句目标不精确", index+1)
			}
		case "alter":
			if !allowedAlters[statement] {
				return fmt.Errorf("第 %d 条 ALTER 不在三列精确白名单", index+1)
			}
		case "backup_drop":
			if statement != "DROP TABLE migration_000057_email_receipt_time_backup" {
				return fmt.Errorf("第 %d 条备份表删除不精确", index+1)
			}
		}
	}
	return nil
}

func TestEmail000057SQLAssetsStrictAllowlistOffline(t *testing.T) {
	if err := validateEmail000057SQLAllowlist(readEmail000057Migration(t, "up"), "up"); err != nil {
		t.Fatalf("000057 up 离线结构白名单失败: %v", err)
	}
	if err := validateEmail000057SQLAllowlist(readEmail000057Migration(t, "down"), "down"); err != nil {
		t.Fatalf("000057 down 离线结构白名单失败: %v", err)
	}
}

func TestEmail000057SQLStrictAllowlistRejectsMaliciousVariants(t *testing.T) {
	validSQL := readEmail000057Migration(t, "up")
	statements, err := parseEmail000057Statements(validSQL)
	if err != nil {
		t.Fatalf("准备恶意变体失败: %v", err)
	}
	maliciousStatements := []string{
		"INSERT IGNORE INTO migration_000057_assertions (assertion_name, passed) SELECT 'x', 1 FROM information_schema.columns",
		"REPLACE INTO migration_000057_assertions (assertion_name, passed) VALUES ('x', 1)",
		"TRUNCATE TABLE email_scene_bindings",
		"DROP TABLE email_scene_bindings",
		"RENAME TABLE email_scene_bindings TO email_scene_bindings_old",
		"LOAD DATA LOCAL INFILE 'x' INTO TABLE email_scene_bindings",
		"CREATE TABLE persistent_surprise (id BIGINT)",
		"ALTER TABLE users ADD COLUMN surprise DATETIME",
		"ALTER TABLE email_scene_bindings ADD COLUMN surprise DATETIME",
		"INSERT INTO users (id) VALUES (1)",
		"UPDATE email_admin_verify_bootstrap_receipts SET created_at = CURRENT_TIMESTAMP",
		"DELETE FROM email_scene_bindings",
		"SET @unsafe = 1",
		"SELECT * FROM users",
		"INSERT INTO migration_000057_assertions (assertion_name, passed) SELECT 'x', 1 FROM users",
	}
	for _, malicious := range maliciousStatements {
		mutated := append([]string(nil), statements...)
		mutated[1] = malicious
		if err := validateEmail000057SQLAllowlist(strings.Join(mutated, ";\n")+";", "up"); err == nil {
			t.Fatalf("严格白名单错误接受恶意语句: %s", malicious)
		}
	}
}

func TestEmail000057SQLStrictAllowlistIgnoresCommentsAndStringLiterals(t *testing.T) {
	validSQL := readEmail000057Migration(t, "up")
	withComments := "-- DROP TABLE users;\n/* REPLACE INTO users VALUES (1); */\n" + validSQL + "\n# LOAD DATA INFILE 'x' INTO TABLE users;\n"
	if err := validateEmail000057SQLAllowlist(withComments, "up"); err != nil {
		t.Fatalf("注释中的禁止词不得造成误判: %v", err)
	}
	stringBoundary, err := parseEmail000057Statements("SELECT 'DROP TABLE users; -- REPLACE INTO x /* LOAD DATA */';")
	if err != nil || len(stringBoundary) != 1 {
		t.Fatalf("字符串内的分号、注释符和禁止词不得破坏语句边界: statements=%d err=%v", len(stringBoundary), err)
	}
	versionedComment := "/*!50000 DROP TABLE users */\n" + validSQL
	if err := validateEmail000057SQLAllowlist(versionedComment, "up"); err == nil {
		t.Fatal("可能被 MySQL 执行的版本化注释必须失败关闭")
	}
}

func TestEmail000057FrozenAssertionsRejectDeleteReplaceReorderAndCommentSpoof(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		statements, err := parseEmail000057Statements(readEmail000057Migration(t, direction))
		if err != nil {
			t.Fatalf("解析 %s 攻击基线失败: %v", direction, err)
		}
		assertionIndexes := make([]int, 0)
		for index, statement := range statements {
			if strings.HasPrefix(strings.ToUpper(statement), "INSERT INTO MIGRATION_000057_ASSERTIONS") {
				assertionIndexes = append(assertionIndexes, index)
			}
		}
		for position, index := range assertionIndexes {
			deleted := append([]string(nil), statements[:index]...)
			deleted = append(deleted, statements[index+1:]...)
			if err := validateEmail000057SQLAllowlist(strings.Join(deleted, ";\n")+";", direction); err == nil {
				t.Fatalf("%s 第 %d 个门禁被删除时必须失败", direction, position+1)
			}

			dummy := append([]string(nil), statements...)
			dummy[index] = "INSERT INTO migration_000057_assertions (assertion_name, passed) SELECT '恒成功伪门禁', 1 FROM information_schema.columns LIMIT 1"
			if err := validateEmail000057SQLAllowlist(strings.Join(dummy, ";\n")+";", direction); err == nil {
				t.Fatalf("%s 第 %d 个门禁替换为常量 SELECT 时必须失败", direction, position+1)
			}

			commentSpoof := append([]string(nil), statements...)
			commentSpoof[index] = "-- " + statements[index] + "\n" + dummy[index]
			if err := validateEmail000057SQLAllowlist(strings.Join(commentSpoof, ";\n")+";", direction); err == nil {
				t.Fatalf("%s 第 %d 个原门禁藏入注释时必须失败", direction, position+1)
			}

			reordered := append([]string(nil), statements...)
			other := assertionIndexes[(position+1)%len(assertionIndexes)]
			reordered[index], reordered[other] = reordered[other], reordered[index]
			if err := validateEmail000057SQLAllowlist(strings.Join(reordered, ";\n")+";", direction); err == nil {
				t.Fatalf("%s 第 %d 个门禁被重排时必须失败", direction, position+1)
			}
		}
	}
}

func TestEmail000057EveryStatementBoundaryFailsClosedOffline(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		statements, err := parseEmail000057Statements(readEmail000057Migration(t, direction))
		if err != nil {
			t.Fatal(err)
		}
		for index := range statements {
			deleted := append([]string(nil), statements[:index]...)
			deleted = append(deleted, statements[index+1:]...)
			if validateEmail000057SQLAllowlist(strings.Join(deleted, ";\n")+";", direction) == nil {
				t.Fatalf("%s 第 %d 个故障注入边界删除后必须失败", direction, index+1)
			}
			replaced := append([]string(nil), statements...)
			replaced[index] = "SELECT 1 FROM information_schema.columns LIMIT 1"
			if validateEmail000057SQLAllowlist(strings.Join(replaced, ";\n")+";", direction) == nil {
				t.Fatalf("%s 第 %d 个故障注入边界替换后必须失败", direction, index+1)
			}
			if index+1 < len(statements) {
				reordered := append([]string(nil), statements...)
				reordered[index], reordered[index+1] = reordered[index+1], reordered[index]
				if validateEmail000057SQLAllowlist(strings.Join(reordered, ";\n")+";", direction) == nil {
					t.Fatalf("%s 第 %d 个故障注入边界重排后必须失败", direction, index+1)
				}
			}
		}
	}
}

type email000057ReceiptState struct {
	id      uint64
	created time.Time
	marker  string
}

type email000057BackupState struct {
	expected int
	original map[uint64]time.Time
	seconds  map[uint64]time.Time
	markers  map[uint64]string
}

func buildEmail000057BackupModel(rows []email000057ReceiptState) email000057BackupState {
	backup := email000057BackupState{original: map[uint64]time.Time{}, seconds: map[uint64]time.Time{}, markers: map[uint64]string{}}
	for _, row := range rows {
		if row.created.Nanosecond() == 0 {
			continue
		}
		backup.expected++
		backup.original[row.id] = row.created
		backup.seconds[row.id] = row.created.Truncate(time.Second)
		backup.markers[row.id] = row.marker
	}
	return backup
}

func validateEmail000057BackupModel(rows []email000057ReceiptState, backup email000057BackupState, normalized bool) bool {
	if len(backup.original) != backup.expected || len(backup.seconds) != backup.expected || len(backup.markers) != backup.expected {
		return false
	}
	sourceIDs := make(map[uint64]struct{}, len(rows))
	for _, row := range rows {
		sourceIDs[row.id] = struct{}{}
		original, exists := backup.original[row.id]
		if !exists {
			continue
		}
		second, hasSecond := backup.seconds[row.id]
		marker, hasMarker := backup.markers[row.id]
		if !hasSecond || !hasMarker {
			return false
		}
		want := original
		if normalized {
			want = second
		}
		if !row.created.Equal(want) || row.marker != marker {
			return false
		}
	}
	// 校验方向必须从备份反查源回执，避免 INNER JOIN 把“备份仍在、源行已丢失”的孤儿证据静默过滤掉。
	for id := range backup.original {
		if _, exists := sourceIDs[id]; !exists {
			return false
		}
		if _, exists := backup.seconds[id]; !exists {
			return false
		}
		if _, exists := backup.markers[id]; !exists {
			return false
		}
	}
	for id := range backup.seconds {
		if _, exists := backup.original[id]; !exists {
			return false
		}
	}
	for id := range backup.markers {
		if _, exists := backup.original[id]; !exists {
			return false
		}
	}
	return true
}

func cloneEmail000057BackupModel(backup email000057BackupState) email000057BackupState {
	cloned := email000057BackupState{
		expected: backup.expected,
		original: make(map[uint64]time.Time, len(backup.original)),
		seconds:  make(map[uint64]time.Time, len(backup.seconds)),
		markers:  make(map[uint64]string, len(backup.markers)),
	}
	for id, value := range backup.original {
		cloned.original[id] = value
	}
	for id, value := range backup.seconds {
		cloned.seconds[id] = value
	}
	for id, value := range backup.markers {
		cloned.markers[id] = value
	}
	return cloned
}

func TestEmail000057BackupModelZeroOneManyAndIncomplete(t *testing.T) {
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	for _, rows := range [][]email000057ReceiptState{
		{},
		{{id: 1, created: base.Add(123 * time.Millisecond), marker: "a"}},
		{{id: 1, created: base, marker: "a"}, {id: 2, created: base.Add(123 * time.Millisecond), marker: "b"}, {id: 3, created: base.Add(987 * time.Millisecond), marker: "c"}},
	} {
		backup := buildEmail000057BackupModel(rows)
		if !validateEmail000057BackupModel(rows, backup, false) {
			t.Fatal("原值备份模型必须完整")
		}
		normalized := append([]email000057ReceiptState(nil), rows...)
		for index := range normalized {
			if second, ok := backup.seconds[normalized[index].id]; ok {
				normalized[index].created = second
			}
		}
		if !validateEmail000057BackupModel(normalized, backup, true) {
			t.Fatal("秒级归一不得修改非时间标记")
		}
		restored := append([]email000057ReceiptState(nil), normalized...)
		for index := range restored {
			if original, ok := backup.original[restored[index].id]; ok {
				restored[index].created = original
			}
		}
		if !validateEmail000057BackupModel(restored, backup, false) {
			t.Fatal("down 必须按主键恢复全部原始毫秒")
		}
		if backup.expected > 0 {
			for id := range backup.original {
				delete(backup.original, id)
				break
			}
			if validateEmail000057BackupModel(restored, backup, false) {
				t.Fatal("备份缺行必须失败关闭")
			}
		}
	}
}

func TestEmail000057BackupModelRejectsOrphansAtEveryEvidenceGate(t *testing.T) {
	base := time.Date(2026, 7, 27, 12, 0, 0, 123000000, time.UTC)
	rows := []email000057ReceiptState{{id: 1, created: base, marker: "a"}}
	backup := buildEmail000057BackupModel(rows)
	gates := []struct {
		name       string
		normalized bool
	}{
		{name: "up 更新后完整性门禁", normalized: true},
		{name: "up DDL 后保留证据门禁", normalized: true},
		{name: "down 恢复后完整性门禁", normalized: false},
		{name: "down 删除备份前门禁", normalized: false},
	}
	for _, gate := range gates {
		t.Run(gate.name, func(t *testing.T) {
			gateRows := append([]email000057ReceiptState(nil), rows...)
			if gate.normalized {
				gateRows[0].created = gateRows[0].created.Truncate(time.Second)
			}
			if validateEmail000057BackupModel(nil, backup, gate.normalized) {
				t.Fatal("源回执缺失时必须失败关闭")
			}

			orphaned := cloneEmail000057BackupModel(backup)
			orphaned.expected++
			orphaned.original[999] = base.Add(time.Millisecond)
			orphaned.seconds[999] = base.Truncate(time.Second)
			orphaned.markers[999] = "orphan"
			if validateEmail000057BackupModel(gateRows, orphaned, gate.normalized) {
				t.Fatal("孤儿备份行必须失败关闭")
			}
		})
	}
}

func TestEmail000057EvidenceAssertionsAreBackupDrivenOffline(t *testing.T) {
	assertions := map[string][]string{
		"up": {
			"receipt 必须全部归一到秒且非时间字段保持不变",
			"000057 专用备份必须保留完整恢复证据",
		},
		"down": {
			"receipt 原始毫秒必须按主键完整恢复且非时间字段不变",
			"删除备份前恢复证据必须仍完整",
		},
	}
	for direction, labels := range assertions {
		statements, err := parseEmail000057Statements(readEmail000057Migration(t, direction))
		if err != nil {
			t.Fatalf("解析 000057 %s 失败: %v", direction, err)
		}
		for _, label := range labels {
			var assertion string
			for _, statement := range statements {
				if strings.Contains(statement, label) {
					assertion = statement
					break
				}
			}
			if assertion == "" {
				t.Fatalf("000057 %s 缺少完整性断言: %s", direction, label)
			}
			for _, required := range []string{
				"FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r",
				"r.id IS NULL",
				"COUNT(r.id)",
				"row_kind = 'manifest'",
				"expected_count",
			} {
				if !strings.Contains(assertion, required) {
					t.Fatalf("断言 %q 必须由备份 LEFT JOIN 驱动并校验计数，缺少: %s", label, required)
				}
			}
		}
	}

	downStatements, err := parseEmail000057Statements(readEmail000057Migration(t, "down"))
	if err != nil {
		t.Fatalf("解析 000057 down 失败: %v", err)
	}
	finalGate, dropBackup := -1, -1
	for index, statement := range downStatements {
		if strings.Contains(statement, "删除备份前恢复证据必须仍完整") {
			finalGate = index
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "DROP TABLE MIGRATION_000057_EMAIL_RECEIPT_TIME_BACKUP") {
			dropBackup = index
		}
	}
	if finalGate == -1 || dropBackup == -1 || dropBackup <= finalGate {
		t.Fatal("down 只能在最终恢复证据门禁通过后删除专用备份表")
	}
}

func findEmail000057AssertionStatement(t *testing.T, direction, label string) string {
	t.Helper()
	statements, err := parseEmail000057Statements(readEmail000057Migration(t, direction))
	if err != nil {
		t.Fatalf("解析 000057 %s 失败: %v", direction, err)
	}
	for _, statement := range statements {
		if strings.Contains(statement, label) {
			return statement
		}
	}
	t.Fatalf("000057 %s 缺少断言: %s", direction, label)
	return ""
}

func validateEmail000057RealMySQLSchemaAssertion(statement string) error {
	broadBackslashRemoval := regexp.MustCompile(`(?i)REPLACE\s*\(\s*(?:normalized_clause|cc\.check_clause)\s*,\s*CHAR\(92\)\s*,\s*''\s*\)`)
	if broadBackslashRemoval.MatchString(statement) {
		return fmt.Errorf("不得全量删除 CHECK_CLAUSE 中的反斜杠")
	}
	if strings.Contains(strings.ToUpper(statement), "REGEXP_REPLACE") {
		return fmt.Errorf("不得用正则宽泛删除 CHECK_CLAUSE 中的字符集 introducer")
	}
	if !strings.Contains(statement, "SELECT index_name, non_unique FROM information_schema.statistics") {
		return fmt.Errorf("主键派生表必须投影 HAVING 使用的 non_unique")
	}
	for _, introducer := range []string{"_utf8mb4", "_latin1"} {
		if strings.Count(statement, "'"+introducer+"'") != 1 {
			return fmt.Errorf("CHECK_CLAUSE 必须且只能显式移除一次 %s 字符集 introducer", introducer)
		}
	}
	narrowQuoteNormalization := "REPLACE(normalized_clause, CONCAT(CHAR(92), CHAR(39)), CHAR(39))"
	if strings.Count(statement, narrowQuoteNormalization) != 3 {
		return fmt.Errorf("三项 CHECK_CLAUSE 比较必须仅规范化反斜杠单引号")
	}
	return nil
}

// normalizeEmail000057CheckClauseFixture 模拟 down 断言对 CHECK_CLAUSE 的窄化归一。
// 字符集 introducer 只接受迁移中明确冻结的白名单，其他合法或未知前缀必须保留并令结构门禁失败关闭。
func normalizeEmail000057CheckClauseFixture(clause string) string {
	normalized := strings.ToLower(clause)
	for _, replacement := range []struct {
		old string
		new string
	}{
		{old: "`", new: ""},
		{old: " ", new: ""},
		{old: "\n", new: ""},
		{old: "_utf8mb4", new: ""},
		{old: "_latin1", new: ""},
		{old: "(", new: ""},
		{old: ")", new: ""},
	} {
		normalized = strings.ReplaceAll(normalized, replacement.old, replacement.new)
	}
	return strings.ReplaceAll(normalized, `\'`, `'`)
}

func TestEmail000057DownRealMySQLStructureCompatibilityOffline(t *testing.T) {
	statement := findEmail000057AssertionStatement(t, "down", "000057 专用备份表结构必须完整")
	t.Run("HAVING字段必须投影", func(t *testing.T) {
		if !strings.Contains(statement, "SELECT index_name, non_unique FROM information_schema.statistics") {
			t.Fatal("information_schema.statistics 派生表必须投影 non_unique，HAVING 才能合法引用")
		}
	})
	t.Run("反斜杠单引号必须窄化规范", func(t *testing.T) {
		narrow := "REPLACE(normalized_clause, CONCAT(CHAR(92), CHAR(39)), CHAR(39))"
		if count := strings.Count(statement, narrow); count != 3 {
			t.Fatalf("三项 CHECK_CLAUSE 比较必须使用窄化规范，实际 %d 处", count)
		}
	})
	t.Run("禁止全量删除反斜杠", func(t *testing.T) {
		narrow := "REPLACE(normalized_clause, CONCAT(CHAR(92), CHAR(39)), CHAR(39))"
		malicious := strings.ReplaceAll(statement, narrow, "REPLACE(normalized_clause, CHAR(92), '')")
		if malicious == statement {
			// 旧 SQL 尚未加入窄化规范时，直接注入危险写法以验证否定测试缝本身有效。
			malicious = strings.Replace(statement, "normalized_clause =", "REPLACE(normalized_clause, CHAR(92), '') =", 1)
		}
		if err := validateEmail000057RealMySQLSchemaAssertion(malicious); err == nil || !strings.Contains(err.Error(), "不得全量删除") {
			t.Fatalf("全量删除反斜杠的变体必须被明确拒绝，实际: %v", err)
		}
	})
	if err := validateEmail000057RealMySQLSchemaAssertion(statement); err != nil {
		t.Fatalf("真实 MySQL 结构断言不满足离线兼容契约: %v", err)
	}
}

func TestEmail000057DownCheckClauseCharsetIntroducerFixturesOffline(t *testing.T) {
	fixtures := []struct {
		name     string
		clause   string
		expected string
	}{
		{
			name:     "latin1形状约束",
			clause:   "((`row_kind` = _latin1\\'manifest\\' and `receipt_id` = 0 and `created_at_original` is null and `created_at_second` is null and `row_fingerprint` is null and `expected_count` is not null) or (`row_kind` = _latin1\\'receipt\\' and `receipt_id` > 0 and `created_at_original` is not null and `created_at_second` is not null and `row_fingerprint` is not null and `expected_count` is null))",
			expected: "row_kind='manifest'andreceipt_id=0andcreated_at_originalisnullandcreated_at_secondisnullandrow_fingerprintisnullandexpected_countisnotnullorrow_kind='receipt'andreceipt_id>0andcreated_at_originalisnotnullandcreated_at_secondisnotnullandrow_fingerprintisnotnullandexpected_countisnull",
		},
		{
			name:     "latin1时间约束",
			clause:   "((`row_kind` = _latin1\\'manifest\\') or ((microsecond(`created_at_original`) <> 0) and (microsecond(`created_at_second`) = 0)))",
			expected: "row_kind='manifest'ormicrosecondcreated_at_original<>0andmicrosecondcreated_at_second=0",
		},
		{
			name:     "latin1指纹约束",
			clause:   "((`row_fingerprint` is null) or regexp_like(`row_fingerprint`,_latin1\\'^[0-9a-f]{64}$\\'))",
			expected: "row_fingerprintisnullorregexp_likerow_fingerprint,'^[0-9a-f]{64}$'",
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			if actual := normalizeEmail000057CheckClauseFixture(fixture.clause); actual != fixture.expected {
				t.Fatalf("CHECK_CLAUSE 归一结果不匹配\n实际: %s\n期望: %s", actual, fixture.expected)
			}
		})
	}

	t.Run("保留utf8mb4兼容", func(t *testing.T) {
		clause := "(`row_kind` = _utf8mb4\\'manifest\\')"
		if actual := normalizeEmail000057CheckClauseFixture(clause); actual != "row_kind='manifest'" {
			t.Fatalf("utf8mb4 introducer 兼容回归: %s", actual)
		}
	})
	t.Run("保留转义引号窄化", func(t *testing.T) {
		clause := "regexp_like(`row_fingerprint`,_latin1\\'^[0-9a-f]{64}$\\')"
		if actual := normalizeEmail000057CheckClauseFixture(clause); strings.Contains(actual, `\'`) || !strings.Contains(actual, "'^[0-9a-f]{64}$'") {
			t.Fatalf("反斜杠单引号必须窄化且正则内容必须保留: %s", actual)
		}
	})
	t.Run("拒绝白名单外introducer", func(t *testing.T) {
		clause := "(`row_kind` = _ucs2\\'manifest\\')"
		if actual := normalizeEmail000057CheckClauseFixture(clause); !strings.Contains(actual, "_ucs2") {
			t.Fatalf("白名单外 introducer 不得被宽泛删除: %s", actual)
		}
	})
}

func TestEmail000057BackupSchemaMutationsFailClosedOffline(t *testing.T) {
	validSQL := readEmail000057Migration(t, "down")
	mutations := []struct {
		name string
		old  string
		new  string
	}{
		{name: "额外列", old: ") = 6 AND (SELECT COUNT(*) FROM information_schema.columns", new: ") = 7 AND (SELECT COUNT(*) FROM information_schema.columns"},
		{name: "无符号主键", old: "column_type = 'bigint unsigned'", new: "column_type = 'bigint'"},
		{name: "列顺序", old: "ordinal_position = 2 AND column_name = 'row_kind'", new: "ordinal_position = 3 AND column_name = 'row_kind'"},
		{name: "可空性", old: "column_name = 'row_kind' AND column_type = 'varchar(16)' AND is_nullable = 'NO'", new: "column_name = 'row_kind' AND column_type = 'varchar(16)' AND is_nullable = 'YES'"},
		{name: "默认值", old: "column_name = 'expected_count' AND column_type = 'bigint unsigned' AND is_nullable = 'YES' AND column_default IS NULL", new: "column_name = 'expected_count' AND column_type = 'bigint unsigned' AND is_nullable = 'YES' AND column_default = '0'"},
		{name: "附加属性", old: "column_name = 'receipt_id' AND column_type = 'bigint unsigned' AND is_nullable = 'NO' AND column_default IS NULL AND extra = ''", new: "column_name = 'receipt_id' AND column_type = 'bigint unsigned' AND is_nullable = 'NO' AND column_default IS NULL AND extra = 'auto_increment'"},
		{name: "列排序规则", old: "collation_name = 'ascii_bin'", new: "collation_name = 'ascii_general_ci'"},
		{name: "存储引擎", old: "engine = 'InnoDB'", new: "engine = 'MyISAM'"},
		{name: "表排序规则", old: "table_collation = 'utf8mb4_0900_ai_ci'", new: "table_collation = 'utf8mb4_general_ci'"},
		{name: "主键唯一性投影", old: "SELECT index_name, non_unique FROM information_schema.statistics", new: "SELECT index_name FROM information_schema.statistics"},
		{name: "主键列", old: "GROUP_CONCAT(column_name ORDER BY seq_in_index) = 'receipt_id'", new: "GROUP_CONCAT(column_name ORDER BY seq_in_index) = 'row_kind'"},
		{name: "形状约束", old: "constraint_name = 'chk_migration_000057_backup_shape'", new: "constraint_name = 'chk_migration_000057_backup_shape_missing'"},
		{name: "时间约束", old: "constraint_name = 'chk_migration_000057_backup_time'", new: "constraint_name = 'chk_migration_000057_backup_time_missing'"},
		{name: "指纹约束", old: "constraint_name = 'chk_migration_000057_backup_fingerprint'", new: "constraint_name = 'chk_migration_000057_backup_fingerprint_missing'"},
		{name: "约束表达式", old: "microsecondcreated_at_second=0", new: "microsecondcreated_at_second>=0"},
		{name: "缺少latin1字符集白名单", old: "'_latin1', ''", new: "'_latin1_missing', ''"},
		{name: "缺少反斜杠单引号规范", old: "REPLACE(normalized_clause, CONCAT(CHAR(92), CHAR(39)), CHAR(39))", new: "normalized_clause"},
		{name: "全量删除反斜杠", old: "REPLACE(normalized_clause, CONCAT(CHAR(92), CHAR(39)), CHAR(39))", new: "REPLACE(normalized_clause, CHAR(92), '')"},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutated := strings.Replace(validSQL, mutation.old, mutation.new, 1)
			if mutated == validSQL {
				t.Fatalf("测试变异未命中冻结 SQL: %s", mutation.old)
			}
			if err := validateEmail000057SQLAllowlist(mutated, "down"); err == nil {
				t.Fatal("备份表结构契约被篡改后必须失败关闭")
			}
		})
	}
}

func TestEmail000057UpStructureAndOrderOffline(t *testing.T) {
	upRaw := readEmail000057Migration(t, "up")
	up := compactMigrationSQL(upRaw)
	requireMigrationOrder(t, upRaw,
		"CREATE TEMPORARY TABLE migration_000057_assertions",
		"场景绑定时间列必须符合 000055 基线",
		"bootstrap receipt 时间列必须符合 000056 基线",
		"000057 专用毫秒备份表必须尚未存在",
		"CREATE TABLE migration_000057_email_receipt_time_backup",
		"receipt 原始毫秒备份必须完整且与源行一致",
		"UPDATE email_admin_verify_bootstrap_receipts",
		"receipt 必须全部归一到秒且非时间字段保持不变",
		"ALTER TABLE email_admin_verify_bootstrap_receipts",
		"ALTER TABLE email_scene_bindings",
		"bootstrap receipt 时间列必须符合 UTC 秒级目标结构",
		"场景绑定时间列必须符合 UTC 秒级目标结构",
		"000057 专用备份必须保留完整恢复证据",
		"DROP TEMPORARY TABLE migration_000057_assertions",
	)

	for _, required := range []string{
		"MODIFY COLUMN created_at DATETIME NOT NULL, MODIFY COLUMN updated_at DATETIME NOT NULL",
		"MODIFY COLUMN created_at DATETIME NOT NULL;",
		"datetime_precision = 0",
		"column_default IS NULL",
		"LOWER(extra) NOT LIKE '%on update%'",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("000057 up 缺少目标列契约: %s", required)
		}
	}
}

func TestEmail000057UpRejectsLossyOrUnknownBaselinesOffline(t *testing.T) {
	up := compactMigrationSQL(readEmail000057Migration(t, "up"))
	for _, required := range []string{
		"datetime_precision = 3",
		"UPPER(column_default) = 'CURRENT_TIMESTAMP(3)'",
		"WHERE MICROSECOND(created_at) <> 0",
		"IF(COUNT(*) = 0, 1, 0)",
		"updated_at' AND data_type = 'datetime'",
		"LOWER(extra) LIKE '%on update current_timestamp%'",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("000057 up 缺少异常基线或精度保护: %s", required)
		}
	}

}

func TestEmail000057DownRestoresExactBaselineOffline(t *testing.T) {
	downRaw := readEmail000057Migration(t, "down")
	down := compactMigrationSQL(downRaw)
	requireMigrationOrder(t, downRaw,
		"CREATE TEMPORARY TABLE migration_000057_assertions",
		"场景绑定时间列必须符合 000057 目标结构",
		"bootstrap receipt 时间列必须符合 000057 目标结构",
		"000057 专用备份表结构必须完整",
		"000057 专用备份数据必须完整且匹配当前秒值",
		"ALTER TABLE email_admin_verify_bootstrap_receipts",
		"UPDATE email_admin_verify_bootstrap_receipts",
		"receipt 原始毫秒必须按主键完整恢复且非时间字段不变",
		"ALTER TABLE email_scene_bindings",
		"ALTER TABLE email_admin_verify_bootstrap_receipts",
		"场景绑定时间列必须恢复 000055 基线",
		"bootstrap receipt 时间列必须恢复 000056 基线",
		"删除备份前恢复证据必须仍完整",
		"DROP TABLE migration_000057_email_receipt_time_backup",
		"DROP TEMPORARY TABLE migration_000057_assertions",
	)

	for _, required := range []string{
		"MODIFY COLUMN created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP",
		"MODIFY COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP",
		"MODIFY COLUMN created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)",
		"datetime_precision = 3",
		"UPPER(column_default) = 'CURRENT_TIMESTAMP(3)'",
	} {
		if !strings.Contains(down, required) {
			t.Fatalf("000057 down 未精确恢复原始结构: %s", required)
		}
	}
}

func TestEmail000057RepeatedAndPartialStatesFailClosedOffline(t *testing.T) {
	up := compactMigrationSQL(readEmail000057Migration(t, "up"))
	down := compactMigrationSQL(readEmail000057Migration(t, "down"))
	for name, sql := range map[string]string{"up": up, "down": down} {
		if strings.Contains(strings.ToUpper(sql), "IF EXISTS") || strings.Contains(strings.ToUpper(sql), "IF NOT EXISTS") {
			t.Fatalf("000057 %s 不得把重复执行伪装成成功", name)
		}
		expectedReceiptAlters := 1
		if name == "down" {
			expectedReceiptAlters = 2
		}
		if strings.Count(sql, "ALTER TABLE email_scene_bindings") != 1 || strings.Count(sql, "ALTER TABLE email_admin_verify_bootstrap_receipts") != expectedReceiptAlters {
			t.Fatalf("000057 %s 的目标表 ALTER 次数不符合冻结流程", name)
		}
	}

	// up 只接受旧默认值，down 只接受无默认值目标结构，因此重复执行和未知 partial 状态都会在 DDL 前失败。
	if strings.Index(up, "column_default IS NULL") < strings.Index(up, "ALTER TABLE email_scene_bindings") {
		t.Fatal("000057 up 不得在前置门禁中接受已迁移结构")
	}
	if strings.Index(down, "column_default IS NULL") > strings.Index(down, "ALTER TABLE email_scene_bindings") {
		t.Fatal("000057 down 必须在 DDL 前确认 000057 目标结构")
	}
}
