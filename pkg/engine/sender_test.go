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

package engine

import "testing"

// Ground: contract — Category.String is exported.
func TestCategoryString(t *testing.T) {
	tests := []struct {
		name string
		cat  Category
		want string
	}{
		{"success", CategorySuccess, "success"},
		{"client fault", CategoryClientFault, "client fault"},
		{"server fault", CategoryServerFault, "server fault"},
		{"timeout", CategoryTimeout, "timeout"},
		{"overload", CategoryOverload, "overload"},
		{"unknown zero value", CategoryUnknown, "unknown"},
		{"unrecognized value", Category(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cat.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
