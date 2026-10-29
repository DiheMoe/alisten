package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

type idl struct {
	Schema   string             `json:"$schema"`
	Title    string             `json:"title"`
	Defs     map[string]*schema `json:"$defs"`
	WS       wsSection          `json:"x-ws"`
	Requests []*message         `json:"x-requests"`
	Events   []*event           `json:"x-events"`
	HTTP     []*endpoint        `json:"x-http"`

	version string
	named   map[string]*schema
	inbound map[string]bool
	goNames map[string]string
}

type wsSection struct {
	Requests   []*message   `json:"requests"`
	Events     []*event     `json:"events"`
	CloseCodes []*closeCode `json:"closeCodes"`
}

type message struct {
	Type        string       `json:"type"`
	Description string       `json:"description"`
	HTTP        *httpBinding `json:"http"`
	Request     *schema      `json:"request"`
	Response    *schema      `json:"response"`
}

type event struct {
	Type        string  `json:"type"`
	Description string  `json:"description"`
	Data        *schema `json:"data"`
}

type endpoint struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	httpBinding
	Auth     string  `json:"auth"`
	Request  *schema `json:"request"`
	Response *schema `json:"response"`
}

type httpBinding struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Status   int    `json:"status"`
	Location bool   `json:"location"`
}

func (b httpBinding) status() int {
	if b.Status == 0 {
		return 200
	}
	return b.Status
}

func (b httpBinding) check(where string) error {
	if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, b.Method) || !pathPattern.MatchString(b.Path) {
		return fmt.Errorf("%s：method %q 或 path %q 不合法", where, b.Method, b.Path)
	}
	if b.Status != 0 && b.Status != 201 && b.Status != 204 {
		return fmt.Errorf("%s：status 只能是 201 或 204，缺省为 200", where)
	}
	if b.Location && b.Status != 201 {
		return fmt.Errorf("%s：location 只能用于 201 的接口", where)
	}
	return nil
}

type closeCode struct {
	Name        string `json:"name"`
	Code        int    `json:"code"`
	Description string `json:"description"`
}

// 生成 OpenAPI 时会把 schema 原样编码回 JSON，所以每个字段都要带 omitempty。
type schema struct {
	Ref                  string             `json:"$ref,omitempty"`
	Type                 string             `json:"type,omitempty"`
	Description          string             `json:"description,omitempty"`
	Enum                 []string           `json:"enum,omitempty"`
	Const                *string            `json:"const,omitempty"`
	Properties           map[string]*schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties *bool              `json:"additionalProperties,omitempty"`
	Items                *schema            `json:"items,omitempty"`
	AnyOf                []*schema          `json:"anyOf,omitempty"`
	OneOf                []*schema          `json:"oneOf,omitempty"`
	AllOf                []*schema          `json:"allOf,omitempty"`
	If                   *schema            `json:"if,omitempty"`
	Then                 *schema            `json:"then,omitempty"`
	MinLength            *int               `json:"minLength,omitempty"`
	MaxLength            *int               `json:"maxLength,omitempty"`
	Minimum              *int64             `json:"minimum,omitempty"`
	Maximum              *int64             `json:"maximum,omitempty"`
	Pattern              string             `json:"pattern,omitempty"`
	MaxBytes             *int               `json:"x-maxBytes,omitempty"`
	URL                  *bool              `json:"x-url,omitempty"`
	HTTPStatus           map[string]int     `json:"x-httpStatus,omitempty"`

	name string
}

func parseIDL(data []byte) (*idl, error) {
	if err := checkJSON(json.NewDecoder(bytes.NewReader(data)), "", false); err != nil {
		return nil, fmt.Errorf("解析 IDL：%w", err)
	}
	var d idl
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("解析 IDL：%w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("解析 IDL：JSON 之后还有多余的内容")
	}
	version, err := protocolVersion(data)
	if err != nil {
		return nil, err
	}
	d.version = version
	if err := d.check(); err != nil {
		return nil, err
	}
	return &d, nil
}

// encoding/json 对重复的键只保留最后一个值，并把显式的 null 解码为 nil，而之后的检查和生成都假定节点存在，所以解码之前先拒绝这两种写法。
// encoding/json 匹配结构体字段时不区分大小写，会把 "MAXLENGTH" 当作 maxLength，所以关键字只接受规范的写法。
func checkJSON(dec *json.Decoder, where string, named bool) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch tok {
	case nil:
		return fmt.Errorf("%s：不能是 null", jsonPath(where))
	case json.Delim('{'):
		var keys []string
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			key := tok.(string)
			if slices.Contains(keys, key) {
				return fmt.Errorf("%s：键 %q 重复", jsonPath(where), key)
			}
			if i := slices.IndexFunc(idlKeywords, func(k string) bool { return strings.EqualFold(k, key) }); !named && i >= 0 && idlKeywords[i] != key {
				return fmt.Errorf("%s：键 %q 应写作 %q", jsonPath(where), key, idlKeywords[i])
			}
			keys = append(keys, key)
			if err := checkJSON(dec, strings.TrimPrefix(where+"."+key, "."), !named && slices.Contains(nameKeyed, key)); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case json.Delim('['):
		for i := 0; dec.More(); i++ {
			if err := checkJSON(dec, fmt.Sprintf("%s[%d]", where, i), false); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	}
	return nil
}

