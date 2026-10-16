package service

import (
	"crypto/sha256"
	"fmt"
)

func Label(root string) string {
	hash := sha256.Sum256([]byte(root))
	return fmt.Sprintf("io.daylog.agent.%x", hash[:4])
}
