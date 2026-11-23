//go:build baremetal && cortexm && !qemu && platformhooks

package runtime

func platformInit0()
func platformInit1()
func platformTicks() uint64
func platformTickNS() uint64
func platformSleep(ticks uint64)
func putchar(c byte)
func getchar() byte
func buffered() int

func ticks() timeUnit {
	return timeUnit(platformTicks())
}

func ticksToNanoseconds(t timeUnit) int64 {
	return int64(t) * int64(platformTickNS())
}

func nanosecondsToTicks(ns int64) timeUnit {
	if ns <= 0 {
		return 0
	}
	return timeUnit(ns / int64(platformTickNS()))
}

func sleepTicks(d timeUnit) {
	if d <= 0 {
		return
	}
	platformSleep(uint64(d))
}
