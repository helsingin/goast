package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/helsingin/goast/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CrossReferenceArgs are the input arguments for the cross-reference tool.
type CrossReferenceArgs struct {
	Mode       string `json:"mode" jsonschema:"required,Query mode: 'unimplemented' (structs NOT implementing interface X), 'missing-field' (structs missing field X), 'field-coverage' (partition structs by field presence), 'unimplemented-services' (gRPC services with no concrete impl)"`
	Interface  string `json:"interface,omitempty" jsonschema:"For unimplemented mode: interface name (e.g. Speaker)"`
	Package    string `json:"package,omitempty" jsonschema:"For unimplemented mode: interface's full import path (e.g. github.com/acme/platform/auth)"`
	Field      string `json:"field,omitempty" jsonschema:"For missing-field / field-coverage modes: exact field name to check (e.g. Port)"`
	Repo       string `json:"repo,omitempty" jsonschema:"Filter structs/services to one repository"`
	NameFilter string `json:"name_filter,omitempty" jsonschema:"Substring filter on struct name (e.g. Config to match only *Config structs)"`
}

// RegisterCrossReference registers the cross-reference tool on the server.
func RegisterCrossReference(server *mcp.Server, holder *index.IndexHolder) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "cross-reference",
		Description: `Set operations across code axes — find gaps, missing implementations, and inconsistencies in one call. ` +
			`Modes: "unimplemented" (which structs DON'T implement interface X?), ` +
			`"missing-field" (which structs are missing field X?), ` +
			`"field-coverage" (partition structs by field presence), ` +
			`"unimplemented-services" (which gRPC services have no concrete impl?). ` +
			`Use name_filter to narrow to specific struct patterns (e.g. "Config"). ` +
			`If you've made code changes recently, call reindex first.`,
	}, func(ctx context.Context, req *mcp.CallToolRequest, args CrossReferenceArgs) (*mcp.CallToolResult, any, error) {
		idx := holder.Get()

		var text string
		var isErr bool

		switch args.Mode {
		case "unimplemented":
			text, isErr = handleUnimplemented(idx, args)
		case "missing-field":
			text, isErr = handleMissingField(idx, args)
		case "field-coverage":
			text, isErr = handleFieldCoverage(idx, args)
		case "unimplemented-services":
			text, isErr = handleUnimplementedServices(idx, args)
		default:
			text = fmt.Sprintf("Invalid mode %q: must be \"unimplemented\", \"missing-field\", \"field-coverage\", or \"unimplemented-services\"", args.Mode)
			isErr = true
		}

		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: text},
			},
		}
		if isErr {
			result.IsError = true
		}
		return result, nil, nil
	})
}

// handleUnimplemented finds structs that do NOT implement a given interface.
func handleUnimplemented(idx *index.Index, args CrossReferenceArgs) (string, bool) {
	if args.Interface == "" || args.Package == "" {
		return "unimplemented mode requires both \"interface\" and \"package\" parameters", true
	}

	// Get all structs (full universe).
	allStructs := getAllStructs(idx, args.Repo, args.NameFilter)

	// Get implementations of the interface.
	impls := idx.FindImplementations(args.Package, args.Interface)
	implSet := makeSymbolKeySet(impls)

	// Partition.
	var notImpl, impl []index.Symbol
	for _, s := range allStructs {
		key := s.ImportPath + "\x00" + s.Name
		if implSet[key] {
			impl = append(impl, s)
		} else {
			notImpl = append(notImpl, s)
		}
	}

	var b strings.Builder
	total := len(notImpl) + len(impl)
	fmt.Fprintf(&b, "Types NOT implementing %s (%s): %d of %d structs", args.Interface, args.Package, len(notImpl), total)
	if args.NameFilter != "" {
		fmt.Fprintf(&b, " matching %q", args.NameFilter)
	}
	b.WriteString("\n\n")

	// Minority side (smaller partition) shown in full; majority capped.
	writeSectionCapped(&b, "NOT IMPLEMENTING", notImpl, len(notImpl) > len(impl))
	writeSectionCapped(&b, "IMPLEMENTING", impl, len(impl) > len(notImpl))

	return b.String(), false
}

