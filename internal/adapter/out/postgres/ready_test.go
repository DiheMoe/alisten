package postgres

import (
	"strings"
	"testing"
)

func TestCheckMigrations(t *testing.T) {
	empty := connect(t, newDatabase(t, "template1"))
	if err := CheckMigrations(t.Context(), empty); err == nil {
		t.Error("没有执行迁移时应未就绪")
	}
	var created bool
	if err := empty.QueryRow(t.Context(), "SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("就绪检查不应创建 goose 的版本表")
	}

	schemaOnly := connect(t, newDatabase(t, schemaTemplate))
	if err := CheckMigrations(t.Context(), schemaOnly); err == nil || !strings.Contains(err.Error(), "River") {
		t.Errorf("只执行了 goose 迁移时的错误 = %v，期望指出 River 迁移未完成", err)
	}

	migrated := connect(t, newDatabase(t, "template1"))
	if err := migrate(t.Context(), migrated, discard); err != nil {
		t.Fatal(err)
	}
	if err := CheckMigrations(t.Context(), migrated); err != nil {
		t.Errorf("迁移完成后应就绪：%v", err)
	}
	if _, err := migrated.Exec(t.Context(), "INSERT INTO goose_db_version (version_id, is_applied) VALUES (99, true)"); err != nil {
		t.Fatal(err)
	}
	if err := CheckMigrations(t.Context(), migrated); err != nil {
		t.Errorf("迁移版本高于要求时也应就绪：%v", err)
	}
}
