package index

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RepoConfig describes a repository to index.
type RepoConfig struct {
	Path string
}

// IndexConfig holds configuration for building an index.
type IndexConfig struct {
	Repos           []RepoConfig
	ExcludePatterns []string
}

// BuildIndex walks all configured repos, parses Go files, and builds an in-memory index.
func BuildIndex(cfg IndexConfig) (*Index, error) {
	var allSymbols []Symbol
	var allPackages []Package
	var allRawRefs []rawReference

	// Track packages by import path to aggregate across files.
	type pkgInfo struct {
		importPath string
		name       string
		repo       string
		dir        string
		doc        string
		fileCount  int
		internal   bool
	}
	pkgMap := make(map[string]*pkgInfo)

	// Collect module paths per repo for cross-repo dependency detection.
	type repoModule struct {
		name       string
		modulePath string
	}
	var repoModules []repoModule

	// First pass: collect module paths.
	for _, rc := range cfg.Repos {
		repoRoot := rc.Path
		repoName := filepath.Base(repoRoot)
		goModPath := filepath.Join(repoRoot, "go.mod")
		modulePath, err := ParseGoMod(goModPath)
		if err != nil {
			continue
		}
		repoModules = append(repoModules, repoModule{name: repoName, modulePath: modulePath})
	}

	// Track all imports per repo for cross-repo dependency detection.
	// Key: repoName, value: set of import paths from that repo's files.
	repoImports := make(map[string]map[string]bool)

	for _, rc := range cfg.Repos {
		repoRoot := rc.Path
		repoName := filepath.Base(repoRoot)

		goModPath := filepath.Join(repoRoot, "go.mod")
		modulePath, err := ParseGoMod(goModPath)
		if err != nil {
			log.Printf("WARNING: skipping repo %s: %v", repoName, err)
			continue
		}

		fset := token.NewFileSet()
		if repoImports[repoName] == nil {
			repoImports[repoName] = make(map[string]bool)
		}

		err = filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip inaccessible paths
			}
			if info.IsDir() {
				base := info.Name()
				if base == "vendor" || base == "testdata" || base == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(info.Name(), ".go") {
				return nil
			}
			if strings.HasSuffix(info.Name(), "_test.go") {
				return nil
			}
			if shouldExclude(path, repoRoot, cfg.ExcludePatterns) {
				return nil
			}

			generated := strings.HasSuffix(info.Name(), ".pb.go")
			importPath := DeriveImportPath(modulePath, repoRoot, path)
			internal := strings.Contains(importPath, "/internal/") || strings.HasSuffix(importPath, "/internal")

			// Track package info.
			if _, ok := pkgMap[importPath]; !ok {
				pkgMap[importPath] = &pkgInfo{
					importPath: importPath,
					name:       "", // filled from AST
					repo:       repoName,
					dir:        filepath.Dir(path),
					internal:   internal,
				}
			}
			pkgMap[importPath].fileCount++

			symbols, pkgDoc, fileImports, fileRefs, err := parseFile(fset, path, repoName, importPath, generated)
			if err != nil {
				log.Printf("WARNING: parse error in %s: %v", path, err)
				return nil
			}

			allSymbols = append(allSymbols, symbols...)
			allRawRefs = append(allRawRefs, fileRefs...)

			// Collect imports for dependency detection.
			for _, imp := range fileImports {
				repoImports[repoName][imp] = true
			}

			pi := pkgMap[importPath]
			if pi.name == "" && len(symbols) > 0 {
				pi.name = symbols[0].PkgName
			}
			if pi.doc == "" && pkgDoc != "" {
				pi.doc = pkgDoc
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking repo %s: %w", repoName, err)
		}
	}

	// Build cross-repo dependency map.
	var deps []RepoDependency
	for fromRepo, imports := range repoImports {
		for _, rm := range repoModules {
			if rm.name == fromRepo {
				continue
			}
			// Find all imports from fromRepo that match rm's module path.
			var matchedPkgs []string
			for imp := range imports {
				if imp == rm.modulePath || strings.HasPrefix(imp, rm.modulePath+"/") {
					matchedPkgs = append(matchedPkgs, imp)
				}
			}
			if len(matchedPkgs) > 0 {
				sort.Strings(matchedPkgs)
				deps = append(deps, RepoDependency{
					FromRepo:    fromRepo,
					ToRepo:      rm.name,
					ImportPaths: matchedPkgs,
				})
			}
		}
	}

	// Count symbols per package.
	symCountByPkg := make(map[string]int)
	for _, s := range allSymbols {
		symCountByPkg[s.ImportPath]++
	}

	// Build package list.
	for _, pi := range pkgMap {
		allPackages = append(allPackages, Package{
			ImportPath:  pi.importPath,
			Name:        pi.name,
			Repo:        pi.repo,
			Dir:         pi.dir,
			Doc:         pi.doc,
			DocSummary:  FirstSentence(pi.doc),
			FileCount:   pi.fileCount,
			SymbolCount: symCountByPkg[pi.importPath],
			Internal:    pi.internal,
		})
	}

	idx := NewIndex(allSymbols, allPackages, deps)
	idx.buildReferences(allRawRefs)
	return idx, nil
}

