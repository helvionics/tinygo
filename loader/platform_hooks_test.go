package loader

import (
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/tinygo-org/tinygo/compileopts"
)

func setupBoardPackageWorkspace(t *testing.T) (dir string, boardPkg string) {
	t.Helper()
	dir = t.TempDir()
	boardPkg = "example.com/mod/board"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/mod\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "board"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "board", "board.go"), []byte("package board\n\nvar Ready = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "board", "board_test.go"), []byte("package board\n\nimport \"testing\"\n\nfunc TestBoard(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "lib.go"), []byte("package lib\n\nfunc Hello() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, boardPkg
}

func makeBoardConfig(dir, boardPkg string, compileTest bool) *compileopts.Config {
	tc := compileopts.TestConfig{CompileTestBinary: compileTest}
	return &compileopts.Config{
		Options: &compileopts.Options{
			Directory:  dir,
			GOOS:       "linux",
			GOARCH:     "amd64",
			TestConfig: tc,
		},
		TestConfig: tc,
		Target: &compileopts.TargetSpec{
			GOOS:         "linux",
			GOARCH:       "amd64",
			BoardPackage: boardPkg,
		},
	}
}

func hasImport(file *ast.File, target string) bool {
	for _, spec := range file.Imports {
		if v, err := strconv.Unquote(spec.Path.Value); err == nil && v == target {
			return true
		}
	}
	return false
}

func TestBoardPackageLoading(t *testing.T) {
	dir, boardPkg := setupBoardPackageWorkspace(t)

	t.Run("main_package_loads_and_injects", func(t *testing.T) {
		cfg := makeBoardConfig(dir, boardPkg, false)
		prog, err := Load(cfg, "./app", types.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := prog.Packages[boardPkg]; !ok {
			t.Fatalf("boardpackage %s not loaded into Program.Packages", boardPkg)
		}
		if prog.MainPkg().ImportPath != "example.com/mod/app" {
			t.Fatalf("MainPkg() = %q, want example.com/mod/app", prog.MainPkg().ImportPath)
		}
		if err := prog.Parse(); err != nil {
			t.Fatal(err)
		}
		if !hasImport(prog.MainPkg().Files[0], boardPkg) {
			t.Fatalf("expected blank import of %s in main file", boardPkg)
		}
	})

	t.Run("virtual_board_import_alias", func(t *testing.T) {
		aliasAppDir := filepath.Join(dir, "aliasapp")
		if err := os.MkdirAll(aliasAppDir, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package main\n\nimport \"board\"\n\nfunc main() { _ = board.Ready }\n"
		if err := os.WriteFile(filepath.Join(aliasAppDir, "main.go"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := makeBoardConfig(dir, boardPkg, false)
		prog, err := Load(cfg, "./aliasapp", types.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := prog.Packages[boardPkg]; !ok {
			t.Fatalf("boardpackage %s not loaded for import \"board\"", boardPkg)
		}
		if err := prog.Parse(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("non_main_package_skips", func(t *testing.T) {
		cfg := makeBoardConfig(dir, boardPkg, false)
		prog, err := Load(cfg, "./lib", types.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := prog.Packages[boardPkg]; ok {
			t.Fatalf("boardpackage %s should not be loaded for non-main package", boardPkg)
		}
	})

	t.Run("go_file_argument_loads", func(t *testing.T) {
		cfg := makeBoardConfig(dir, boardPkg, false)
		prog, err := Load(cfg, "./app/main.go", types.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := prog.Packages[boardPkg]; !ok {
			t.Fatalf("boardpackage %s not loaded for .go file build", boardPkg)
		}
	})

	t.Run("self_test_skips_injection", func(t *testing.T) {
		cfg := makeBoardConfig(dir, boardPkg, true)
		prog, err := Load(cfg, "./board", types.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if prog.boardPackageTarget() != "" {
			t.Fatalf("boardPackageTarget() = %q, want empty for self-test", prog.boardPackageTarget())
		}
		if err := prog.Parse(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("load_for_analysis_does_not_mutate", func(t *testing.T) {
		cfg := makeBoardConfig(dir, boardPkg, false)
		analysis, err := LoadForAnalysis(cfg, []string{"./app"}, types.SizesFor("gc", "amd64"), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, pkg := range analysis.Packages {
			if pkg.PkgPath == boardPkg {
				t.Fatalf("LoadForAnalysis should not load boardpackage %s", boardPkg)
			}
			if pkg.PkgPath == "example.com/mod/app" && hasImport(pkg.Syntax[0], boardPkg) {
				t.Fatalf("LoadForAnalysis should not mutate AST imports")
			}
		}
	})
}
