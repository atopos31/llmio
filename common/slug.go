package common

// IsSafeForwardSlug 判断字符串能否用作转发专属地址的路径标识（/p/{slug}/...）。
// 只允许 URL 安全字符：长度 1-64，首字符为字母或数字，其余为字母、数字、-、_、.。
// 首字符限制天然排除了 "." 与 ".."。
func IsSafeForwardSlug(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case (c == '-' || c == '_' || c == '.') && i > 0:
		default:
			return false
		}
	}
	return true
}
