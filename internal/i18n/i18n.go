// Package i18n picks Chinese or English for user-facing messages.
//
// Order: MGEN_LANG, then LC_ALL, LC_MESSAGES, LANG. Anything starting with
// "zh" means Chinese; everything else (including unset) means English.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

// Zh reports whether messages should be in Chinese.
var Zh = detect()

func detect() bool {
	for _, k := range []string{"MGEN_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return strings.HasPrefix(strings.ToLower(v), "zh")
		}
	}
	return false
}

// T returns zh or en depending on the current language.
func T(zh, en string) string {
	if Zh {
		return zh
	}
	return en
}

// Tf is T followed by fmt.Sprintf.
func Tf(zh, en string, args ...any) string {
	return fmt.Sprintf(T(zh, en), args...)
}

// Errorf builds an error with a localized message.
func Errorf(zh, en string, args ...any) error {
	return fmt.Errorf("%s", Tf(zh, en, args...))
}
