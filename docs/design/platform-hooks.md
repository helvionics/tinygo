# Out-of-Tree Platform Hooks and Target Modules

## Overview and Goals

This document defines a minimal TinyGo fork architecture that splits the compiler, SoC support, and board definitions into independent layers.

1. **Compiler, runtime, stdlib, and CPU architecture bases** (`targets/cortex-m*.json`, `src/device/arm/`) stay in the TinyGo fork.
2. **SoC HAL modules** (`github.com/helvionics/soc`, containing SVD register definitions, startup assembly, linker scripts, clock and peripheral drivers) live in separate Go modules.
3. **Board modules** (`github.com/helvionics/board`, containing pin assignments, power sequencing, external memory, peripheral instances, board `target.json`) live in separate Go modules.
4. **Demo and user applications** (`github.com/helvionics/demo`) live in normal Go modules and select a board via `-target`.

The PoC keeps all non-Cortex-M targets in-tree untouched (ESP32, RISC-V, AVR, GBA, Kendryte, SiFive, WASM/WASI, and synthesized hosted OS targets, along with `src/machine` and non-Cortex-M `src/device/*` / `src/runtime/*` files), and deletes Cortex-M chip-specific `src/device/<chip>` packages (keeping `src/device/arm` for `cortexm` arch runtime files), Cortex-M chip-specific `src/runtime/runtime_<chip>.go` / `time_nxpmk66f18.go` files, and Cortex-M board `targets/*.json` / linker files while keeping CPU architecture base targets (`targets/cortex-m*.json`) and `targets/cortex-m-qemu.json` (plus `targets/cortex-m-qemu.s`, `targets/lm3s6965.ld`, and `targets/arm.ld`). Cortex-M baremetal targets always use the platform hook contract directly, with zero `platformhooks` build tag needed and zero changes to `compiler`, `interp`, or `transform`.

## Non-Goals and Phase 1 Scope

To keep the compiler diff minimal, this design omits several features from Phase 1.

- **No `//go:weak` pragma or LTO weak linking.** Hooks are plain bodyless declarations in `runtime` satisfied via `//go:linkname` from the board or SoC package. All 8 hooks belong to the required board contract (using trivial stubs such as returning `0` from `buffered()` when stdin is unused), and unreferenced hooks (such as `getchar` or `buffered` when stdin is not read) are dead-stripped by LTO.
- **No `transform.CheckNoHeapInit` or `interp` boot-hook tracking.** Hook authors follow documented pre-heap and `runtime/volatile` rules.
- **No `src/machine` dependency on Cortex-M.** Baremetal `cortexm && !qemu` `runtime` and `crypto/rand` have zero dependency on `src/machine`, while `src/machine` remains in-tree for non-Cortex-M targets.
- **No separate `socInit` and `boardInit` split.** The board package imports the SoC package directly, so one pre-heap hook (`platformInit0`) and one post-heap hook (`platformInit1`) let the board control exact initialization order.
- **Supported schedulers in Phase 1** on baremetal `cortexm` are `tasks` (`scheduler_cooperative.go`) and `none` (`scheduler_none.go`).
- **Out of scope for Phase 1** are `scheduler.cores` (requires `numCPU`, `spinLock`, and 7 multicore helpers in `scheduler_cores.go` and `gc_stack_cores.go`), `asyncify` (WASM-only `task_asyncify_wasm.S`), `threads` (OS pthread/futex), `wasmEntryReactor` (`-buildmode=c-shared`), fractional-nanosecond tick periods (such as 32.768 kHz RTCs), non-`cortexm` baremetal architectures, and virtual `import "board"` aliasing (deferred to Phase 2).

## Baremetal Runtime Isolation

Deleting Cortex-M chip `src/device/<chip>` directories (keeping `src/device/arm`), Cortex-M chip `src/runtime/runtime_<chip>.go` files, and `src/runtime/time_nxpmk66f18.go` isolates baremetal `cortexm && !qemu` from in-tree chip or board code while leaving all non-Cortex-M targets (`src/machine`, non-ARM `src/device/*`, non-Cortex-M `src/runtime/*`, `src/runtime/interrupt/*`, `src/runtime/scheduler_cores.go`, `loader/goroot.go`) intact. Selecting `scheduler=cores` on baremetal `cortexm && !qemu` is rejected in `builder/config.go` `NewConfig` until Phase 2.

