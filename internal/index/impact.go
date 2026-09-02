package index

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type SymbolChangeKind string

const (
	SymbolAdded    SymbolChangeKind = "added"
	SymbolModified SymbolChangeKind = "modified"
	SymbolDeleted  SymbolChangeKind = "deleted"
)

type SymbolIdentity struct {
	Repository string     `json:"repository"`
	Language   string     `json:"language"`
	Package    string     `json:"package"`
	Name       string     `json:"name"`
	Kind       SymbolKind `json:"kind"`
}

type SymbolChange struct {
	Symbol SymbolIdentity   `json:"symbol"`
	Change SymbolChangeKind `json:"change"`
}

type FileChange struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"`
	Status  string `json:"status"`
}

type ImpactReport struct {
	Snapshot             SourceSnapshot   `json:"snapshot"`
	ChangedFiles         []FileChange     `json:"changed_files"`
	ChangedSymbols       []SymbolChange   `json:"changed_symbols"`
	AffectedSymbols      []SymbolIdentity `json:"affected_symbols"`
	Interfaces           []SymbolIdentity `json:"interfaces"`
	ConfigStructs        []SymbolIdentity `json:"config_structs"`
	Services             []SymbolIdentity `json:"services"`
	CandidateTests       []SymbolIdentity `json:"candidate_tests"`
	AffectedRepositories []string         `json:"affected_repositories"`
	ImpactDigest         string           `json:"impact_digest"`
}

type parsedSymbolContent struct {
	identity SymbolIdentity
	digest   string
}

func (h *IndexHolder) ImpactSince(repository, base string) (ImpactReport, error) {
	h.mu.RLock()
	idx := h.idx
	generation := h.generation
	var selected RepositoryStatus
	for _, candidate := range h.repositories {
		if candidate.Name == repository {
			selected = candidate
			break
		}
	}
	h.mu.RUnlock()
	if selected.Name == "" {
		return ImpactReport{}, fmt.Errorf("repository %q is not in the published index", repository)
	}
	if selected.GitRoot == "" || selected.WorktreeDigest == "" {
		return ImpactReport{}, fmt.Errorf("repository %q lacks a Git source snapshot", repository)
	}
	current, err := captureCurrentSourceIdentity(selected.GitRoot)
	if err != nil {
		return ImpactReport{}, err
	}
	if current.HeadCommit != selected.Head || current.WorktreeDigest != selected.WorktreeDigest {
		return ImpactReport{}, fmt.Errorf("repository %q changed after Goast generation %d was published; reindex before impact analysis", repository, generation)
	}
	snapshot, err := captureSourceSnapshot(selected.GitRoot, repository, base, generation)
	if err != nil {
		return ImpactReport{}, err
	}
	report, err := buildImpactReport(idx, selected, snapshot)
	if err != nil {
		return ImpactReport{}, err
	}
	report.ImpactDigest = ImpactDigest(report)
	return report, nil
}

