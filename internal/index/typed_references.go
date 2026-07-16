package index

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
)

// typeSourcePackage groups parsed files before constructing the distinct
// production, internal-test, and external-test type-check variants used by the
// Go toolchain. Internal tests share the production symbol namespace but use a
// distinct checking identity so imported dependants see production declarations
// only and production call sites are not emitted twice.
type typeSourcePackage struct {
	baseImportPath string
	dir            string
	fset           *token.FileSet
	production     []typeSourceFile
	internalTests  []typeSourceFile
	externalTests  map[string][]typeSourceFile
	emitReferences bool
}

type typeSourceFile struct {
	path string
	file *ast.File
}

// typeCheckPackage retains one coherent package variant. checkPath is its
// private go/types identity, while symbolPath is the namespace published by the
// index. Only referenceFiles contribute call sites; production files included
// in an internal-test variant are dependencies, not duplicate callers.
type typeCheckPackage struct {
	checkPath      string
	symbolPath     string
	importable     bool
	fset           *token.FileSet
	files          []*ast.File
	referenceFiles []*ast.File
	emitReferences bool
}

type checkedTypePackage struct {
	pkg  *types.Package
	info *types.Info
	err  error
}

type methodTarget struct {
	importPath string
	receiver   string
}

var knownGOOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "illumos": true, "ios": true, "js": true,
	"linux": true, "netbsd": true, "openbsd": true, "plan9": true,
	"solaris": true, "wasip1": true, "windows": true,
}

var knownGOARCH = map[string]bool{
	"386": true, "amd64": true, "arm": true, "arm64": true,
	"loong64": true, "mips": true, "mips64": true, "mips64le": true,
	"mipsle": true, "ppc64": true, "ppc64le": true, "riscv64": true,
	"s390x": true, "wasm": true,
}

// sourceTypeChecker imports configured packages directly from their parsed
// source. Host-context external imports prefer compiled export data; alternate
// contexts load target-selected source through go/build. Type errors are
// non-fatal to the structural index, and only selections that go/types actually
// proves are published.
type sourceTypeChecker struct {
	inputs           map[string]*typeCheckPackage
	imports          map[string]*typeCheckPackage
	checked          map[string]checkedTypePackage
	checking         map[string]bool
	fallback         types.Importer
	context          build.Context
	external         map[string]checkedTypePackage
	externalChecking map[string]bool
}

func newSourceTypeChecker(inputs map[string]*typeCheckPackage, buildContext build.Context) *sourceTypeChecker {
	checker := &sourceTypeChecker{
		inputs:           inputs,
		imports:          make(map[string]*typeCheckPackage, len(inputs)),
		checked:          make(map[string]checkedTypePackage, len(inputs)),
		checking:         make(map[string]bool, len(inputs)),
		fallback:         importer.Default(),
		context:          buildContext,
		external:         make(map[string]checkedTypePackage),
		externalChecking: make(map[string]bool),
	}
	for _, input := range inputs {
		if input.importable {
			checker.imports[input.symbolPath] = input
		}
	}
	return checker
}

func (c *sourceTypeChecker) Import(path string) (*types.Package, error) {
	return c.ImportFrom(path, "", 0)
}

func (c *sourceTypeChecker) ImportFrom(path, dir string, mode types.ImportMode) (*types.Package, error) {
	if input, ok := c.imports[path]; ok {
		checked, err := c.check(input.checkPath)
		if err == nil && checked.pkg != nil {
			return checked.pkg, nil
		}
		return nil, err
	}
	return c.importExternal(path, dir, mode)
}

func (c *sourceTypeChecker) check(path string) (checkedTypePackage, error) {
	if checked, ok := c.checked[path]; ok {
		return checked, checked.err
	}
	input, ok := c.inputs[path]
	if !ok {
		return checkedTypePackage{}, fmt.Errorf("package %q is not indexed", path)
	}
	if c.checking[path] {
		return checkedTypePackage{}, fmt.Errorf("import cycle while checking %q", path)
	}

	c.checking[path] = true
	defer delete(c.checking, path)

	var info *types.Info
	if input.emitReferences {
		info = &types.Info{
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
		}
	}
	conf := types.Config{
		Importer:    c,
		FakeImportC: inputContainsImportC(input.files),
		Sizes:       types.SizesFor(c.context.Compiler, c.context.GOARCH),
		Error:       func(error) {},
	}
	pkg, err := conf.Check(input.checkPath, input.fset, input.files, info)
	checked := checkedTypePackage{pkg: pkg, info: info, err: err}
	// Cache partial packages too. Their locally proven selections remain useful,
	// while importers reject the associated error instead of treating the partial
	// package as a sound dependency.
	c.checked[path] = checked
	return checked, err
}

