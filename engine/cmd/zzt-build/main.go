// Command zzt-build turns an authored ZWD source file into a vanilla .ZZT
// world. Unlike the generation service, this is a deterministic authoring
// gate: it does not repair or reinterpret source. It compiles, runs the full
// generated-world evaluation, optionally renders every board, and only then
// atomically publishes the binary.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/benhoyt/zztgo"
)

func main() {
	var outPath string
	var previewDir string
	var displayName string
	flag.StringVar(&outPath, "out", "", "output .ZZT path (default: beside source)")
	flag.StringVar(&previewDir, "preview", "", "optional directory for one PNG per board")
	flag.StringVar(&displayName, "title", "", "expected title wordmark (default: compiled world name)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: zzt-build [options] WORLD.zwd")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	sourcePath := flag.Arg(0)
	srcBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		fatalf("read %s: %v", sourcePath, err)
	}
	src := string(srcBytes)
	world, err := zztgo.CompileZWDWorld(src)
	if err != nil {
		fatalf("compile %s: %v", sourcePath, err)
	}
	if displayName == "" {
		displayName = world.Info.Name
	}

	report := zztgo.EvalGeneratedZWD(src, displayName)
	fmt.Print(report.String())
	extraChecks := []zztgo.EvalCheck{
		zztgo.EvalZWDLayoutRoutes(src),
		zztgo.EvalZWDOOP(src),
	}
	extraPassed := true
	for _, check := range extraChecks {
		mark := "PASS"
		if !check.Passed {
			mark = "FAIL"
			extraPassed = false
		}
		fmt.Printf("%s %s", mark, check.Name)
		if check.Detail != "" {
			fmt.Printf(": %s", check.Detail)
		}
		fmt.Println()
	}
	if !report.Passed() || !extraPassed {
		fatalf("quality gate failed; no binary was written")
	}

	data, err := zztgo.CompileZWD(src)
	if err != nil {
		fatalf("compile %s after evaluation: %v", sourcePath, err)
	}
	if outPath == "" {
		ext := filepath.Ext(sourcePath)
		outPath = strings.TrimSuffix(sourcePath, ext) + ".ZZT"
	}
	if err := writeFileAtomic(outPath, data, 0644); err != nil {
		fatalf("write %s: %v", outPath, err)
	}

	if previewDir != "" {
		if err := os.MkdirAll(previewDir, 0755); err != nil {
			fatalf("create preview directory: %v", err)
		}
		for board := int16(0); board <= world.BoardCount; board++ {
			png, err := zztgo.RenderZWDBoardPNG(src, board)
			if err != nil {
				fatalf("render board %d: %v", board, err)
			}
			path := filepath.Join(previewDir, fmt.Sprintf("board-%02d.png", board))
			if err := writeFileAtomic(path, png, 0644); err != nil {
				fatalf("write %s: %v", path, err)
			}
		}
	}

	fmt.Printf("BUILT %s (%d bytes, %d boards)\n", outPath, len(data), world.BoardCount+1)
	if previewDir != "" {
		fmt.Printf("PREVIEWS %s\n", previewDir)
	}
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".zzt-build-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "zzt-build: "+format+"\n", args...)
	os.Exit(1)
}
