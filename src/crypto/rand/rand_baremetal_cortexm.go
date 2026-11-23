//go:build baremetal && cortexm && !qemu && platformhooks

package rand

import "errors"

func init() {
	Reader = &reader{}
}

type reader struct{}

func (r *reader) Read(b []byte) (int, error) {
	return 0, errors.New("crypto/rand: not implemented on baremetal")
}
