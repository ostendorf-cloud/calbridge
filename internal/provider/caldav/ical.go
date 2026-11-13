// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package caldav

import (
	"fmt"
	"strings"
)

// This file holds the iCalendar stream format of RFC 5545 section 3.1:
// content lines, their folding, their parameters and the escaping of a
// text value. It knows nothing about calendars, which is what keeps the
// event mapping in event.go readable.

// icalLine is one content line: a property name, its parameters and its
// value, all unescaped.
type icalLine struct {
	Name   string
	Params map[string][]string
	Value  string
}

// param returns the first value of a parameter, empty where the line
// carries none.
func (l icalLine) param(name string) string {
	values := l.Params[strings.ToUpper(name)]
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

// icalComponent is one BEGIN/END block with its properties and the
// components nested inside it.
type icalComponent struct {
	Name     string
	Props    []icalLine
	Children []*icalComponent
}

// prop returns the first property with the given name, and whether the
// component carries one at all.
func (c *icalComponent) prop(name string) (icalLine, bool) {
	for _, p := range c.Props {
		if p.Name == name {
			return p, true
		}
	}

	return icalLine{}, false
}

// props returns every property with the given name, in order.
func (c *icalComponent) props(name string) []icalLine {
	var out []icalLine
	for _, p := range c.Props {
		if p.Name == name {
			out = append(out, p)
		}
	}

	return out
}

// value returns the value of the first property with the given name.
func (c *icalComponent) value(name string) string {
	p, _ := c.prop(name)

	return p.Value
}

// children returns the nested components with the given name.
func (c *icalComponent) children(name string) []*icalComponent {
	var out []*icalComponent
	for _, child := range c.Children {
		if child.Name == name {
			out = append(out, child)
		}
	}

	return out
}

// parseICalendar reads a stream into its top level component. RFC 5545
// section 3.4 makes that one VCALENDAR.
func parseICalendar(text string) (*icalComponent, error) {
	lines, err := contentLines(text)
	if err != nil {
		return nil, err
	}

	var root *icalComponent
	var stack []*icalComponent
	for _, line := range lines {
		switch line.Name {
		case "BEGIN":
			component := &icalComponent{Name: strings.ToUpper(line.Value)}
			switch {
			case len(stack) > 0:
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, component)
			case root != nil:
				return nil, fmt.Errorf("icalendar holds a second top level %s: %w", component.Name, ErrServer)
			default:
				root = component
			}
			stack = append(stack, component)
		case "END":
			if len(stack) == 0 {
				return nil, fmt.Errorf("icalendar ends %q that never began: %w", line.Value, ErrServer)
			}
			if open := stack[len(stack)-1]; !strings.EqualFold(open.Name, line.Value) {
				return nil, fmt.Errorf("icalendar ends %q inside %s: %w", line.Value, open.Name, ErrServer)
			}
			stack = stack[:len(stack)-1]
		default:
			if len(stack) == 0 {
				return nil, fmt.Errorf("icalendar property %s sits outside a component: %w", line.Name, ErrServer)
			}
			open := stack[len(stack)-1]
			open.Props = append(open.Props, line)
		}
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("icalendar leaves %s open: %w", stack[len(stack)-1].Name, ErrServer)
	}
	if root == nil {
		return nil, fmt.Errorf("icalendar holds no component: %w", ErrServer)
	}

	return root, nil
}

// contentLines unfolds the stream and parses every line. RFC 5545
// section 3.1 folds a long line by inserting a line break and one space
// or tab, and unfolding removes exactly that break and that character.
func contentLines(text string) ([]icalLine, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	var out []icalLine
	var current strings.Builder
	flush := func() error {
		raw := current.String()
		current.Reset()
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		line, err := parseContentLine(raw)
		if err != nil {
			return err
		}
		out = append(out, line)

		return nil
	}

	for _, raw := range strings.Split(text, "\n") {
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			current.WriteString(raw[1:])

			continue
		}
		if err := flush(); err != nil {
			return nil, err
		}
		current.WriteString(raw)
	}
	if err := flush(); err != nil {
		return nil, err
	}

	return out, nil
}

// parseContentLine splits one unfolded line into its name, parameters
// and value.
func parseContentLine(raw string) (icalLine, error) {
	name, rest, err := splitName(raw)
	if err != nil {
		return icalLine{}, err
	}

	line := icalLine{Name: name, Params: map[string][]string{}}
	for strings.HasPrefix(rest, ";") {
		var param string
		param, rest = splitParam(rest[1:])
		key, value, found := strings.Cut(param, "=")
		if !found {
			return icalLine{}, fmt.Errorf("icalendar parameter %q on %s carries no value: %w", param, name, ErrServer)
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
			// A quoted value is one value, even where it holds the comma
			// that separates two unquoted ones.
			line.Params[key] = append(line.Params[key], value[1:len(value)-1])

			continue
		}
		line.Params[key] = append(line.Params[key], strings.Split(value, ",")...)
	}
	if !strings.HasPrefix(rest, ":") {
		return icalLine{}, fmt.Errorf("icalendar line %s carries no value: %w", name, ErrServer)
	}
	line.Value = unescapeText(rest[1:])

	return line, nil
}

