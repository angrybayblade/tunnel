package proxy

import (
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
)

const ApiKeyLen int = 43
const MAX_CONNECTION_POOL_SIZE int = 5

func createSesssionKey(key []byte) string {
	s := sha256.New()
	s.Write(key)
	return strings.ToLower(base64.URLEncoding.EncodeToString(s.Sum(nil))[:ApiKeyLen])
}

type Addr struct {
	host string
	port int
}

func (a *Addr) ToString() string {
	return a.host + ":" + strconv.Itoa(a.port)
}
