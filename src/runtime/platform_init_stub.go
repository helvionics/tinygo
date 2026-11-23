//go:build !(baremetal && cortexm && !qemu && platformhooks)

package runtime

func platformInit0() {}
func platformInit1() {}
