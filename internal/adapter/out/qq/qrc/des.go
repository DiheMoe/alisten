// Package qrc 解密并解析 QQ 音乐的 QRC 歌词：密文是十六进制字符串，
// 加密为 QQ 改版 DES 的三轮 EDE，解压为 zlib。改版 DES 与标准 DES 不兼容：
// 32 位字内字节小端、两个 S 盒条目被改动、PC-2 的 D 半区错位一位。
package qrc

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

var qrcKey = []byte("!@#)(*$%123ZXC!@!@#)(NHL")

// DecryptHex 解密十六进制的 QRC 密文。
func DecryptHex(content string) (string, error) {
	encrypted, err := hex.DecodeString(strings.TrimSpace(content))
	if err != nil {
		return "", fmt.Errorf("QRC 密文不是十六进制：%w", err)
	}
	if len(encrypted)%8 != 0 {
		return "", fmt.Errorf("QRC 密文长度 %d 不是 8 的倍数", len(encrypted))
	}
	plain := make([]byte, len(encrypted))
	for i := 0; i < len(encrypted); i += 8 {
		copy(plain[i:i+8], tripleDESDecrypt(encrypted[i:i+8]))
	}
	r, err := zlib.NewReader(bytes.NewReader(plain))
	if err != nil {
		return "", fmt.Errorf("QRC 解压：%w", err)
	}
	text, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("QRC 解压：%w", err)
	}
	return strings.TrimSpace(string(text)), nil
}

const (
	modeDecrypt = 0
	modeEncrypt = 1
)

func tripleDESDecrypt(block []byte) []byte {
	keys := [3][16][6]byte{
		keySchedule(qrcKey[16:], modeDecrypt),
		keySchedule(qrcKey[8:], modeEncrypt),
		keySchedule(qrcKey[0:], modeDecrypt),
	}
	data := append([]byte(nil), block...)
	for i := 0; i < 3; i++ {
		data = desCrypt(data, keys[i])
	}
	return data
}

// bitNum 取输入中第 b 位（逻辑序号），放到结果的 c 位上；逻辑字节 n 在物理字节 (n/4)*4+3-n%4。
func bitNum(a []byte, b, c int) uint32 {
	return uint32((a[(b/32)*4+3-(b%32)/8]>>(7-b%8))&1) << c
}

func bitNumIntr(a uint32, b, c int) uint32 {
	return ((a >> (31 - b)) & 1) << c
}

func bitNumIntl(a uint32, b, c int) uint32 {
	return ((a << b) & 0x80000000) >> c
}

func sBoxBit(a byte) byte {
	return (a & 32) | ((a & 31) >> 1) | ((a & 1) << 4)
}

func initialPermutation(input []byte) (uint32, uint32) {
	return bitNum(input, 57, 31) | bitNum(input, 49, 30) | bitNum(input, 41, 29) | bitNum(input, 33, 28) |
			bitNum(input, 25, 27) | bitNum(input, 17, 26) | bitNum(input, 9, 25) | bitNum(input, 1, 24) |
			bitNum(input, 59, 23) | bitNum(input, 51, 22) | bitNum(input, 43, 21) | bitNum(input, 35, 20) |
			bitNum(input, 27, 19) | bitNum(input, 19, 18) | bitNum(input, 11, 17) | bitNum(input, 3, 16) |
			bitNum(input, 61, 15) | bitNum(input, 53, 14) | bitNum(input, 45, 13) | bitNum(input, 37, 12) |
			bitNum(input, 29, 11) | bitNum(input, 21, 10) | bitNum(input, 13, 9) | bitNum(input, 5, 8) |
			bitNum(input, 63, 7) | bitNum(input, 55, 6) | bitNum(input, 47, 5) | bitNum(input, 39, 4) |
			bitNum(input, 31, 3) | bitNum(input, 23, 2) | bitNum(input, 15, 1) | bitNum(input, 7, 0),
		bitNum(input, 56, 31) | bitNum(input, 48, 30) | bitNum(input, 40, 29) | bitNum(input, 32, 28) |
			bitNum(input, 24, 27) | bitNum(input, 16, 26) | bitNum(input, 8, 25) | bitNum(input, 0, 24) |
			bitNum(input, 58, 23) | bitNum(input, 50, 22) | bitNum(input, 42, 21) | bitNum(input, 34, 20) |
			bitNum(input, 26, 19) | bitNum(input, 18, 18) | bitNum(input, 10, 17) | bitNum(input, 2, 16) |
			bitNum(input, 60, 15) | bitNum(input, 52, 14) | bitNum(input, 44, 13) | bitNum(input, 36, 12) |
			bitNum(input, 28, 11) | bitNum(input, 20, 10) | bitNum(input, 12, 9) | bitNum(input, 4, 8) |
			bitNum(input, 62, 7) | bitNum(input, 54, 6) | bitNum(input, 46, 5) | bitNum(input, 38, 4) |
			bitNum(input, 30, 3) | bitNum(input, 22, 2) | bitNum(input, 14, 1) | bitNum(input, 6, 0)
}

