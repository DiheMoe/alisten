package protocol

import (
	"strings"
	"testing"
)

// B 站曲目 ID 的格式与 internal/domain/track 的规则一致：BV 号，或从第 2 P 起的 BV 号/分P序号。
func TestBilibiliTrackIDs(t *testing.T) {
	valid := []string{"BV1xx411c7mD", "BV1xx411c7mD/2", "BV1xx411c7mD/9999"}
	invalid := []string{"BV1xx411c7mD/1", "BV1xx411c7mD/02", "av170001", "BV1xx411c7mD/extra", ""}
	for _, id := range valid {
		if err := decodeData("queue.add", `{"track":{"source":"bilibili","id":"`+id+`"}}`); err != nil {
			t.Errorf("合法 ID %q：%v", id, err)
		}
	}
	for _, id := range invalid {
		err := decodeData("queue.add", `{"track":{"source":"bilibili","id":"`+id+`"}}`)
		if got := errText(err); !strings.Contains(got, "data.track.id") {
			t.Errorf("非法 ID %q：错误 = %q，应指出 data.track.id", id, got)
		}
	}
}
