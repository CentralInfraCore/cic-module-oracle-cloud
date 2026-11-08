package main

// canonical_json.go implements cic-canonical-json/1, the one definition of "these two
// JSON texts are the same value" used by this module. The rules, limits and the
// reasons for them are in docs/design/specs/canonical-json.md; the expected results
// are in testdata/canonical_json_vectors.json. Go is one implementation of that
// specification, not the definition of it.
//
// Why not encoding/json: it reads every number as a float64 (two different integers
// above 2^53 compare equal), keeps only the last of a duplicated key, and turns an
// invalid UTF-8 byte or a lone surrogate escape into U+FFFD, so distinct texts could
// collapse into one. A decision about whether a plan has any work to do, or whether
// two configurations are the same, must not rest on that.

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Bounds. They keep the canonical form cheap and the recursion shallow whatever a
// caller sends; an input beyond them is refused, never truncated.
const (
	canonMaxInput  = 1 << 20 // bytes of input text
	canonMaxDepth  = 64      // nesting of arrays and objects
	canonMaxDigits = 1000    // digits in a number's integer and fraction parts
	canonMaxExpLen = 6       // digits in a number's written exponent
	canonMaxExp    = 10000   // |exponent| of the normalized number
)

type canonMember struct {
	key string // decoded key, UTF-8
	val []byte // canonical form of the value
}

type canonParser struct {
	src []byte
	pos int
}

// canonicalJSON returns the cic-canonical-json/1 form of one JSON text, or an error
// when the text is not strict JSON (RFC 8259) or exceeds a bound. Errors carry the
// byte offset and a reason, never the offending text.
func canonicalJSON(raw []byte) ([]byte, error) {
	if len(raw) > canonMaxInput {
		return nil, fmt.Errorf("json text is %d bytes, over the %d byte limit", len(raw), canonMaxInput)
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("json text is not valid UTF-8")
	}
	p := &canonParser{src: raw}
	p.skipSpace()
	out, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.src) {
		return nil, p.fail("unexpected data after the value")
	}
	return out, nil
}

func (p *canonParser) fail(why string) error {
	return fmt.Errorf("json: %s at byte %d", why, p.pos)
}

