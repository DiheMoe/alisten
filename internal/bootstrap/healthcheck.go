package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const healthcheckTimeout = 2 * time.Second

func runHealthcheck(*slog.Logger) error {
	opsListen, err := loadOpsListen()
	if err != nil {
		return err
	}
	url := "http://" + loopbackAddr(opsListen) + "/readyz"

	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("请求 %s：%w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回 %d", url, resp.StatusCode)
	}
	return nil
}

func loopbackAddr(listen string) string {
	host, port, _ := net.SplitHostPort(listen)
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return net.JoinHostPort(host, port)
}