func inversePermutation(s0, s1 uint32) []byte {
	data := make([]byte, 8)
	data[3] = byte(bitNumIntr(s1, 7, 7) | bitNumIntr(s0, 7, 6) | bitNumIntr(s1, 15, 5) | bitNumIntr(s0, 15, 4) | bitNumIntr(s1, 23, 3) | bitNumIntr(s0, 23, 2) | bitNumIntr(s1, 31, 1) | bitNumIntr(s0, 31, 0))
	data[2] = byte(bitNumIntr(s1, 6, 7) | bitNumIntr(s0, 6, 6) | bitNumIntr(s1, 14, 5) | bitNumIntr(s0, 14, 4) | bitNumIntr(s1, 22, 3) | bitNumIntr(s0, 22, 2) | bitNumIntr(s1, 30, 1) | bitNumIntr(s0, 30, 0))
	data[1] = byte(bitNumIntr(s1, 5, 7) | bitNumIntr(s0, 5, 6) | bitNumIntr(s1, 13, 5) | bitNumIntr(s0, 13, 4) | bitNumIntr(s1, 21, 3) | bitNumIntr(s0, 21, 2) | bitNumIntr(s1, 29, 1) | bitNumIntr(s0, 29, 0))
	data[0] = byte(bitNumIntr(s1, 4, 7) | bitNumIntr(s0, 4, 6) | bitNumIntr(s1, 12, 5) | bitNumIntr(s0, 12, 4) | bitNumIntr(s1, 20, 3) | bitNumIntr(s0, 20, 2) | bitNumIntr(s1, 28, 1) | bitNumIntr(s0, 28, 0))
	data[7] = byte(bitNumIntr(s1, 3, 7) | bitNumIntr(s0, 3, 6) | bitNumIntr(s1, 11, 5) | bitNumIntr(s0, 11, 4) | bitNumIntr(s1, 19, 3) | bitNumIntr(s0, 19, 2) | bitNumIntr(s1, 27, 1) | bitNumIntr(s0, 27, 0))
	data[6] = byte(bitNumIntr(s1, 2, 7) | bitNumIntr(s0, 2, 6) | bitNumIntr(s1, 10, 5) | bitNumIntr(s0, 10, 4) | bitNumIntr(s1, 18, 3) | bitNumIntr(s0, 18, 2) | bitNumIntr(s1, 26, 1) | bitNumIntr(s0, 26, 0))
	data[5] = byte(bitNumIntr(s1, 1, 7) | bitNumIntr(s0, 1, 6) | bitNumIntr(s1, 9, 5) | bitNumIntr(s0, 9, 4) | bitNumIntr(s1, 17, 3) | bitNumIntr(s0, 17, 2) | bitNumIntr(s1, 25, 1) | bitNumIntr(s0, 25, 0))
	data[4] = byte(bitNumIntr(s1, 0, 7) | bitNumIntr(s0, 0, 6) | bitNumIntr(s1, 8, 5) | bitNumIntr(s0, 8, 4) | bitNumIntr(s1, 16, 3) | bitNumIntr(s0, 16, 2) | bitNumIntr(s1, 24, 1) | bitNumIntr(s0, 24, 0))
	return data
}

