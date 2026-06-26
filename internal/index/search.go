package index

import (
	"sort"
	"strings"
)

// SearchParams holds filter parameters for symbol search.
type SearchParams struct {
	Query            string
	Kind             string // func, method, type, const, var, interface, struct
	Repo             string
	Package          string // import path prefix
	ExportedOnly     bool
	Receiver         string
	IncludeGenerated bool
	Limit            int
}

const DefaultLimit = 100

// SearchSymbols finds symbols matching the given search parameters.
func (idx *Index) SearchSymbols(params SearchParams) []Symbol {
	limit := params.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}

	// Parse query for exact match prefix.
	exact := false
	query := params.Query
	if strings.HasPrefix(query, "exact:") {
		exact = true
		query = strings.TrimPrefix(query, "exact:")
	}
	queryLower := strings.ToLower(query)

	// Determine candidate set.
	var candidates []int
	if exact {
		candidates = idx.byNameLower[queryLower]
	} else if params.Repo != "" {
		candidates = idx.byRepo[params.Repo]
	} else if params.Package != "" {
		// Collect all packages matching the prefix.
		for pkg, indices := range idx.byPkg {
			if pkg == params.Package || strings.HasPrefix(pkg, params.Package+"/") {
				candidates = append(candidates, indices...)
			}
		}
	} else {
		// Full scan — build list of all indices.
		candidates = make([]int, len(idx.Symbols))
		for i := range idx.Symbols {
			candidates[i] = i
		}
	}

	// Filter candidates.
	var results []Symbol
	for _, i := range candidates {
		s := idx.Symbols[i]

		// Query filter: match against name, doc, and import path.
		if query != "" {
			if exact {
				if s.Name != query {
					continue
				}
			} else {
				nameLower := strings.ToLower(s.Name)
				nameMatch := strings.Contains(nameLower, queryLower)
				docMatch := strings.Contains(strings.ToLower(s.Doc), queryLower)
				pathMatch := strings.Contains(strings.ToLower(s.ImportPath), queryLower)
				if !nameMatch && !docMatch && !pathMatch {
					continue
				}
			}
		}

		// Kind filter — handle "interface" and "struct" as type+typeKind.
		if params.Kind != "" {
			switch params.Kind {
			case "interface":
				if s.Kind != SymbolType || s.TypeKind != TypeInterface {
					continue
				}
			case "struct":
				if s.Kind != SymbolType || s.TypeKind != TypeStruct {
					continue
				}
			default:
				if string(s.Kind) != params.Kind {
					continue
				}
			}
		}

		// Repo filter (if not already used for candidate set).
		if params.Repo != "" && s.Repo != params.Repo {
			continue
		}

		// Package filter (if not already used for candidate set).
		if params.Package != "" {
			if s.ImportPath != params.Package && !strings.HasPrefix(s.ImportPath, params.Package+"/") {
				continue
			}
		}

		// Exported filter.
		if params.ExportedOnly && !s.Exported {
			continue
		}

		// Receiver filter.
		if params.Receiver != "" && !strings.EqualFold(s.Receiver, params.Receiver) {
			continue
		}

		// Generated filter.
		if !params.IncludeGenerated && s.Generated {
			continue
		}

		results = append(results, s)
	}

	// Sort: exact name match first, name match before doc/path match,
	// exported before unexported, then alphabetical.
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]

		// Exact match on name sorts first.
		aExact := strings.EqualFold(a.Name, query)
		bExact := strings.EqualFold(b.Name, query)
		if aExact != bExact {
			return aExact
		}

		// Name substring match ranks above doc/path-only match.
		if query != "" && !exact {
			aName := strings.Contains(strings.ToLower(a.Name), queryLower)
			bName := strings.Contains(strings.ToLower(b.Name), queryLower)
			if aName != bName {
				return aName
			}
		}

		// Exported before unexported.
		if a.Exported != b.Exported {
			return a.Exported
		}

		// Alphabetical by name.
		return a.Name < b.Name
	})

	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// ListPackages returns packages, optionally filtered by repo and internal visibility.
func (idx *Index) ListPackages(repo string, includeInternal bool) []Package {
	var result []Package
	for _, p := range idx.Packages {
		if repo != "" && p.Repo != repo {
			continue
		}
		if !includeInternal && p.Internal {
			continue
		}
		result = append(result, p)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ImportPath < result[j].ImportPath
	})
	return result
}

// GetSymbol looks up a symbol by import path and name.
// For methods, name should be in the format "Type.Method".
func (idx *Index) GetSymbol(importPath, name string) *Symbol {
	indices, ok := idx.byPkg[importPath]
	if !ok {
		return nil
	}

	// Check for method format: Type.Method
	receiver := ""
	methodName := name
	if parts := strings.SplitN(name, ".", 2); len(parts) == 2 {
		receiver = parts[0]
		methodName = parts[1]
	}

	for _, i := range indices {
		s := &idx.Symbols[i]
		if receiver != "" {
			if s.Name == methodName && s.Receiver == receiver {
				return s
			}
		} else {
			if s.Name == name {
				return s
			}
		}
	}
	return nil
}

// RepoCount returns the number of distinct repos in the index.
func (idx *Index) RepoCount() int {
	return len(idx.byRepo)
}