- `targets/cortex-m0.json` and `targets/cortex-m0plus.json` add `"armv6m"` to `"build-tags"`, and `src/runtime/runtime_cortexm_hardfault.go` (`//go:build armv6m`) and `src/runtime/runtime_cortexm_hardfault_debug.go` (`//go:build cortexm && !armv6m`) replace the chip-specific `atsamd21 || nrf51 || py32` build tags so ARMv6-M targets do not access ARMv7-M/ARMv8-M SCB fault registers.
- Baremetal non-QEMU Cortex-M targets inherit `cortex-m` (`baremetal`, `cortexm`) and compile `src/runtime/runtime_platform.go` (`//go:build baremetal && cortexm && !qemu`), `src/runtime/runtime_platform_cortexm.go` (`//go:build baremetal && cortexm && !qemu`), `src/runtime/rand_norng.go` (updated to include `baremetal && cortexm && !qemu`, while `src/runtime/rand_hwrng.go` excludes `cortexm && !qemu`), existing arch/baremetal files (`arch_cortexm.go`, `runtime_cortexm.go`, `runtime_cortexm_abort.go`, `runtime_cortexm_hardfault*.go`, `baremetal.go`, `baremetal_memory.go`, `nonhosted.go`, `rand_startup.go`, `gc_*.go`), and the selected `scheduler_*.go` (`tasks` or `none`).
- Because `runtime_platform*.go` is gated by `baremetal && cortexm && !qemu`, `src/runtime/runtime_cortexm_qemu.go` (`cortexm && qemu`) and all non-Cortex-M targets do not collide.
- All other targets (host OS, WASI, WASM, QEMU, non-Cortex-M) compile `src/runtime/platform_init_stub.go` (`//go:build !(baremetal && cortexm && !qemu)`), keeping `scheduler_*.go` working with empty `platformInit0()` and `platformInit1()` stubs.
- Because `src/runtime/wait_other.go` excludes `cortexm` (`!cortexm`), `src/runtime/runtime_platform_cortexm.go` supplies `Reset_Handler` and `waitForEvents()` with zero symbol collisions.
- `src/crypto/rand/rand_baremetal_cortexm.go` (`//go:build baremetal && cortexm && !qemu`) returns `errors.New("crypto/rand: not implemented on baremetal")` until a Phase 2 RNG hook exists, while `src/crypto/rand/rand_baremetal.go` adds `&& !(cortexm && !qemu)` so non-Cortex-M targets keep using `machine.GetRNG` unchanged.

## Boot Sequence

Hook execution context depends on the selected scheduler.

### Cooperative Scheduler (`tasks`)

```text
Reset_Handler                  (runtime_platform_cortexm.go)
  preinit()                    arch .bss zeroing and .data copy
  run()                        (scheduler_cooperative.go)
    platformInit0()            system stack, pre-heap, scheduler inactive
    initRand()
    initHeap()
    go func() {                spawns main goroutine (default 2048 B stack on cortex-m)
      platformInit1()          main goroutine stack, heap and scheduler active
      initAll()                package init() functions in import order
      callMain()               main.main()
      mainExited = true
    }()
    scheduler(false)
  exit(0)
```

### No-Scheduler Mode (`none`)

```text
Reset_Handler
  preinit()
  run()                        (scheduler_none.go)
    platformInit0()            main stack, pre-heap
    initRand()
    initHeap()
    platformInit1()            main stack, heap active
    initAll()                  package init() functions in import order
    callMain()                 main.main()
  exit(0)
```

## Minimal `runtime` Hook Contract

All platform hooks use builtin Go types only so unexported `runtime.timeUnit` never leaks across `//go:linkname`.

### Runtime Files

- `src/runtime/runtime_platform.go` (`//go:build baremetal && cortexm && !qemu`) declares the 8 bodyless hooks and implements the `timeUnit` adapter functions.
- `src/runtime/runtime_platform_cortexm.go` (`//go:build baremetal && cortexm && !qemu`) defines `//export Reset_Handler` `func main() { preinit(); run(); exit(0) }` and `func waitForEvents() { arm.Asm("wfe") }`. SoC startup assembly in `extra-files` provides the vector table referencing `Reset_Handler` and must not define `Reset_Handler` itself.
- `src/runtime/platform_init_stub.go` (`//go:build !(baremetal && cortexm && !qemu)`) defines empty `func platformInit0() {}` and `func platformInit1() {}` stubs so all other targets compile unchanged.