// handleMissingField finds structs missing a given field, showing field lists for context.
func handleMissingField(idx *index.Index, args CrossReferenceArgs) (string, bool) {
	if args.Field == "" {
		return "missing-field mode requires the \"field\" parameter", true
	}

	allStructs := getAllStructs(idx, args.Repo, args.NameFilter)

	var missing, present []index.Symbol
	presentFields := make(map[string]index.Field) // key: importPath\x00name
	for _, s := range allStructs {
		if f, ok := getField(s, args.Field); ok {
			present = append(present, s)
			presentFields[s.ImportPath+"\x00"+s.Name] = f
		} else {
			missing = append(missing, s)
		}
	}

	var b strings.Builder
	total := len(missing) + len(present)
	fmt.Fprintf(&b, "Structs")
	if args.NameFilter != "" {
		fmt.Fprintf(&b, " matching %q", args.NameFilter)
	}
	fmt.Fprintf(&b, " MISSING field %q: %d of %d", args.Field, len(missing), total)
	b.WriteString("\n\n")

	// Minority side shown in full; majority capped.
	capMissing := len(missing) > len(present)
	capPresent := len(present) > len(missing)

	// MISSING section: show fields for context.
	missingLimit := len(missing)
	if capMissing && missingLimit > sectionCap {
		missingLimit = sectionCap
	}
	b.WriteString(fmt.Sprintf("MISSING (%d):\n", len(missing)))
	for _, s := range missing[:missingLimit] {
		fmt.Fprintf(&b, "  %-18s %s | %s\n", s.Name, s.ImportPath, s.Repo)
		fmt.Fprintf(&b, "    Fields: %s\n", summarizeFields(s.Fields))
	}
	if missingLimit < len(missing) {
		fmt.Fprintf(&b, "  ... and %d more (use repo or name_filter to narrow)\n", len(missing)-missingLimit)
	}
	b.WriteString("\n")

	// PRESENT section: show the matched field with details.
	presentLimit := len(present)
	if capPresent && presentLimit > sectionCap {
		presentLimit = sectionCap
	}
	b.WriteString(fmt.Sprintf("PRESENT (%d):\n", len(present)))
	for _, s := range present[:presentLimit] {
		f := presentFields[s.ImportPath+"\x00"+s.Name]
		fmt.Fprintf(&b, "  %-18s %s\n", s.Name, formatFieldDetail(f))
		fmt.Fprintf(&b, "    %s | %s\n", s.ImportPath, s.Repo)
	}
	if presentLimit < len(present) {
		fmt.Fprintf(&b, "  ... and %d more\n", len(present)-presentLimit)
	}
	b.WriteString("\n")

	return b.String(), false
}