// 值为对象、键由 IDL 作者命名的字段（类型名、属性名、状态码）。新增这类字段时要加入这个列表，否则与关键字只有大小写不同的名字会被拒绝。
var nameKeyed = []string{"$defs", "properties", "x-httpStatus"}

var idlKeywords = jsonNames(idl{}, wsSection{}, message{}, event{}, endpoint{}, httpBinding{}, closeCode{}, schema{})

func jsonNames(values ...any) []string {
	var names []string
	for _, v := range values {
		t := reflect.TypeOf(v)
		for i := range t.NumField() {
			if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func jsonPath(where string) string {
	if where == "" {
		return "顶层"
	}
	return where
}

// 重新编码会按键排序并去掉空白，所以版本只随 IDL 的内容变化，不受格式影响。
func protocolVersion(data []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("解析 IDL：%w", err)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:])[:16], nil
}

const pathParam = `\{([a-z][A-Za-z0-9]*)\}`

var (
	typeNamePattern    = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	enumValuePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	fieldNamePattern   = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	messageTypePattern = regexp.MustCompile(`^[a-z][A-Za-z]*(\.[a-z][A-Za-z]*)*$`)
	pathPattern        = regexp.MustCompile(`^/api(/[a-z][a-z0-9.-]*|/` + pathParam + `)+$`)
	pathParamPattern   = regexp.MustCompile(pathParam)
)

func pathParams(path string) []string {
	var params []string
	for _, m := range pathParamPattern.FindAllStringSubmatch(path, -1) {
		params = append(params, m[1])
	}
	return params
}

// 生成的 TypeScript client 中，路径参数与这些名字是同一个函数中的标识符，不能重名。
var tsClientNames = []string{"body", "send", "encodeURIComponent"}

var tsFixedNames = []string{
	"PROTOCOL_VERSION", "RequestDataMap", "ResponseDataMap", "ConnectionEventDataMap", "RoomEventDataMap", "RequestType",
	"ConnectionEventType", "RoomEventType", "RequestMessage", "ResponseMessage", "ConnectionEventMessage", "RoomEventMessage",
	"ServerMessage", "CloseCode", "Send", "roomRequests", "ErrorResponse", "Auth", "HttpRequest", "HttpSend", "httpClient",
}

// ES 模块总是严格模式，所以列表包含严格模式的保留字，以及 eval 和 arguments。
var jsReservedWords = []string{
	"arguments", "await", "break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete", "do",
	"else", "enum", "eval", "export", "extends", "false", "finally", "for", "function", "if", "implements", "import", "in",
	"instanceof", "interface", "let", "new", "null", "package", "private", "protected", "public", "return", "static", "super",
	"switch", "this", "throw", "true", "try", "typeof", "var", "void", "while", "with", "yield",
}

func (d *idl) check() error {
	if d.Title == "" {
		return errors.New("IDL 缺少 title，它是 OpenAPI 文档的标题")
	}
	d.named = map[string]*schema{}
	d.inbound = map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(d.Defs)) {
		if !typeNamePattern.MatchString(name) {
			return fmt.Errorf("$defs.%s：类型名应为大写字母开头的驼峰式", name)
		}
		if err := d.checkDef("$defs."+name, d.Defs[name]); err != nil {
			return err
		}
		d.Defs[name].name = name
		if !d.Defs[name].scalar() {
			d.named[name] = d.Defs[name]
		}
	}
	for _, name := range []string{"Empty", "ErrorInfo"} {
		if s := d.Defs[name]; s == nil || s.Type != "object" {
			return fmt.Errorf("$defs 缺少对象类型 %s，生成的消息信封要用到它", name)
		}
	}

	requests := map[string]bool{}
	for i, m := range d.WS.Requests {
		where := fmt.Sprintf("x-ws.requests[%d]", i)
		if m.HTTP != nil {
			return fmt.Errorf("%s：只在 WebSocket 上使用的请求没有 http", where)
		}
		if err := d.checkMessage(where, m, requests); err != nil {
			return err
		}
	}
	for i, m := range d.Requests {
		if err := d.checkMessage(fmt.Sprintf("x-requests[%d]", i), m, requests); err != nil {
			return err
		}
	}
	events := map[string]bool{}
	for i, e := range d.WS.Events {
		if err := d.checkEvent(fmt.Sprintf("x-ws.events[%d]", i), e, events); err != nil {
			return err
		}
	}
	if !slices.ContainsFunc(d.WS.Events, func(e *event) bool { return e.Type == "error" }) {
		return errors.New("x-ws.events 缺少 error 事件，生成的消息信封要用到它")
	}
	for i, e := range d.Events {
		if err := d.checkEvent(fmt.Sprintf("x-events[%d]", i), e, events); err != nil {
			return err
		}
	}
	// 房间的事件流由 api 适配器手写，不在 IDL 中。
	names := endpointNames{operations: map[string]bool{}, constants: map[string]bool{}, routes: map[string]string{"GET /api/room/events": "房间的事件流"}, paths: map[string]string{}}
	for i, m := range d.Requests {
		names.operations[camel(m.Type)] = true
		if err := d.checkRoomRoute(fmt.Sprintf("x-requests[%d]", i), m, names); err != nil {
			return err
		}
	}
	for i, e := range d.HTTP {
		if err := d.checkEndpoint(fmt.Sprintf("x-http[%d]", i), e, names); err != nil {
			return err
		}
	}
	if err := checkCloseCodes(d.WS.CloseCodes); err != nil {
		return err
	}
	for _, name := range d.typeNames() {
		if d.inbound[name] && d.Defs[name] == nil {
			if err := d.markInbound(d.named[name]); err != nil {
				return err
			}
		}
	}
	d.inlineScalars()
	if err := d.checkGoNames(); err != nil {
		return err
	}
	for _, name := range d.typeNames() {
		if slices.Contains(tsFixedNames, name) {
			return fmt.Errorf("生成的 TypeScript 标识符 %s 重复：既是生成器写出的 %s，也是类型 %s", name, name, name)
		}
	}
	return nil
}

