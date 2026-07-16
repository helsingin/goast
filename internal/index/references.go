package index

import (
	"go/ast"
	"go/token"
	"sort"
	"strings"
)

// Reference records a site where an indexed symbol is referenced (called or
// used by name). References are resolved: every Reference corresponds to a
// known entry in Index.Symbols.
type Reference struct {
	FromSymbol int    // index into Index.Symbols — the containing function/method
	FilePath   string // file where the reference occurs
	Line       int    // 1-based line number
	Column     int    // 1-based column; preserves distinct same-line references
}

// rawReference is an unresolved reference collected during parsing. It is
// resolved to a Reference (with a symbol index) after the full index is built.
type rawReference struct {
	fromImportPath string
	fromName       string // "Foo" for funcs, "Recv.Method" for methods
	targetPath     string // "" means same-package as caller
	targetReceiver string // non-empty for a receiver-resolved method reference
	targetName     string
	filePath       string
	line           int
	column         int
}

// goBuiltins are predeclared identifiers we exclude from reference collection
// since they don't correspond to indexed symbols.
var goBuiltins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
	// Predeclared type conversions that show up as call expressions.
	"bool": true, "byte": true, "rune": true, "string": true, "error": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "float32": true, "float64": true, "complex64": true,
	"complex128": true, "any": true, "comparable": true,
	"true": true, "false": true, "nil": true, "iota": true,
}

// collectReferences walks every function/method body in the file and emits
// raw references for:
//   - unqualified function calls (`Foo()`)
//   - qualified calls and value refs whose qualifier matches a file-level
//     import alias (`pkg.Foo`, `pkg.Foo()`)
//
// Receiver selections are collected separately by collectTypedMethodReferences;
// this syntax-only pass deliberately leaves them alone rather than guessing.
func collectReferences(fset *token.FileSet, file *ast.File, importPath string, aliasMap map[string]string) []rawReference {
	var refs []rawReference

	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}

		fromName := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			recv := receiverTypeName(fd.Recv.List[0].Type)
			if recv != "" {
				fromName = recv + "." + fd.Name.Name
			}
		}

		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok {
					if id.Name != "" && !goBuiltins[id.Name] {
						refs = append(refs, rawReference{
							fromImportPath: importPath,
							fromName:       fromName,
							targetPath:     "", // same-package
							targetName:     id.Name,
							filePath:       file.Name.Name, // placeholder; replaced below
							line:           fset.Position(id.Pos()).Line,
							column:         fset.Position(id.Pos()).Column,
						})
					}
				}
			case *ast.SelectorExpr:
				id, ok := x.X.(*ast.Ident)
				if !ok {
					return true
				}
				path, ok := aliasMap[id.Name]
				if !ok {
					return true // not a package qualifier — probably field access
				}
				refs = append(refs, rawReference{
					fromImportPath: importPath,
					fromName:       fromName,
					targetPath:     path,
					targetName:     x.Sel.Name,
					filePath:       file.Name.Name, // placeholder; replaced below
					line:           fset.Position(x.Sel.Pos()).Line,
					column:         fset.Position(x.Sel.Pos()).Column,
				})
			}
			return true
		})
	}

	// Fill in the concrete file path (token.Position.Filename) on each ref
	// using the first declaration's position.
	if len(refs) > 0 && file.Pos().IsValid() {
		filename := fset.Position(file.Pos()).Filename
		for i := range refs {
			refs[i].filePath = filename
		}
	}

	return refs
}

