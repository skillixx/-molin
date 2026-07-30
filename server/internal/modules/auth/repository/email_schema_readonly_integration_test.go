package repository

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

const (
	emailSchemaReadonlyAck  = "I_UNDERSTAND_READ_ONLY_SCHEMA_QUERY"
	emailSchemaVersionQuery = "SELECT version, dirty FROM schema_migrations"
)

// TestEmailSchemaReadonlyGate54 只读取 migration 版本元数据，不执行任何 DDL、DML 或 migration。
// 默认始终跳过；只有运行开关和固定确认值同时匹配时，QA wrapper 才能显式启用。
func TestEmailSchemaReadonlyGate54(t *testing.T) {
	if os.Getenv("RUN_EMAIL_SCHEMA_READONLY") != "1" || os.Getenv("EMAIL_SCHEMA_READONLY_ACK") != emailSchemaReadonlyAck {
		t.Skip("schema_gate=SKIP")
	}

	host, port := os.Getenv("MYSQL_HOST"), os.Getenv("MYSQL_PORT")
	database, user := os.Getenv("MYSQL_DATABASE"), os.Getenv("MYSQL_USER")
	if host == "" || port == "" || database == "" || user == "" {
		t.Fatal("schema_gate=CONFIG_MISSING reachable=false gate_54_0=false")
	}

	// 使用结构化驱动配置生成连接串，敏感值只保留在当前测试进程内，禁止进入测试输出。
	driverConfig := mysqldriver.Config{
		User:                 user,
		Passwd:               os.Getenv("MYSQL_PASSWORD"),
		Net:                  "tcp",
		Addr:                 host + ":" + port,
		DBName:               database,
		ParseTime:            true,
		AllowNativePasswords: true,
	}
	dsn := driverConfig.FormatDSN()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("schema_gate=CONNECT_INIT_FAILED reachable=false gate_54_0=false")
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var version uint
	var dirty bool
	if err := db.QueryRowContext(ctx, emailSchemaVersionQuery).Scan(&version, &dirty); err != nil {
		t.Fatal("schema_gate=QUERY_FAILED reachable=false gate_54_0=false")
	}

	if version != 54 || dirty {
		t.Fatalf("schema_gate=VERSION_MISMATCH reachable=true version=%d dirty=%t gate_54_0=false", version, dirty)
	}
	t.Log("schema_gate=PASS reachable=true version=54 dirty=false gate_54_0=true")
}