func (d *idl) checkDef(where string, s *schema) error {
	switch {
	case s.Type == "object":
		return d.checkObject(where, s, false)
	case s.Enum == nil:
		if s.Type != "string" && s.Type != "integer" {
			return fmt.Errorf(`%s：类型应为对象、字符串枚举、"string" 或 "integer"`, where)
		}
		return checkScalar(where, s, true)
	}
	if err := allowOnly(where, s, "type", "description", "enum", "x-httpStatus"); err != nil {
		return err
	}
	if s.Type != "string" || len(s.Enum) == 0 {
		return fmt.Errorf("%s：枚举应为非空的字符串枚举", where)
	}
	names := map[string]bool{}
	for _, v := range s.Enum {
		if !enumValuePattern.MatchString(v) || names[pascal(v)] {
			return fmt.Errorf("%s：枚举值 %q 应由字母、数字和下划线组成，且不能与其他值生成相同的常量名", where, v)
		}
		names[pascal(v)] = true
	}
	for _, code := range slices.Sorted(maps.Keys(s.HTTPStatus)) {
		if status := s.HTTPStatus[code]; !slices.Contains(s.Enum, code) || status < 100 || status > 599 {
			return fmt.Errorf("%s：x-httpStatus 中的 %s: %d 不是枚举值或不是 HTTP 状态码", where, code, status)
		}
	}
	return nil
}

func (d *idl) checkObject(where string, s *schema, inbound bool) error {
	if err := allowOnly(where, s, "type", "description", "properties", "required", "additionalProperties", "oneOf", "allOf"); err != nil {
		return err
	}
	if s.Type != "object" || s.AdditionalProperties == nil || *s.AdditionalProperties {
		return fmt.Errorf(`%s：对象应写明 "type": "object" 和 "additionalProperties": false`, where)
	}
	for _, name := range s.Required {
		if s.Properties[name] == nil {
			return fmt.Errorf("%s：required 中的 %s 不在 properties 里", where, name)
		}
	}
	goNames := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		if !fieldNamePattern.MatchString(name) {
			return fmt.Errorf("%s.properties.%s：字段名应为小写字母开头的驼峰式", where, name)
		}
		if other, ok := goNames[pascal(name)]; ok {
			return fmt.Errorf("%s.properties：%s 和 %s 生成相同的 Go 字段名 %s", where, other, name, pascal(name))
		}
		goNames[pascal(name)] = name
		if err := d.checkField(where+".properties."+name, s.Properties[name], slices.Contains(s.Required, name), inbound); err != nil {
			return err
		}
	}
	if s.OneOf != nil {
		if err := checkAlternatives(where, s); err != nil {
			return err
		}
	}
	if s.AllOf != nil {
		return d.checkConditions(where, s)
	}
	return nil
}