func buildImpactReport(idx *Index, repository RepositoryStatus, snapshot SourceSnapshot) (ImpactReport, error) {
	activePath, err := canonicalDirectory(repository.ActivePath)
	if err != nil {
		return ImpactReport{}, fmt.Errorf("resolve active repository %q: %w", repository.Name, err)
	}
	modulePrefix, err := filepath.Rel(repository.GitRoot, activePath)
	if err != nil {
		return ImpactReport{}, fmt.Errorf("resolve repository %q relative to Git root: %w", repository.Name, err)
	}
	if modulePrefix == ".." || strings.HasPrefix(modulePrefix, ".."+string(filepath.Separator)) {
		return ImpactReport{}, fmt.Errorf("repository %q is outside its Git root", repository.Name)
	}
	modulePrefix = filepath.ToSlash(modulePrefix)
	if modulePrefix == "." {
		modulePrefix = ""
	}
	changes, err := changedFilesSince(repository.GitRoot, snapshot.BaseCommit, modulePrefix)
	if err != nil {
		return ImpactReport{}, err
	}
	currentModule, err := ParseGoMod(filepath.Join(activePath, "go.mod"))
	if err != nil {
		return ImpactReport{}, err
	}
	baseModuleBytes, err := runGitBytes(repository.GitRoot, "show", snapshot.BaseCommit+":"+gitModulePath(modulePrefix, "go.mod"))
	if err != nil {
		return ImpactReport{}, fmt.Errorf("read base go.mod: %w", err)
	}
	baseModule, err := modulePathFromBytes(baseModuleBytes)
	if err != nil {
		return ImpactReport{}, err
	}

	baseSymbols := make(map[string]parsedSymbolContent)
	currentSymbols := make(map[string]parsedSymbolContent)
	for _, change := range changes {
		if oldPath := basePathForChange(change); strings.HasSuffix(oldPath, ".go") && change.Status != "A" {
			source, err := runGitBytes(repository.GitRoot, "show", snapshot.BaseCommit+":"+gitModulePath(modulePrefix, oldPath))
			if err != nil {
				return ImpactReport{}, fmt.Errorf("read base file %q: %w", oldPath, err)
			}
			for _, symbol := range parseSymbolContents(source, baseModule, repository.Name, oldPath, repository.IncludeTests) {
				baseSymbols[parsedSymbolKey(symbol)] = symbol
			}
		}
		if strings.HasSuffix(change.Path, ".go") && change.Status != "D" {
			source, err := os.ReadFile(filepath.Join(activePath, filepath.FromSlash(change.Path)))
			if err != nil {
				return ImpactReport{}, fmt.Errorf("read current file %q: %w", change.Path, err)
			}
			for _, symbol := range parseSymbolContents(source, currentModule, repository.Name, change.Path, repository.IncludeTests) {
				currentSymbols[parsedSymbolKey(symbol)] = symbol
			}
		}
	}

	changedSymbols := make([]SymbolChange, 0)
	for key, currentSymbol := range currentSymbols {
		baseSymbol, found := baseSymbols[key]
		switch {
		case !found:
			changedSymbols = append(changedSymbols, SymbolChange{Symbol: currentSymbol.identity, Change: SymbolAdded})
		case baseSymbol.digest != currentSymbol.digest:
			changedSymbols = append(changedSymbols, SymbolChange{Symbol: currentSymbol.identity, Change: SymbolModified})
		}
	}
	for key, baseSymbol := range baseSymbols {
		if _, found := currentSymbols[key]; !found {
			changedSymbols = append(changedSymbols, SymbolChange{Symbol: baseSymbol.identity, Change: SymbolDeleted})
		}
	}
	sortSymbolChanges(changedSymbols)

	report := ImpactReport{Snapshot: snapshot, ChangedFiles: changes, ChangedSymbols: changedSymbols}
	currentIndexSymbols := make(map[string]Symbol)
	for _, symbol := range idx.Symbols {
		identity := identityFromSymbol(symbol)
		currentIndexSymbols[symbolIdentityKey(identity)] = symbol
	}
	affected := make(map[string]SymbolIdentity)
	interfaces := make(map[string]SymbolIdentity)
	configs := make(map[string]SymbolIdentity)
	services := make(map[string]SymbolIdentity)
	affectedPackages := make(map[string]struct{})
	repositories := map[string]struct{}{repository.Name: {}}
	for _, change := range changedSymbols {
		affectedPackages[change.Symbol.Package] = struct{}{}
		currentSymbol, exists := currentIndexSymbols[symbolIdentityKey(change.Symbol)]
		if !exists {
			continue
		}
		addSymbolIdentity(affected, change.Symbol)
		for _, reference := range idx.FindReferences(change.Symbol.Package, change.Symbol.Name) {
			caller := idx.Symbols[reference.FromSymbol]
			identity := identityFromSymbol(caller)
			addSymbolIdentity(affected, identity)
			affectedPackages[identity.Package] = struct{}{}
			repositories[identity.Repository] = struct{}{}
		}
		for _, related := range relatedInterfaces(idx, currentSymbol) {
			identity := identityFromSymbol(related)
			addSymbolIdentity(interfaces, identity)
			addSymbolIdentity(affected, identity)
			affectedPackages[identity.Package] = struct{}{}
			repositories[identity.Repository] = struct{}{}
		}
		if currentSymbol.Kind == SymbolType && currentSymbol.TypeKind == TypeStruct &&
			strings.Contains(strings.ToLower(currentSymbol.Name), "config") {
			addSymbolIdentity(configs, change.Symbol)
		}
		if currentSymbol.Kind == SymbolType && currentSymbol.TypeKind == TypeInterface && currentSymbol.Generated &&
			strings.HasSuffix(currentSymbol.Name, "ServiceServer") {
			addSymbolIdentity(services, change.Symbol)
		}
	}
	for _, dependency := range idx.Dependencies {
		if dependency.ToRepo == repository.Name {
			repositories[dependency.FromRepo] = struct{}{}
		}
	}
	tests := make(map[string]SymbolIdentity)
	for _, symbol := range idx.Symbols {
		if _, affectedPackage := affectedPackages[symbol.ImportPath]; !affectedPackage ||
			symbol.Kind != SymbolFunc || !strings.HasSuffix(symbol.FilePath, "_test.go") || !strings.HasPrefix(symbol.Name, "Test") {
			continue
		}
		addSymbolIdentity(tests, identityFromSymbol(symbol))
	}
	report.AffectedSymbols = sortedSymbolIdentityMap(affected)
	report.Interfaces = sortedSymbolIdentityMap(interfaces)
	report.ConfigStructs = sortedSymbolIdentityMap(configs)
	report.Services = sortedSymbolIdentityMap(services)
	report.CandidateTests = sortedSymbolIdentityMap(tests)
	report.AffectedRepositories = sortedStringSet(repositories)
	return report, nil
}

