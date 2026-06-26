package index

import "strings"

// SymbolKind represents the kind of a symbol.
type SymbolKind string

const (
	SymbolFunc   SymbolKind = "func"
	SymbolMethod SymbolKind = "method"
	SymbolType   SymbolKind = "type"
	SymbolConst  SymbolKind = "const"
	SymbolVar    SymbolKind = "var"
)

// TypeKind represents the underlying kind of a type symbol.
type TypeKind string

const (
	TypeStruct    TypeKind = "struct"
	TypeInterface TypeKind = "interface"
	TypeAlias     TypeKind = "alias"
	TypeOther     TypeKind = "other"
)

// Field represents a struct field.
type Field struct {
	Name         string
	Type         string
	Tag          string
	Doc          string
	DefaultValue string // populated from constructor functions returning composite literals
}

// EmbeddedRef represents an embedded type in a struct or interface declaration.
// ImportPath is resolved from the file's imports at extraction time; if the
// qualifier couldn't be resolved (rare) or the embed is same-package, it holds
// the current type's own import path. Name is always the base type name with
// generics/pointers stripped (e.g. "UnimplementedFooServer").
type EmbeddedRef struct {
	ImportPath string
	Name       string
}

// Symbol represents a Go symbol (function, method, type, const, var).
type Symbol struct {
	Name              string
	Kind              SymbolKind
	Exported          bool
	Repo              string
	ImportPath        string
	PkgName           string
	FilePath          string
	Line              int
	EndLine           int
	Signature         string
	Receiver          string
	TypeKind          TypeKind
	Fields            []Field
	Methods           []string      // interface method signatures (direct only — embeds not flattened here)
	MethodDescriptor  string        // for methods: normalized descriptor like "Speak(string)(error)"
	MethodDescriptors []string      // for interface types: list of direct method descriptors
	Embeds            []EmbeddedRef // for struct/interface types: embedded type refs resolved via file imports
	Doc               string
	DocSummary        string
	Generated         bool
}

// Package represents a Go package.
type Package struct {
	ImportPath  string
	Name        string
	Repo        string
	Dir         string
	Doc         string
	DocSummary  string
	FileCount   int
	SymbolCount int
	Internal    bool
}

// RepoDependency represents a cross-repo import dependency.
type RepoDependency struct {
	FromRepo    string   // repo that imports
	ToRepo      string   // repo being imported
	ImportPaths []string // specific packages imported
}

// Index holds all symbols and packages with lookup maps.
type Index struct {
	Symbols      []Symbol
	Packages     []Package
	Dependencies []RepoDependency
	References   map[string][]Reference // key: "importPath\x00name"

	byNameLower     map[string][]int // lowercase name → symbol indices
	byPkg           map[string][]int // import path → symbol indices
	byRepo          map[string][]int // repo name → symbol indices
	pkgByImportPath map[string]int   // import path → package index
	implsByIface    map[int][]int    // interface symbol index → implementing type symbol indices
	ifacesByType    map[int][]int    // type symbol index → satisfied interface symbol indices
}

// NewIndex builds an Index with lookup maps from the given symbols and packages.
func NewIndex(symbols []Symbol, packages []Package, deps []RepoDependency) *Index {
	idx := &Index{
		Symbols:         symbols,
		Packages:        packages,
		Dependencies:    deps,
		byNameLower:     make(map[string][]int, len(symbols)),
		byPkg:           make(map[string][]int),
		byRepo:          make(map[string][]int),
		pkgByImportPath: make(map[string]int, len(packages)),
	}

	for i, s := range symbols {
		lower := strings.ToLower(s.Name)
		idx.byNameLower[lower] = append(idx.byNameLower[lower], i)
		idx.byPkg[s.ImportPath] = append(idx.byPkg[s.ImportPath], i)
		idx.byRepo[s.Repo] = append(idx.byRepo[s.Repo], i)
	}

	for i, p := range packages {
		idx.pkgByImportPath[p.ImportPath] = i
	}

	idx.buildImplMap()

	return idx
}

// buildImplMap computes which concrete types implement which interfaces by
// comparing method descriptor sets, with embedded types and interfaces
// flattened to fixpoint so promoted methods participate.
func (idx *Index) buildImplMap() {
	idx.implsByIface = make(map[int][]int)
	idx.ifacesByType = make(map[int][]int)

	type methodSet struct {
		typeIdx     int
		isInterface bool
		embeds      []EmbeddedRef
		descriptors map[string]bool
	}
	typeKey := func(importPath, name string) string {
		return importPath + "\x00" + name
	}

	// First pass: one methodSet per type symbol (struct, interface, other).
	sets := make(map[string]*methodSet)
	for i, s := range idx.Symbols {
		if s.Kind != SymbolType {
			continue
		}
		descs := make(map[string]bool, len(s.MethodDescriptors))
		for _, d := range s.MethodDescriptors {
			descs[d] = true
		}
		sets[typeKey(s.ImportPath, s.Name)] = &methodSet{
			typeIdx:     i,
			isInterface: s.TypeKind == TypeInterface,
			embeds:      s.Embeds,
			descriptors: descs,
		}
	}

	// Second pass: attach direct methods to their receiver types.
	for _, s := range idx.Symbols {
		if s.Kind != SymbolMethod || s.MethodDescriptor == "" || s.Receiver == "" {
			continue
		}
		if ts, ok := sets[typeKey(s.ImportPath, s.Receiver)]; ok {
			ts.descriptors[s.MethodDescriptor] = true
		}
	}

	// Resolve an EmbeddedRef to a key in `sets`. An empty ImportPath on the
	// ref means the qualifier didn't resolve at extraction time; as a
	// best-effort fallback, match any single type with that Name.
	resolve := func(ref EmbeddedRef) *methodSet {
		if ref.ImportPath != "" {
			if ts, ok := sets[typeKey(ref.ImportPath, ref.Name)]; ok {
				return ts
			}
			return nil
		}
		var found *methodSet
		for _, ts := range sets {
			if idx.Symbols[ts.typeIdx].Name == ref.Name {
				if found != nil {
					return nil // ambiguous by name — skip
				}
				found = ts
			}
		}
		return found
	}

	// Flatten embeds to fixpoint. Each iteration unions in embedded method
	// descriptors; loop until no set grows. Sets are monotone and bounded,
	// so this always terminates.
	for {
		changed := false
		for _, ts := range sets {
			for _, ref := range ts.embeds {
				other := resolve(ref)
				if other == nil || other == ts {
					continue
				}
				for d := range other.descriptors {
					if !ts.descriptors[d] {
						ts.descriptors[d] = true
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}

	// Match: for each interface with at least one method, find concrete
	// types (non-interfaces) whose flattened method set covers the
	// interface's flattened set.
	for _, iface := range sets {
		if !iface.isInterface || len(iface.descriptors) == 0 {
			continue
		}
		for _, ts := range sets {
			if ts.isInterface || ts == iface {
				continue
			}
			if len(ts.descriptors) < len(iface.descriptors) {
				continue
			}
			match := true
			for d := range iface.descriptors {
				if !ts.descriptors[d] {
					match = false
					break
				}
			}
			if match {
				idx.implsByIface[iface.typeIdx] = append(idx.implsByIface[iface.typeIdx], ts.typeIdx)
				idx.ifacesByType[ts.typeIdx] = append(idx.ifacesByType[ts.typeIdx], iface.typeIdx)
			}
		}
	}
}
