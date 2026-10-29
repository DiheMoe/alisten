package netease

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand/v2"
	"slices"
	"strings"
)

const (
	linuxKey      = "rFgB&h#%2?^eDg:Q"
	eapiKey       = "e82ckenh8dichen8"
	weapiNonce    = "0CoJUm6Qyw8W8jud"
	weapiIV       = "0102030405060708"
	weapiExponent = "010001"
	weapiModulus  = "00e0b509f6259df8642dbc35662901477df22677ec152b5ff68ace615bb7b725152b3ab17a876aea8a5aa76d2e417629ec4ee341f56135fccf695280104e0312ecbda92557c93870114af6c9d05c4f7f0c3685b7a46bee255932575cce10b424d813cfe4875d3e82047b97ddef52741d546b8e289dc6935b3ece0462db0a22b8e7"
)

func linuxParams(data []byte) string {
	return strings.ToUpper(hex.EncodeToString(ecb(data, linuxKey)))
}

func eapiParams(path string, payload []byte) string {
	path = strings.Replace(path, "/eapi/", "/api/", 1)
	digest := md5.Sum(fmt.Appendf(nil, "nobody%suse%smd5forencrypt", path, payload))
	data := fmt.Appendf(nil, "%s-36cd479b6b5-%s-36cd479b6b5-%s", path, payload, hex.EncodeToString(digest[:]))
	return hex.EncodeToString(ecb(data, eapiKey))
}

func weapiParams(data []byte, secretKey string) (params, encSecKey string) {
	params = cbc([]byte(cbc(data, weapiNonce)), secretKey)
	reversed := []byte(secretKey)
	slices.Reverse(reversed)
	text := new(big.Int).SetBytes(reversed)
	exponent, _ := new(big.Int).SetString(weapiExponent, 16)
	modulus, _ := new(big.Int).SetString(weapiModulus, 16)
	return params, fmt.Sprintf("%0256x", text.Exp(text, exponent, modulus))
}

func newSecretKey() string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	key := make([]byte, 16)
	for i := range key {
		key[i] = letters[rand.IntN(len(letters))]
	}
	return string(key)
}

func ecb(data []byte, key string) []byte {
	block, _ := aes.NewCipher([]byte(key))
	data = pad(data)
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Encrypt(out[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return out
}

func cbc(data []byte, key string) string {
	block, _ := aes.NewCipher([]byte(key))
	data = pad(data)
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, []byte(weapiIV)).CryptBlocks(out, data)
	return base64.StdEncoding.EncodeToString(out)
}

func pad(data []byte) []byte {
	n := aes.BlockSize - len(data)%aes.BlockSize
	return append(slices.Clip(data), bytes.Repeat([]byte{byte(n)}, n)...)
}