func changedFilesSince(root, base, modulePrefix string) ([]FileChange, error) {
	diffArguments := []string{"diff", "--name-status", "-z", "--find-renames", base, "--"}
	if modulePrefix != "" {
		diffArguments = append(diffArguments, modulePrefix)
	}
	output, err := runGitBytes(root, diffArguments...)
	if err != nil {
		return nil, fmt.Errorf("read changes since %s: %w", base, err)
	}
	parts := splitNUL(output)
	changes := make([]FileChange, 0)
	for index := 0; index < len(parts); {
		status := parts[index]
		index++
		if index >= len(parts) {
			return nil, fmt.Errorf("malformed Git name-status output")
		}
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if index+1 >= len(parts) {
				return nil, fmt.Errorf("malformed Git rename output")
			}
			oldPath, oldInside := stripModulePrefix(parts[index], modulePrefix)
			newPath, newInside := stripModulePrefix(parts[index+1], modulePrefix)
			switch {
			case oldInside && newInside:
				changes = append(changes, FileChange{Status: string(status[0]), OldPath: oldPath, Path: newPath})
			case newInside:
				changes = append(changes, FileChange{Status: "A", Path: newPath})
			case oldInside:
				changes = append(changes, FileChange{Status: "D", Path: oldPath})
			}
			index += 2
			continue
		}
		if relative, inside := stripModulePrefix(parts[index], modulePrefix); inside {
			changes = append(changes, FileChange{Status: string(status[0]), Path: relative})
		}
		index++
	}
	untrackedArguments := []string{"ls-files", "--others", "--exclude-standard", "-z", "--"}
	if modulePrefix != "" {
		untrackedArguments = append(untrackedArguments, modulePrefix)
	}
	untracked, err := runGitBytes(root, untrackedArguments...)
	if err != nil {
		return nil, fmt.Errorf("list untracked changes: %w", err)
	}
	known := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		known[change.Path] = struct{}{}
	}
	for _, gitPath := range splitNUL(untracked) {
		untrackedPath, inside := stripModulePrefix(gitPath, modulePrefix)
		if !inside {
			continue
		}
		if _, found := known[untrackedPath]; !found {
			changes = append(changes, FileChange{Status: "A", Path: untrackedPath})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		return changes[i].OldPath < changes[j].OldPath
	})
	return changes, nil
}

func gitModulePath(modulePrefix, relative string) string {
	if modulePrefix == "" {
		return relative
	}
	return path.Join(modulePrefix, relative)
}

func stripModulePrefix(value, modulePrefix string) (string, bool) {
	value = filepath.ToSlash(value)
	if modulePrefix == "" {
		return value, value != ""
	}
	prefix := strings.TrimSuffix(modulePrefix, "/") + "/"
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	return strings.TrimPrefix(value, prefix), true
}

func parseSymbolContents(source []byte, module, repository, relativePath string, includeTests bool) []parsedSymbolContent {
	if strings.HasSuffix(relativePath, "_test.go") && !includeTests {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relativePath, source, parser.ParseComments)
	if err != nil {
		return nil
	}
	directory := path.Dir(filepath.ToSlash(relativePath))
	importPath := module
	if directory != "." {
		importPath += "/" + directory
	}
	if strings.HasSuffix(relativePath, "_test.go") && strings.HasSuffix(file.Name.Name, "_test") {
		importPath = externalTestSymbolPath(importPath, file.Name.Name)
	}
	tokenFile := fset.File(file.Pos())
	result := make([]parsedSymbolContent, 0)
	add := func(name string, kind SymbolKind, node ast.Node) {
		start := tokenFile.Offset(node.Pos())
		end := tokenFile.Offset(node.End())
		if start < 0 || end < start || end > len(source) {
			return
		}
		result = append(result, parsedSymbolContent{
			identity: SymbolIdentity{Repository: repository, Language: "go", Package: importPath, Name: name, Kind: kind},
			digest:   fmt.Sprintf("%x", sha256.Sum256(source[start:end])),
		})
	}
	for _, declaration := range file.Decls {
		switch typed := declaration.(type) {
		case *ast.FuncDecl:
			name := typed.Name.Name
			kind := SymbolFunc
			if typed.Recv != nil && len(typed.Recv.List) > 0 {
				kind = SymbolMethod
				name = receiverTypeName(typed.Recv.List[0].Type) + "." + name
			}
			add(name, kind, typed)
		case *ast.GenDecl:
			for _, specification := range typed.Specs {
				switch value := specification.(type) {
				case *ast.TypeSpec:
					add(value.Name.Name, SymbolType, typed)
				case *ast.ValueSpec:
					kind := SymbolVar
					if typed.Tok == token.CONST {
						kind = SymbolConst
					}
					for _, name := range value.Names {
						add(name.Name, kind, typed)
					}
				}
			}
		}
	}
	return result
}