func (d *idl) checkField(where string, s *schema, required, inbound bool) error {
	switch {
	case s.AnyOf != nil:
		if inbound || !required {
			return fmt.Errorf("%s：可以为 null 的字段只能出现在响应和事件中，并且必须是必填字段", where)
		}
		if err := allowOnly(where, s, "anyOf", "description"); err != nil {
			return err
		}
		if len(s.AnyOf) != 2 || s.AnyOf[1].Type != "null" || len(s.AnyOf[1].keywords()) != 1 {
			return fmt.Errorf(`%s：anyOf 只支持 [类型, {"type": "null"}]`, where)
		}
		return d.checkValue(where+".anyOf[0]", s.AnyOf[0], inbound)
	case s.Type == "array":
		if inbound || !required {
			return fmt.Errorf("%s：数组只能出现在响应和事件中，并且必须是必填字段", where)
		}
		if err := allowOnly(where, s, "type", "description", "items"); err != nil {
			return err
		}
		if s.Items == nil {
			return fmt.Errorf("%s：数组缺少 items", where)
		}
		return d.checkValue(where+".items", s.Items, inbound)
	default:
		return d.checkValue(where, s, inbound)
	}
}

func (d *idl) checkValue(where string, s *schema, inbound bool) error {
	if s.Ref != "" {
		if err := allowOnly(where, s, "$ref", "description"); err != nil {
			return err
		}
		_, err := d.resolve(where, s.Ref)
		return err
	}
	switch s.Type {
	case "string", "integer":
		return checkScalar(where, s, false)
	case "number", "boolean":
		if inbound {
			return fmt.Errorf("%s：请求中不支持 %s", where, s.Type)
		}
		return allowOnly(where, s, "type", "description")
	default:
		return fmt.Errorf("%s：不支持的类型 %q", where, s.Type)
	}
}

// x-url 只能写在 $defs 的标量类型中（def 为真），因为网页按标量类型生成同名的校验函数。
func checkScalar(where string, s *schema, def bool) error {
	if s.Type == "integer" {
		if err := allowOnly(where, s, "type", "description", "minimum", "maximum"); err != nil {
			return err
		}
		return checkBounds(where, s)
	}
	allowed := []string{"type", "description", "minLength", "maxLength", "pattern", "x-maxBytes"}
	if def {
		allowed = append(allowed, "x-url")
	}
	if err := allowOnly(where, s, allowed...); err != nil {
		return err
	}
	// 网页生成的校验函数不检查 pattern，因为 JavaScript 和 Go 的正则表达式语法不同。
	if s.URL != nil && (!*s.URL || s.Pattern != "") {
		return fmt.Errorf("%s：x-url 只能是 true，并且不能与 pattern 同时使用", where)
	}
	if err := checkBounds(where, s); err != nil {
		return err
	}
	return checkPattern(where, s.Pattern)
}

func checkBounds(where string, s *schema) error {
	for _, n := range []*int{s.MinLength, s.MaxLength, s.MaxBytes} {
		if n != nil && *n < 0 {
			return fmt.Errorf("%s：minLength、maxLength 和 x-maxBytes 不能是负数", where)
		}
	}
	if s.MinLength != nil && s.MaxLength != nil && *s.MinLength > *s.MaxLength {
		return fmt.Errorf("%s：minLength 大于 maxLength", where)
	}
	if s.Minimum != nil && s.Maximum != nil && *s.Minimum > *s.Maximum {
		return fmt.Errorf("%s：minimum 大于 maximum", where)
	}
	return nil
}

func checkPattern(where, pattern string) error {
	if pattern == "" {
		return nil
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("%s：pattern 无法编译：%w", where, err)
	}
	return nil
}

func (d *idl) resolve(where, ref string) (*schema, error) {
	name, ok := strings.CutPrefix(ref, "#/$defs/")
	if !ok || d.Defs[name] == nil {
		return nil, fmt.Errorf("%s：引用的类型 %s 不存在", where, ref)
	}
	return d.Defs[name], nil
}

// 分支的 properties 中不在 required 里的字段是各分支共用的可选字段：每个分支都要列出同样的一组，解码时不参与匹配分支。
func checkAlternatives(where string, s *schema) error {
	if len(s.OneOf) < 2 {
		return fmt.Errorf("%s：oneOf 至少要有两个分支", where)
	}
	shared := optionalFields(s.OneOf[0])
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		if !slices.Contains(shared, name) && !slices.ContainsFunc(s.OneOf, func(alt *schema) bool { return slices.Contains(alt.Required, name) }) {
			return fmt.Errorf("%s：%s 不属于 oneOf 的任何分支", where, name)
		}
	}
	for i, alt := range s.OneOf {
		w := fmt.Sprintf("%s.oneOf[%d]", where, i)
		if err := allowOnly(w, alt, "properties", "required", "additionalProperties"); err != nil {
			return err
		}
		if alt.AdditionalProperties == nil || *alt.AdditionalProperties || len(alt.Required) == 0 {
			return fmt.Errorf(`%s：分支应由 required、properties 和 "additionalProperties": false 组成，required 不能为空`, w)
		}
		for j, name := range alt.Required {
			if alt.Properties[name] == nil {
				return fmt.Errorf("%s：required 中的 %s 不在 properties 里", w, name)
			}
			if slices.Contains(alt.Required[:j], name) {
				return fmt.Errorf("%s：required 中的 %s 重复", w, name)
			}
		}
		if optional := optionalFields(alt); !slices.Equal(optional, shared) {
			return fmt.Errorf("%s：可选字段应与第一个分支相同（%s），实际为（%s）", w, strings.Join(shared, "、"), strings.Join(optional, "、"))
		}
		for _, name := range slices.Sorted(maps.Keys(alt.Properties)) {
			if len(alt.Properties[name].keywords()) != 0 {
				return fmt.Errorf("%s：properties 中的 %s 应为 {}", w, name)
			}
			if s.Properties[name] == nil || slices.Contains(s.Required, name) {
				return fmt.Errorf("%s：%s 应是外层对象的可选字段", w, name)
			}
		}
	}
	return nil
}

