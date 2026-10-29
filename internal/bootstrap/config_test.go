package bootstrap

import (
	"log/slog"
	"os"
	"testing"
)

// 先调用 t.Setenv，测试结束后才会恢复原值。
func unsetenv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func TestListenAddr(t *testing.T) {
	cases := []struct {
		name    string
		value   *string
		want    string
		wantErr bool
	}{
		{name: "未设置时使用默认值", value: nil, want: ":8080"},
		{name: "指定主机和端口", value: ptr("127.0.0.1:8081"), want: "127.0.0.1:8081"},
		{name: "空字符串不回退到默认值", value: ptr(""), wantErr: true},
		{name: "缺少端口", value: ptr("8080"), wantErr: true},
		{name: "端口为 0", value: ptr(":0"), wantErr: true},
		{name: "端口超出范围", value: ptr(":70000"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value == nil {
				unsetenv(t, "ALISTEN_LISTEN")
			} else {
				t.Setenv("ALISTEN_LISTEN", *tc.value)
			}
			got, err := listenAddr("ALISTEN_LISTEN", ":8080")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际得到 %q", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("listenAddr() = %q, %v，期望 %q", got, err, tc.want)
			}
		})
	}
}

func TestLoadLogLevel(t *testing.T) {
	cases := []struct {
		name    string
		value   *string
		want    slog.Level
		wantErr bool
	}{
		{name: "未设置时为 info", value: nil, want: slog.LevelInfo},
		{name: "debug", value: ptr("debug"), want: slog.LevelDebug},
		{name: "未知级别", value: ptr("verbose"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value == nil {
				unsetenv(t, "ALISTEN_LOG_LEVEL")
			} else {
				t.Setenv("ALISTEN_LOG_LEVEL", *tc.value)
			}
			got, err := loadLogLevel()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际得到 %v", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("loadLogLevel() = %v, %v，期望 %v", got, err, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }
