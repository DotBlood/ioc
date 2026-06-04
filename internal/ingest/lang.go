package ingest

import (
	"path/filepath"
	"strings"
)

// Language identifies how a file should be segmented into semantic units for
// chunking. Detection is by extension; unknown types fall back to Generic.
type Language uint8

const (
	Generic  Language = iota // prose/config: split on blank-line paragraphs
	Go                       // top-level func/type/var/const/import
	Python                   // def/class (+ decorators)
	Markdown                 // headings (#)
	JSTS                     // function/class/export/const/...
)

func (l Language) String() string {
	switch l {
	case Go:
		return "go"
	case Python:
		return "python"
	case Markdown:
		return "markdown"
	case JSTS:
		return "jsts"
	default:
		return "generic"
	}
}

// DetectLanguage maps a file path to a Language by extension.
func DetectLanguage(path string) Language {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return Go
	case ".py":
		return Python
	case ".md", ".markdown":
		return Markdown
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return JSTS
	default:
		return Generic
	}
}

// boundaries returns the 0-based line indices at which a new semantic unit
// starts (always excluding 0, which is implicit). The result is ascending and
// unique. For code languages the boundary is lifted up over immediately
// preceding doc-comments / decorators so they stay attached to their unit.
func boundaries(lang Language, lines []string) []int {
	var raw []int
	for i, ln := range lines {
		if isUnitStart(lang, ln) {
			raw = append(raw, i)
		}
	}
	// Lift each code boundary over its preceding comment/decorator block.
	seen := map[int]bool{}
	var out []int
	for _, i := range raw {
		b := liftBoundary(lang, lines, i)
		if b > 0 && !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	return out
}

// isUnitStart reports whether a column-0 line begins a semantic unit.
func isUnitStart(lang Language, ln string) bool {
	switch lang {
	case Go:
		return hasAnyPrefix(ln, "func ", "func(", "type ", "var ", "const ", "import (", "import \"")
	case Python:
		return hasAnyPrefix(ln, "def ", "class ", "async def ")
	case Markdown:
		return strings.HasPrefix(ln, "#")
	case JSTS:
		return hasAnyPrefix(ln, "function ", "class ", "export ", "const ", "let ", "async ",
			"interface ", "type ", "enum ", "abstract ")
	default: // Generic: handled separately (paragraph starts), see paragraphBoundaries.
		return false
	}
}

// liftBoundary moves a code unit's start up over contiguous preceding comment
// and decorator lines (at column 0) so doc-comments/decorators chunk with it.
func liftBoundary(lang Language, lines []string, i int) int {
	pref := commentPrefixes(lang)
	for i > 0 {
		prev := lines[i-1]
		if hasAnyPrefix(prev, pref...) {
			i--
			continue
		}
		break
	}
	return i
}

// commentPrefixes are the column-0 line prefixes that should attach upward to
// the following unit (doc-comments, decorators).
func commentPrefixes(lang Language) []string {
	switch lang {
	case Go:
		return []string{"//"}
	case Python:
		return []string{"@", "#"}
	case JSTS:
		return []string{"//", "/*", "*", "@"}
	default:
		return nil
	}
}

// paragraphBoundaries returns starts of paragraphs (a non-blank line preceded by
// a blank line) — the Generic segmentation.
func paragraphBoundaries(lines []string) []int {
	var out []int
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" && strings.TrimSpace(lines[i-1]) == "" {
			out = append(out, i)
		}
	}
	return out
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
