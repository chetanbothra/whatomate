package leadutil

import (
	"errors"
	"strings"
	"unicode"
)

var (
	// ErrInvalidPhone is returned when the phone number cannot be normalised or is invalid
	ErrInvalidPhone = errors.New("invalid phone number")

	// nationalLengths maps country code to standard national significant number length
	nationalLengths = map[string]int{
		"91":  10, // India
		"971": 9,  // UAE
		"1":   10, // US/Canada
		"44":  10, // UK
		"65":  8,  // Singapore
	}
)

// NormalizePhone normalises a raw phone string from Meta Instant Forms into digits-only with country code.
//
// Rules:
// 1. Strip everything except digits and leading '+'.
// 2. '+ ' prefix: drop the '+' and keep the rest as is.
// 3. '00' prefix: drop '00'.
// 4. If length == national length for defaultCC (e.g. 10 for 91): prepend defaultCC.
// 5. If length == national + 1 with a leading '0' (e.g. 09876543210): drop the '0', prepend defaultCC.
// 6. If already starts with defaultCC and length == cc + national: keep.
// 7. Final check: total digits must be between 10 and 15. For 91, the first national digit must be 6-9.
func NormalizePhone(raw, defaultCC string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrInvalidPhone
	}

	defaultCC = strings.TrimPrefix(defaultCC, "+")

	// Keep only digits and leading '+'
	var b strings.Builder
	for i, r := range raw {
		if r == '+' && i == 0 {
			b.WriteRune(r)
		} else if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" {
		return "", ErrInvalidPhone
	}

	// 2. '+' prefix: drop '+'
	if strings.HasPrefix(s, "+") {
		s = s[1:]
	} else if strings.HasPrefix(s, "00") {
		// 3. '00' prefix: drop 00
		s = s[2:]
	}

	natLen, hasNatLen := nationalLengths[defaultCC]
	if hasNatLen {
		// 4. Length == national length for defaultCC: prepend defaultCC
		if len(s) == natLen {
			s = defaultCC + s
		} else if len(s) == natLen+1 && strings.HasPrefix(s, "0") {
			// 5. Length == national + 1 with leading 0: drop 0, prepend defaultCC
			s = defaultCC + s[1:]
		} else if strings.HasPrefix(s, defaultCC) && len(s) == len(defaultCC)+natLen {
			// 6. Already starts with defaultCC and length == cc + national: keep
		}
	}

	// 7. Final checks:
	// Total length must be 10-15 digits
	if len(s) < 10 || len(s) > 15 {
		return "", ErrInvalidPhone
	}

	// For country code 91, first national digit must be 6, 7, 8, or 9
	if strings.HasPrefix(s, "91") && len(s) == 12 {
		firstNatDigit := s[2]
		if firstNatDigit < '6' || firstNatDigit > '9' {
			return "", ErrInvalidPhone
		}
	}

	return s, nil
}