### Hook Signatures and Contracts

All 8 hooks belong to the standard board contract (board packages must implement all 8; unreferenced hooks such as `getchar`/`buffered` when stdin is unused, or time hooks under `scheduler.none` without `time`, are dead-stripped by LTO and only referenced hooks cause link errors if omitted). `runtime` calls `platformInit0`, `platformInit1`, and `putchar` unconditionally (boot and panic/`print` paths), calls `platformTicks`, `platformTickNS`, and `platformSleep` whenever `time` or `tasks` scheduler sleeps are linked, and calls `getchar` and `buffered` when stdin is read.

1. `runtime.platformInit0()` runs before `initHeap()` on the system stack with the scheduler inactive (`PRIMASK` untouched from reset, initially `0`). Heap allocation and scheduler primitives (`time.Sleep`, `Gosched`, channels) are forbidden. Mark the board's `platformInit0` implementation and its helpers `//go:noheap` (`compiler/symbol.go` requires `//go:noheap` to appear on a function definition with a body rather than a bodyless declaration). Any transient interrupt masking must restore `PRIMASK`, and peripheral interrupts whose handlers depend on `initAll()` or the scheduler must remain disabled until driver setup in `main()` or application startup.
2. `runtime.platformInit1()` runs after `initHeap()` and before `initAll()`, on the main goroutine stack (`tasks`, 2048 B default) or main stack (`none`), with interrupts left in their boot state. Spawning background goroutines or yielding/blocking on scheduler primitives (`time.Sleep`, `Gosched`, channels) before `initAll()` is forbidden so package `init()` order cannot race with background tasks. Avoid calling stdlib packages whose `init()` has not yet executed, and do not enable interrupts whose handlers depend on uninitialized package `init()` state.
3. `runtime.platformTicks() uint64` returns a monotonic hardware tick count. The product `platformTicks() * platformTickNS()` must stay `<= math.MaxInt64` (because `timeUnit` and `ticksToNanoseconds` use signed `int64`; uptime rollover past `math.MaxInt64 / platformTickNS()` ticks is out of scope in Phase 1). It must not regress, allocate, or fail from interrupt context.
4. `runtime.platformTickNS() uint64` returns nanoseconds per tick (for example `1000` for 1 us) and must return a fixed integer in `1..math.MaxInt64` so LTO inlines it as a constant. Returning `0` triggers an unchecked division-by-zero fault; values `> math.MaxInt64` wrap negative on `int64` cast. Fractional-nanosecond clocks are out of scope in Phase 1.
5. `runtime.platformSleep(ticks uint64)` is called with interrupts enabled and `ticks > 0`. On every wait iteration, the implementation masks interrupts (`PRIMASK = 1`), arms the hardware timer/alarm inside that critical section, checks wakeup/deadline state, executes `WFI` (so a pending or already-fired interrupt wakes `WFI` without a missed-wakeup race), disarms any remaining timer alarm/pending IRQ before returning (so early peripheral-IRQ wakeups never leave a stale timer IRQ), and unmasks interrupts so pending ISRs run. Under the default `tasks` scheduler (`!scheduler.none`), it arms `min(ticks, maxHardwareTicks)` (clamping to the timer's maximum period is valid because the cooperative scheduler loop re-checks `ticks()` and calls `sleepTicks` again if time remains) and may return early on any interrupt wakeup (never returning immediately without waiting when no interrupt has fired). Under `scheduler.none` (`//go:build scheduler.none`), it loops with that same `PRIMASK`/arm/`WFI`/unmask sequence (re-arming sub-intervals if `ticks` exceeds the timer's maximum count, and re-checking `platformTicks()` after any interrupt) until at least `ticks` have elapsed so unrelated interrupts never cause an early return.
6. `runtime.putchar(c byte)` writes one byte synchronously without allocating or acquiring non-reentrant locks (so fault or panic handlers preempting normal execution never deadlock). It must be safe to call before `platformInit1()` (guard uninitialized hardware via `runtime/volatile` and drop the byte) and from fault or panic handlers.
7. `runtime.getchar() byte` is called from task/application context (not from interrupt or fault handlers) and blocks until one byte is available, guarding uninitialized console hardware via `runtime/volatile`. Calling `getchar()` during `platformInit0()` or `platformInit1()`/`initAll()` (before startup completes) is unsupported; if called when the console is uninitialized or absent, it blocks (yielding via `runtime.Gosched()` under `tasks`, or polling/`arm.Asm("wfe")` under `scheduler.none`). Under normal operation after `platformInit1()`, it yields via `runtime.Gosched()` under `tasks` (`!scheduler.none`) or polls/waits using `arm.Asm("wfe")` under `scheduler.none` (`runtime.Gosched()` is a no-op under `scheduler.none`).
8. `runtime.buffered() int` returns the non-negative count of unread RX bytes (`0` when uninitialized or unused) and must not allocate or block (safe to call from task or interrupt context if needed).

