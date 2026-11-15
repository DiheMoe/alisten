package prepare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

type publisher func(ctx context.Context, o port.PrepareOutcome) error

func (p publisher) PublishPrepared(ctx context.Context, o port.PrepareOutcome) error {
	return p(ctx, o)
}

func TestAnnounceOnlyLogsAFailedBroadcast(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil)))
	outcome := port.PrepareOutcome{Track: track.Track{Source: track.Link, ID: "https://example.com/a"}, JobID: 7}
	var published port.PrepareOutcome
	limited := false
	announce := Announce(publisher(func(ctx context.Context, o port.PrepareOutcome) error {
		published = o
		_, limited = ctx.Deadline()
		return errors.New("redis 不可用")
	}), logger)

	announce(outcome)

	if published != outcome {
		t.Errorf("广播的准备结果 = %+v，期望 %+v", published, outcome)
	}
	if !limited {
		t.Error("广播没有设期限")
	}
	var line struct {
		Level   string `json:"level"`
		JobID   int64  `json:"job_id"`
		TrackID string `json:"track_id"`
	}
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("日志不是一行 JSON：%v\n%s", err, logs.String())
	}
	if line.Level != "WARN" || line.JobID != outcome.JobID || line.TrackID != outcome.Track.ID {
		t.Errorf("日志 = %s，期望一条带 job_id 和 track_id 的 WARN", logs.String())
	}
}
