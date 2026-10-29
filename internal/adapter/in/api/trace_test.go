package api

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/app/port/porttest"
)

func TestWebSocketRequestSpans(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		roomID, _, ticket := f.enter(t, "")
		conn := f.join(t, roomID, ticket)

		writeFrame(t, conn, `{"kind":"request","type":"ping","id":"p1","data":{"clientTimeMs":1}}`)
		readMessage(t, conn)
		writeFrame(t, conn, `[]`)
		readMessage(t, conn)
		writeFrame(t, conn, `{"kind":"request","type":"chat.send","id":"c1","data":{"content":"你好"}}`)
		readMessage(t, conn)
		readMessage(t, conn)
		writeFrame(t, conn, `{"kind":"request","type":"queue.add","id":"a1","data":{"source":"qq","keyword":"晴天"}}`)
		if m := readMessage(t, conn); m["error"].(map[string]any)["code"] != "PROVIDER_ERROR" {
			t.Fatalf("按关键词点歌的回复 = %v，期望 PROVIDER_ERROR", m)
		}
		synctest.Wait()

		var got []string
		traces := map[trace.TraceID]bool{}
		for _, span := range f.spans.Ended() {
			if span.SpanKind() != trace.SpanKindServer || span.Parent().IsValid() {
				t.Errorf("%s 的类型 = %v、父 span = %v，期望没有父 span 的 server span", span.Name(), span.SpanKind(), span.Parent())
			}
			traces[span.SpanContext().TraceID()] = true
			got = append(got, span.Name()+" "+span.Status().Code.String())
			if !slices.Contains(span.Attributes(), attribute.String("alisten.room_id", roomID)) {
				t.Errorf("%s 的属性 = %v，期望带着房间 ID", span.Name(), span.Attributes())
			}
		}
		slices.Sort(got)
		if want := []string{"WS chat.send Unset", "WS hello Unset", "WS queue.add Error"}; !slices.Equal(got, want) {
			t.Errorf("span = %s，期望 %s", strings.Join(got, "、"), strings.Join(want, "、"))
		}
		if len(traces) != len(got) {
			t.Errorf("%d 个 span 只在 %d 条 trace 中，期望各是一条 trace", len(got), len(traces))
		}
	})
}
