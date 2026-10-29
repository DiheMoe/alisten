package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
)

func openAPI(d *idl) ([]byte, error) {
	schemas := map[string]any{
		"ErrorResponse": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{"error": componentRef("ErrorInfo")},
			"required":             []string{"error"},
		},
	}
	for name, s := range d.Defs {
		if s.scalar() {
			continue
		}
		if err := addComponent(schemas, name, s); err != nil {
			return nil, err
		}
	}
	paths := map[string]map[string]any{}
	for _, e := range d.HTTP {
		op := map[string]any{
			"operationId": e.Name,
			"responses":   openAPIResponses(e.httpBinding, e.Response),
		}
		if e.Description != "" {
			op["summary"] = e.Description
		}
		if params := pathParams(e.Path); len(params) > 0 {
			list := make([]any, len(params))
			for i, p := range params {
				list[i] = map[string]any{"name": p, "in": "path", "required": true, "schema": map[string]any{"type": "string"}}
			}
			op["parameters"] = list
		}
		if e.Request != nil {
			op["requestBody"] = openAPIBody(e.Request)
		}
		security := []any{}
		if scheme := map[string]string{"member": "memberToken", "admin": "adminToken"}[e.Auth]; scheme != "" {
			security = append(security, map[string]any{scheme: []any{}})
		}
		op["security"] = security
		for _, s := range []*schema{e.Request, e.Response} {
			if err := addInlineComponent(schemas, s); err != nil {
				return nil, err
			}
		}
		if paths[e.Path] == nil {
			paths[e.Path] = map[string]any{}
		}
		paths[e.Path][strings.ToLower(e.Method)] = op
	}
	for _, m := range d.Requests {
		op, err := roomOperation(schemas, m)
		if err != nil {
			return nil, err
		}
		if paths[m.HTTP.Path] == nil {
			paths[m.HTTP.Path] = map[string]any{}
		}
		paths[m.HTTP.Path][strings.ToLower(m.HTTP.Method)] = op
	}

	bearer := map[string]any{"type": "http", "scheme": "bearer"}
	ticket := map[string]any{"type": "http", "scheme": "bearer", "description": "进房票据或常驻房间的 API Key"}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": d.Title, "version": d.version},
		"paths":   paths,
		"components": map[string]any{
			"schemas":         schemas,
			"securitySchemes": map[string]any{"memberToken": bearer, "ticket": ticket, "adminToken": bearer},
		},
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func componentRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func openAPIBody(s *schema) map[string]any {
	return map[string]any{
		"required": true,
		"content":  map[string]any{"application/json": map[string]any{"schema": componentRef(s.dataType())}},
	}
}

func openAPIResponses(b httpBinding, s *schema) map[string]any {
	success := map[string]any{"description": "成功"}
	if s != nil {
		success["content"] = map[string]any{"application/json": map[string]any{"schema": componentRef(s.dataType())}}
	}
	if b.Location {
		success["headers"] = map[string]any{"Location": map[string]any{"description": "新资源的地址", "schema": map[string]any{"type": "string"}}}
	}
	return map[string]any{
		strconv.Itoa(b.status()): success,
		"default": map[string]any{
			"description": "失败",
			"content":     map[string]any{"application/json": map[string]any{"schema": componentRef("ErrorResponse")}},
		},
	}
}

// 路径参数不在查询串和请求体中；204 的请求没有响应体。
func roomOperation(schemas map[string]any, m *message) (map[string]any, error) {
	b := *m.HTTP
	response := m.Response
	if b.status() == 204 {
		response = nil
	}
	op := map[string]any{
		"operationId": camel(m.Type),
		"responses":   openAPIResponses(b, response),
		"security":    []any{map[string]any{"ticket": []any{}}},
	}
	if m.Description != "" {
		op["summary"] = m.Description
	}
	if err := addInlineComponent(schemas, response); err != nil {
		return nil, err
	}
	pathNames := pathParams(b.Path)
	var params []any
	for _, name := range pathNames {
		s, err := openAPISchema(m.Request.Properties[name])
		if err != nil {
			return nil, err
		}
		params = append(params, map[string]any{"name": name, "in": "path", "required": true, "schema": s})
	}
	if b.Method == "GET" || b.Method == "DELETE" {
		for _, name := range slices.Sorted(maps.Keys(m.Request.Properties)) {
			if slices.Contains(pathNames, name) {
				continue
			}
			s, err := openAPISchema(m.Request.Properties[name])
			if err != nil {
				return nil, err
			}
			params = append(params, map[string]any{"name": name, "in": "query", "required": slices.Contains(m.Request.Required, name), "schema": s})
		}
	} else {
		body, err := roomBody(schemas, m.Request, pathNames)
		if err != nil {
			return nil, err
		}
		op["requestBody"] = body
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	return op, nil
}

// 请求体没有字段时可以不带请求体。
func roomBody(schemas map[string]any, s *schema, pathNames []string) (map[string]any, error) {
	var bodySchema any = componentRef(s.name)
	fields := len(s.Properties)
	if len(pathNames) == 0 {
		if err := addInlineComponent(schemas, s); err != nil {
			return nil, err
		}
	} else {
		rest := *s
		rest.Properties = maps.Clone(s.Properties)
		for _, name := range pathNames {
			delete(rest.Properties, name)
		}
		rest.Required = slices.DeleteFunc(slices.Clone(s.Required), func(name string) bool { return slices.Contains(pathNames, name) })
		fields = len(rest.Properties)
		v, err := openAPISchema(&rest)
		if err != nil {
			return nil, err
		}
		bodySchema = v
	}
	return map[string]any{
		"required": fields > 0,
		"content":  map[string]any{"application/json": map[string]any{"schema": bodySchema}},
	}, nil
}

func addInlineComponent(schemas map[string]any, s *schema) error {
	if s == nil || s.Ref != "" {
		return nil
	}
	return addComponent(schemas, s.name, s)
}

func addComponent(schemas map[string]any, name string, s *schema) error {
	v, err := openAPISchema(s)
	if err != nil {
		return err
	}
	schemas[name] = v
	return nil
}

func openAPISchema(s *schema) (any, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return rewriteSchema(v), nil
}

func rewriteSchema(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if s, ok := x.(string); ok && k == "$ref" {
				v[k] = "#/components/schemas/" + refName(s)
			} else {
				v[k] = rewriteSchema(x)
			}
		}
		// 服务端接受非 ASCII 的主机名和路径，所以 format 是 iri 而不是 uri。
		if v["x-url"] == true {
			v["format"] = "iri"
		}
	case []any:
		for i, x := range v {
			v[i] = rewriteSchema(x)
		}
	}
	return v
}