// handleFieldCoverage partitions structs by field presence, showing field details.
func handleFieldCoverage(idx *index.Index, args CrossReferenceArgs) (string, bool) {
	if args.Field == "" {
		return "field-coverage mode requires the \"field\" parameter", true
	}

	allStructs := getAllStructs(idx, args.Repo, args.NameFilter)

	var with, without []index.Symbol
	withFields := make(map[string]index.Field)
	for _, s := range allStructs {
		if f, ok := getField(s, args.Field); ok {
			with = append(with, s)
			withFields[s.ImportPath+"\x00"+s.Name] = f
		} else {
			without = append(without, s)
		}
	}

	var b strings.Builder
	total := len(with) + len(without)
	fmt.Fprintf(&b, "Field %q coverage: %d of %d structs", args.Field, len(with), total)
	if args.NameFilter != "" {
		fmt.Fprintf(&b, " matching %q", args.NameFilter)
	}
	fmt.Fprintf(&b, " have it\n\n")

	// Minority side shown in full; majority capped.
	capWith := len(with) > len(without)
	capWithout := len(without) > len(with)

	// WITH section: show the field details.
	withLimit := len(with)
	if capWith && withLimit > sectionCap {
		withLimit = sectionCap
	}
	b.WriteString(fmt.Sprintf("WITH %q (%d):\n", args.Field, len(with)))
	for _, s := range with[:withLimit] {
		f := withFields[s.ImportPath+"\x00"+s.Name]
		fmt.Fprintf(&b, "  %-18s %s\n", s.Name, formatFieldDetail(f))
		fmt.Fprintf(&b, "    %s | %s\n", s.ImportPath, s.Repo)
	}
	if withLimit < len(with) {
		fmt.Fprintf(&b, "  ... and %d more\n", len(with)-withLimit)
	}
	b.WriteString("\n")

	// WITHOUT section: show fields for context.
	withoutLimit := len(without)
	if capWithout && withoutLimit > sectionCap {
		withoutLimit = sectionCap
	}
	b.WriteString(fmt.Sprintf("WITHOUT %q (%d):\n", args.Field, len(without)))
	for _, s := range without[:withoutLimit] {
		fmt.Fprintf(&b, "  %s\n", s.Name)
		fmt.Fprintf(&b, "    %s | %s\n", s.ImportPath, s.Repo)
		fmt.Fprintf(&b, "    Fields: %s\n", summarizeFields(s.Fields))
	}
	if withoutLimit < len(without) {
		fmt.Fprintf(&b, "  ... and %d more (use repo or name_filter to narrow)\n", len(without)-withoutLimit)
	}
	b.WriteString("\n")

	return b.String(), false
}

// handleUnimplementedServices finds gRPC services with no concrete implementation.
func handleUnimplementedServices(idx *index.Index, args CrossReferenceArgs) (string, bool) {
	services := idx.ListServices(args.Repo)

	var unimpl, impl []index.Symbol
	implTypes := make(map[string][]index.Symbol) // key: importPath\x00name → implementing types
	for _, svc := range services {
		impls := idx.FindImplementations(svc.ImportPath, svc.Name)
		if len(impls) == 0 {
			unimpl = append(unimpl, svc)
		} else {
			impl = append(impl, svc)
			implTypes[svc.ImportPath+"\x00"+svc.Name] = impls
		}
	}

	var b strings.Builder
	total := len(unimpl) + len(impl)
	fmt.Fprintf(&b, "gRPC services WITHOUT concrete implementation: %d of %d", len(unimpl), total)
	if args.Repo != "" {
		fmt.Fprintf(&b, " in %s", args.Repo)
	}
	b.WriteString("\n\n")

	// Minority side shown in full; majority capped.
	capUnimpl := len(unimpl) > len(impl)
	capImpl := len(impl) > len(unimpl)

	unimplLimit := len(unimpl)
	if capUnimpl && unimplLimit > sectionCap {
		unimplLimit = sectionCap
	}
	b.WriteString(fmt.Sprintf("UNIMPLEMENTED (%d):\n", len(unimpl)))
	for _, svc := range unimpl[:unimplLimit] {
		serviceName := strings.TrimSuffix(svc.Name, "ServiceServer")
		fmt.Fprintf(&b, "  %-20s %s | %s\n", serviceName, svc.ImportPath, svc.Repo)
		if len(svc.Methods) > 0 {
			fmt.Fprintf(&b, "    Methods: %s\n", summarizeMethods(svc.Methods))
		}
	}
	if unimplLimit < len(unimpl) {
		fmt.Fprintf(&b, "  ... and %d more\n", len(unimpl)-unimplLimit)
	}
	b.WriteString("\n")

	implLimit := len(impl)
	if capImpl && implLimit > sectionCap {
		implLimit = sectionCap
	}
	b.WriteString(fmt.Sprintf("IMPLEMENTED (%d):\n", len(impl)))
	for _, svc := range impl[:implLimit] {
		serviceName := strings.TrimSuffix(svc.Name, "ServiceServer")
		types := implTypes[svc.ImportPath+"\x00"+svc.Name]
		fmt.Fprintf(&b, "  %-20s %s | %s\n", serviceName, svc.ImportPath, svc.Repo)
		for _, t := range types {
			fmt.Fprintf(&b, "    -> %s (%s)\n", t.Name, t.ImportPath)
		}
	}
	if implLimit < len(impl) {
		fmt.Fprintf(&b, "  ... and %d more\n", len(impl)-implLimit)
	}
	b.WriteString("\n")

	return b.String(), false
}

