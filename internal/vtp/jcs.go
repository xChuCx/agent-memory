package vtp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// CanonicalizeRFC8785 produces a canonical JSON byte slice conforming to RFC 8785 (JCS).
// It supports UTF-16 code unit property ordering, ECMAScript number serialization,
// and strict JSON whitespace and escaping rules.
func CanonicalizeRFC8785(v any) ([]byte, error) {
	if err := validateNoLoneSurrogates(v); err != nil {
		return nil, err
	}

	// First round-trip through standard json.Marshal to honor json struct tags, omitempty, etc.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := formatJCS(&buf, generic); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func validateNoLoneSurrogates(v any) error {
	if v == nil {
		return nil
	}
	return validateReflectValue(reflect.ValueOf(v))
}

func validateReflectValue(val reflect.Value) error {
	switch val.Kind() {
	case reflect.String:
		s := val.String()
		if !utf8.ValidString(s) {
			return errors.New("JCS: invalid UTF-8 sequence")
		}
		for _, r := range s {
			if r >= 0xD800 && r <= 0xDFFF {
				return fmt.Errorf("JCS: lone surrogate U+%04X not allowed", r)
			}
			if r == utf8.RuneError {
				return errors.New("JCS: invalid Unicode character in string")
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < val.Len(); i++ {
			if err := validateReflectValue(val.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		for _, k := range val.MapKeys() {
			if err := validateReflectValue(k); err != nil {
				return err
			}
			if err := validateReflectValue(val.MapIndex(k)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			if val.Type().Field(i).PkgPath != "" {
				// unexported field
				continue
			}
			if err := validateReflectValue(field); err != nil {
				return err
			}
		}
	case reflect.Pointer, reflect.Interface:
		if !val.IsNil() {
			return validateReflectValue(val.Elem())
		}
	}
	return nil
}

func formatJCS(buf *bytes.Buffer, v any) error {
	if v == nil {
		buf.WriteString("null")
		return nil
	}

	switch val := v.(type) {
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		if err := formatJCSString(buf, val); err != nil {
			return err
		}
	case json.Number:
		if err := formatJCSNumber(buf, val); err != nil {
			return err
		}
	case float64:
		formatted, err := FormatJCSFloat(val)
		if err != nil {
			return err
		}
		buf.WriteString(formatted)
	case int, int8, int16, int32, int64:
		buf.WriteString(fmt.Sprintf("%d", val))
	case uint, uint8, uint16, uint32, uint64:
		buf.WriteString(fmt.Sprintf("%d", val))
	case []any:
		buf.WriteByte('[')
		for i, elem := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := formatJCS(buf, elem); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		buf.WriteByte('{')
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		// RFC 8785 Section 3.2.3: sort keys by UTF-16 code units
		sort.Slice(keys, func(i, j int) bool {
			return compareUTF16(keys[i], keys[j]) < 0
		})
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := formatJCSString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := formatJCS(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("JCS: unsupported type %T", v)
	}
	return nil
}

// formatJCSString serializes a string according to RFC 8785 Section 3.2.2.2.
func formatJCSString(buf *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return errors.New("JCS: invalid UTF-8 string")
	}
	buf.WriteByte('"')
	for _, r := range s {
		if r >= 0xD800 && r <= 0xDFFF {
			return fmt.Errorf("JCS: lone surrogate U+%04X not allowed", r)
		}
		if r == utf8.RuneError {
			return errors.New("JCS: invalid Unicode character in string")
		}
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				buf.WriteString(fmt.Sprintf(`\u%04x`, r))
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
	return nil
}

// formatJCSNumber serializes numbers per RFC 8785 Section 3.2.2.3.
func formatJCSNumber(buf *bytes.Buffer, num json.Number) error {
	s := num.String()
	f, err := num.Float64()
	if err != nil {
		return fmt.Errorf("JCS: invalid number %q: %w", s, err)
	}
	formatted, err := FormatJCSFloat(f)
	if err != nil {
		return err
	}
	buf.WriteString(formatted)
	return nil
}

// FormatJCSFloat formats an IEEE-754 double precision float according to ECMAScript 7.1.12.1 / RFC 8785.
func FormatJCSFloat(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", errors.New("JCS: NaN and Infinity are not permitted in JSON")
	}
	if f == 0 {
		return "0", nil
	}

	isNeg := math.Signbit(f)
	abs := math.Abs(f)

	var res string
	if abs >= 1e-6 && abs < 1e21 {
		// Decimal representation per ECMAScript 7.1.12.1
		res = strconv.FormatFloat(abs, 'f', -1, 64)
	} else {
		// Exponential representation per ECMAScript 7.1.12.1
		expStr := strconv.FormatFloat(abs, 'e', -1, 64)
		eIdx := strings.IndexByte(expStr, 'e')
		significand := expStr[:eIdx]
		expPart := expStr[eIdx+1:]
		expSign := expPart[0] // '+' or '-'
		expDigits := strings.TrimLeft(expPart[1:], "0")
		if expDigits == "" {
			expDigits = "0"
		}
		res = significand + "e" + string(expSign) + expDigits
	}

	if isNeg {
		return "-" + res, nil
	}
	return res, nil
}

// compareUTF16 compares two strings by UTF-16 code units per RFC 8785 Section 3.2.3.
func compareUTF16(a, b string) int {
	uA := utf16.Encode([]rune(a))
	uB := utf16.Encode([]rune(b))
	minLen := len(uA)
	if len(uB) < minLen {
		minLen = len(uB)
	}
	for i := 0; i < minLen; i++ {
		if uA[i] < uB[i] {
			return -1
		}
		if uA[i] > uB[i] {
			return 1
		}
	}
	if len(uA) < len(uB) {
		return -1
	}
	if len(uA) > len(uB) {
		return 1
	}
	return 0
}
