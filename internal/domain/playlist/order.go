package playlist

import (
	"errors"
	"slices"

	"github.com/dihemoe/alisten/internal/domain/track"
)

var (
	ErrBeforeMoved   = errors.New("before 不能是要移动的歌")
	ErrBeforeMissing = errors.New("歌单已经变了，刷新之后再试")
)

// order 是显示顺序；返回移动后的显示顺序。moved 里不在 order 中的歌忽略，重复的只算一次；
// 都不在 order 中时原样返回，此时不检查 before 是否还在。
func Move(order []track.Track, moved []track.Track, before *track.Track) ([]track.Track, error) {
	if before != nil && slices.Contains(moved, *before) {
		return nil, ErrBeforeMoved
	}
	var block []track.Track
	for _, t := range moved {
		if slices.Contains(order, t) && !slices.Contains(block, t) {
			block = append(block, t)
		}
	}
	if len(block) == 0 {
		return slices.Clone(order), nil
	}
	rest := make([]track.Track, 0, len(order)-len(block))
	for _, t := range order {
		if !slices.Contains(block, t) {
			rest = append(rest, t)
		}
	}
	at := len(rest)
	if before != nil {
		at = slices.Index(rest, *before)
		if at < 0 {
			return nil, ErrBeforeMissing
		}
	}
	return slices.Concat(rest[:at], block, rest[at:]), nil
}