// splitName reads the property name off the front of a content line.
func splitName(raw string) (name, rest string, err error) {
	for i, r := range raw {
		if r == ':' || r == ';' {
			if i == 0 {
				return "", "", fmt.Errorf("icalendar line carries no name: %w", ErrServer)
			}

			return strings.ToUpper(raw[:i]), raw[i:], nil
		}
	}

	return "", "", fmt.Errorf("icalendar line carries no name: %w", ErrServer)
}

// splitParam reads one parameter off the front, honouring the quoted
// form RFC 5545 section 3.1 allows for a value holding a colon or a
// semicolon.
func splitParam(raw string) (param, rest string) {
	quoted := false
	for i, r := range raw {
		switch {
		case r == '"':
			quoted = !quoted
		case (r == ':' || r == ';') && !quoted:
			return raw[:i], raw[i:]
		}
	}

	return raw, ""
}

// unescapeText reverses the escaping RFC 5545 section 3.3.11 defines for
// a text value.
func unescapeText(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}

	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 >= len(value) {
			out.WriteByte(value[i])

			continue
		}
		i++
		switch value[i] {
		case 'n', 'N':
			out.WriteByte('\n')
		case '\\', ';', ',':
			out.WriteByte(value[i])
		default:
			out.WriteByte('\\')
			out.WriteByte(value[i])
		}
	}

	return out.String()
}

// escapeText applies the escaping RFC 5545 section 3.3.11 defines.
func escapeText(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		";", `\;`,
		",", `\,`,
		"\r\n", `\n`,
		"\n", `\n`,
		"\r", `\n`,
	)

	return replacer.Replace(value)
}

// icalWriter builds an iCalendar stream.
type icalWriter struct {
	out strings.Builder
}

// begin opens a component.
func (w *icalWriter) begin(name string) { w.line("BEGIN", nil, name, false) }

// end closes a component.
func (w *icalWriter) end(name string) { w.line("END", nil, name, false) }

// text writes a property whose value is escaped as RFC 5545 text.
func (w *icalWriter) text(name, value string) {
	w.line(name, nil, value, true)
}

// raw writes a property whose value is already in its wire form, such as
// a date-time or a recurrence rule.
func (w *icalWriter) raw(name string, params []string, value string) {
	w.line(name, params, value, false)
}

// line writes one content line, folded to the 75 octet limit RFC 5545
// section 3.1 sets.
func (w *icalWriter) line(name string, params []string, value string, escape bool) {
	var b strings.Builder
	b.WriteString(name)
	for _, p := range params {
		b.WriteString(";")
		b.WriteString(p)
	}
	b.WriteString(":")
	if escape {
		b.WriteString(escapeText(value))
	} else {
		b.WriteString(value)
	}
	w.out.WriteString(fold(b.String()))
}

// String returns the stream built so far.
func (w *icalWriter) String() string { return w.out.String() }

// maxLineOctets is the limit RFC 5545 section 3.1 sets on a content
// line, counted in octets and excluding the line break.
const maxLineOctets = 75

// fold breaks a content line at the octet limit and terminates it. A
// break never falls inside a multi-byte character, because a continuation
// is read as raw octets after the leading space is removed.
func fold(line string) string {
	var out strings.Builder
	remaining := line
	first := true
	for len(remaining) > maxLineOctets {
		cut := maxLineOctets
		if !first {
			// A continuation line spends one octet on its leading space.
			cut = maxLineOctets - 1
		}
		for cut > 1 && isContinuationByte(remaining[cut]) {
			cut--
		}
		if !first {
			out.WriteString(" ")
		}
		out.WriteString(remaining[:cut])
		out.WriteString("\r\n")
		remaining = remaining[cut:]
		first = false
	}
	if !first {
		out.WriteString(" ")
	}
	out.WriteString(remaining)
	out.WriteString("\r\n")

	return out.String()
}

// isContinuationByte reports whether the byte is the middle of a UTF-8
// sequence, which a fold must not split.
func isContinuationByte(b byte) bool { return b&0xC0 == 0x80 }

// quoteParam returns a parameter value in the quoted form where it holds
// a character the unquoted form cannot carry.
func quoteParam(value string) string {
	if strings.ContainsAny(value, `:;,"`) {
		return `"` + strings.ReplaceAll(value, `"`, "") + `"`
	}

	return value
}
