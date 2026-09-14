// Package req reads the version requirements a release carries: the strings
// in a release's `dependencies` and `requires`, and the ones a host CLI
// passes on the command line.
//
// The registry and lpm share it so a requirement means the same thing on both
// sides. The registry rejects what lpm could not resolve, rather than storing
// it for every client to fail on later.
package req

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Parse reads a requirement the way cargo does and returns what it matches.
//
//	^1.2      >=1.2.0 and <2.0.0
//	1.2       the same; a bare requirement is a caret requirement
//	~1.2      >=1.2.0 and <1.3.0
//	=1.2.3    that version alone
//	>=1, <2   both comparators
//	1.2.*     any patch of 1.2
//	*         any version
//	^1 || ^3  either
func Parse(spec string) (*semver.Constraints, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return nil, fmt.Errorf("a version requirement is required")
	}

	constraint, err := semver.NewConstraint(Normalize(trimmed))
	if err != nil {
		return nil, fmt.Errorf("%q is not a version requirement: %w", spec, err)
	}
	return constraint, nil
}

// Normalize prefixes every bare term with '^'. Semver on its own reads a bare
// `1.2` as `>=1.2.0, <1.3.0`, and cargo reads it as `^1.2`; a package author
// writing `1.2` means the caret. A term that already carries an operator, and
// a wildcard term, both mean what semver says they mean.
func Normalize(spec string) string {
	alternatives := strings.Split(spec, "||")
	for i, alternative := range alternatives {
		terms := strings.Split(alternative, ",")
		for j, term := range terms {
			terms[j] = caret(strings.TrimSpace(term))
		}
		alternatives[i] = strings.Join(terms, ", ")
	}
	return strings.Join(alternatives, " || ")
}

func caret(term string) string {
	if term == "" || term[0] < '0' || term[0] > '9' {
		return term // an operator, or nothing to prefix
	}
	if strings.ContainsAny(term, "xX*") {
		return term // a wildcard already says what it covers
	}
	return "^" + term
}
