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

package grpcsender

import "fmt"

// codecName is unique to this package so it cannot collide with a codec
// registered elsewhere in the process.
const codecName = "leettest-raw"

// discarded stands in for a response nobody asked to keep. Handing it to the
// codec instead of a byte slice means the body is never copied: at a few
// thousand requests a second that copy would be the only allocation on the
// path, and it would buy nothing.
type discarded struct{}

// rawCodec moves bytes without touching them. The payload was encoded once
// before the run started, and responses stay undecoded until something needs
// their fields.
type rawCodec struct{}

func (rawCodec) Marshal(v any) ([]byte, error) {
	b, ok := v.(*[]byte)
	if !ok {
		return nil, fmt.Errorf("%s: cannot marshal %T, want *[]byte", codecName, v)
	}

	return *b, nil
}

func (rawCodec) Unmarshal(data []byte, v any) error {
	switch dst := v.(type) {
	case *discarded:
		return nil
	case *[]byte:
		*dst = append((*dst)[:0], data...)

		return nil
	default:
		return fmt.Errorf("%s: cannot unmarshal into %T, want *[]byte", codecName, v)
	}
}

func (rawCodec) Name() string { return codecName }
