package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var sslModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

func loadDatabaseConfig() (*pgxpool.Config, error) {
	host, user, database := os.Getenv("DATABASE_HOST"), os.Getenv("DATABASE_USERNAME"), os.Getenv("DATABASE_NAME")
	if host == "" || user == "" || database == "" {
		return nil, errors.New("缺少 DATABASE_HOST、DATABASE_USERNAME 或 DATABASE_NAME")
	}
	port, err := envPort("DATABASE_PORT", 5432)
	if err != nil {
		return nil, err
	}
	sslmode := envOr("DATABASE_SSLMODE", "prefer")
	if !slices.Contains(sslModes, sslmode) {
		return nil, fmt.Errorf("DATABASE_SSLMODE 的值 %q 不合法，可选值为 %s", sslmode, strings.Join(sslModes, "、"))
	}
	cfg, err := pgxpool.ParseConfig(fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s", quoteDSN(host), port, quoteDSN(user), quoteDSN(database), sslmode))
	if err != nil {
		return nil, fmt.Errorf("无法由 DATABASE_* 和 PG* 环境变量组成 PostgreSQL 的连接配置：%w", err)
	}
	// 密码不放进连接串：pgx 的 ConnString() 原样返回连接串，解析错误中的脱敏也只是尽力而为。
	cfg.ConnConfig.Password = os.Getenv("DATABASE_PASSWORD")
	return cfg, nil
}

func quoteDSN(value string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(value) + "'"
}