func shouldExclude(path, repoRoot string, patterns []string) bool {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return false
	}
	for _, pat := range patterns {
		matched, _ := filepath.Match(pat, rel)
		if matched {
			return true
		}
		// Also try matching just the filename.
		matched, _ = filepath.Match(pat, filepath.Base(rel))
		if matched {
			return true
		}
	}
	return false
}

func parseFile(fset *token.FileSet, path, repoName, importPath string, generated bool) ([]Symbol, string, []string, []rawReference, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, "", nil, nil, err
	}

	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, "", nil, nil, err
	}

	pkgName := file.Name.Name
	pkgDoc := ""
	if file.Doc != nil {
		pkgDoc = file.Doc.Text()
	}

	// Extract imports and build alias→importPath map for embed resolution.
	var imports []string
	aliasMap := make(map[string]string, len(file.Imports))
	for _, imp := range file.Imports {
		// imp.Path.Value is quoted, e.g. `"fmt"`.
		p := strings.Trim(imp.Path.Value, `"`)
		imports = append(imports, p)

		if imp.Name != nil {
			switch imp.Name.Name {
			case "_", ".":
				// Blank and dot imports don't bind a qualifier we can use for embed resolution.
				continue
			default:
				aliasMap[imp.Name.Name] = p
			}
		} else {
			// Default alias is the last path segment. Approximation — if the
			// imported package's declared name differs from its directory, embed
			// refs through that alias will fail to resolve. Acceptable: the
			// convention holds for almost all Go code.
			alias := p
			if i := strings.LastIndex(p, "/"); i >= 0 {
				alias = p[i+1:]
			}
			aliasMap[alias] = p
		}
	}

	var symbols []Symbol

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			sym := extractFunc(fset, d, repoName, importPath, pkgName, path, generated)
			symbols = append(symbols, sym)

		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					ts := spec.(*ast.TypeSpec)
					sym := extractType(fset, d, ts, repoName, importPath, pkgName, path, generated, aliasMap)
					symbols = append(symbols, sym)
				}
			case token.CONST, token.VAR:
				syms := extractConstVar(fset, d, repoName, importPath, pkgName, path, generated)
				symbols = append(symbols, syms...)
			}
		}
	}

	symbols = extractDefaults(fset, file, symbols)
	refs := collectReferences(fset, file, importPath, aliasMap)

	return symbols, pkgDoc, imports, refs, nil
}

func extractFunc(fset *token.FileSet, d *ast.FuncDecl, repo, importPath, pkgName, filePath string, generated bool) Symbol {
	kind := SymbolFunc
	receiver := ""
	descriptor := ""
	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = SymbolMethod
		receiver = receiverTypeName(d.Recv.List[0].Type)
		descriptor = methodDescriptor(d.Name.Name, d.Type, fset)
	}

	sig := formatFuncSignature(fset, d)
	doc := ExtractDoc(d.Doc)

	pos := fset.Position(d.Pos())
	endPos := fset.Position(d.End())

	return Symbol{
		Name:             d.Name.Name,
		Kind:             kind,
		Exported:         d.Name.IsExported(),
		Repo:             repo,
		ImportPath:       importPath,
		PkgName:          pkgName,
		FilePath:         filePath,
		Line:             pos.Line,
		EndLine:          endPos.Line,
		Signature:        sig,
		Receiver:         receiver,
		MethodDescriptor: descriptor,
		Doc:              doc,
		DocSummary:       FirstSentence(doc),
		Generated:        generated,
	}
}