// FindImplementations returns concrete types that implement the given interface.
func (idx *Index) FindImplementations(importPath, name string) []Symbol {
	// Find the interface symbol index.
	indices, ok := idx.byPkg[importPath]
	if !ok {
		return nil
	}
	ifaceIdx := -1
	for _, i := range indices {
		s := idx.Symbols[i]
		if s.Name == name && s.Kind == SymbolType && s.TypeKind == TypeInterface {
			ifaceIdx = i
			break
		}
	}
	if ifaceIdx < 0 {
		return nil
	}

	typeIndices := idx.implsByIface[ifaceIdx]
	results := make([]Symbol, 0, len(typeIndices))
	for _, ti := range typeIndices {
		results = append(results, idx.Symbols[ti])
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].ImportPath != results[j].ImportPath {
			return results[i].ImportPath < results[j].ImportPath
		}
		return results[i].Name < results[j].Name
	})
	return results
}

// FindInterfaces returns interfaces satisfied by the given type.
func (idx *Index) FindInterfaces(importPath, name string) []Symbol {
	// Find the type symbol index.
	indices, ok := idx.byPkg[importPath]
	if !ok {
		return nil
	}
	typeIdx := -1
	for _, i := range indices {
		s := idx.Symbols[i]
		if s.Name == name && s.Kind == SymbolType && s.TypeKind != TypeInterface {
			typeIdx = i
			break
		}
	}
	if typeIdx < 0 {
		return nil
	}

	ifaceIndices := idx.ifacesByType[typeIdx]
	results := make([]Symbol, 0, len(ifaceIndices))
	for _, ii := range ifaceIndices {
		results = append(results, idx.Symbols[ii])
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].ImportPath != results[j].ImportPath {
			return results[i].ImportPath < results[j].ImportPath
		}
		return results[i].Name < results[j].Name
	})
	return results
}

// ListServices returns gRPC service interfaces (*ServiceServer) from generated files,
// excluding Unsafe* stubs. Optionally filtered by repo.
func (idx *Index) ListServices(repo string) []Symbol {
	var results []Symbol
	for _, s := range idx.Symbols {
		if !s.Generated {
			continue
		}
		if s.Kind != SymbolType || s.TypeKind != TypeInterface {
			continue
		}
		if !strings.HasSuffix(s.Name, "ServiceServer") {
			continue
		}
		if strings.HasPrefix(s.Name, "Unsafe") {
			continue
		}
		if repo != "" && s.Repo != repo {
			continue
		}
		results = append(results, s)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Repo != results[j].Repo {
			return results[i].Repo < results[j].Repo
		}
		return results[i].Name < results[j].Name
	})
	return results
}

// ListDependencies returns cross-repo import dependencies, optionally filtered by repo.
func (idx *Index) ListDependencies(repo string) []RepoDependency {
	if repo == "" {
		result := make([]RepoDependency, len(idx.Dependencies))
		copy(result, idx.Dependencies)
		sort.Slice(result, func(i, j int) bool {
			if result[i].FromRepo != result[j].FromRepo {
				return result[i].FromRepo < result[j].FromRepo
			}
			return result[i].ToRepo < result[j].ToRepo
		})
		return result
	}
	var result []RepoDependency
	for _, d := range idx.Dependencies {
		if d.FromRepo == repo || d.ToRepo == repo {
			result = append(result, d)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].FromRepo != result[j].FromRepo {
			return result[i].FromRepo < result[j].FromRepo
		}
		return result[i].ToRepo < result[j].ToRepo
	})
	return result
}

// GetPackage looks up a package by import path.
func (idx *Index) GetPackage(importPath string) *Package {
	i, ok := idx.pkgByImportPath[importPath]
	if !ok {
		return nil
	}
	return &idx.Packages[i]
}

// FieldSearchParams holds filter parameters for struct field search.
type FieldSearchParams struct {
	Query   string // substring match on field name, type, tag, and doc
	Repo    string
	Package string // import path prefix
	Limit   int
}

// FieldResult represents a struct field match with its parent struct context.
type FieldResult struct {
	StructName        string
	StructImportPath  string
	Repo              string
	FilePath          string
	Line              int
	FieldName         string
	FieldType         string
	FieldTag          string
	FieldDoc          string
	FieldDefaultValue string
}

// SearchFields searches across struct fields in all indexed symbols.
// Unlike SearchSymbols, there is no default limit — all matching fields are returned
// unless an explicit Limit is set. Config fields are a bounded dataset and cross-fleet
// queries need completeness.
func (idx *Index) SearchFields(params FieldSearchParams) []FieldResult {
	queryLower := strings.ToLower(params.Query)

	var results []FieldResult
	for _, s := range idx.Symbols {
		if s.Kind != SymbolType || s.TypeKind != TypeStruct {
			continue
		}
		if len(s.Fields) == 0 {
			continue
		}
		if params.Repo != "" && s.Repo != params.Repo {
			continue
		}
		if params.Package != "" {
			if s.ImportPath != params.Package && !strings.HasPrefix(s.ImportPath, params.Package+"/") {
				continue
			}
		}

		for _, f := range s.Fields {
			if params.Query != "" {
				nameMatch := strings.Contains(strings.ToLower(f.Name), queryLower)
				typeMatch := strings.Contains(strings.ToLower(f.Type), queryLower)
				tagMatch := strings.Contains(strings.ToLower(f.Tag), queryLower)
				docMatch := strings.Contains(strings.ToLower(f.Doc), queryLower)
				if !nameMatch && !typeMatch && !tagMatch && !docMatch {
					continue
				}
			}
			results = append(results, FieldResult{
				StructName:        s.Name,
				StructImportPath:  s.ImportPath,
				Repo:              s.Repo,
				FilePath:          s.FilePath,
				Line:              s.Line,
				FieldName:         f.Name,
				FieldType:         f.Type,
				FieldTag:          f.Tag,
				FieldDoc:          f.Doc,
				FieldDefaultValue: f.DefaultValue,
			})
			if params.Limit > 0 && len(results) >= params.Limit {
				return results
			}
		}
	}
	return results
}
