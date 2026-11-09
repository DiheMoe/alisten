package playlist

import (
	"errors"
	"slices"
	"testing"

	"github.com/dihemoe/alisten/internal/domain/track"
)

func tr(id string) track.Track { return track.Track{Source: track.NetEase, ID: id} }

func trs(ids ...string) []track.Track {
	out := make([]track.Track, len(ids))
	for i, id := range ids {
		out[i] = tr(id)
	}
	return out
}

func TestMove(t *testing.T) {
	order := trs("a", "b", "c", "d", "e")
	at := func(id string) *track.Track { t := tr(id); return &t }
	tests := []struct {
		name    string
		moved   []track.Track
		before  *track.Track
		want    []track.Track
		wantErr error
	}{
		{"移到最前", trs("d"), at("a"), trs("d", "a", "b", "c", "e"), nil},
		{"移到最后", trs("b"), nil, trs("a", "c", "d", "e", "b"), nil},
		{"移到中间", trs("a"), at("d"), trs("b", "c", "a", "d", "e"), nil},
		{"多首保持给定先后", trs("e", "b"), at("d"), trs("a", "c", "e", "b", "d"), nil},
		{"多首移到最后保持给定先后", trs("c", "a"), nil, trs("b", "d", "e", "c", "a"), nil},
		{"忽略不存在的歌", trs("x", "c", "y"), at("a"), trs("c", "a", "b", "d", "e"), nil},
		{"重复的歌只算一次", trs("c", "c", "b"), nil, trs("a", "d", "e", "c", "b"), nil},
		{"before 紧跟在移动的歌之后", trs("b"), at("c"), order, nil},
		{"before 是被移动的歌", trs("b", "c"), at("c"), nil, ErrBeforeMoved},
		{"before 是不存在的被移动的歌", trs("x"), at("x"), nil, ErrBeforeMoved},
		{"before 不存在", trs("b"), at("x"), nil, ErrBeforeMissing},
		{"空移动", nil, at("c"), order, nil},
		{"移动的歌都不存在", trs("x"), at("y"), order, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(order)
			got, err := Move(order, tt.moved, tt.before)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v，期望 %v", err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Move = %v，期望 %v", got, tt.want)
			}
			if !slices.Equal(order, before) {
				t.Errorf("order 被修改：%v", order)
			}
		})
	}
}