func extractType(fset *token.FileSet, gd *ast.GenDecl, ts *ast.TypeSpec, repo, importPath, pkgName, filePath string, generated bool, aliasMap map[string]string) Symbol {
	var tk TypeKind
	var fields []Field
	var methods []string
	var methodDescs []string
	var embeds []EmbeddedRef

	switch t := ts.Type.(type) {
	case *ast.StructType:
		tk = TypeStruct
		fields, embeds = extractStructFields(fset, t, aliasMap, importPath)
	case *ast.InterfaceType:
		tk = TypeInterface
		methods, methodDescs, embeds = extractInterfaceMethods(fset, t, aliasMap, importPath)
	default:
		tk = TypeOther
	}

	// Use the doc from the GenDecl if the TypeSpec has none (common for single-type decls).
	doc := ExtractDoc(ts.Doc)
	if doc == "" {
		doc = ExtractDoc(gd.Doc)
	}

	sig := formatTypeSignature(fset, ts)

	pos := fset.Position(ts.Pos())
	// For the end line, use the GenDecl end if this is the only spec (covers the closing brace).
	endPos := fset.Position(ts.End())
	if len(gd.Specs) == 1 {
		endPos = fset.Position(gd.End())
	}

	return Symbol{
		Name:              ts.Name.Name,
		Kind:              SymbolType,
		Exported:          ts.Name.IsExported(),
		Repo:              repo,
		ImportPath:        importPath,
		PkgName:           pkgName,
		FilePath:          filePath,
		Line:              pos.Line,
		EndLine:           endPos.Line,
		Signature:         sig,
		TypeKind:          tk,
		Fields:            fields,
		Methods:           methods,
		MethodDescriptors: methodDescs,
		Embeds:            embeds,
		Doc:               doc,
		DocSummary:        FirstSentence(doc),
		Generated:         generated,
	}
}

func extractConstVar(fset *token.FileSet, gd *ast.GenDecl, repo, importPath, pkgName, filePath string, generated bool) []Symbol {
	var kind SymbolKind
	if gd.Tok == token.CONST {
		kind = SymbolConst
	} else {
		kind = SymbolVar
	}

	var symbols []Symbol
	for _, spec := range gd.Specs {
		vs := spec.(*ast.ValueSpec)
		// Use spec doc, fall back to gd doc for single-spec decls.
		doc := ExtractDoc(vs.Doc)
		if doc == "" && len(gd.Specs) == 1 {
			doc = ExtractDoc(gd.Doc)
		}

		for nameIdx, name := range vs.Names {
			sig := name.Name
			if vs.Type != nil {
				sig += " " + exprString(fset, vs.Type)
			}
			if nameIdx < len(vs.Values) {
				sig += " = " + constValueString(fset, vs.Values[nameIdx])
			}

			pos := fset.Position(vs.Pos())
			endPos := fset.Position(vs.End())

			symbols = append(symbols, Symbol{
				Name:       name.Name,
				Kind:       kind,
				Exported:   name.IsExported(),
				Repo:       repo,
				ImportPath: importPath,
				PkgName:    pkgName,
				FilePath:   filePath,
				Line:       pos.Line,
				EndLine:    endPos.Line,
				Signature:  sig,
				Doc:        doc,
				DocSummary: FirstSentence(doc),
				Generated:  generated,
			})
		}
	}
	return symbols
}

func extractStructFields(fset *token.FileSet, st *ast.StructType, aliasMap map[string]string, currentPath string) ([]Field, []EmbeddedRef) {
	if st.Fields == nil {
		return nil, nil
	}
	var fields []Field
	var embeds []EmbeddedRef
	for _, f := range st.Fields.List {
		typeStr := exprString(fset, f.Type)
		tag := ""
		if f.Tag != nil {
			tag = f.Tag.Value
		}
		doc := ExtractDoc(f.Doc)

		if len(f.Names) == 0 {
			// Embedded field.
			fields = append(fields, Field{
				Name: typeStr,
				Type: typeStr,
				Tag:  tag,
				Doc:  doc,
			})
			if ref, ok := resolveEmbed(f.Type, aliasMap, currentPath); ok {
				embeds = append(embeds, ref)
			}
		} else {
			for _, name := range f.Names {
				fields = append(fields, Field{
					Name: name.Name,
					Type: typeStr,
					Tag:  tag,
					Doc:  doc,
				})
			}
		}
	}
	return fields, embeds
}

