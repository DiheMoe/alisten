package qq

import (
	"context"
	"strings"

	"github.com/dihemoe/alisten/internal/app/port"
)

func (c *Client) CurrentAccount(ctx context.Context) (*port.SourceAccount, error) {
	cookie, err := c.cookie.get(ctx)
	if err != nil {
		return nil, err
	}
	id := qqAccountID(cookie)
	if id == "" {
		return nil, nil
	}
	return &port.SourceAccount{ID: id}, nil
}

func qqAccountID(cookie string) string {
	for _, name := range []string{"wxuin", "uin", "ptui_loginuin", "luin", "pt2gguin", "superuin", "p_uin", "musicid", "userid", "user_id"} {
		value := qqAccountCookieValue(cookie, name)
		if value == "" {
			continue
		}
		return strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(value, "o"), "0"))
	}
	return ""
}

func qqAccountCookieValue(cookie, name string) string {
	for part := range strings.SplitSeq(cookie, ";") {
		key, value, ok := strings.Cut(part, "=")
		if ok && strings.TrimSpace(key) == name {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
