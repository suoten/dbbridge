package service

import "testing"

func TestParseBackupName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"_bak_users_20260830_153022", "users"},
		// 原表名本身包含下划线和数字时间戳样式（旧实现按数下划线解析会出错）
		{"_bak_order_20260102_153022_20260830_153022", "order_20260102_153022"},
		{"_bak_sys_user_log_20260830_153022", "sys_user_log"},
		// 非标准格式兜底
		{"_bak_weird", "weird"},
		{"notbackup", "notbackup"},
	}
	for _, c := range cases {
		if got := ParseBackupName(c.in); got != c.want {
			t.Errorf("ParseBackupName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