### Signed-Safe Time Adapters in `src/runtime/runtime_platform.go`

`runtime.timeUnit` is `int64`. The adapters handle non-positive inputs without unsigned wraparound.

```go
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
```

Durations with `0 < ns < platformTickNS()` truncate to `0` ticks, matching TinyGo's existing behavior.

### Rules for Hook Authors

1. **No heap allocation in `platformInit0`.** Use static globals and `//go:noheap`.
2. **Zero-initialized package globals.** Globals written in `platformInit0` or `platformInit1` must be zero-initialized `var` declarations. Non-constant package initializers run later during `initAll()` and overwrite hook writes.
3. **Use `runtime/volatile` for deferred globals read during `initAll`.** `interp` evaluates package `init()` functions before boot hooks run and only skips constant folding for volatile loads (`IsVolatile()`) or globals already marked `hasExternalStore` (`interp/interpreter.go`). `sync/atomic` lowers to non-volatile atomic loads (`compiler/atomic.go`), which `interp` folds to `nil`. Any global written in `platformInit0` or `platformInit1` and read from `init()`-reachable code (such as a console pointer read in `putchar`) must be accessed via `runtime/volatile`.

## Target JSON and Go-Module Resolution

`compileopts/target.go` supports three target forms in both `-target` and `"inherits"`.

