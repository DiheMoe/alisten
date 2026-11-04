package qq

import (
	"strings"
	"testing"
)

// 合成的 QRC 明文与对应密文（由实现自身的加密方向生成，并已与真实上游响应对照验证过算法）。
// 不用真实歌词做固定件：歌词文本有版权，不随仓库分发。
const syntheticHex = "00e89b5b5297130317c5ab48f1bc398427fd116a7f781c1dcaba11f091bae35bce4e9e7a7f21d22faf11f3df6f877a90322a503bb5255e44f12d71001af063e612c430215267ba372156edff0bac9efeed9f0406a00f9d17f871cd9d6eaf6216286a7994ae27ca58f71c683d283e92209d036ffede8968c1f27f423a4699c6ca6bf0f54437fb0db6401288906def72ff55ce2697d3847b837bebbc018995724dd9a2d7fbf34727541ebaf8af8e8c57cf9a233d8cc2290aa2bab3a11b075fa27867ebd472fc2900e7f939cd03c64a12a36261c64cfe31f488c3b1d9fa5e397f5a66a7063aecdfab30ce6c065fc2bb6cf1231f2d1d7f64747d75be30c4599be937fab61467621a4b788a9d6e3f363f5c7befabca8bce3a42cabee6fdfbbf716e3d550bc006072cc57d9747cb873e23df8546b8077dc5b7e1723ae5f4b8ae0d76bb06832509e92dc31dfc7407a70e5bb568e8ed2c17b46d153a0ca277660e4caf1c"

const syntheticPlain = `<?xml version="1.0" encoding="utf-8"?>
<QrcInfos>
<QrcHeadInfo SaveTime="1" Version="100"/>
<LyricInfo LyricCount="1">
<Lyric_1 LyricType="1" LyricContent="[ti:合成测试歌]
[ar:虚构歌手]
[al:不存在的专辑]
[by:]
[offset:0]
[kana:1ce4试1shi4例]
[1000,1500]你(1000,500)好(1500,500)世(2000,500)
[3000,1200]括(3000,200)号(3200,200)（(3400,200)内(3600,200)容(3800,200)）(4000,200)
[5000,1000]再(5000,500)见(5500,500)
"/>
</LyricInfo>
</QrcInfos>`

func TestDecryptQRCHexGolden(t *testing.T) {
	got, err := decryptQRCHex(syntheticHex)
	if err != nil {
		t.Fatalf("解密失败：%v", err)
	}
	if got != syntheticPlain {
		t.Errorf("解密结果与固定明文不一致：%.80q", got)
	}
}

func TestDecryptQRCHexRejectsBadInput(t *testing.T) {
	if _, err := decryptQRCHex("not-hex"); err == nil {
		t.Error("非十六进制输入没有报错")
	}
	if _, err := decryptQRCHex("0011"); err == nil {
		t.Error("长度不是 8 的倍数没有报错")
	}
	if _, err := decryptQRCHex(strings.Repeat("00", 16)); err == nil {
		t.Error("非 zlib 数据没有报错")
	}
}

func TestParseAndRender(t *testing.T) {
	tags, lines := parseQRC(syntheticPlain)
	if tags["ti"] != "合成测试歌" {
		t.Errorf("ti 标签 = %q", tags["ti"])
	}
	if _, ok := tags["kana"]; !ok {
		t.Error("kana 注解行应按标签处理")
	}
	if len(lines) != 3 {
		t.Fatalf("解析出 %d 行，期望 3 行", len(lines))
	}
	line := lines[0]
	if line.StartMs != 1000 || line.DurationMs != 1500 || line.text() != "你好世" {
		t.Errorf("首行 = [%d,%d]%q", line.StartMs, line.DurationMs, line.text())
	}
	if len(line.Words) != 3 || line.Words[0] != (qrcWord{StartMs: 1000, DurationMs: 500, Text: "你"}) {
		t.Errorf("首行逐字 = %#v", line.Words)
	}
	// 歌词原文里的全角圆括号不是时间标记，要保留在文本里。
	if text := lines[1].text(); text != "括号（内容）" {
		t.Errorf("含括号的行 = %q", text)
	}

	lrc := renderLRCLines(tags, lines)
	if !strings.Contains(lrc, "[ti:合成测试歌]\n") || !strings.Contains(lrc, "[00:01.00]你好世\n") {
		t.Errorf("LRC 渲染缺少预期行：%.120q", lrc)
	}
	yrc := renderYRCLines(lines)
	if !strings.Contains(yrc, "[1000,1500](1000,500,0)你(1500,500,0)好(2000,500,0)世\n") {
		t.Errorf("YRC 渲染缺少预期行：%.120q", yrc)
	}
}

// 没有逐字时间标记的内容（纯 LRC 文本）解析不出行。
func TestParsePlainLRCYieldsNoLines(t *testing.T) {
	_, lines := parseQRC("[00:01.00]普通歌词\n[00:03.00]第二行\n")
	if len(lines) != 0 {
		t.Errorf("纯 LRC 解析出 %d 行，期望 0", len(lines))
	}
}