// getAllStructs returns all exported struct symbols, optionally filtered by repo and name substring.
func getAllStructs(idx *index.Index, repo, nameFilter string) []index.Symbol {
	results := idx.SearchSymbols(index.SearchParams{
		Kind:         "struct",
		Repo:         repo,
		ExportedOnly: true,
		Limit:        10000,
	})
	if nameFilter == "" {
		return results
	}
	filterLower := strings.ToLower(nameFilter)
	var filtered []index.Symbol
	for _, s := range results {
		if strings.Contains(strings.ToLower(s.Name), filterLower) {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// makeSymbolKeySet builds a set of "importPath\x00name" keys from symbols.
func makeSymbolKeySet(symbols []index.Symbol) map[string]bool {
	set := make(map[string]bool, len(symbols))
	for _, s := range symbols {
		set[s.ImportPath+"\x00"+s.Name] = true
	}
	return set
}

// getField returns the named field from a symbol's Fields, if present.
func getField(s index.Symbol, fieldName string) (index.Field, bool) {
	for _, f := range s.Fields {
		if f.Name == fieldName {
			return f, true
		}
	}
	return index.Field{}, false
}

// formatFieldDetail formats a field's type, tag key, and default value.
func formatFieldDetail(f index.Field) string {
	var parts []string
	parts = append(parts, f.Name, f.Type)
	if yamlKey := parseTagKey(f.Tag, "yaml"); yamlKey != "" {
		parts = append(parts, "yaml:"+yamlKey)
	} else if jsonKey := parseTagKey(f.Tag, "json"); jsonKey != "" {
		parts = append(parts, "json:"+jsonKey)
	}
	result := strings.Join(parts, " ")
	if f.DefaultValue != "" {
		result += " [default: " + f.DefaultValue + "]"
	}
	return result
}

// summarizeFields returns a compact comma-separated summary of field names and types.
func summarizeFields(fields []index.Field) string {
	if len(fields) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, f.Name+" "+f.Type)
	}
	return strings.Join(parts, ", ")
}

// summarizeMethods returns a compact comma-separated list of method names,
// excluding mustEmbed* methods.
func summarizeMethods(methods []string) string {
	var names []string
	for _, m := range methods {
		if strings.HasPrefix(m, "mustEmbed") {
			continue
		}
		// Extract method name (first word before parenthesis).
		name := m
		if idx := strings.Index(m, "("); idx > 0 {
			name = m[:idx]
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// sectionCap is the max items to show per section before truncating with "... and N more".
const sectionCap = 30

// writeSectionCapped writes a labeled section of symbols. If cap is true, limits to sectionCap items.
func writeSectionCapped(b *strings.Builder, label string, symbols []index.Symbol, cap bool) {
	limit := len(symbols)
	if cap && limit > sectionCap {
		limit = sectionCap
	}
	fmt.Fprintf(b, "%s (%d):\n", label, len(symbols))
	for _, s := range symbols[:limit] {
		fmt.Fprintf(b, "  %-18s %s | %s\n", s.Name, s.ImportPath, s.Repo)
	}
	if limit < len(symbols) {
		fmt.Fprintf(b, "  ... and %d more (use repo or name_filter to narrow)\n", len(symbols)-limit)
	}
	b.WriteString("\n")
}
