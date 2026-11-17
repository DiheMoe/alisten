package porttest

import (
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
)

func newRoom(t *testing.T, s ContractStore, r port.NewRoom) string {
	t.Helper()
	id, err := s.CreateRoom(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func capacityOf(t *testing.T, s ContractStore, ref string) int {
	t.Helper()
	r, err := s.FindRoom(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return r.Info.Capacity
}

func ephemeralCapacitySetting(t *testing.T, s ContractStore) int {
	t.Helper()
	settings, err := s.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return settings.EphemeralRoomCapacity
}

func runRoomsContract(t *testing.T, open func(t *testing.T) ContractStore) {
	t.Run("房间带着创建时的人数上限，落盘时写回", func(t *testing.T) {
		s := open(t)
		lobby := newRoom(t, s, port.NewRoom{Kind: room.Ephemeral, Name: "临时", Capacity: 16})
		hall := newRoom(t, s, port.NewRoom{Kind: room.Persistent, Name: "大厅", Slug: "hall", Capacity: 64})

		rooms, err := s.ListRooms(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		listed := map[string]int{}
		for _, r := range rooms {
			listed[r.Info.ID] = r.Info.Capacity
		}
		if listed[lobby] != 16 || listed[hall] != 64 {
			t.Errorf("房间列表里的人数上限 = %v，期望 %s 为 16，%s 为 64", listed, lobby, hall)
		}
		for ref, want := range map[string]int{lobby: 16, hall: 64, "hall": 64} {
			if got := capacityOf(t, s, ref); got != want {
				t.Errorf("FindRoom(%q) 的人数上限 = %d，期望 %d", ref, got, want)
			}
		}

		term, snap, err := s.TakeRoom(t.Context(), hall)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Info.Capacity != 64 {
			t.Errorf("接管时快照的人数上限 = %d，期望 64", snap.Info.Capacity)
		}
		snap.Info.Capacity = 100
		if err := s.SaveRoom(t.Context(), term, snap, nil); err != nil {
			t.Fatal(err)
		}
		if got := capacityOf(t, s, hall); got != 100 {
			t.Errorf("落盘之后 FindRoom 的人数上限 = %d，期望 100", got)
		}
		if _, snap, err := s.TakeRoom(t.Context(), hall); err != nil || snap.Info.Capacity != 100 {
			t.Errorf("落盘之后再次接管 = %#v, %v，期望人数上限 100", snap.Info, err)
		}
	})

	t.Run("房间的人数上限只能在 2 到 128 之间", func(t *testing.T) {
		s := open(t)
		for _, capacity := range []int{2, 128} {
			if _, err := s.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "大厅", Capacity: capacity}); err != nil {
				t.Errorf("人数上限 %d 的房间创建失败：%v", capacity, err)
			}
		}
		for _, capacity := range []int{1, 129} {
			if _, err := s.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "大厅", Capacity: capacity}); err == nil {
				t.Errorf("人数上限 %d 的房间应创建失败", capacity)
			}
		}

		id := newRoom(t, s, port.NewRoom{Kind: room.Persistent, Name: "大厅", Capacity: 64})
		term, snap, err := s.TakeRoom(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, capacity := range []int{1, 129} {
			snap.Info.Capacity = capacity
			if err := s.SaveRoom(t.Context(), term, snap, nil); err == nil {
				t.Errorf("人数上限 %d 的快照应落盘失败", capacity)
			}
		}
		if got := capacityOf(t, s, id); got != 64 {
			t.Errorf("落盘失败之后的人数上限 = %d，期望仍是 64", got)
		}
	})

	t.Run("临时房间最多人数没有设置过时为 0，只能设成 2 到 128", func(t *testing.T) {
		s := open(t)
		if got := ephemeralCapacitySetting(t, s); got != 0 {
			t.Errorf("没有设置过时 = %d，期望 0", got)
		}
		for _, capacity := range []int{2, 128, 32} {
			if err := s.SetEphemeralRoomCapacity(t.Context(), capacity); err != nil {
				t.Fatal(err)
			}
			if got := ephemeralCapacitySetting(t, s); got != capacity {
				t.Errorf("设成 %d 之后 = %d", capacity, got)
			}
		}
		for _, capacity := range []int{1, 129} {
			if err := s.SetEphemeralRoomCapacity(t.Context(), capacity); err == nil {
				t.Errorf("设成 %d 应失败", capacity)
			}
		}
		if got := ephemeralCapacitySetting(t, s); got != 32 {
			t.Errorf("设置失败之后 = %d，期望仍是 32", got)
		}
	})
}