// 按名称排序。
func optionalFields(alt *schema) []string {
	var names []string
	for _, name := range slices.Sorted(maps.Keys(alt.Properties)) {
		if !slices.Contains(alt.Required, name) {
			names = append(names, name)
		}
	}
	return names
}

func (d *idl) checkConditions(where string, s *schema) error {
	if len(s.AllOf) == 0 {
		return fmt.Errorf("%s：allOf 至少要有一个条件", where)
	}
	seen := map[string]int{}
	for i, entry := range s.AllOf {
		w := fmt.Sprintf("%s.allOf[%d]", where, i)
		if err := allowOnly(w, entry, "if", "then"); err != nil {
			return err
		}
		if entry.If == nil || entry.Then == nil {
			return fmt.Errorf("%s：要同时包含 if 和 then", w)
		}
		if err := d.checkCondition(w, s, entry.If, entry.Then); err != nil {
			return err
		}
		name := entry.If.Required[0]
		key := name + "\x00" + *entry.If.Properties[name].Const
		if first, ok := seen[key]; ok {
			return fmt.Errorf("%s：与 allOf[%d] 重复，同一个字段的同一个取值只能有一个条件", w, first)
		}
		seen[key] = i
	}
	return nil
}

func (d *idl) checkCondition(where string, s *schema, cond, then *schema) error {
	if err := allowOnly(where+".if", cond, "properties", "required"); err != nil {
		return err
	}
	if len(cond.Properties) != 1 || len(cond.Required) != 1 || cond.Properties[cond.Required[0]] == nil {
		return fmt.Errorf("%s.if：应写成 {\"properties\": {字段: {\"const\": 值}}, \"required\": [字段]}", where)
	}
	name := cond.Required[0]
	c := cond.Properties[name]
	if err := allowOnly(where+".if.properties."+name, c, "const"); err != nil {
		return err
	}
	p := s.Properties[name]
	if c.Const == nil || p == nil || p.Ref == "" || !slices.Contains(s.Required, name) {
		return fmt.Errorf("%s.if：只支持按必填的枚举字段取值判断", where)
	}
	if !slices.Contains(d.Defs[refName(p.Ref)].Enum, *c.Const) {
		return fmt.Errorf("%s.if：%q 不是 %s 的取值", where, *c.Const, p.Ref)
	}
	if err := allowOnly(where+".then", then, "properties"); err != nil {
		return err
	}
	for _, target := range slices.Sorted(maps.Keys(then.Properties)) {
		w := where + ".then.properties." + target
		if p := s.Properties[target]; p == nil || p.Type != "string" || !slices.Contains(s.Required, target) {
			return fmt.Errorf("%s：then 只能约束必填的字符串字段", w)
		}
		c := then.Properties[target]
		if c.Ref != "" {
			if err := allowOnly(w, c, "$ref"); err != nil {
				return err
			}
			if def, err := d.resolve(w, c.Ref); err != nil {
				return err
			} else if !def.scalar() || def.Type != "string" {
				return fmt.Errorf("%s：只能引用字符串的标量类型", w)
			}
			continue
		}
		if err := allowOnly(w, c, "minLength", "maxLength", "pattern", "x-maxBytes"); err != nil {
			return err
		}
		if err := checkBounds(w, c); err != nil {
			return err
		}
		if err := checkPattern(w, c.Pattern); err != nil {
			return err
		}
	}
	return nil
}

func (d *idl) checkMessage(where string, m *message, seen map[string]bool) error {
	if !messageTypePattern.MatchString(m.Type) || seen[pascal(m.Type)] {
		return fmt.Errorf("%s：消息类型 %q 格式不对或重复", where, m.Type)
	}
	seen[pascal(m.Type)] = true
	if m.Request == nil || m.Request.Ref != "" {
		return fmt.Errorf("%s.request：请求要内联定义为对象，每种请求解码出不同的类型", where)
	}
	if err := d.addInline(where+".request", m.Request, pascal(m.Type)+"Request", true); err != nil {
		return err
	}
	return d.addData(where+".response", m.Response, pascal(m.Type)+"Response")
}