func extractInterfaceMethods(fset *token.FileSet, it *ast.InterfaceType, aliasMap map[string]string, currentPath string) (methods []string, descriptors []string, embeds []EmbeddedRef) {
	if it.Methods == nil {
		return nil, nil, nil
	}
	for _, m := range it.Methods.List {
		if len(m.Names) > 0 {
			// Named method.
			sig := m.Names[0].Name + strings.TrimPrefix(exprString(fset, m.Type), "func")
			methods = append(methods, sig)

			// Compute descriptor if the type is a function type.
			if ft, ok := m.Type.(*ast.FuncType); ok {
				descriptors = append(descriptors, methodDescriptor(m.Names[0].Name, ft, fset))
			}
		} else {
			// Embedded interface. Skip type-elem constraints (union/tilde forms
			// used in generic constraints) — those aren't interfaces we can flatten.
			if _, isFunc := m.Type.(*ast.FuncType); isFunc {
				continue
			}
			if ref, ok := resolveEmbed(m.Type, aliasMap, currentPath); ok {
				embeds = append(embeds, ref)
			}
		}
	}
	return methods, descriptors, embeds
}

func formatFuncSignature(fset *token.FileSet, d *ast.FuncDecl) string {
	// Clone the decl without body.
	clone := *d
	clone.Body = nil

	var buf bytes.Buffer
	err := printer.Fprint(&buf, fset, &clone)
	if err != nil {
		return d.Name.Name + "(...)"
	}
	return buf.String()
}

func formatTypeSignature(fset *token.FileSet, ts *ast.TypeSpec) string {
	return "type " + ts.Name.Name + " " + exprString(fset, ts.Type)
}

func exprString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	printer.Fprint(&buf, fset, expr)
	return buf.String()
}

// resolveEmbed turns an embedded-type AST expression into an EmbeddedRef with the
// import path resolved via the file's alias map. Returns false if the expression
// isn't a recognizable type name (e.g. a type-constraint union like A|B).
//
// Handles: Ident (`Foo`), SelectorExpr (`pb.Foo`), StarExpr (`*Foo` / `*pb.Foo`),
// IndexExpr / IndexListExpr (generics: `Foo[T]`, `Foo[T,U]`).
func resolveEmbed(expr ast.Expr, aliasMap map[string]string, currentPath string) (EmbeddedRef, bool) {
	qualifier, name := splitEmbedName(expr)
	if name == "" {
		return EmbeddedRef{}, false
	}
	if qualifier == "" {
		return EmbeddedRef{ImportPath: currentPath, Name: name}, true
	}
	if p, ok := aliasMap[qualifier]; ok {
		return EmbeddedRef{ImportPath: p, Name: name}, true
	}
	// Qualified but unresolved — the import isn't in our alias map (shouldn't
	// happen for well-formed code). Emit with an empty ImportPath so buildImplMap
	// can fall back to a by-name lookup if it wants to.
	return EmbeddedRef{ImportPath: "", Name: name}, true
}

func splitEmbedName(expr ast.Expr) (qualifier, name string) {
	switch t := expr.(type) {
	case *ast.Ident:
		return "", t.Name
	case *ast.SelectorExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name, t.Sel.Name
		}
		return "", t.Sel.Name
	case *ast.StarExpr:
		return splitEmbedName(t.X)
	case *ast.IndexExpr:
		return splitEmbedName(t.X)
	case *ast.IndexListExpr:
		return splitEmbedName(t.X)
	}
	return "", ""
}

// methodDescriptor computes a normalized method descriptor for interface matching.
// Format: "Name(type1,type2)(rettype1,rettype2)" — parameter names are stripped.
// Examples: "Speak(string)(error)", "Volume()(int)", "Process(context.Context,*Request)(*Response,error)"
func methodDescriptor(name string, ft *ast.FuncType, fset *token.FileSet) string {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('(')
	if ft.Params != nil {
		first := true
		for _, field := range ft.Params.List {
			typeStr := exprString(fset, field.Type)
			// Each field can declare multiple names (e.g., "a, b int").
			count := len(field.Names)
			if count == 0 {
				count = 1 // unnamed parameter
			}
			for i := 0; i < count; i++ {
				if !first {
					b.WriteByte(',')
				}
				b.WriteString(typeStr)
				first = false
			}
		}
	}
	b.WriteByte(')')
	b.WriteByte('(')
	if ft.Results != nil {
		first := true
		for _, field := range ft.Results.List {
			typeStr := exprString(fset, field.Type)
			count := len(field.Names)
			if count == 0 {
				count = 1
			}
			for i := 0; i < count; i++ {
				if !first {
					b.WriteByte(',')
				}
				b.WriteString(typeStr)
				first = false
			}
		}
	}
	b.WriteByte(')')
	return b.String()
}

