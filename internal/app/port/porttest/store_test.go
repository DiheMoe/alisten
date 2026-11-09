package porttest

import "testing"

func TestUpdateMemberRejectsTextPostgreSQLCannotStore(t *testing.T) {
	store := New()
	m, err := store.CreateMember(t.Context(), []byte("token"), "甲")
	if err != nil {
		t.Fatal(err)
	}
	bad := "a\x00@example.com"
	if _, err := store.UpdateMember(t.Context(), m.ID, nil, nil, &bad); err == nil {
		t.Error("含 NUL 的邮箱应报错")
	}
}
