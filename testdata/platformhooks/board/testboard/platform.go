package testboard

import (
	"device/arm"
	"runtime/volatile"
	_ "unsafe"
)

var (
	init0Done    volatile.Register32
	consoleReady volatile.Register32
)

//go:linkname platformInit0 runtime.platformInit0
//go:noheap
func platformInit0() {
	init0Done.Set(1)
}

//go:linkname platformInit1 runtime.platformInit1
func platformInit1() {
	consoleReady.Set(1)
}

//go:linkname platformTicks runtime.platformTicks
func platformTicks() uint64 { return 1 }

//go:linkname platformTickNS runtime.platformTickNS
func platformTickNS() uint64 { return 1000 }

//go:linkname platformSleep runtime.platformSleep
func platformSleep(ticks uint64) { arm.Asm("wfi") }

//go:linkname putchar runtime.putchar
func putchar(c byte) {
	if consoleReady.Get() == 0 {
		return
	}
	_ = c
}

//go:linkname getchar runtime.getchar
func getchar() byte { return 0 }

//go:linkname buffered runtime.buffered
func buffered() int { return 0 }
