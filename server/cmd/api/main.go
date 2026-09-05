package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"molin/server/internal/bootstrap"
)

func main() {
	app, err := bootstrap.NewApp()
	if err != nil {
		log.Fatal(err)
	}

	// SIGINT/SIGTERM必须进入同步关闭序列，等待HTTP请求、视频Worker和中间件按顺序收口。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := app.RunContext(ctx); err != nil {
		log.Fatal(err)
	}
}