func (d *idl) checkEvent(where string, e *event, seen map[string]bool) error {
	if !messageTypePattern.MatchString(e.Type) || seen[pascal(e.Type)] {
		return fmt.Errorf("%s：事件类型 %q 格式不对或重复", where, e.Type)
	}
	seen[pascal(e.Type)] = true
	return d.addData(where+".data", e.Data, pascal(e.Type)+"Event")
}

type endpointNames struct {
	operations map[string]bool
	// 接口名的大驼峰形式是 Go 中 Route 常量名的一部分，不同的接口名可能转成相同的形式。
	constants map[string]bool
	routes    map[string]string
	paths     map[string]string
}

func (n endpointNames) addRoute(where string, b httpBinding, owner string) error {
	// 路径参数的名字不影响路由：ServeMux 登记两条这样的路由会 panic，OpenAPI 也把它们视为同一个 path。
	shape := pathParamPattern.ReplaceAllString(b.Path, "{}")
	route := b.Method + " " + shape
	if first, ok := n.routes[route]; ok {
		return fmt.Errorf("%s：%s %s 与%s重复，路径参数的名字不参与比较", where, b.Method, b.Path, first)
	}
	n.routes[route] = owner
	// OpenAPI 中各方法共用这个 path 的参数名。
	if first, ok := n.paths[shape]; ok && first != b.Path {
		return fmt.Errorf("%s：路径 %s 与 %s 只有参数名不同，同一个路径形状要用相同的参数名", where, b.Path, first)
	}
	n.paths[shape] = b.Path
	return nil
}

func roomPath(path string) bool {
	return path == "/api/room" || strings.HasPrefix(path, "/api/room/")
}

// 检查 http 绑定能否按 protocol/http.go 中 RoomRoute 的规则组装出请求。
func (d *idl) checkRoomRoute(where string, m *message, names endpointNames) error {
	if m.HTTP == nil {
		return fmt.Errorf("%s：缺少 http", where)
	}
	b, where := *m.HTTP, where+".http"
	if err := b.check(where); err != nil {
		return err
	}
	if !roomPath(b.Path) {
		return fmt.Errorf("%s：path %q 应是 /api/room 或在它之下", where, b.Path)
	}
	response := m.Response
	if response.Ref != "" {
		response = d.Defs[refName(response.Ref)]
	}
	if b.status() == 204 && len(response.Properties) > 0 {
		return fmt.Errorf("%s：204 的请求的 response 应为空对象，HTTP 不返回它", where)
	}
	params := pathParams(b.Path)
	for i, p := range params {
		if slices.Contains(params[:i], p) {
			return fmt.Errorf("%s：路径参数 %s 重复", where, p)
		}
		if f := m.Request.Properties[p]; f == nil || !slices.Contains(m.Request.Required, p) || d.jsonType(f) != "string" {
			return fmt.Errorf("%s：路径参数 %s 应是请求中必填的字符串字段", where, p)
		}
	}
	if b.Method == "GET" || b.Method == "DELETE" {
		for _, name := range slices.Sorted(maps.Keys(m.Request.Properties)) {
			if d.jsonType(m.Request.Properties[name]) == "object" {
				return fmt.Errorf("%s：%s 请求的字段 %s 来自路径或查询串，应是字符串、整数或枚举", where, b.Method, name)
			}
		}
	}
	return names.addRoute(where, b, "房间内请求 "+m.Type+" 的路由")
}

// 请求的字段已经检查过，不会是数组、布尔或 null，所以只有 string、integer 和 object 三种。
func (d *idl) jsonType(s *schema) string {
	if s.Ref != "" {
		return d.Defs[refName(s.Ref)].Type
	}
	return s.Type
}

