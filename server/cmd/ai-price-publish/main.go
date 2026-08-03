// 命令 ai-price-publish 是 G3 测试环境价格版本的受控发布入口。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"molin/server/internal/config"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/pkg/db"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("AI 价格发布失败：%v", err)
		os.Exit(1)
	}
	log.Println("AI 价格版本发布成功")
}

func run(args []string) error {
	flags := flag.NewFlagSet("ai-price-publish", flag.ContinueOnError)
	versionID := flags.Uint64("version-id", 0, "已审批价格版本 ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("AI_PRICE_PUBLISH_APPROVED")) != "YES" {
		return errors.New("必须显式设置 AI_PRICE_PUBLISH_APPROVED=YES")
	}
	if *versionID == 0 {
		return errors.New("version-id 必须是正整数")
	}
	rawAppEnv, appEnvSet := os.LookupEnv("APP_ENV")
	if !appEnvSet || strings.TrimSpace(rawAppEnv) == "" {
		return errors.New("必须显式设置非生产 APP_ENV")
	}
	cfg := config.Load()
	if !cfg.IsSafeNonProduction() {
		return errors.New("该命令只允许在明确的非生产环境运行")
	}
	gormDB, err := db.New(cfg.MySQLHost, cfg.MySQLPort, cfg.MySQLUser, cfg.MySQLPassword, cfg.MySQLDatabase)
	if err != nil {
		return fmt.Errorf("连接测试数据库失败：%w", err)
	}
	if sqlDB, dbErr := gormDB.DB(); dbErr == nil {
		defer func() { _ = sqlDB.Close() }()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := repository.NewG3PricingRepository(gormDB).PublishApprovedVersion(ctx, *versionID, time.Now().UTC()); err != nil {
		return fmt.Errorf("发布价格版本失败：%w", err)
	}
	return nil
}