// buildReferences resolves raw references into Index.References. A reference
// is kept only if both its caller and its target resolve to indexed symbols;
// stdlib and third-party references are silently dropped.
func (idx *Index) buildReferences(raw []rawReference) {
	if idx.References == nil {
		idx.References = make(map[string][]Reference)
	}

	for _, r := range raw {
		targetPath := r.targetPath
		if targetPath == "" {
			targetPath = r.fromImportPath
		}

		targetName := r.targetName
		if r.targetReceiver != "" {
			targetName = r.targetReceiver + "." + targetName
		}

		// Look up the exact target. A method requires Receiver.Method; an
		// unqualified name deliberately cannot resolve to a same-named method.
		if !idx.hasReferenceTarget(targetPath, targetName) {
			continue
		}

		// Resolve the caller symbol index.
		fromIdx := idx.lookupReferenceCaller(r.fromImportPath, r.fromName, r.filePath, r.line)
		if fromIdx < 0 {
			continue
		}

		key := targetPath + "\x00" + targetName
		idx.References[key] = append(idx.References[key], Reference{
			FromSymbol: fromIdx,
			FilePath:   r.filePath,
			Line:       r.line,
			Column:     r.column,
		})
	}

	// The type-checker traverses package groups in import order while the
	// syntax collector follows files. Normalize both into stable, duplicate-free
	// reference lists so reindexing produces byte-for-byte deterministic output.
	for key, refs := range idx.References {
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].FilePath != refs[j].FilePath {
				return refs[i].FilePath < refs[j].FilePath
			}
			if refs[i].Line != refs[j].Line {
				return refs[i].Line < refs[j].Line
			}
			if refs[i].Column != refs[j].Column {
				return refs[i].Column < refs[j].Column
			}
			return refs[i].FromSymbol < refs[j].FromSymbol
		})
		out := refs[:0]
		for _, ref := range refs {
			if len(out) > 0 {
				last := out[len(out)-1]
				if ref.FromSymbol == last.FromSymbol && ref.FilePath == last.FilePath && ref.Line == last.Line && ref.Column == last.Column {
					continue
				}
			}
			out = append(out, ref)
		}
		idx.References[key] = out
	}
}

func (idx *Index) lookupReferenceCaller(importPath, name, filePath string, line int) int {
	indices, ok := idx.byPkg[importPath]
	if !ok {
		return -1
	}
	receiver, symbolName := "", name
	if parts := strings.SplitN(name, ".", 2); len(parts) == 2 {
		receiver, symbolName = parts[0], parts[1]
	}
	fallback := -1
	for _, symbolIndex := range indices {
		symbol := idx.Symbols[symbolIndex]
		if symbol.Name != symbolName || symbol.Receiver != receiver {
			continue
		}
		if (receiver == "" && symbol.Kind != SymbolFunc) || (receiver != "" && symbol.Kind != SymbolMethod) {
			continue
		}
		if fallback < 0 {
			fallback = symbolIndex
		}
		if symbol.FilePath == filePath && line >= symbol.Line && line <= symbol.EndLine {
			return symbolIndex
		}
	}
	return fallback
}

func (idx *Index) hasReferenceTarget(importPath, name string) bool {
	if idx.lookupSymbolIndex(importPath, name) >= 0 {
		return true
	}
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return false
	}
	receiver, method := parts[0], parts[1]
	for _, symbolIndex := range idx.byPkg[importPath] {
		symbol := idx.Symbols[symbolIndex]
		if symbol.Kind != SymbolType || symbol.TypeKind != TypeInterface || symbol.Name != receiver {
			continue
		}
		for _, descriptor := range symbol.MethodDescriptors {
			if strings.HasPrefix(descriptor, method+"(") {
				return true
			}
		}
	}
	return false
}

// lookupSymbolIndex returns the Symbols index of the named symbol in the
// given package, or -1 if not found. Methods are addressed as "Type.Method".
func (idx *Index) lookupSymbolIndex(importPath, name string) int {
	indices, ok := idx.byPkg[importPath]
	if !ok {
		return -1
	}
	receiver, methodName := "", name
	if parts := strings.SplitN(name, ".", 2); len(parts) == 2 {
		receiver = parts[0]
		methodName = parts[1]
	}
	for _, i := range indices {
		s := idx.Symbols[i]
		if receiver != "" {
			if s.Name == methodName && s.Receiver == receiver {
				return i
			}
		} else if s.Name == name && s.Kind != SymbolMethod {
			return i
		}
	}
	return -1
}

// FindReferences returns all known references to the given symbol. Returns
// nil if the symbol has no indexed references (or isn't in the index at all).
// Methods are addressed as "Type.Method".
func (idx *Index) FindReferences(importPath, name string) []Reference {
	key := importPath + "\x00" + name
	refs := idx.References[key]
	if len(refs) == 0 {
		return nil
	}
	out := make([]Reference, len(refs))
	copy(out, refs)
	return out
}
