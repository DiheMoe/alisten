package bootstrap

import (
	"log/slog"
	"os"
	"slices"
)

// 发布构建通过 -ldflags "-X github.com/dihemoe/alisten/internal/bootstrap.version=..." 覆盖。
var version = "dev"

type subcommand func(base *slog.Logger) error

var subcommands = map[string]subcommand{
	"serve":       runServe,
	"worker":      runWorker,
	"migrate":     runMigrate,
	"healthcheck": runHealthcheck,
}

func Main(args []string) int {
	level, levelErr := loadLogLevel()
	base := newLogger(os.Stdout, level)
	if levelErr != nil {
		base.With("component", "main").Error("配置不合法", "error", levelErr)
		return 1
	}

	if len(args) == 0 {
		base.With("component", "main").Error("缺少子命令", "available", subcommandNames())
		return 2
	}
	name := args[0]
	run, ok := subcommands[name]
	if !ok {
		base.With("component", "main").Error("未知的子命令", "command", name, "available", subcommandNames())
		return 2
	}

	logger := base.With("component", name)
	// 把标准库 log 包和 OpenTelemetry 的日志接入 slog，第三方库的日志也输出为 JSON。
	slog.SetDefault(logger)
	routeOpenTelemetryLogs(base.With("component", "otel"))
	if err := run(base); err != nil {
		logger.Error("子命令运行失败", "error", err)
		return 1
	}
	return 0
}

func subcommandNames() []string {
	names := make([]string, 0, len(subcommands))
	for name := range subcommands {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