1. **Go package path (`pkg:<importpath>` or `pkg:<importpath>/<file>.json`).** Every `pkg:` string rejects an empty body (`pkg:`), backslashes (`\`), whitespace, a leading `/` (`pkg:/...`), a trailing `/`, empty path segments (`//`), `.` or `..` path segments, and `@` version strings anywhere in the string. When a `pkg:` string ends with `.json`, split on the last `/` into `<importpath>` and `<file>.json`, also rejecting `pkg:.json`, `pkg:<importpath>/.json`, or a `.json` entry with no `/` after `pkg:`. Otherwise the spec path defaults to `target.json` inside `<importpath>`.
2. **File path (any non-`pkg:` entry ending in `.json`).** Absolute paths are used directly and checked for existence. Inside `"inherits"`, resolve relative paths against the declaring `.json` directory first, then fall back to `$TINYGOROOT/<path>`. On `-target`, resolve relative paths against `Options.Directory` if non-empty, else the working directory.
3. **Built-in target name.** Any other string resolves to `$TINYGOROOT/targets/<strings.ToLower(name)>.json`.

### `pkg:` Module Resolution

`compileopts.LoadTarget` runs before `GOOS`, `GOARCH`, `BuildTags`, or the synthetic `GOROOT` exist, and CLI invocations leave `Options.Directory` empty (`main.go`). Therefore `pkg:` resolution runs `go list -m -json all` lazily on the first `pkg:` reference (cached across all nested `"inherits"` within that `LoadTarget` call) in `Options.Directory` when non-empty, else `os.Getwd()`, inheriting the caller's Go environment unmodified (`GOWORK`, `GOPROXY`, `GOMODCACHE`, host `GOROOT`). Built-in and `.json` file targets without `pkg:` never invoke `go list -m` and continue to work outside a Go module. Running in the workspace root ensures the application's `go.mod`, `go.work`, and `replace` directives govern transitive `pkg:` inheritance even when an intermediate SoC spec sits in the read-only Go module cache.

- Reading module metadata (`go list -m -json all`) works even when the target directory contains no `.go` files or only target-tagged `.go` files.
- Match the longest module `Path` where `importpath == m.Path` or `strings.HasPrefix(importpath, m.Path+"/")`. If `m.Error != nil`, return `m.Error.Err` explicitly; else if `m.Replace != nil && m.Replace.Error != nil`, return `m.Replace.Error.Err`. Otherwise use `m.Dir` when non-empty, else `m.Replace.Dir` when `m.Replace != nil`; if still empty (module not downloaded), return an explicit error. Join the module directory with any subpath and verify the directory and `.json` file exist.
- Missing `go` binary, missing `go.mod` or `go.work`, unrequired modules, or missing target files return an explicit error naming the `pkg:` target and search directory.

### Inheritance Cycles and Path Resolution

- Track active resolved target file paths (canonicalized with `filepath.Abs` and `filepath.EvalSymlinks`, falling back to `filepath.Clean`) in a `visiting map[string]bool` stack during recursive loading and return an error on cycle (`target inheritance cycle: ...`). Canonicalize `$TINYGOROOT/targets` with the same `filepath.Abs`/`filepath.EvalSymlinks` helper before any containment check.
- Resolve `linkerscript` and `extra-files` entries: absolute paths are kept as-is and verified to exist during `LoadTarget`; relative entries resolve against the directory of the `.json` spec that declared them first, then `$TINYGOROOT/<path>`. Matches resolved in an external target directory (canonical path outside `$TINYGOROOT/targets`, including in-tree test fixtures like `testdata/platformhooks`) are rewritten to absolute paths. Matches resolved inside `$TINYGOROOT/targets` or via the `$TINYGOROOT/<path>` fallback stay relative to `$TINYGOROOT` (rewritten with `targets/` prefix if resolved in `$TINYGOROOT/targets`). Missing entries fail early naming the checked path(s).
- Record canonicalized absolute external target directories (any resolved target `.json` directory outside `$TINYGOROOT/targets`, from either `pkg:` or an external file path, normalized via `filepath.Abs`/`filepath.EvalSymlinks`) in `TargetSpec.LinkerSearchDirs []string` (`json:"-"`), and record the leaf target's directory in `TargetSpec.TargetDir string` (`json:"-"`), set to the canonicalized absolute directory when outside `$TINYGOROOT/targets` or `""` when inside `$TINYGOROOT/targets`. Modify `overrideProperties` (`compileopts/target.go`) to skip `LinkerSearchDirs` and `TargetDir` (or `json:"-"` fields) so reflection does not apply parent-before-child appending, duplicate-value errors, or child overwrite, and instead merge `LinkerSearchDirs` explicitly in `resolveInherits` (`compileopts/target.go`) in depth-first pre-order (child directory first, then each parent recursively in `"inherits"` array order, keeping the first occurrence on deduplication) while preserving the leaf's `TargetDir`. `compileopts/config.go` `LDFlags()` emits `-L <dir>` for each `LinkerSearchDirs` entry ahead of `-L <TINYGOROOT>`, so a board linker script can `INCLUDE` sibling scripts, inherited SoC module scripts, and `$TINYGOROOT/targets/*.ld`.
- `config.TargetDir()` returns `c.Target.TargetDir` (the leaf CLI `-target` directory when outside `$TINYGOROOT/targets`, or `""` for built-in target names and file paths inside `$TINYGOROOT/targets`).
- In `builder/build.go:866-883`, extract an `extraFilePaths(root, targetDir, path)` helper: relative `extra-files` resolve to `abspath = filepath.Join(root, path)` with `trimPath = path.Join("github.com/tinygo-org/tinygo", filepath.ToSlash(path))`, while absolute `extra-files` (`filepath.IsAbs(path)`) set `abspath = path`. When `config.TrimPath()` is false, `compileConfig` remains `nil` (`trimPath` is unused). When `config.TrimPath()` is true (`builder/build.go:872`), `extraFilePaths` computes `trimPath` (for `cFileCompileConfig{recordedPath: config.CSourcePath(trimPath)}` used by `builder/cc.go:141-147` for `-fdebug-compilation-dir` and `-frandom-seed`) and appends `-ffile-prefix-map=<filepath.Dir(abspath)>=<config.CSourcePath(path.Dir(trimPath))>` (`compileopts/config.go:80-92`, calling `path.Dir` before `CSourcePath` so `CSourcePath` preserves the leading `/_/` or `//_/_/` virtual root prefix without `path.Clean` collapsing `//`):
  - When `config.TargetDir() != ""`, `filepath.Rel(targetDir, abspath)` succeeds (`err == nil`), and the result does not start with `..`, use `trimPath = target/<rel>` (forward slashes).
  - Otherwise (when `config.TargetDir() == ""`, `filepath.Rel` returns an error such as across Windows volumes, or `abspath` is outside `config.TargetDir()`), use `trimPath = external/<hex(sha256(dirKey)[:4])>/<basename>` (`dirKey` is `filepath.ToSlash(relDir)` when `targetDir != ""` and `filepath.Rel(targetDir, filepath.Dir(abspath))` succeeds, else `filepath.ToSlash(filepath.Dir(abspath))`).

## `"boardpackage"` Loading in `loader/loader.go`

`TargetSpec.BoardPackage` (`"boardpackage"` in target JSON, matching `"linkerscript"` naming) holds a Go import path (child overrides parent when non-empty; setting `"-"` clears an inherited board package). Its syntax is not validated eagerly in `compileopts.LoadTarget` (invalid import paths fail in `loader.Load` when `go list` resolves `BoardPackage`). It does not add a build tag.

1. **Eligibility.** Automatic `BoardPackage` loading and blank-import injection apply only when `BoardPackage != ""`, `BoardPackage != "-"`, `!config.TestConfig.StandardPackage`, `!p.analysis` (`analysis` parameter of `load`, so `LoadForAnalysis` in `loader/analysis.go` never loads extra packages or mutates analyzed `Syntax` ASTs), `rootPkg.Name == "main"` (including synthetic `<pkg>.test` roots produced by `tinygo test` / `CompileTestBinary`), and `strings.TrimSuffix(rootPkg.ImportPath, ".test") != BoardPackage` (preventing self-import errors when building or testing `BoardPackage` itself; external `package <board>_test` tests in the board module must import `<board>` explicitly if any test references runtime hooks).
2. **Package discovery and `MainPkg()` invariant.** `loader.Load` appends packages from the initial `go list -deps -e` (`PackageJSONFlag()`, `loader/loader.go:98-107,174`) invocation into `p.sorted` in `go list` postorder, and `builder/build.go` calls `lprogram.MainPkg()` (`p.sorted[len(p.sorted)-1]`) before `lprogram.Parse()`. After the initial `go list` loop finishes, if eligibility holds and `BoardPackage` is not already present in `p.Packages`, `loader.Load` runs a second `go list -deps -e` (`PackageJSONFlag()`) on `BoardPackage` using the same working directory, environment (synthetic `GOROOT`, `GOOS`, `GOARCH`), and `-tags` flags as the initial `go list` call (without `-test`, requiring `BoardPackage` to be reachable in the build workspace's `go.mod` or `go.work`). If that second `go list` command fails or any returned package has `pkg.Error != nil`, `loader.Load` returns the error using the same error handling as the initial `go list` pass. Otherwise, any package from the second query not already in `p.Packages` is added to `p.Packages` and inserted into `p.sorted` immediately *before* `rootPkg`, preserving `go list` dependency postorder among `BoardPackage` and its dependencies, never overwriting test-augmented packages, and keeping `p.MainPkg()` (`rootPkg`) at `p.sorted[len(p.sorted)-1]`.
3. **AST injection.** During `Parse()` (before `Program.check()`), if eligibility holds and `len(rootPkg.Files) > 0` (normal `main` packages always have at least one `.go` file; empty packages already fail `go list`/build), scan all files in `rootPkg.Files` for an existing import of `BoardPackage`. If absent, inject a synthetic `_ "<BoardPackage>"` `*ast.ImportSpec` into `rootPkg.Files[0]` (appending to `rootPkg.Files[0].Imports`, adding to an existing non-`"C"` import `ast.GenDecl` while setting `Lparen`/`Rparen` if converting a single-line `import`, or inserting a new import `ast.GenDecl` in `file.Decls` so CGo `import "C"` blocks are untouched; works on generated `_testmain.go` too) so `Program.check()` (`dep.index < job.index`), `ast.File.Imports` readers, and `pkg.Pkg.Imports()` see the dependency edge and `BoardPackage`'s `init()` runs before `main.init()`.
4. **Hook completeness.** If `"boardpackage"` is omitted or `"-"` on a baremetal Cortex-M target, linking fails unless the application or another imported package defines the hooks referenced by the program.

## Application Portability

In Phase 1, applications using standard library packages (`time`, `println`, `os`) require zero board imports because `"boardpackage"` links the board hooks automatically. Applications needing named PCB peripherals (`LED`, `CAN0`) either import an interface-based HAL package populated by `platformInit1` or import the board package directly until Phase 2 adds `import "board"` aliasing.

## Touchpoint Inventory

| Package | File / Dir | Change | Approx. LOC |
| :--- | :--- | :--- | :--- |
| `compiler/` | — | None | 0 |
| `interp/` | — | None | 0 |
| `transform/` | — | None | 0 |
| `src/machine/` | *(kept)* | Kept intact for non-Cortex-M targets | 0 |
| `src/device/` | `arm/` & non-Cortex-M kept; Cortex-M chip dirs removed | Keep `src/device/arm` and non-Cortex-M chip packages; delete Cortex-M chip register packages | N/A (delete) |
| `src/crypto/rand` | `rand_baremetal.go`, `rand_baremetal_cortexm.go` | Exclude `cortexm && !qemu` from `rand_baremetal.go`; add `baremetal && cortexm && !qemu` error stub | ~12 |
| `compileopts` | `target.go`, `config.go` | Relative paths, `overrideProperties` skip for `LinkerSearchDirs` and `TargetDir`, `pkg:` resolution, `BoardPackage` (`boardpackage`) | ~165 |
| `builder` | `build.go`, `config.go` | Absolute `extra-files` and `CSourcePath` `-trimpath` mapping; reject `scheduler=cores` on baremetal `cortexm` | ~35 |
| `loader` | `loader.go`, `analysis.go` | Conditional `BoardPackage` discovery/insertion before `rootPkg`, AST blank import, `!p.analysis` guard | ~110 |
| `src/runtime` | Cortex-M `runtime_<chip>.go`, `time_nxpmk66f18.go`, `rand_{hwrng,norng}.go`, `runtime_cortexm_hardfault*.go` | Delete Cortex-M chip files; gate `rand_hwrng.go`/`rand_norng.go` for `cortexm && !qemu` and `hardfault*.go` on `armv6m`/`!armv6m` | ~10 (net) |
| `targets` | `cortex-m0.json`, `cortex-m0plus.json` | Add `"armv6m"` to `"build-tags"` | ~2 |
| `src/runtime` | `scheduler_{cooperative,none}.go` | Call `platformInit0()` and `platformInit1()` | ~6 |
| `src/runtime` | `runtime_platform.go` | 8 `baremetal && cortexm && !qemu` hooks and signed-safe `timeUnit` adapters | ~45 |
| `src/runtime` | `runtime_platform_cortexm.go` | `Reset_Handler` and `waitForEvents()` under `baremetal && cortexm && !qemu` | ~12 |
| `src/runtime` | `platform_init_stub.go` | Empty `!(baremetal && cortexm && !qemu)` stubs for `platformInit0()` and `platformInit1()` | ~8 |

Total new/modified fork code is roughly 380 lines across `compileopts`, `builder`, `loader`, `src/crypto/rand`, and `src/runtime`.

## End-to-End Example

### SoC Module (`github.com/helvionics/soc/rp2/rp2350/target.json`)

```json
{
    "inherits": ["cortex-m33"],
    "inheritable-only": true,
    "build-tags": ["soc.rp2350"],
    "scheduler": "tasks",
    "extra-files": ["rp2350.s", "embedded_block.s"],
    "linkerscript": "rp2350.ld"
}
```

### Board Module (`github.com/helvionics/board/seeed/xiao-rp2350/target.json`)

```json
{
    "inherits": ["pkg:github.com/helvionics/soc/rp2/rp2350"],
    "linkerscript": "memmap.ld",
    "boardpackage": "github.com/helvionics/board/seeed/xiao-rp2350"
}
```

### Board Hook Implementation (`github.com/helvionics/board/seeed/xiao-rp2350/platform.go`)

```go
package xiaorp2350

import (
	"runtime/volatile"
	_ "unsafe"

	"github.com/helvionics/soc/rp2/rp2350"
)

var consoleReady volatile.Register32

//go:linkname platformInit0 runtime.platformInit0
//go:noheap
func platformInit0() {
	rp2350.InitClocks(12_000_000)
	rp2350.InitTimer()
}

//go:linkname platformInit1 runtime.platformInit1
func platformInit1() {
	initConsoleUART()
	consoleReady.Set(1)
}

//go:linkname platformTicks runtime.platformTicks
func platformTicks() uint64 { return rp2350.Ticks() }

//go:linkname platformTickNS runtime.platformTickNS
func platformTickNS() uint64 { return 1000 }

//go:linkname platformSleep runtime.platformSleep
func platformSleep(ticks uint64) { rp2350.Sleep(ticks) }

//go:linkname putchar runtime.putchar
func putchar(c byte) {
	if consoleReady.Get() == 0 {
		return
	}
	writeConsole(c)
}

//go:linkname getchar runtime.getchar
func getchar() byte { return readConsole() }

//go:linkname buffered runtime.buffered
func buffered() int { return consoleBuffered() }
```

### Demo Application (`github.com/helvionics/demo/cmd/blinky/main.go`)

```go
package main

import "time"

func main() {
	for {
		println("tick")
		time.Sleep(time.Second)
	}
}
```

Build from `github.com/helvionics/demo` (with `helvionics/soc` and `helvionics/board` in `go.mod` or `go.work`):

```bash
tinygo build -target=pkg:github.com/helvionics/board/seeed/xiao-rp2350 ./cmd/blinky
```

## Verification Plan and Phase 2 Work

### Verification Plan

- **In-tree test suite update:** remove test cases that reference deleted Cortex-M board targets (across `compileopts/target_test.go`, `builder/builder_test.go`, `builder/sizes_test.go`, `main_test.go`, and `build_test.go`), keeping all tests for retained non-Cortex-M targets (`esp32*`, RISC-V, AVR, `wasm*`, `wasi*`, host OS), `cortex-m*` compile/IR/loader tests, and `cortex-m-qemu` linked execution tests active and passing (with zero changes to `compiler/`, `interp/`, or `transform/`, including `compiler/compiler_test.go`).
- **Unit tests (`compileopts`, `loader`, `builder`):** test `pkg:` target and `inherits` resolution with `go.mod` and `go.work`, Go-file-less target packages, cycle detection, relative `linkerscript` and `extra-files` fallback and error paths, custom `LinkerSearchDirs` `-L` order, `CSourcePath`-prefixed `-trimpath` sanitization, `LoadForAnalysis` non-mutation, and `BoardPackage` (`boardpackage`) loading across main, non-main, `.go` file, test binary, and self-import builds.
- **Integration fixture (`testdata/platformhooks`):** multi-module out-of-tree fixture with a Cortex-M33 SoC module and board module verifying `-opt=0` LLVM IR (`platformInit0` before `initRand`/`initHeap`, `platformInit1` before `initAll` inside the main goroutine closure spawned by `runtime.run`, volatile `consoleReady` load preserved in `runtime.putchar`) and a linked `-opt=z` ELF with valid sections and entry point.
- **End-to-end PoC verification (`helvionics/soc`, `helvionics/board`, `helvionics/demo`):** build `github.com/helvionics/demo/cmd/blinky` targeting `pkg:github.com/helvionics/board/seeed/xiao-rp2350` against local `go.work` checkouts of those external repos (outside the TinyGo fork's standalone CI), and verify non-baremetal (`compiler`, `interp`, `transform`, host/WASM `builder`) tests pass.

### Phase 2 Work

- Virtual `import "board"` alias in `loader` plus `tinygo info` `-overlay` output for `gopls`.
- `scheduler.cores` multicore hook contract (`numCPU`, `spinLock`, and SMP scheduler helpers).
- Additional architecture entry files beyond `cortexm`: the 8-hook contract and `timeUnit` adapters in `src/runtime/runtime_platform.go` remain unchanged across CPU architectures, and each added architecture (`riscv`, `avr`, `xtensa`) only needs a thin `src/runtime/runtime_platform_<arch>.go` for its reset entry point, `preinit()`, `waitForEvents()` instruction, and critical-section IRQ masking inside `platformSleep`.
