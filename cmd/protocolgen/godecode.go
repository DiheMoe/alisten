package main

import (
	"bytes"
	"fmt"
	"go/format"
	"maps"
	"slices"
	"strings"
)

type goPattern struct {
	name, expr string
}

type goDecoder struct {
	b        bytes.Buffer
	patterns []goPattern
}

func goDecode(d *idl) ([]byte, error) {
	var g goDecoder
	g.registry("decodeConnectionRequest", d.WS.Requests)
	g.registry("decodeRoomRequest", d.Requests)
	for _, e := range d.HTTP {
		if e.Request != nil {
			name := e.Request.name
			fmt.Fprintf(&g.b, "\nfunc Decode%s(body []byte) (%s, error) {\nreturn decode%s(body, \"\")\n}\n", name, name, name)
		}
	}
	for _, name := range d.typeNames() {
		if !d.inbound[name] {
			continue
		}
		if s := d.named[name]; s.Enum != nil {
			g.enum(name, s)
		} else {
			g.object(name, s)
		}
	}

	var b bytes.Buffer
	b.WriteString(goHeader)
	if len(g.patterns) == 0 {
		b.WriteString("\nimport \"encoding/json\"\n")
	} else {
		b.WriteString("\nimport (\n\"encoding/json\"\n\"regexp\"\n)\n\nvar (\n")
		for _, p := range g.patterns {
			fmt.Fprintf(&b, "%s = regexp.MustCompile(%s)\n", p.name, goRawString(p.expr))
		}
		b.WriteString(")\n")
	}
	b.Write(g.b.Bytes())
	return format.Source(b.Bytes())
}

func (g *goDecoder) registry(fn string, messages []*message) {
	fmt.Fprintf(&g.b, "\nfunc %s(typ string, raw json.RawMessage, path string) (any, bool, error) {\nswitch typ {\n", fn)
	for _, m := range messages {
		fmt.Fprintf(&g.b, "case Request%s:\nv, err := decode%s(raw, path)\nreturn v, true, err\n", pascal(m.Type), m.Request.name)
	}
	g.b.WriteString("}\nreturn nil, false, nil\n}\n")
}

func (g *goDecoder) enum(name string, s *schema) {
	consts := make([]string, len(s.Enum))
	for i, v := range s.Enum {
		consts[i] = name + pascal(v)
	}
	reason := "取值应为 " + strings.Join(s.Enum, "、") + " 之一"
	fmt.Fprintf(&g.b, "\nfunc decode%s(raw json.RawMessage, path string) (%s, error) {\ns, err := decodeString(raw, path)\nif err != nil {\nreturn \"\", err\n}\nswitch v := %s(s); v {\ncase %s:\nreturn v, nil\n}\nreturn \"\", invalid(path, %q)\n}\n",
		name, name, name, strings.Join(consts, ", "), reason)
}

func (g *goDecoder) object(name string, s *schema) {
	fmt.Fprintf(&g.b, "\nfunc decode%s(raw json.RawMessage, path string) (%s, error) {\nvar v %s\n", name, name, name)
	fields := slices.Sorted(maps.Keys(s.Properties))
	if len(fields) == 0 {
		g.b.WriteString("_, err := decodeObject(raw, path)\nreturn v, err\n}\n")
		return
	}
	fmt.Fprintf(&g.b, "m, err := decodeObject(raw, path, %s)\nif err != nil {\nreturn v, err\n}\n", goStrings(fields))
	if len(s.Required) > 0 {
		fmt.Fprintf(&g.b, "if err = requireFields(m, path, %s); err != nil {\nreturn v, err\n}\n", goStrings(slices.Sorted(slices.Values(s.Required))))
	}
	if s.OneOf != nil {
		groups := make([]string, len(s.OneOf))
		reasons := make([]string, len(s.OneOf))
		for i, alt := range s.OneOf {
			groups[i] = "[]string{" + goStrings(alt.Required) + "}"
			reasons[i] = strings.Join(alt.Required, "、")
		}
		optional := "nil"
		if names := optionalFields(s.OneOf[0]); len(names) > 0 {
			optional = "[]string{" + goStrings(names) + "}"
		}
		fmt.Fprintf(&g.b, "if err = checkOneOf(m, path, %q, %s, %s); err != nil {\nreturn v, err\n}\n",
			"只能提供以下字段组合之一："+strings.Join(reasons, "；"), optional, strings.Join(groups, ", "))
	}
	for _, f := range fields {
		p := s.Properties[f]
		at := fmt.Sprintf("field(path, %q)", f)
		decode := "decodeString"
		switch {
		case p.Ref != "":
			decode = "decode" + refName(p.Ref)
		case p.Type == "integer":
			decode = "decodeInteger"
		}
		if slices.Contains(s.Required, f) {
			fmt.Fprintf(&g.b, "if v.%s, err = %s(m[%q], %s); err != nil {\nreturn v, err\n}\n", pascal(f), decode, f, at)
			g.checks("v."+pascal(f), at, p, name+pascal(f))
			continue
		}
		fmt.Fprintf(&g.b, "if r, ok := m[%q]; ok {\nx, err := %s(r, %s)\nif err != nil {\nreturn v, err\n}\n", f, decode, at)
		g.checks("x", at, p, name+pascal(f))
		fmt.Fprintf(&g.b, "v.%s = &x\n}\n", pascal(f))
	}
	for _, entry := range s.AllOf {
		cond := entry.If.Required[0]
		value := *entry.If.Properties[cond].Const
		fmt.Fprintf(&g.b, "if v.%s == %s%s {\n", pascal(cond), refName(s.Properties[cond].Ref), pascal(value))
		for _, f := range slices.Sorted(maps.Keys(entry.Then.Properties)) {
			g.checks("v."+pascal(f), fmt.Sprintf("field(path, %q)", f), entry.Then.Properties[f], name+pascal(f)+"When"+pascal(value))
		}
		g.b.WriteString("}\n")
	}
	g.b.WriteString("return v, nil\n}\n")
}

func (g *goDecoder) checks(value, at string, s *schema, patternName string) {
	check := func(fn, limit string) {
		fmt.Fprintf(&g.b, "if err = %s(%s, %s, %s); err != nil {\nreturn v, err\n}\n", fn, value, at, limit)
	}
	if s.MinLength != nil {
		check("checkMinLength", fmt.Sprint(*s.MinLength))
	}
	if s.MaxLength != nil {
		check("checkMaxLength", fmt.Sprint(*s.MaxLength))
	}
	if s.MaxBytes != nil {
		check("checkMaxBytes", fmt.Sprint(*s.MaxBytes))
	}
	if s.URL != nil {
		fmt.Fprintf(&g.b, "if err = checkURL(%s, %s); err != nil {\nreturn v, err\n}\n", value, at)
	}
	if s.Pattern != "" {
		name := "pattern" + patternName
		g.patterns = append(g.patterns, goPattern{name: name, expr: s.Pattern})
		check("checkPattern", name)
	}
	if s.Minimum != nil {
		check("checkMinimum", fmt.Sprint(*s.Minimum))
	}
	if s.Maximum != nil {
		check("checkMaximum", fmt.Sprint(*s.Maximum))
	}
}

func goStrings(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}

func goRawString(s string) string {
	if strings.Contains(s, "`") {
		return fmt.Sprintf("%q", s)
	}
	return "`" + s + "`"
}
