package media

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var (
	link     = track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}
	netease  = track.Track{Source: track.NetEase, ID: "186016"}
	bilibili = track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD"}
	ready    = track.Prepared{Duration: time.Minute, Gain: 1}
)

type tracksTable map[track.Track]port.PreparedTrack

func (t tracksTable) PreparedTrack(_ context.Context, tr track.Track) (port.PreparedTrack, bool, error) {
	p, ok := t[tr]
	return p, ok, nil
}

func (tracksTable) SavePreparedTrack(context.Context, track.Track, port.PreparedTrack) error {
	return errors.New("不支持写入")
}

func putAudio(t *testing.T, store *porttest.MediaStore, tr track.Track) {
	t.Helper()
	if err := store.Put(t.Context(), AudioObject(Key(tr.ID)), strings.NewReader("mp3"), "audio/mpeg"); err != nil {
		t.Fatal(err)
	}
}

func TestTracksRequireLinkAudio(t *testing.T) {
	store := porttest.NewMediaStore()
	linkResult := port.PreparedTrack{Meta: track.Meta{Title: "晴天"}, Result: ready, MediaKey: Key(link.ID), MediaSize: 3}
	tracks := NewTracks(tracksTable{link: linkResult, netease: {Result: ready}}, store)

	if _, ok, err := tracks.PreparedTrack(t.Context(), link); err != nil || ok {
		t.Errorf("音频不在对象存储中时 = %v, %v，期望没有结果", ok, err)
	}
	putAudio(t, store, link)
	if got, ok, err := tracks.PreparedTrack(t.Context(), link); err != nil || !ok || got != linkResult {
		t.Errorf("音频在对象存储中时 = %+v, %v, %v", got, ok, err)
	}
	store.Fail(errors.New("连接被拒绝"))
	if _, ok, err := tracks.PreparedTrack(t.Context(), link); err == nil || ok {
		t.Errorf("对象存储出错时 = %v, %v，期望返回错误，由调用方重试", ok, err)
	}
	if _, ok, err := tracks.PreparedTrack(t.Context(), netease); err != nil || !ok {
		t.Errorf("网易云的结果 = %v, %v，期望不检查对象存储", ok, err)
	}
}

func TestTracksRequireBilibiliAudio(t *testing.T) {
	store := porttest.NewMediaStore()
	result := port.PreparedTrack{Meta: track.Meta{Title: "晴天（钢琴版）"}, Result: ready, MediaKey: Key(bilibili.ID), MediaSize: 3}
	tracks := NewTracks(tracksTable{bilibili: result}, store)
	if _, ok, err := tracks.PreparedTrack(t.Context(), bilibili); err != nil || ok {
		t.Errorf("音频不在对象存储中时 = %v, %v，期望没有结果", ok, err)
	}
	putAudio(t, store, bilibili)
	if got, ok, err := tracks.PreparedTrack(t.Context(), bilibili); err != nil || !ok || got != result {
		t.Errorf("音频在对象存储中时 = %+v, %v, %v", got, ok, err)
	}
}

type snapshots struct {
	port.RoomSnapshots
	snap room.Snapshot
}

func (s snapshots) TakeRoom(context.Context, string) (uint32, room.Snapshot, error) {
	return 3, s.snap, nil
}

func readyEntry(id room.EntryID, tr track.Track) room.Entry {
	return room.Entry{ID: id, Track: tr, Meta: track.Meta{Title: tr.ID}, Ready: true, Prep: ready}
}

func restoredSnapshot() room.Snapshot {
	other := track.Track{Source: track.Link, ID: "https://example.com/b.mp3"}
	pending := readyEntry(2, link)
	return room.Snapshot{
		Current: &room.NowPlaying{Entry: readyEntry(1, other)},
		Pending: &pending,
		Waiting: []room.Entry{readyEntry(3, other), readyEntry(4, netease), {ID: 5, Track: link}, readyEntry(6, bilibili)},
	}
}

func TestSnapshotsUnreadyStoredTracksWithoutAudio(t *testing.T) {
	store := porttest.NewMediaStore()
	putAudio(t, store, track.Track{Source: track.Link, ID: "https://example.com/b.mp3"})
	term, snap, err := NewSnapshots(snapshots{snap: restoredSnapshot()}, store, slog.New(slog.DiscardHandler)).TakeRoom(t.Context(), "room")
	if err != nil || term != 3 {
		t.Fatal(term, err)
	}
	if !snap.Current.Entry.Ready {
		t.Error("正在播放的歌不应改回准备中")
	}
	if snap.Pending.Ready || snap.Pending.Prep != (track.Prepared{}) {
		t.Errorf("音频已不在对象存储中的待播 = %+v，期望改回准备中", *snap.Pending)
	}
	for i, want := range []bool{true, true, false, false} {
		if got := snap.Waiting[i].Ready; got != want {
			t.Errorf("等待队列第 %d 首 Ready = %v，期望 %v", i+1, got, want)
		}
	}
}

func TestSnapshotsKeepEntriesWhenStorageFails(t *testing.T) {
	store := porttest.NewMediaStore()
	store.Fail(errors.New("连接被拒绝"))
	var logs bytes.Buffer
	_, snap, err := NewSnapshots(snapshots{snap: restoredSnapshot()}, store, slog.New(slog.NewJSONHandler(&logs, nil))).TakeRoom(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Pending.Ready || !snap.Waiting[0].Ready {
		t.Error("对象存储出错时，条目应按曲目表恢复")
	}
	if n := strings.Count(logs.String(), "检查曲目的音频失败"); n != 1 {
		t.Errorf("日志中记录了 %d 次失败，期望出错后不再检查其余条目：\n%s", n, logs.String())
	}
}

type stalledStore struct{ port.MediaStore }

func (stalledStore) Exists(ctx context.Context, _ string) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

func TestSnapshotsStopCheckingAfterBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var logs bytes.Buffer
		type taken struct {
			snap room.Snapshot
			err  error
		}
		done := make(chan taken, 1)
		go func() {
			_, snap, err := NewSnapshots(snapshots{snap: restoredSnapshot()}, stalledStore{}, slog.New(slog.NewJSONHandler(&logs, nil))).TakeRoom(ctx, "room")
			done <- taken{snap, err}
		}()

		time.Sleep(restoreCheckTimeout - time.Nanosecond)
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("检查在预算用完之前就结束了")
		default:
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		select {
		case got := <-done:
			if got.err != nil || !got.snap.Pending.Ready || !got.snap.Waiting[0].Ready {
				t.Errorf("预算用完之后 = %+v, %v，期望其余条目按曲目表恢复", got.snap, got.err)
			}
			if !strings.Contains(logs.String(), "检查曲目的音频失败") {
				t.Errorf("日志中没有检查失败的 WARN：%s", logs.String())
			}
		default:
			t.Fatalf("预算 %v 用完之后检查没有结束", restoreCheckTimeout)
		}
	})
}
