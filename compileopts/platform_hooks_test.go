package compileopts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tinygo-org/tinygo/goenv"
)

func TestParsePkgTargetInvalid(t *testing.T) {
	invalid := []string{
		"pkg:",
		"pkg:/foo/bar",
		"pkg:foo/bar/",
		"pkg:foo//bar",
		"pkg:foo/./bar",
		"pkg:foo/../bar",
		"pkg:foo/bar@v1.0.0",
		"pkg:.json",
		"pkg:foo/.json",
		"pkg:foo.json",
		`pkg:foo\bar`,
		"pkg:foo bar",
	}
	for _, tc := range invalid {
		if _, _, err := parsePkgTarget(tc); err == nil {
			t.Errorf("parsePkgTarget(%q) succeeded, want error", tc)
		}
	}

	imp, file, err := parsePkgTarget("pkg:example.com/board/myboard")
	if err != nil || imp != "example.com/board/myboard" || file != "target.json" {
		t.Fatalf("got (%q, %q, %v), want (example.com/board/myboard, target.json, nil)", imp, file, err)
	}

	imp, file, err = parsePkgTarget("pkg:example.com/board/myboard/alt.json")
	if err != nil || imp != "example.com/board/myboard" || file != "alt.json" {
		t.Fatalf("got (%q, %q, %v), want (example.com/board/myboard, alt.json, nil)", imp, file, err)
	}
}

func TestPkgTargetResolutionGoModAndGoWork(t *testing.T) {
	for _, useWork := range []bool{false, true} {
		name := "gomod"
		if useWork {
			name = "gowork"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			socDir := filepath.Join(root, "soc")
			boardDir := filepath.Join(root, "board")
			appDir := filepath.Join(root, "app")
			for _, d := range []string{
				filepath.Join(socDir, "rp2", "rp2350"),
				filepath.Join(boardDir, "myboard"),
				appDir,
			} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			if err := os.WriteFile(filepath.Join(socDir, "go.mod"), []byte("module example.com/soc\n\ngo 1.22\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(socDir, "rp2", "rp2350", "soc.ld"), []byte("INCLUDE \"targets/arm.ld\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(socDir, "rp2", "rp2350", "start.s"), []byte(".text\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(socDir, "rp2", "rp2350", "target.json"), []byte(`{
				"inherits": ["cortex-m33"],
				"inheritable-only": true,
				"extra-files": ["start.s"],
				"linkerscript": "soc.ld"
			}`), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(boardDir, "go.mod"), []byte("module example.com/board\n\ngo 1.22\n\nrequire example.com/soc v0.0.0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(boardDir, "myboard", "board.ld"), []byte("INCLUDE \"soc.ld\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(boardDir, "myboard", "target.json"), []byte(`{
				"inherits": ["pkg:example.com/soc/rp2/rp2350"],
				"linkerscript": "board.ld",
				"boardpackage": "example.com/board/myboard"
			}`), 0o600); err != nil {
				t.Fatal(err)
			}

			if useWork {
				if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.22\n\nuse (\n\t./soc\n\t./board\n\t./app\n)\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(appDir, "go.mod"), []byte("module example.com/app\n\ngo 1.22\n\nrequire example.com/board v0.0.0\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(socDir, "go.mod"), []byte("module example.com/soc\n\ngo 1.22\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				// In go.work without VCS tags, replace directives in go.work resolve local v0.0.0 requires.
				workContent := "go 1.22\n\nuse (\n\t./soc\n\t./board\n\t./app\n)\n\nreplace (\n\texample.com/soc v0.0.0 => ./soc\n\texample.com/board v0.0.0 => ./board\n)\n"
				if err := os.WriteFile(filepath.Join(root, "go.work"), []byte(workContent), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				appMod := "module example.com/app\n\ngo 1.22\n\nrequire example.com/board v0.0.0\n\nreplace (\n\texample.com/board => ../board\n\texample.com/soc => ../soc\n)\n"
				if err := os.WriteFile(filepath.Join(appDir, "go.mod"), []byte(appMod), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			spec, err := LoadTarget(&Options{
				Target:    "pkg:example.com/board/myboard",
				Directory: appDir,
			})
			if err != nil {
				t.Fatalf("LoadTarget failed: %v", err)
			}
			if spec.InheritableOnly {
				t.Fatal("board target inherited InheritableOnly=true")
			}
			if spec.BoardPackage != "example.com/board/myboard" {
				t.Fatalf("BoardPackage = %q, want example.com/board/myboard", spec.BoardPackage)
			}

			wantBoardDir := canonicalPath(filepath.Join(boardDir, "myboard"))
			wantSocDir := canonicalPath(filepath.Join(socDir, "rp2", "rp2350"))
			if spec.TargetDir != wantBoardDir {
				t.Fatalf("TargetDir = %q, want %q", spec.TargetDir, wantBoardDir)
			}
			wantSearch := []string{wantBoardDir, wantSocDir}
			if !slices.Equal(spec.LinkerSearchDirs, wantSearch) {
				t.Fatalf("LinkerSearchDirs = %v, want %v", spec.LinkerSearchDirs, wantSearch)
			}
			if !filepath.IsAbs(spec.LinkerScript) || spec.LinkerScript != filepath.Join(wantBoardDir, "board.ld") {
				t.Fatalf("LinkerScript = %q, want %q", spec.LinkerScript, filepath.Join(wantBoardDir, "board.ld"))
			}
			wantStart := filepath.Join(wantSocDir, "start.s")
			if !slices.Contains(spec.ExtraFiles, wantStart) {
				t.Fatalf("ExtraFiles = %v, missing %s", spec.ExtraFiles, wantStart)
			}

			cfg := &Config{Target: spec, Options: &Options{}}
			ldflags := cfg.LDFlags()
			tinygoRoot := goenv.Get("TINYGOROOT")
			idxBoard := indexFlagPair(ldflags, "-L", wantBoardDir)
			idxSoc := indexFlagPair(ldflags, "-L", wantSocDir)
			idxRoot := indexFlagPair(ldflags, "-L", tinygoRoot)
			if idxBoard < 0 || idxSoc < 0 || idxRoot < 0 || !(idxBoard < idxSoc && idxSoc < idxRoot) {
				t.Fatalf("unexpected -L order in LDFlags %v", ldflags)
			}
		})
	}
}

func indexFlagPair(args []string, flag, val string) int {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return i
		}
	}
	return -1
}

func TestTargetInheritanceCycleAndMissingResources(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	if err := os.WriteFile(a, []byte(`{"inherits":["b.json"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(`{"inherits":["a.json"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTarget(&Options{Target: a}); err == nil || !strings.Contains(err.Error(), "target inheritance cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}

	missingLD := filepath.Join(dir, "missing-ld.json")
	if err := os.WriteFile(missingLD, []byte(`{"inherits":["cortex-m33"],"linkerscript":"nope.ld"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTarget(&Options{Target: missingLD}); err == nil || !strings.Contains(err.Error(), "nope.ld") {
		t.Fatalf("expected missing linkerscript error, got: %v", err)
	}

	fallbackLD := filepath.Join(dir, "fallback-ld.json")
	if err := os.WriteFile(fallbackLD, []byte(`{"inherits":["cortex-m33"],"linkerscript":"targets/arm.ld"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := LoadTarget(&Options{Target: fallbackLD})
	if err != nil {
		t.Fatalf("fallback linkerscript failed: %v", err)
	}
	if spec.LinkerScript != "targets/arm.ld" {
		t.Fatalf("LinkerScript = %q, want targets/arm.ld", spec.LinkerScript)
	}
}
