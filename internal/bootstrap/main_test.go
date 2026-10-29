package bootstrap

import "testing"

func TestMainExitCodes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{name: "缺少子命令", args: nil, want: 2},
		{name: "未知的子命令", args: []string{"bogus"}, want: 2},
		{name: "子命令出错", args: []string{"worker"}, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetenv(t, "DATABASE_HOST")
			unsetenv(t, "ALISTEN_LOG_LEVEL")
			if got := Main(tc.args); got != tc.want {
				t.Errorf("Main(%q) = %d，期望 %d", tc.args, got, tc.want)
			}
		})
	}
}