// extractDefaults walks function declarations looking for return statements
// with composite literals. For each composite literal, it extracts field → value
// mappings and populates DefaultValue on matching struct symbols from the same file.
func extractDefaults(fset *token.FileSet, file *ast.File, symbols []Symbol) []Symbol {
	// Build a map from struct type name → indices in symbols slice (same file only).
	structIndices := make(map[string][]int)
	for i, s := range symbols {
		if s.Kind == SymbolType && s.TypeKind == TypeStruct && len(s.Fields) > 0 {
			structIndices[s.Name] = append(structIndices[s.Name], i)
		}
	}
	if len(structIndices) == 0 {
		return symbols
	}

	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		// Walk the function body looking for return statements with composite literals.
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			ret, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, expr := range ret.Results {
				processCompositeLit(fset, expr, symbols, structIndices)
			}
			return false
		})
	}
	return symbols
}

// processCompositeLit extracts default values from a composite literal expression
// and applies them to matching struct symbols. Handles &T{}, T{}, and *T{}.
// Recurses into nested composite literals to populate defaults on sub-config structs.
func processCompositeLit(fset *token.FileSet, expr ast.Expr, symbols []Symbol, structIndices map[string][]int) {
	// Unwrap &T{} → T{}
	if ue, ok := expr.(*ast.UnaryExpr); ok && ue.Op == token.AND {
		expr = ue.X
	}
	cl, ok := expr.(*ast.CompositeLit)
	if !ok {
		return
	}

	typeName := compositeLitTypeName(cl)
	if typeName == "" {
		return
	}

	indices := structIndices[typeName]

	// Extract field → value from keyed elements.
	defaults := make(map[string]string)
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		defaults[key.Name] = defaultValueString(fset, kv.Value)

		// Recurse into nested composite literals to populate defaults
		// on the nested struct's fields (e.g. GRPC: GRPCConfig{Port: 50051}).
		processCompositeLit(fset, kv.Value, symbols, structIndices)
	}
	if len(defaults) == 0 {
		return
	}

	// Apply defaults to matching struct symbols (if the type is in this file).
	for _, idx := range indices {
		for i, f := range symbols[idx].Fields {
			if val, ok := defaults[f.Name]; ok && symbols[idx].Fields[i].DefaultValue == "" {
				symbols[idx].Fields[i].DefaultValue = val
			}
		}
	}
}

// compositeLitTypeName extracts the type name from a composite literal's Type field.
// Handles Ident (T{}), StarExpr (*T{}), and SelectorExpr (pkg.T{}).
func compositeLitTypeName(cl *ast.CompositeLit) string {
	if cl.Type == nil {
		return ""
	}
	return typeNameFromExpr(cl.Type)
}

func typeNameFromExpr(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return typeNameFromExpr(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	default:
		return ""
	}
}

// defaultValueString returns a human-readable string for an AST expression used as
// a default value in a composite literal.
func defaultValueString(fset *token.FileSet, expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.BasicLit:
		return v.Value
	case *ast.Ident:
		return v.Name
	case *ast.UnaryExpr:
		if v.Op == token.AND {
			// &Config{...} → recurse into the composite lit
			if cl, ok := v.X.(*ast.CompositeLit); ok {
				name := compositeLitTypeName(cl)
				if name != "" {
					return "&" + name + "{...}"
				}
				return "&{...}"
			}
		}
		if v.Op == token.SUB {
			// -1, -0.5, etc.
			return "-" + defaultValueString(fset, v.X)
		}
		return exprString(fset, expr)
	case *ast.CompositeLit:
		name := compositeLitTypeName(v)
		if name != "" {
			return name + "{...}"
		}
		return "{...}"
	default:
		return exprString(fset, expr)
	}
}

// constValueString prints a const/var RHS expression for use in a signature.
// Long expressions (multi-line, complex composites) collapse to a type-name summary
// so signatures stay one line.
func constValueString(fset *token.FileSet, expr ast.Expr) string {
	const maxLen = 120
	switch v := expr.(type) {
	case *ast.CompositeLit:
		if name := typeNameFromExpr(v.Type); name != "" {
			return name + "{...}"
		}
		return "{...}"
	case *ast.FuncLit:
		return "func{...}"
	}
	s := exprString(fset, expr)
	if strings.ContainsAny(s, "\n") {
		if i := strings.IndexByte(s, '\n'); i > 0 {
			s = s[:i] + "…"
		}
	}
	if len(s) > maxLen {
		s = s[:maxLen-1] + "…"
	}
	return s
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	default:
		return ""
	}
}
