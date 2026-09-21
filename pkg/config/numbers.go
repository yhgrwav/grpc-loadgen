// Copyright 2026 yhgrwav
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
)

// ErrAmbiguousNumber says an unquoted number starts with a zero.
var ErrAmbiguousNumber = errors.New("number with a leading zero is ambiguous")

var leadingZero = regexp.MustCompile(`^[-+]?0[0-9_]+$`)

// checkNumbers rejects unquoted scalars like 0123 or 08. The YAML decoder
// reads the first as octal 83 and the second as the string "08", and both
// reach the target as a number the user did not write, with no error.
func checkNumbers(raw []byte) error {
	file, err := parser.ParseBytes(raw, 0)
	if err != nil {
		return err
	}

	var v numberVisitor
	for _, doc := range file.Docs {
		ast.Walk(&v, doc)
	}

	return errors.Join(v.errs...)
}

type numberVisitor struct {
	errs []error
}

func (v *numberVisitor) Visit(node ast.Node) ast.Visitor {
	tk := node.GetToken()
	if tk == nil || tk.Type == token.SingleQuoteType || tk.Type == token.DoubleQuoteType {
		return v
	}

	if _, scalar := node.(ast.ScalarNode); scalar && leadingZero.MatchString(tk.Value) {
		v.errs = append(v.errs, fmt.Errorf("line %d: %w: %s — write it without the zero, or quote it as a string",
			tk.Position.Line, ErrAmbiguousNumber, tk.Value))
	}

	return v
}