func feistel(state uint32, key [6]byte) uint32 {
	t1 := bitNumIntl(state, 31, 0) | ((state & 0xf0000000) >> 1) | bitNumIntl(state, 4, 5) |
		bitNumIntl(state, 3, 6) | ((state & 0x0f000000) >> 3) | bitNumIntl(state, 8, 11) |
		bitNumIntl(state, 7, 12) | ((state & 0x00f00000) >> 5) | bitNumIntl(state, 12, 17) |
		bitNumIntl(state, 11, 18) | ((state & 0x000f0000) >> 7) | bitNumIntl(state, 16, 23)
	t2 := bitNumIntl(state, 15, 0) | ((state & 0x0000f000) << 15) | bitNumIntl(state, 20, 5) |
		bitNumIntl(state, 19, 6) | ((state & 0x00000f00) << 13) | bitNumIntl(state, 24, 11) |
		bitNumIntl(state, 23, 12) | ((state & 0x000000f0) << 11) | bitNumIntl(state, 28, 17) |
		bitNumIntl(state, 27, 18) | ((state & 0x0000000f) << 9) | bitNumIntl(state, 0, 23)
	lrg := [6]byte{byte(t1 >> 24), byte(t1 >> 16), byte(t1 >> 8), byte(t2 >> 24), byte(t2 >> 16), byte(t2 >> 8)}
	for i := range lrg {
		lrg[i] ^= key[i]
	}
	state = uint32(sBox[0][sBoxBit(lrg[0]>>2)])<<28 |
		uint32(sBox[1][sBoxBit(((lrg[0]&0x03)<<4)|(lrg[1]>>4))])<<24 |
		uint32(sBox[2][sBoxBit(((lrg[1]&0x0f)<<2)|(lrg[2]>>6))])<<20 |
		uint32(sBox[3][sBoxBit(lrg[2]&0x3f)])<<16 |
		uint32(sBox[4][sBoxBit(lrg[3]>>2)])<<12 |
		uint32(sBox[5][sBoxBit(((lrg[3]&0x03)<<4)|(lrg[4]>>4))])<<8 |
		uint32(sBox[6][sBoxBit(((lrg[4]&0x0f)<<2)|(lrg[5]>>6))])<<4 |
		uint32(sBox[7][sBoxBit(lrg[5]&0x3f)])
	return bitNumIntl(state, 15, 0) | bitNumIntl(state, 6, 1) | bitNumIntl(state, 19, 2) |
		bitNumIntl(state, 20, 3) | bitNumIntl(state, 28, 4) | bitNumIntl(state, 11, 5) |
		bitNumIntl(state, 27, 6) | bitNumIntl(state, 16, 7) | bitNumIntl(state, 0, 8) |
		bitNumIntl(state, 14, 9) | bitNumIntl(state, 22, 10) | bitNumIntl(state, 25, 11) |
		bitNumIntl(state, 4, 12) | bitNumIntl(state, 17, 13) | bitNumIntl(state, 30, 14) |
		bitNumIntl(state, 9, 15) | bitNumIntl(state, 1, 16) | bitNumIntl(state, 7, 17) |
		bitNumIntl(state, 23, 18) | bitNumIntl(state, 13, 19) | bitNumIntl(state, 31, 20) |
		bitNumIntl(state, 26, 21) | bitNumIntl(state, 2, 22) | bitNumIntl(state, 8, 23) |
		bitNumIntl(state, 18, 24) | bitNumIntl(state, 12, 25) | bitNumIntl(state, 29, 26) |
		bitNumIntl(state, 5, 27) | bitNumIntl(state, 21, 28) | bitNumIntl(state, 10, 29) |
		bitNumIntl(state, 3, 30) | bitNumIntl(state, 24, 31)
}

func desCrypt(input []byte, key [16][6]byte) []byte {
	s0, s1 := initialPermutation(input)
	for i := 0; i < 15; i++ {
		prev := s1
		s1 = feistel(s1, key[i]) ^ s0
		s0 = prev
	}
	s0 = feistel(s1, key[15]) ^ s0
	return inversePermutation(s0, s1)
}

func keySchedule(key []byte, mode int) [16][6]byte {
	var schedule [16][6]byte
	roundShift := [16]int{1, 1, 2, 2, 2, 2, 2, 2, 1, 2, 2, 2, 2, 2, 2, 1}
	permC := [28]int{56, 48, 40, 32, 24, 16, 8, 0, 57, 49, 41, 33, 25, 17, 9, 1, 58, 50, 42, 34, 26, 18, 10, 2, 59, 51, 43, 35}
	permD := [28]int{62, 54, 46, 38, 30, 22, 14, 6, 61, 53, 45, 37, 29, 21, 13, 5, 60, 52, 44, 36, 28, 20, 12, 4, 27, 19, 11, 3}
	compression := [48]int{13, 16, 10, 23, 0, 4, 2, 27, 14, 5, 20, 9, 22, 18, 11, 3, 25, 7, 15, 6, 26, 19, 12, 1, 40, 51, 30, 36, 46, 54, 29, 39, 50, 44, 32, 47, 43, 48, 38, 55, 33, 52, 45, 41, 49, 35, 28, 31}
	var c, d uint32
	for i := 0; i < 28; i++ {
		c += bitNum(key, permC[i], 31-i)
		d += bitNum(key, permD[i], 31-i)
	}
	for i := 0; i < 16; i++ {
		shift := roundShift[i]
		c = ((c << shift) | (c >> (28 - shift))) & 0xfffffff0
		d = ((d << shift) | (d >> (28 - shift))) & 0xfffffff0
		toGen := i
		if mode == modeDecrypt {
			toGen = 15 - i
		}
		for j := 0; j < 24; j++ {
			schedule[toGen][j/8] |= byte(bitNumIntr(c, compression[j], 7-(j%8)))
		}
		for j := 24; j < 48; j++ {
			schedule[toGen][j/8] |= byte(bitNumIntr(d, compression[j]-27, 7-(j%8)))
		}
	}
	return schedule
}
