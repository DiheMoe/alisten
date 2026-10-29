package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// testdata/Link.json 的用例也由网页生成的测试（protocol.gen.test.ts）使用，两边对每个用例的判断必须相同。
func TestLinkCases(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "Link.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Value string
		Valid bool
		Note  string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		frame, err := json.Marshal(map[string]any{"kind": "request", "type": RequestQueueAdd, "id": "a1", "data": map[string]any{"track": map[string]string{"source": "link", "id": c.Value}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeRequest(frame); (err == nil) != c.Valid {
			t.Errorf("%s：%q 的解码错误为 %v，期望接受：%v", c.Note, c.Value, err, c.Valid)
		}
	}
}