func (d *idl) checkEndpoint(where string, e *endpoint, names endpointNames) error {
	if !fieldNamePattern.MatchString(e.Name) || names.operations[e.Name] || names.constants[pascal(e.Name)] {
		return fmt.Errorf("%s：接口名 %q 格式不对，或与其他接口、房间请求的 operationId 重复", where, e.Name)
	}
	names.operations[e.Name], names.constants[pascal(e.Name)] = true, true
	if err := e.check(where); err != nil {
		return err
	}
	if roomPath(e.Path) {
		return fmt.Errorf("%s：path %q 是 /api/room 或在它之下，这些路径留给房间内请求", where, e.Path)
	}
	params := pathParams(e.Path)
	for i, p := range params {
		if slices.Contains(params[:i], p) || slices.Contains(tsClientNames, p) {
			return fmt.Errorf("%s：路径参数 %s 重复，或与生成的 TypeScript client 中的标识符重名", where, p)
		}
		if slices.Contains(jsReservedWords, p) {
			return fmt.Errorf("%s：路径参数 %s 是 JavaScript 的保留字，不能作为生成的 TypeScript client 中的参数名", where, p)
		}
	}
	if err := names.addRoute(where, e.httpBinding, "接口 "+e.Name+" 的路由"); err != nil {
		return err
	}
	if !slices.Contains([]string{"none", "member", "admin"}, e.Auth) {
		return fmt.Errorf("%s：auth 应为 none、member 或 admin", where)
	}
	if e.Method == "GET" || e.Method == "DELETE" {
		if e.Request != nil {
			return fmt.Errorf("%s：%s 请求没有请求体", where, e.Method)
		}
	} else {
		if e.Request == nil || e.Request.Ref != "" {
			return fmt.Errorf("%s.request：请求体要内联定义为对象", where)
		}
		if err := d.addInline(where+".request", e.Request, pascal(e.Name)+"Request", true); err != nil {
			return err
		}
	}
	if e.status() == 204 {
		if e.Response != nil {
			return fmt.Errorf("%s：204 的接口没有 response", where)
		}
		return nil
	}
	return d.addData(where+".response", e.Response, pascal(e.Name)+"Response")
}

func (d *idl) addData(where string, s *schema, name string) error {
	if s == nil {
		return fmt.Errorf("%s：缺少定义", where)
	}
	if s.Ref == "" {
		return d.addInline(where, s, name, false)
	}
	if err := allowOnly(where, s, "$ref"); err != nil {
		return err
	}
	def, err := d.resolve(where, s.Ref)
	if err != nil {
		return err
	}
	if def.Type != "object" {
		return fmt.Errorf("%s：应引用对象类型", where)
	}
	return nil
}

func (d *idl) addInline(where string, s *schema, name string, inbound bool) error {
	if d.named[name] != nil {
		return fmt.Errorf("%s：生成的类型名 %s 与其他类型重复", where, name)
	}
	if err := d.checkObject(where, s, inbound); err != nil {
		return err
	}
	s.name = name
	d.named[name] = s
	d.inbound[name] = inbound
	return nil
}

func checkCloseCodes(codes []*closeCode) error {
	names, values := map[string]bool{}, map[int]bool{}
	for i, c := range codes {
		if !fieldNamePattern.MatchString(c.Name) || names[pascal(c.Name)] || values[c.Code] || c.Code < 1000 || c.Code > 4999 {
			return fmt.Errorf("x-ws.closeCodes[%d]：名称 %q 或关闭码 %d 不合法或重复", i, c.Name, c.Code)
		}
		names[pascal(c.Name)], values[c.Code] = true, true
	}
	return nil
}

func (d *idl) checkGoNames() error {
	declared := map[string]string{}
	var err error
	declare := func(name, what string) {
		if first, ok := declared[name]; ok && err == nil {
			err = fmt.Errorf("生成的 Go 标识符 %s 重复：既是%s，也是%s", name, first, what)
		}
		declared[name] = what
	}
	declare("Version", "生成器写出的 Version")
	declare("ErrorResponse", "生成器写出的 ErrorResponse")
	declare("decodeConnectionRequest", "生成器写出的 decodeConnectionRequest")
	declare("decodeRoomRequest", "生成器写出的 decodeRoomRequest")
	declare("RoomRoutes", "生成器写出的 RoomRoutes")
	for _, name := range d.typeNames() {
		s := d.named[name]
		declare(name, "类型 "+name)
		for _, v := range s.Enum {
			declare(name+pascal(v), fmt.Sprintf("枚举 %s 的取值 %s", name, v))
		}
		if s.HTTPStatus != nil {
			declare(lowerFirst(name)+"HTTPStatus", fmt.Sprintf("枚举 %s 的 HTTP 状态码表", name))
		}
		if !d.inbound[name] {
			continue
		}
		declare("decode"+name, fmt.Sprintf("类型 %s 的解码函数", name))
		for _, f := range slices.Sorted(maps.Keys(s.Properties)) {
			if s.Properties[f].Pattern != "" {
				declare("pattern"+name+pascal(f), fmt.Sprintf("类型 %s 的字段 %s 的 pattern", name, f))
			}
		}
		for _, entry := range s.AllOf {
			value := *entry.If.Properties[entry.If.Required[0]].Const
			for _, f := range slices.Sorted(maps.Keys(entry.Then.Properties)) {
				if entry.Then.Properties[f].Pattern != "" {
					declare("pattern"+name+pascal(f)+"When"+pascal(value), fmt.Sprintf("类型 %s 的字段 %s 在 %s 时的 pattern", name, f, value))
				}
			}
		}
	}
	for _, m := range slices.Concat(d.WS.Requests, d.Requests) {
		declare("Request"+pascal(m.Type), fmt.Sprintf("请求 %s 的常量", m.Type))
	}
	for _, e := range slices.Concat(d.WS.Events, d.Events) {
		declare("Event"+pascal(e.Type), fmt.Sprintf("事件 %s 的常量", e.Type))
	}
	for _, c := range d.WS.CloseCodes {
		declare("Close"+pascal(c.Name), fmt.Sprintf("关闭码 %s 的常量", c.Name))
	}
	for _, e := range d.HTTP {
		declare("Route"+pascal(e.Name), fmt.Sprintf("接口 %s 的常量", e.Name))
		if e.Request != nil {
			declare("Decode"+e.Request.name, fmt.Sprintf("接口 %s 的请求体的解码函数", e.Name))
		}
	}
	d.goNames = declared
	return err
}