func (p *canonParser) skipSpace() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *canonParser) value(depth int) ([]byte, error) {
	if p.pos >= len(p.src) {
		return nil, p.fail("a value is missing")
	}
	switch c := p.src[p.pos]; {
	case c == '{':
		return p.object(depth + 1)
	case c == '[':
		return p.array(depth + 1)
	case c == '"':
		s, err := p.str()
		if err != nil {
			return nil, err
		}
		return canonString(nil, s), nil
	case c == 't':
		return p.literal("true")
	case c == 'f':
		return p.literal("false")
	case c == 'n':
		return p.literal("null")
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return nil, p.fail("an unexpected character")
}

func (p *canonParser) literal(word string) ([]byte, error) {
	if len(p.src)-p.pos < len(word) || string(p.src[p.pos:p.pos+len(word)]) != word {
		return nil, p.fail("an invalid literal")
	}
	p.pos += len(word)
	return []byte(word), nil
}

func (p *canonParser) array(depth int) ([]byte, error) {
	if depth > canonMaxDepth {
		return nil, p.fail("nesting is deeper than the limit")
	}
	p.pos++ // [
	out := []byte{'['}
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == ']' {
		p.pos++
		return append(out, ']'), nil
	}
	for first := true; ; first = false {
		if !first {
			out = append(out, ',')
		}
		p.skipSpace()
		v, err := p.value(depth)
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
		p.skipSpace()
		if p.pos >= len(p.src) {
			return nil, p.fail("an array is not closed")
		}
		switch p.src[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return append(out, ']'), nil
		default:
			return nil, p.fail("expected , or ] in an array")
		}
	}
}

func (p *canonParser) object(depth int) ([]byte, error) {
	if depth > canonMaxDepth {
		return nil, p.fail("nesting is deeper than the limit")
	}
	p.pos++ // {
	var members []canonMember
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '}' {
		p.pos++
		return []byte("{}"), nil
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.src) || p.src[p.pos] != '"' {
			return nil, p.fail("an object key must be a string")
		}
		key, err := p.str()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos >= len(p.src) || p.src[p.pos] != ':' {
			return nil, p.fail("expected : after an object key")
		}
		p.pos++
		p.skipSpace()
		v, err := p.value(depth)
		if err != nil {
			return nil, err
		}
		members = append(members, canonMember{key: string(key), val: v})
		p.skipSpace()
		if p.pos >= len(p.src) {
			return nil, p.fail("an object is not closed")
		}
		if p.src[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.src[p.pos] != '}' {
			return nil, p.fail("expected , or } in an object")
		}
		p.pos++
		break
	}
	// Byte order of the decoded keys; a repeated key is refused, never merged.
	sort.Slice(members, func(i, j int) bool { return members[i].key < members[j].key })
	for i := 1; i < len(members); i++ {
		if members[i].key == members[i-1].key {
			return nil, errors.New("json: an object has a duplicate key")
		}
	}
	out := []byte{'{'}
	for i, m := range members {
		if i > 0 {
			out = append(out, ',')
		}
		out = canonString(out, []byte(m.key))
		out = append(out, ':')
		out = append(out, m.val...)
	}
	return append(out, '}'), nil
}

// str reads a JSON string at p.pos (on the opening quote) and returns it decoded.
func (p *canonParser) str() ([]byte, error) {
	p.pos++ // opening quote
	var out []byte
	for {
		if p.pos >= len(p.src) {
			return nil, p.fail("a string is not closed")
		}
		c := p.src[p.pos]
		switch {
		case c == '"':
			p.pos++
			return out, nil
		case c < 0x20:
			return nil, p.fail("a raw control character in a string")
		case c != '\\':
			out = append(out, c)
			p.pos++
			continue
		}
		p.pos++ // backslash
		if p.pos >= len(p.src) {
			return nil, p.fail("a string is not closed")
		}
		e := p.src[p.pos]
		p.pos++
		switch e {
		case '"', '\\', '/':
			out = append(out, e)
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, err := p.hex4()
			if err != nil {
				return nil, err
			}
			switch {
			case r >= 0xD800 && r <= 0xDBFF:
				if p.pos+1 >= len(p.src) || p.src[p.pos] != '\\' || p.src[p.pos+1] != 'u' {
					return nil, p.fail("a high surrogate escape without a low surrogate")
				}
				p.pos += 2
				lo, err := p.hex4()
				if err != nil {
					return nil, err
				}
				if lo < 0xDC00 || lo > 0xDFFF {
					return nil, p.fail("a high surrogate escape without a low surrogate")
				}
				r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
			case r >= 0xDC00 && r <= 0xDFFF:
				return nil, p.fail("a lone low surrogate escape")
			}
			var buf [4]byte
			n := utf8.EncodeRune(buf[:], r)
			out = append(out, buf[:n]...)
		default:
			return nil, p.fail("an invalid escape in a string")
		}
	}
}

func (p *canonParser) hex4() (rune, error) {
	if p.pos+4 > len(p.src) {
		return 0, p.fail("a short \\u escape")
	}
	var r rune
	for i := 0; i < 4; i++ {
		c := p.src[p.pos+i]
		var d rune
		switch {
		case c >= '0' && c <= '9':
			d = rune(c - '0')
		case c >= 'a' && c <= 'f':
			d = rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = rune(c-'A') + 10
		default:
			return 0, p.fail("a bad \\u escape")
		}
		r = r<<4 | d
	}
	p.pos += 4
	return r, nil
}

// canonString appends s (UTF-8) as a JSON string: only the quote, the backslash and
// the characters below 0x20 are escaped (the latter as \u00xx in lower case); every
// other character is written as it is, with no HTML escaping.
func canonString(out []byte, s []byte) []byte {
	const hexdigits = "0123456789abcdef"
	out = append(out, '"')
	for _, c := range s {
		switch {
		case c == '"':
			out = append(out, '\\', '"')
		case c == '\\':
			out = append(out, '\\', '\\')
		case c < 0x20:
			out = append(out, '\\', 'u', '0', '0', hexdigits[c>>4], hexdigits[c&0xf])
		default:
			out = append(out, c)
		}
	}
	return append(out, '"')
}

// number reads a JSON number (RFC 8259 grammar) and returns its exact value as
// <digits>e<exponent>, with no float64 involved: the significant digits without
// leading or trailing zeros, and the power of ten that scales them. Zero, whatever
// its spelling (-0, 0.0, 0e5), is "0".
func (p *canonParser) number() ([]byte, error) {
	neg := false
	if p.src[p.pos] == '-' {
		neg = true
		p.pos++
	}
	start := p.pos
	if p.pos >= len(p.src) {
		return nil, p.fail("a number without digits")
	}
	switch c := p.src[p.pos]; {
	case c == '0':
		p.pos++
	case c >= '1' && c <= '9':
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
	default:
		return nil, p.fail("a number without digits")
	}
	intPart := p.src[start:p.pos]
	var frac []byte
	if p.pos < len(p.src) && p.src[p.pos] == '.' {
		p.pos++
		fs := p.pos
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
		if p.pos == fs {
			return nil, p.fail("a fraction without digits")
		}
		frac = p.src[fs:p.pos]
	}
	exp := 0
	if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		p.pos++
		eneg := false
		if p.pos < len(p.src) && (p.src[p.pos] == '+' || p.src[p.pos] == '-') {
			eneg = p.src[p.pos] == '-'
			p.pos++
		}
		es := p.pos
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
		if p.pos == es {
			return nil, p.fail("an exponent without digits")
		}
		if p.pos-es > canonMaxExpLen {
			return nil, p.fail("an exponent with too many digits")
		}
		exp, _ = strconv.Atoi(string(p.src[es:p.pos]))
		if eneg {
			exp = -exp
		}
	}
	if len(intPart)+len(frac) > canonMaxDigits {
		return nil, p.fail("a number with too many digits")
	}
	digits := append(append([]byte{}, intPart...), frac...)
	exp -= len(frac)
	i := 0
	for i < len(digits) && digits[i] == '0' {
		i++
	}
	digits = digits[i:]
	if len(digits) == 0 {
		return []byte("0"), nil
	}
	for len(digits) > 0 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
		exp++
	}
	if exp > canonMaxExp || exp < -canonMaxExp {
		return nil, p.fail("a number outside the exponent limit")
	}
	out := make([]byte, 0, len(digits)+8)
	if neg {
		out = append(out, '-')
	}
	out = append(out, digits...)
	out = append(out, 'e')
	return strconv.AppendInt(out, int64(exp), 10), nil
}
