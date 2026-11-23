package builder

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tinygo-org/tinygo/compileopts"
)

func TestExtraFilePathsTrimPath(t *testing.T) {
	root := "/tinygo"
	targetDir := "/workspace/board/myboard"

	abs, trim := extraFilePaths(root, targetDir, "targets/cortex-m-qemu.s")
	if abs != "/tinygo/targets/cortex-m-qemu.s" || trim != "github.com/tinygo-org/tinygo/targets/cortex-m-qemu.s" {
		t.Fatalf("relative got (%q, %q)", abs, trim)
	}

	abs, trim = extraFilePaths(root, targetDir, "/workspace/board/myboard/start.s")
	if abs != "/workspace/board/myboard/start.s" || trim != "target/start.s" {
		t.Fatalf("in-targetDir got (%q, %q)", abs, trim)
	}

	abs, trim = extraFilePaths(root, targetDir, "/workspace/soc/cm33/vectors.s")
	if abs != "/workspace/soc/cm33/vectors.s" || !strings.HasPrefix(trim, "external/") || !strings.HasSuffix(trim, "/vectors.s") {
		t.Fatalf("external got (%q, %q)", abs, trim)
	}
}

func TestNewConfigRejectsSchedulerCoresOnBaremetalCortexM(t *testing.T) {
	targetFile := filepath.Join(t.TempDir(), "cm33.json")
	if err := os.WriteFile(targetFile, []byte(`{"inherits":["cortex-m33"],"build-tags":["platformhooks"],"scheduler":"cores"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewConfig(&compileopts.Options{
		Target:    targetFile,
		Semaphore: sema,
	})
	if err == nil || !strings.Contains(err.Error(), "scheduler=cores") {
		t.Fatalf("expected scheduler=cores rejection, got: %v", err)
	}
}

func TestPlatformHooksIntegrationFixture(t *testing.T) {
	appDir, err := filepath.Abs("../testdata/platformhooks/app")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("llvm_ir_opt0", func(t *testing.T) {
		options := &compileopts.Options{
			Target:        "pkg:example.com/platformhooks/board/testboard",
			Directory:     appDir,
			Opt:           "0",
			InterpTimeout: 180 * time.Second,
			Semaphore:     sema,
		}
		config, err := NewConfig(options)
		if err != nil {
			t.Fatal(err)
		}
		outIR := filepath.Join(t.TempDir(), "app.ll")
		if _, err := Build(".", outIR, t.TempDir(), config); err != nil {
			t.Fatal(err)
		}
		irBytes, err := os.ReadFile(outIR)
		if err != nil {
			t.Fatal(err)
		}
		ir := string(irBytes)

		runBody := findLLVMFunc(t, ir, "@runtime.run(")
		idxInit0 := strings.Index(runBody, "@runtime.platformInit0")
		idxRand := strings.Index(runBody, "@runtime.initRand")
		idxHeap := strings.Index(runBody, "@runtime.initHeap")
		if idxInit0 < 0 || idxRand < 0 || idxHeap < 0 || !(idxInit0 < idxRand && idxRand < idxHeap) {
			t.Fatalf("unexpected boot order in runtime.run: init0=%d rand=%d heap=%d", idxInit0, idxRand, idxHeap)
		}

		if !strings.Contains(ir, "@runtime.platformInit1") || !strings.Contains(ir, "@runtime.initAll") {
			t.Fatal("missing @runtime.platformInit1 or @runtime.initAll in LLVM IR")
		}

		putcharBody := findLLVMFunc(t, ir, "@runtime.putchar(")
		if !strings.Contains(putcharBody, "(*runtime/volatile.Register32).Get") || !strings.Contains(putcharBody, "consoleReady") {
			t.Fatalf("expected volatile Register32.Get(consoleReady) in runtime.putchar, got:\n%s", putcharBody)
		}
		loadBody := findLLVMFunc(t, ir, "runtime/volatile.LoadUint32")
		if !strings.Contains(loadBody, "load volatile i32") {
			t.Fatalf("expected load volatile i32 in runtime/volatile.LoadUint32, got:\n%s", loadBody)
		}
	})

	t.Run("linked_elf_optz", func(t *testing.T) {
		options := &compileopts.Options{
			Target:        "pkg:example.com/platformhooks/board/testboard",
			Directory:     appDir,
			Opt:           "z",
			Debug:         true,
			InterpTimeout: 180 * time.Second,
			Semaphore:     sema,
		}
		config, err := NewConfig(options)
		if err != nil {
			t.Fatal(err)
		}
		outELF := filepath.Join(t.TempDir(), "app.elf")
		res, err := Build(".", outELF, t.TempDir(), config)
		if err != nil {
			t.Fatal(err)
		}
		f, err := elf.Open(res.Executable)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if f.Entry == 0 {
			t.Fatal("ELF entry point is 0")
		}
		if f.Section(".text") == nil {
			t.Fatal("missing .text section in linked ELF")
		}
	})
}

func findLLVMFunc(t *testing.T, ir, sym string) string {
	t.Helper()
	for line := range strings.SplitSeq(ir, "\n") {
		if strings.HasPrefix(line, "define ") && strings.Contains(line, sym) {
			start := strings.Index(ir, line)
			body := ir[start:]
			if end := strings.Index(body, "\n}"); end > 0 {
				return body[:end]
			}
			return body
		}
	}
	t.Fatalf("missing definition for %s in LLVM IR", sym)
	return ""
}
