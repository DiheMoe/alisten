package porttest

import (
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
)

type ContractStore interface {
	port.Members
	port.Playlists
	port.Listens
	port.ListenRecorder
}

// 每个子测试都会调用一次 open。open 返回的存储必须是空的，因为子测试之间会重复使用 alice 这样的 token 和用户名。
func RunStoreContract(t *testing.T, open func(t *testing.T) ContractStore) {
	runMembersContract(t, open)
	runPlaylistsContract(t, open)
	runListensContract(t, open)
}

// 不属于任何成员或歌单。写成 UUID 的格式，因为 PostgreSQL 遇到不是 UUID 的成员 ID 会报错，不会返回成员不存在。
const missingID = "01a15778-1911-7a28-8b2e-e08c2a25f388"