func (d *idl) checkHandwritten(dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	for _, path := range paths {
		if strings.HasSuffix(path, ".gen.go") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, name := range topLevelNames(f) {
			if what, ok := d.goNames[name]; ok {
				return fmt.Errorf("生成的 Go 标识符 %s 与 %s 中手写的声明重名：它是%s", name, filepath.Base(path), what)
			}
		}
	}
	return nil
}

func topLevelNames(f *ast.File) []string {
	var names []string
	for _, decl := range f.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Recv == nil {
				names = append(names, decl.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					names = append(names, spec.Name.Name)
				case *ast.ValueSpec:
					for _, n := range spec.Names {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	return names
}

// 请求引用到的类型要生成解码函数，所以也按请求的限制再检查一遍。
func (d *idl) markInbound(s *schema) error {
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		p := s.Properties[name]
		if p.Ref == "" || d.inbound[refName(p.Ref)] || d.Defs[refName(p.Ref)].scalar() {
			continue
		}
		def := d.Defs[refName(p.Ref)]
		d.inbound[def.name] = true
		if def.Enum != nil {
			continue
		}
		if err := d.checkObject("$defs."+def.name, def, true); err != nil {
			return err
		}
		if err := d.markInbound(def); err != nil {
			return err
		}
	}
	return nil
}

func (d *idl) typeNames() []string {
	return slices.Sorted(maps.Keys(d.named))
}

func (d *idl) urlDefs() []string {
	var names []string
	for _, name := range slices.Sorted(maps.Keys(d.Defs)) {
		if d.Defs[name].URL != nil {
			names = append(names, name)
		}
	}
	return names
}

func (s *schema) scalar() bool {
	return s.Type != "object" && s.Enum == nil
}

func (d *idl) inlineScalars() {
	for _, s := range d.named {
		for name, p := range s.Properties {
			s.Properties[name] = d.inlined(p)
		}
		for _, entry := range s.AllOf {
			for name, p := range entry.Then.Properties {
				entry.Then.Properties[name] = d.inlined(p)
			}
		}
	}
}

func (d *idl) inlined(s *schema) *schema {
	switch {
	case s.AnyOf != nil:
		s.AnyOf[0] = d.inlined(s.AnyOf[0])
	case s.Items != nil:
		s.Items = d.inlined(s.Items)
	case s.Ref != "" && d.Defs[refName(s.Ref)].scalar():
		v := *d.Defs[refName(s.Ref)]
		v.name = ""
		if s.Description != "" {
			v.Description = s.Description
		}
		return &v
	}
	return s
}

func (s *schema) dataType() string {
	if s.Ref != "" {
		return refName(s.Ref)
	}
	return s.name
}

func refName(ref string) string {
	return strings.TrimPrefix(ref, "#/$defs/")
}

func (s *schema) keywords() []string {
	var k []string
	add := func(set bool, name string) {
		if set {
			k = append(k, name)
		}
	}
	add(s.Ref != "", "$ref")
	add(s.Type != "", "type")
	add(s.Description != "", "description")
	add(s.Enum != nil, "enum")
	add(s.Const != nil, "const")
	add(s.Properties != nil, "properties")
	add(s.Required != nil, "required")
	add(s.AdditionalProperties != nil, "additionalProperties")
	add(s.Items != nil, "items")
	add(s.AnyOf != nil, "anyOf")
	add(s.OneOf != nil, "oneOf")
	add(s.AllOf != nil, "allOf")
	add(s.If != nil, "if")
	add(s.Then != nil, "then")
	add(s.MinLength != nil, "minLength")
	add(s.MaxLength != nil, "maxLength")
	add(s.Minimum != nil, "minimum")
	add(s.Maximum != nil, "maximum")
	add(s.Pattern != "", "pattern")
	add(s.MaxBytes != nil, "x-maxBytes")
	add(s.URL != nil, "x-url")
	add(s.HTTPStatus != nil, "x-httpStatus")
	return k
}

func allowOnly(where string, s *schema, allowed ...string) error {
	for _, k := range s.keywords() {
		if !slices.Contains(allowed, k) {
			return fmt.Errorf("%s：这里不支持 %s", where, k)
		}
	}
	return nil
}
