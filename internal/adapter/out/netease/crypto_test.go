package netease

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"testing"
)

func decryptCBC(t *testing.T, text, key string) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, []byte(weapiIV)).CryptBlocks(out, data)
	return string(out[:len(out)-int(out[len(out)-1])])
}

func TestWeapiParams(t *testing.T) {
	params, encSecKey := weapiParams([]byte(`{"id":"186016"}`), "abcdefghijklmnop")
	if got := decryptCBC(t, decryptCBC(t, params, "abcdefghijklmnop"), weapiNonce); got != `{"id":"186016"}` {
		t.Errorf("两层解密之后 = %q", got)
	}
	if !regexp.MustCompile(`^[0-9a-f]{256}$`).MatchString(encSecKey) {
		t.Errorf("encSecKey = %q，期望 256 位十六进制", encSecKey)
	}
	if key := newSecretKey(); !regexp.MustCompile(`^[0-9A-Za-z]{16}$`).MatchString(key) {
		t.Errorf("随机密钥 = %q", key)
	}
}

func TestEapiParams(t *testing.T) {
	plain := decryptECB(t, eapiParams("/eapi/song/enhance/player/url/v1", []byte(`{"ids":[1]}`)), eapiKey)
	digest := md5.Sum([]byte(`nobody/api/song/enhance/player/url/v1use{"ids":[1]}md5forencrypt`))
	if want := `/api/song/enhance/player/url/v1-36cd479b6b5-{"ids":[1]}-36cd479b6b5-` + hex.EncodeToString(digest[:]); plain != want {
		t.Errorf("eapi 参数解密之后 = %q\n期望 %q", plain, want)
	}
}

func TestLinuxParams(t *testing.T) {
	params := linuxParams([]byte(`{"method":"POST"}`))
	if !regexp.MustCompile(`^[0-9A-F]+$`).MatchString(params) {
		t.Errorf("linuxapi 参数 = %q，期望大写十六进制", params)
	}
	if got := decryptECB(t, params, linuxKey); got != `{"method":"POST"}` {
		t.Errorf("linuxapi 参数解密之后 = %q", got)
	}
}
