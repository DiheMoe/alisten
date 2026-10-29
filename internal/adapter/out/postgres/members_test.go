package postgres

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
)

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func createMember(t *testing.T, store *Store, token, name string) member.Member {
	t.Helper()
	m, err := store.CreateMember(t.Context(), tokenHash(token), name)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreateAndFindMember(t *testing.T) {
	store, _ := newStore(t)
	m := createMember(t, store, "alice", "游客-1a2b")
	if !isUUID(m.ID) || m.ID[14] != '7' {
		t.Errorf("成员 ID %q 应是数据库生成的 UUIDv7", m.ID)
	}
	if want := (member.Member{ID: m.ID, Name: "游客-1a2b"}); m != want {
		t.Errorf("新成员 = %#v，期望 %#v", m, want)
	}

	got, err := store.MemberByToken(t.Context(), tokenHash("alice"))
	if err != nil || got != m {
		t.Errorf("MemberByToken = %#v, %v，期望 %#v", got, err, m)
	}
	if _, err := store.MemberByToken(t.Context(), tokenHash("bob")); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("未知 token 的错误 = %v，期望 ErrMemberNotFound", err)
	}
	if _, err := store.CreateMember(t.Context(), tokenHash("alice"), "重复"); err == nil {
		t.Error("token 哈希重复时应失败")
	}
}

func TestMemberAvatarIsNullWhenUnset(t *testing.T) {
	store, pool := newStore(t)
	m := createMember(t, store, "alice", "游客-1a2b")
	avatar := func() string {
		t.Helper()
		var hash string
		if err := pool.QueryRow(t.Context(), "SELECT coalesce(avatar_hash, 'NULL') FROM members WHERE id = $1", m.ID).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	if hash := avatar(); hash != "NULL" {
		t.Errorf("新成员的 avatar_hash = %q，期望 NULL", hash)
	}
	if _, err := store.UpdateMember(t.Context(), m.ID, nil, ptr("0bc83cb571cd1c50ba6f3e8a78ef1346")); err != nil {
		t.Fatal(err)
	}
	if hash := avatar(); hash != "0bc83cb571cd1c50ba6f3e8a78ef1346" {
		t.Errorf("设置头像之后 avatar_hash = %q", hash)
	}
	got, err := store.UpdateMember(t.Context(), m.ID, nil, ptr(""))
	if err != nil {
		t.Fatal(err)
	}
	if hash := avatar(); hash != "NULL" || got.AvatarHash != "" {
		t.Errorf("清除头像之后 avatar_hash = %q、成员的头像哈希 = %q，期望 NULL 和空串", hash, got.AvatarHash)
	}
}

func TestUpdateMemberKeepsOmittedFields(t *testing.T) {
	store, _ := newStore(t)
	m := createMember(t, store, "alice", "游客-1a2b")

	got, err := store.UpdateMember(t.Context(), m.ID, ptr("甲"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (member.Member{ID: m.ID, Name: "甲"}); got != want {
		t.Errorf("只改昵称 = %#v，期望 %#v", got, want)
	}
	got, err = store.UpdateMember(t.Context(), m.ID, nil, ptr("0bc83cb571cd1c50ba6f3e8a78ef1346"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (member.Member{ID: m.ID, Name: "甲", AvatarHash: "0bc83cb571cd1c50ba6f3e8a78ef1346"}); got != want {
		t.Errorf("只改头像 = %#v，期望 %#v", got, want)
	}
	if found, _ := store.MemberByToken(t.Context(), tokenHash("alice")); found != got {
		t.Errorf("修改后按 token 查到 %#v，期望 %#v", found, got)
	}

	if _, err := store.UpdateMember(t.Context(), "01a15778-1911-7a28-8b2e-e08c2a25f388", ptr("乙"), nil); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("修改不存在的成员，错误 = %v，期望 ErrMemberNotFound", err)
	}
}
