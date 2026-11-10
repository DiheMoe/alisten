package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"rsc.io/qr"

	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/protocol"
)

type unlimited struct{}

func (unlimited) Take(context.Context, string, int, time.Duration) (time.Duration, error) {
	return 0, nil
}

// 二维码的内容是网页上兑换配对码的地址，配对码放在片段里。
func TestPairingCodeQRImage(t *testing.T) {
	store := porttest.New()
	s := services(store, store, "")
	s.Accounts = identity.NewAccounts(store, store, unlimited{}, porttest.NewCodes(), porttest.NewCodes(), porttest.NewDevices(), s.Gateway)
	s.PublicURL = "https://alisten.example"
	server, _ := start(t, s)
	token := memberToken(t, server)
	auth := []string{"Authorization", "Bearer " + token}
	if got := send(t, server, http.MethodPost, "/api/account", `{"username":"alice","password":"correct horse"}`, auth...); got.status != http.StatusCreated {
		t.Fatalf("注册 = %#v", got)
	}
	got := send(t, server, http.MethodPost, "/api/account/pairing-codes", "{}", auth...)
	var created protocol.CreatePairingCodeResponse
	if err := json.Unmarshal([]byte(got.body), &created); err != nil || got.status != http.StatusCreated {
		t.Fatalf("生成配对码 = %#v", got)
	}
	code, err := qr.Encode("https://alisten.example/pair#"+created.Code, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	if want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()); created.QrImage != want {
		t.Errorf("二维码 = %.60q…，期望内容为 https://alisten.example/pair#%s 的 PNG", created.QrImage, created.Code)
	}
}
