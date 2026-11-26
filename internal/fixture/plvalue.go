package fixture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The fixture builder reads a trigger the way the ladder pass's first
// builder did, so a trigger builds the same fixture under either. A JSON
// value decodes to a map[string]any, []any, string, json.Number, bool or
// nil. Numbers keep their text, and str, truthy and encodeCanonical read
// them as that builder read them: an integer of up to 20 characters, sign
// included, stays a number, a longer one becomes a string, and any other
// number is a float written with 15 significant digits.

// errNumberRange is a number the float type cannot hold.
var errNumberRange = errors.New("a number is out of range")

// decodeJSON decodes exactly one JSON value of valid UTF-8, nested at most
// maxJSONDepth deep.
func decodeJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("not valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("data after the JSON value")
	}
	if depth(v, 0) > maxJSONDepth {
		return nil, fmt.Errorf("nested over %d deep", maxJSONDepth)
	}
	if loneSurrogate(data) {
		return nil, errors.New("a lone surrogate escape")
	}
	return v, nil
}

// loneSurrogate reports a \u escape of a surrogate that is not one half of
// a pair, which the first builder refused and Go's decoder would turn into
// U+FFFD. data is valid JSON, so only string text holds a backslash.
func loneSurrogate(data []byte) bool {
	hex4 := func(i int) (rune, bool) {
		if i+6 > len(data) || data[i] != '\\' || data[i+1] != 'u' {
			return 0, false
		}
		n, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
		return rune(n), err == nil
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		r, ok := hex4(i)
		if !ok {
			i++ // another escape: skip the escaped byte
			continue
		}
		switch {
		case r >= 0xD800 && r <= 0xDBFF:
			lo, ok := hex4(i + 6)
			if !ok || lo < 0xDC00 || lo > 0xDFFF {
				return true
			}
			i += 11
		case r >= 0xDC00 && r <= 0xDFFF:
			return true
		default:
			i += 5
		}
	}
	return false
}

// depth is the nesting of arrays and objects in v, where v sits at d.
func depth(v any, d int) int {
	most := d
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			most = max(most, depth(e, d+1))
		}
		most = max(most, d+1)
	case map[string]any:
		for _, e := range x {
			most = max(most, depth(e, d+1))
		}
		most = max(most, d+1)
	}
	return most
}

// maxIntChars is the longest integer text, sign included, that stays a
// number.
const maxIntChars = 20

// maxJSONDepth is the deepest nesting of arrays and objects a trigger may
// have.
const maxJSONDepth = 512

// number is the canonical text of a JSON number and whether it stays a
// number; a long integer becomes a string with its text.
func number(n json.Number) (string, bool, error) {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		if len(s) > maxIntChars {
			return s, false, nil
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return strconv.FormatInt(i, 10), true, nil
		}
		if u, err := strconv.ParseUint(s, 10, 64); err == nil {
			return strconv.FormatUint(u, 10), true, nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return "", false, errNumberRange
	}
	if f == 0 {
		return "0", true, nil
	}
	return strconv.FormatFloat(f, 'g', 15, 64), true, nil
}

// scalar reports whether v is a plain value: a string or a number. A
// boolean, array or object is a reference, as it was to the first builder.
func scalar(v any) bool {
	switch v.(type) {
	case string, json.Number:
		return true
	}
	return false
}

// str is the text of a scalar v and whether v is one.
func str(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case json.Number:
		s, _, err := number(x)
		if err != nil {
			return string(x), true
		}
		return s, true
	}
	return "", false
}

// truthy is the truth of a JSON value: false for null, false, "", "0" and
// a number equal to 0; true for anything else, an empty array included.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != "" && x != "0"
	case json.Number:
		s, isNum, err := number(x)
		if err != nil {
			return true
		}
		if !isNum {
			return s != "" && s != "0"
		}
		return s != "0"
	}
	return true
}

// encodeCanonical writes v as compact JSON with object keys sorted.
func encodeCanonical(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := encodeValue(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func encodeValue(b *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case string:
		encodeString(b, x)
	case int:
		b.WriteString(strconv.Itoa(x))
	case json.Number:
		s, isNum, err := number(x)
		if err != nil {
			return err
		}
		if isNum {
			b.WriteString(s)
		} else {
			encodeString(b, s)
		}
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := encodeValue(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case []string:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeString(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		b.WriteByte('{')
		for i, k := range slices.Sorted(maps.Keys(x)) {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeString(b, k)
			b.WriteByte(':')
			if err := encodeValue(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("cannot encode %T", v)
	}
	return nil
}

// encodeString escapes only the quote, the backslash and control bytes,
// and writes every other byte as it is.
func encodeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := range len(s) {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\f':
			b.WriteString(`\f`)
		case '\b':
			b.WriteString(`\b`)
		default:
			if c < 0x20 {
				fmt.Fprintf(b, `\u%04x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}
