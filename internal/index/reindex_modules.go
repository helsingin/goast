package index

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/build"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// go/build's external importer can consult modules in the server's working
// directory. Validate that module graph and its sources as well as GOPATH.
// Read-only/offline commands must never update the user's module files.
func digestImportModules() (string, error) {
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command(filepath.Join(build.Default.GOROOT, "bin", "go"), args...)
		cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off")
		return cmd.Output()
	}
	envData, err := run("env", "-json", "GOMOD", "GOWORK")
	if err != nil {
		return "", err
	}
	var env struct {
		GOMOD  string
		GOWORK string
	}
	if err := json.Unmarshal(envData, &env); err != nil {
		return "", err
	}
	parts := []string{string(envData)}
	for _, path := range []string{env.GOMOD, env.GOWORK} {
		if path == "" || path == "off" || path == os.DevNull {
			continue
		}
		sum := path + ".sum"
		if filepath.Base(path) == "go.mod" {
			sum = filepath.Join(filepath.Dir(path), "go.sum")
		}
		for _, file := range []string{path, sum} {
			data, err := os.ReadFile(file)
			if os.IsNotExist(err) {
				parts = append(parts, file+":absent")
				continue
			}
			if err != nil {
				return "", err
			}
			parts = append(parts, file, digestBytes(data))
		}
	}
	if (env.GOMOD == "" || env.GOMOD == os.DevNull) && (env.GOWORK == "" || env.GOWORK == "off") {
		return digestParts(parts...), nil
	}
	modules, err := run("list", "-mod=readonly", "-m", "-e", "-json", "all")
	if err != nil {
		return "", fmt.Errorf("validate external import modules: %w", err)
	}
	parts = append(parts, digestBytes(modules))
	type module struct {
		Path, Version, Dir, GoMod string
		Replace                   *module
	}
	decoder := json.NewDecoder(bytes.NewReader(modules))
	for {
		var entry module
		if err := decoder.Decode(&entry); err == io.EOF {
			break
		} else if err != nil {
			return "", err
		}
		parts = append(parts, entry.Path, entry.Version)
		if entry.Replace != nil {
			entry = *entry.Replace
		}
		// Unavailable modules are negative inputs. Go's next resolution pass
		// changes the graph/Dir if their metadata or source becomes available.
		if entry.Dir == "" {
			continue
		}
		value, err := digestIndexTree(entry.Dir, false, func(path string, info fs.FileInfo) bool {
			if info.IsDir() {
				return info.Name() == ".git"
			}
			return !isTypeInput(path)
		})
		if err != nil {
			return "", err
		}
		parts = append(parts, entry.Dir, value)
	}
	return digestParts(parts...), nil
}
