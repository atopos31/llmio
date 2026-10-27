package handler

import (
	"bytes"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// 展示层的 body 编码标签。库里存的是原始字节，请求体不一定合法 UTF-8。
const (
	// encodingUTF8 原始字节就是合法 UTF-8，可以直接展示。
	encodingUTF8 = "utf-8"
	// encodingGB18030 原始字节不是合法 UTF-8，但按 GB18030 解码后是正常文本。
	encodingGB18030 = "gb18030"
	// encodingBinary 两种解释都不成立，非法字节只能替换成 U+FFFD。
	encodingBinary = "binary"
	// encodingMixed 数组里各段编码不一致（实际很少见）。
	encodingMixed = "mixed"
)

// decodeBodyText 把落库的原始请求/响应字节转成能安全塞进 JSON 的文本，
// 并返回这次用了哪种解释，供前端打标注。
//
// 为什么需要它：请求体是按字节原样保存的。客户端在中文 Windows 上常用
// GBK/GB18030 发送，这些字节不是合法 UTF-8；encoding/json 编码非法字节时会
// 把它们逐个换成 U+FFFD，于是“你好，世界”的 GBK 字节在页面上变成
// “���ã�����” 这种不可逆的乱码。
// 库里的字节本身没坏（往返实测逐字节相同），所以只要在读路径上补一层解码即可，
// 不需要迁移数据、也不该在写入路径上改字节（那会破坏上游转发与原始留存）。
func decodeBodyText(raw []byte) (text string, encoding string) {
	if len(raw) == 0 {
		return "", ""
	}
	if utf8.Valid(raw) {
		return string(raw), encodingUTF8
	}
	if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw); err == nil && looksLikeText(decoded) {
		return string(decoded), encodingGB18030
	}
	// 解不出来就只能是二进制：用替换符保证 JSON 可编码，同时把事实标出来。
	return string(bytes.ToValidUTF8(raw, []byte("\uFFFD"))), encodingBinary
}

// looksLikeText 判断 GB18030 解码结果是否像正常文本。
// GB18030 的字节范围很宽，PNG/图片这类二进制也可能“解码成功”成一堆怪字符，
// 因此要求结果合法 UTF-8、没有替换符、没有控制字符，避免把二进制硬解成乱码文字。
func looksLikeText(b []byte) bool {
	if len(b) == 0 || !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if r == utf8.RuneError {
			return false
		}
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}