func modulePathFromBytes(value []byte) (string, error) {
	for _, line := range strings.Split(string(value), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("base go.mod has no module directive")
}

func relatedInterfaces(idx *Index, symbol Symbol) []Symbol {
	if symbol.Kind == SymbolMethod && symbol.Receiver != "" {
		return idx.FindInterfaces(symbol.ImportPath, symbol.Receiver)
	}
	if symbol.Kind != SymbolType {
		return nil
	}
	if symbol.TypeKind == TypeInterface {
		return idx.FindImplementations(symbol.ImportPath, symbol.Name)
	}
	return idx.FindInterfaces(symbol.ImportPath, symbol.Name)
}

func identityFromSymbol(symbol Symbol) SymbolIdentity {
	name := symbol.Name
	if symbol.Kind == SymbolMethod && symbol.Receiver != "" {
		name = symbol.Receiver + "." + symbol.Name
	}
	return SymbolIdentity{Repository: symbol.Repo, Language: "go", Package: symbol.ImportPath, Name: name, Kind: symbol.Kind}
}

func ImpactDigest(report ImpactReport) string {
	copyReport := report
	copyReport.ImpactDigest = ""
	copyReport.ChangedFiles = append([]FileChange(nil), report.ChangedFiles...)
	copyReport.ChangedSymbols = append([]SymbolChange(nil), report.ChangedSymbols...)
	copyReport.AffectedSymbols = append([]SymbolIdentity(nil), report.AffectedSymbols...)
	copyReport.Interfaces = append([]SymbolIdentity(nil), report.Interfaces...)
	copyReport.ConfigStructs = append([]SymbolIdentity(nil), report.ConfigStructs...)
	copyReport.Services = append([]SymbolIdentity(nil), report.Services...)
	copyReport.CandidateTests = append([]SymbolIdentity(nil), report.CandidateTests...)
	copyReport.AffectedRepositories = append([]string(nil), report.AffectedRepositories...)
	sort.Slice(copyReport.ChangedFiles, func(i, j int) bool {
		if copyReport.ChangedFiles[i].Path != copyReport.ChangedFiles[j].Path {
			return copyReport.ChangedFiles[i].Path < copyReport.ChangedFiles[j].Path
		}
		return copyReport.ChangedFiles[i].OldPath < copyReport.ChangedFiles[j].OldPath
	})
	sortSymbolChanges(copyReport.ChangedSymbols)
	copyReport.AffectedSymbols = sortedSymbolIdentities(copyReport.AffectedSymbols)
	copyReport.Interfaces = sortedSymbolIdentities(copyReport.Interfaces)
	copyReport.ConfigStructs = sortedSymbolIdentities(copyReport.ConfigStructs)
	copyReport.Services = sortedSymbolIdentities(copyReport.Services)
	copyReport.CandidateTests = sortedSymbolIdentities(copyReport.CandidateTests)
	sort.Strings(copyReport.AffectedRepositories)
	encoded, _ := json.Marshal(copyReport)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func basePathForChange(change FileChange) string {
	if change.OldPath != "" {
		return change.OldPath
	}
	return change.Path
}

func parsedSymbolKey(symbol parsedSymbolContent) string { return symbolIdentityKey(symbol.identity) }
func symbolIdentityKey(symbol SymbolIdentity) string {
	return symbol.Repository + "\x00" + symbol.Package + "\x00" + symbol.Name + "\x00" + string(symbol.Kind)
}
func addSymbolIdentity(target map[string]SymbolIdentity, symbol SymbolIdentity) {
	target[symbolIdentityKey(symbol)] = symbol
}
func sortedSymbolIdentityMap(values map[string]SymbolIdentity) []SymbolIdentity {
	result := make([]SymbolIdentity, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return sortedSymbolIdentities(result)
}
func sortedSymbolIdentities(values []SymbolIdentity) []SymbolIdentity {
	result := append([]SymbolIdentity(nil), values...)
	sort.Slice(result, func(i, j int) bool { return symbolIdentityKey(result[i]) < symbolIdentityKey(result[j]) })
	return result
}
func sortSymbolChanges(values []SymbolChange) {
	sort.Slice(values, func(i, j int) bool {
		left := symbolIdentityKey(values[i].Symbol) + "\x00" + string(values[i].Change)
		right := symbolIdentityKey(values[j].Symbol) + "\x00" + string(values[j].Change)
		return left < right
	})
}
func sortedStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
