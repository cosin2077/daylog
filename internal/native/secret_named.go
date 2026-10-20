package native

import (
	"errors"
	"regexp"
	"strings"
)

var secretName = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,160}$`)

func validateNamedSecret(name, value string) error {
	if !secretName.MatchString(name) {
		return errors.New("凭证名称无效")
	}
	if value != "" && (len(value) > 2560 || strings.ContainsAny(value, "\x00\r\n")) {
		return errors.New("凭证须为不超过 2560 字节的单行文本")
	}
	return nil
}