func (c *sourceTypeChecker) importExternal(path, dir string, mode types.ImportMode) (*types.Package, error) {
	if checked, ok := c.external[path]; ok {
		return checked.pkg, checked.err
	}
	if c.externalChecking[path] {
		return nil, fmt.Errorf("import cycle while checking external package %q", path)
	}

	if sameBuildContext(c.context, build.Default) {
		var pkg *types.Package
		var err error
		if from, ok := c.fallback.(types.ImporterFrom); ok {
			pkg, err = from.ImportFrom(path, dir, mode)
		} else {
			pkg, err = c.fallback.Import(path)
		}
		if err == nil {
			return pkg, nil
		}
	}

	buildPackage, buildErr := c.context.Import(path, dir, 0)
	if buildErr != nil && buildPackage == nil {
		return nil, buildErr
	}
	if buildPackage == nil {
		return nil, fmt.Errorf("build context could not locate package %q", path)
	}

	c.externalChecking[path] = true
	defer delete(c.externalChecking, path)
	fset := token.NewFileSet()
	fileNames := make([]string, 0, len(buildPackage.GoFiles)+len(buildPackage.CgoFiles))
	fileNames = append(fileNames, buildPackage.GoFiles...)
	fileNames = append(fileNames, buildPackage.CgoFiles...)
	files := make([]*ast.File, 0, len(fileNames))
	for _, name := range fileNames {
		file, err := parser.ParseFile(fset, filepath.Join(buildPackage.Dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("package %q has no files in build context %s/%s", path, c.context.GOOS, c.context.GOARCH)
	}
	conf := types.Config{
		Importer:    c,
		FakeImportC: len(buildPackage.CgoFiles) > 0,
		Sizes:       types.SizesFor(c.context.Compiler, c.context.GOARCH),
		Error:       func(error) {},
	}
	pkg, err := conf.Check(path, fset, files, nil)
	checked := checkedTypePackage{pkg: pkg, err: err}
	c.external[path] = checked
	if err != nil {
		return nil, err
	}
	return pkg, nil
}

// collectTypedMethodReferencesForContexts uses go/types selections to resolve method
// values, method expressions, direct calls, and promoted-method calls. It does
// not guess when type checking cannot prove a receiver, so every emitted target
// identifies one concrete declared Receiver.Method symbol.
func collectTypedMethodReferencesForContexts(sources map[string]*typeSourcePackage, configured []BuildContext) ([]rawReference, error) {
	buildContexts, err := effectiveBuildContexts(configured)
	if err != nil {
		return nil, err
	}
	var refs []rawReference
	for _, buildContext := range buildContexts {
		inputs := buildTypeCheckPackages(sources, buildContext)
		refs = append(refs, collectTypedMethodReferences(inputs, buildContext)...)
	}
	return refs, nil
}

func collectTypedMethodReferences(inputs map[string]*typeCheckPackage, buildContext build.Context) []rawReference {
	checker := newSourceTypeChecker(inputs, buildContext)
	paths := make([]string, 0, len(inputs))
	for path := range inputs {
		if inputs[path].emitReferences {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	for _, path := range paths {
		_, _ = checker.check(path)
	}
	checkedPaths := make([]string, 0, len(checker.checked))
	for path := range checker.checked {
		checkedPaths = append(checkedPaths, path)
	}
	sort.Strings(checkedPaths)
	interfaceMethods := checker.interfaceMethodTargets(checkedPaths)

	var refs []rawReference
	for _, path := range paths {
		checked := checker.checked[path]
		if checked.info == nil {
			continue
		}
		input := inputs[path]
		for _, file := range input.referenceFiles {
			refs = append(refs, typedMethodReferencesInFile(input.fset, file, input.symbolPath, checked.info, interfaceMethods, checker.symbolPath)...)
		}
	}
	return refs
}

func (c *sourceTypeChecker) interfaceMethodTargets(paths []string) map[*types.Func]methodTarget {
	targets := make(map[*types.Func]methodTarget)
	for _, path := range paths {
		pkg := c.checked[path].pkg
		if pkg == nil {
			continue
		}
		names := pkg.Scope().Names()
		sort.Strings(names)
		for _, name := range names {
			typeName, ok := pkg.Scope().Lookup(name).(*types.TypeName)
			if !ok || typeName.IsAlias() {
				continue
			}
			named, ok := typeName.Type().(*types.Named)
			if !ok {
				continue
			}
			iface, ok := named.Underlying().(*types.Interface)
			if !ok {
				continue
			}
			iface.Complete()
			for i := 0; i < iface.NumExplicitMethods(); i++ {
				method := iface.ExplicitMethod(i)
				targets[method] = methodTarget{importPath: c.inputs[path].symbolPath, receiver: typeName.Name()}
			}
		}
	}
	return targets
}

func typedMethodReferencesInFile(fset *token.FileSet, file *ast.File, importPath string, info *types.Info, interfaceMethods map[*types.Func]methodTarget, canonicalPath func(string) string) []rawReference {
	var refs []rawReference
	filename := fset.Position(file.Pos()).Filename

	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}

		fromName := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			if recv := receiverTypeName(fd.Recv.List[0].Type); recv != "" {
				fromName = recv + "." + fd.Name.Name
			}
		}

		ast.Inspect(fd.Body, func(node ast.Node) bool {
			selection, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			typedSelection, ok := info.Selections[selection]
			if !ok {
				return true
			}
			method, ok := typedSelection.Obj().(*types.Func)
			if !ok {
				return true
			}
			targetPath, receiver := declaredReceiver(method)
			targetPath = canonicalPath(targetPath)
			if target, ok := interfaceMethods[method]; ok {
				targetPath, receiver = target.importPath, target.receiver
			}
			if targetPath == "" || receiver == "" {
				return true
			}
			refs = append(refs, rawReference{
				fromImportPath: importPath,
				fromName:       fromName,
				targetPath:     targetPath,
				targetReceiver: receiver,
				targetName:     method.Name(),
				filePath:       filename,
				line:           fset.Position(selection.Sel.Pos()).Line,
				column:         fset.Position(selection.Sel.Pos()).Column,
			})
			return true
		})
	}
	return refs
}

func (c *sourceTypeChecker) symbolPath(checkPath string) string {
	if input, ok := c.inputs[checkPath]; ok {
		return input.symbolPath
	}
	return checkPath
}

func addTypeSourceFile(sources map[string]*typeSourcePackage, baseImportPath, symbolPath string, fset *token.FileSet, file *ast.File, filePath string, testFile, emitReferences bool) {
	source, ok := sources[baseImportPath]
	if !ok {
		source = &typeSourcePackage{
			baseImportPath: baseImportPath,
			dir:            filepath.Dir(filePath),
			fset:           fset,
			externalTests:  make(map[string][]typeSourceFile),
			emitReferences: emitReferences,
		}
		sources[baseImportPath] = source
	}
	// Duplicate import paths across configured repositories are already
	// ambiguous in the public index. Keep the first FileSet authoritative for
	// typed references rather than merging unrelated AST position spaces.
	if source.fset != fset {
		return
	}
	source.emitReferences = source.emitReferences || emitReferences
	typedFile := typeSourceFile{path: filePath, file: file}
	switch {
	case !testFile:
		source.production = append(source.production, typedFile)
	case symbolPath == baseImportPath:
		source.internalTests = append(source.internalTests, typedFile)
	default:
		source.externalTests[symbolPath] = append(source.externalTests[symbolPath], typedFile)
	}
}

func buildTypeCheckPackages(sources map[string]*typeSourcePackage, buildContext build.Context) map[string]*typeCheckPackage {
	inputs := make(map[string]*typeCheckPackage, len(sources)*2)
	for _, source := range sources {
		production, internalTests, selectedExternal := selectedTypeSourceFiles(buildContext, source)
		if len(production) > 0 {
			referenceFiles := production
			if !source.emitReferences {
				referenceFiles = nil
			}
			inputs[source.baseImportPath] = &typeCheckPackage{
				checkPath:      source.baseImportPath,
				symbolPath:     source.baseImportPath,
				importable:     true,
				fset:           source.fset,
				files:          production,
				referenceFiles: referenceFiles,
				emitReferences: source.emitReferences,
			}
		}
		if len(internalTests) > 0 {
			checkPath := source.baseImportPath + " [internal.test]"
			inputs[checkPath] = &typeCheckPackage{
				checkPath:      checkPath,
				symbolPath:     source.baseImportPath,
				fset:           source.fset,
				files:          append(append(make([]*ast.File, 0, len(production)+len(internalTests)), production...), internalTests...),
				referenceFiles: internalTests,
				emitReferences: source.emitReferences,
			}
		}
		for symbolPath := range source.externalTests {
			files := selectedExternal[symbolPath]
			if len(files) == 0 {
				continue
			}
			inputs[symbolPath] = &typeCheckPackage{
				checkPath:      symbolPath,
				symbolPath:     symbolPath,
				fset:           source.fset,
				files:          files,
				referenceFiles: files,
				emitReferences: source.emitReferences,
			}
		}
	}
	return inputs
}

func matchingTypeSourceFiles(buildContext build.Context, sourceFiles []typeSourceFile) []*ast.File {
	files := make([]*ast.File, 0, len(sourceFiles))
	for _, sourceFile := range sourceFiles {
		match, err := buildContext.MatchFile(filepath.Dir(sourceFile.path), filepath.Base(sourceFile.path))
		if err == nil && match && (buildContext.CgoEnabled || !astFileImportsC(sourceFile.file)) {
			files = append(files, sourceFile.file)
		}
	}
	return files
}

func selectedTypeSourceFiles(buildContext build.Context, source *typeSourcePackage) (production, internalTests []*ast.File, external map[string][]*ast.File) {
	external = make(map[string][]*ast.File, len(source.externalTests))
	buildPackage, _ := buildContext.ImportDir(source.dir, 0)
	if buildPackage == nil {
		production = matchingTypeSourceFiles(buildContext, source.production)
		internalTests = matchingTypeSourceFiles(buildContext, source.internalTests)
		for symbolPath, files := range source.externalTests {
			external[symbolPath] = matchingTypeSourceFiles(buildContext, files)
		}
		return production, internalTests, external
	}
	productionNames := fileNameSet(buildPackage.GoFiles, buildPackage.CgoFiles)
	internalNames := fileNameSet(buildPackage.TestGoFiles)
	externalNames := fileNameSet(buildPackage.XTestGoFiles)
	production = typeSourceFilesNamed(source.production, productionNames)
	internalTests = typeSourceFilesNamed(source.internalTests, internalNames)
	for symbolPath, files := range source.externalTests {
		external[symbolPath] = typeSourceFilesNamed(files, externalNames)
	}
	return production, internalTests, external
}

func fileNameSet(groups ...[]string) map[string]bool {
	result := make(map[string]bool)
	for _, group := range groups {
		for _, name := range group {
			result[name] = true
		}
	}
	return result
}

func typeSourceFilesNamed(sourceFiles []typeSourceFile, names map[string]bool) []*ast.File {
	files := make([]*ast.File, 0, len(sourceFiles))
	for _, sourceFile := range sourceFiles {
		if names[filepath.Base(sourceFile.path)] {
			files = append(files, sourceFile.file)
		}
	}
	return files
}

func effectiveBuildContexts(configured []BuildContext) ([]build.Context, error) {
	if len(configured) == 0 {
		if types.SizesFor(build.Default.Compiler, build.Default.GOARCH) == nil {
			return nil, fmt.Errorf("host build context has no %s type sizes for GOARCH %q", build.Default.Compiler, build.Default.GOARCH)
		}
		return []build.Context{build.Default}, nil
	}
	contexts := make([]build.Context, 0, len(configured))
	seen := make(map[string]bool, len(configured))
	for configuredIndex, configuredContext := range configured {
		buildContext := build.Default
		if configuredContext.GOOS != "" {
			buildContext.GOOS = configuredContext.GOOS
		}
		if configuredContext.GOARCH != "" {
			buildContext.GOARCH = configuredContext.GOARCH
		}
		if configuredContext.CGOEnabled != nil {
			buildContext.CgoEnabled = *configuredContext.CGOEnabled
		} else if buildContext.GOOS != build.Default.GOOS || buildContext.GOARCH != build.Default.GOARCH {
			buildContext.CgoEnabled = false
		}
		buildTags, err := normalizedBuildTags(configuredContext.BuildTags)
		if err != nil {
			return nil, fmt.Errorf("build context %d: build_tags: %w", configuredIndex, err)
		}
		buildContext.BuildTags = buildTags
		if configuredContext.ToolTags != nil {
			toolTags, err := normalizedBuildTags(configuredContext.ToolTags)
			if err != nil {
				return nil, fmt.Errorf("build context %d: tool_tags: %w", configuredIndex, err)
			}
			buildContext.ToolTags = toolTags
		} else if buildContext.GOARCH != build.Default.GOARCH {
			buildContext.ToolTags = nil
		}
		if buildContext.GOOS == "" || buildContext.GOARCH == "" {
			return nil, fmt.Errorf("build context %d: GOOS and GOARCH must resolve to non-empty values", configuredIndex)
		}
		if !knownGOOS[buildContext.GOOS] {
			return nil, fmt.Errorf("build context %d: unsupported GOOS %q", configuredIndex, buildContext.GOOS)
		}
		if !knownGOARCH[buildContext.GOARCH] {
			return nil, fmt.Errorf("build context %d: unsupported GOARCH %q", configuredIndex, buildContext.GOARCH)
		}
		if types.SizesFor(buildContext.Compiler, buildContext.GOARCH) == nil {
			return nil, fmt.Errorf("build context %d: no %s type sizes for GOARCH %q", configuredIndex, buildContext.Compiler, buildContext.GOARCH)
		}
		runtimePackage, runtimeErr := buildContext.Import("runtime", "", 0)
		if runtimeErr != nil || runtimePackage == nil || len(runtimePackage.GoFiles)+len(runtimePackage.CgoFiles) == 0 {
			return nil, fmt.Errorf("build context %d: unsupported or incoherent target %s/%s", configuredIndex, buildContext.GOOS, buildContext.GOARCH)
		}
		key := strings.Join([]string{
			buildContext.GOOS,
			buildContext.GOARCH,
			fmt.Sprint(buildContext.CgoEnabled),
			strings.Join(buildContext.BuildTags, ","),
			strings.Join(buildContext.ToolTags, ","),
		}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		contexts = append(contexts, buildContext)
	}
	return contexts, nil
}

func normalizedBuildTags(tags []string) ([]string, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	result := append([]string(nil), tags...)
	sort.Strings(result)
	out := result[:0]
	for _, tag := range result {
		if !validBuildTag(tag) {
			return nil, fmt.Errorf("invalid tag %q", tag)
		}
		if len(out) == 0 || tag != out[len(out)-1] {
			out = append(out, tag)
		}
	}
	return out, nil
}

func validBuildTag(tag string) bool {
	if tag == "" {
		return false
	}
	for _, char := range tag {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func sameBuildContext(a, b build.Context) bool {
	return a.GOOS == b.GOOS &&
		a.GOARCH == b.GOARCH &&
		a.CgoEnabled == b.CgoEnabled &&
		strings.Join(a.BuildTags, "\x00") == strings.Join(b.BuildTags, "\x00") &&
		strings.Join(a.ToolTags, "\x00") == strings.Join(b.ToolTags, "\x00") &&
		strings.Join(a.ReleaseTags, "\x00") == strings.Join(b.ReleaseTags, "\x00")
}

func inputContainsImportC(files []*ast.File) bool {
	for _, file := range files {
		if astFileImportsC(file) {
			return true
		}
	}
	return false
}

func astFileImportsC(file *ast.File) bool {
	for _, spec := range file.Imports {
		if spec.Path != nil && spec.Path.Value == `"C"` {
			return true
		}
	}
	return false
}

func externalTestSymbolPath(baseImportPath, packageName string) string {
	// Whitespace is forbidden in Go import paths, which makes this namespace
	// provably disjoint from every real package while remaining readable in MCP
	// results and exact-package arguments.
	return baseImportPath + " [" + packageName + "]"
}

func declaredReceiver(method *types.Func) (importPath, name string) {
	signature, ok := method.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return "", ""
	}
	typ := types.Unalias(signature.Recv().Type())
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	}
	named, ok := typ.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return "", ""
	}
	return named.Obj().Pkg().Path(), named.Obj().Name()
}
