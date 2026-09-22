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

package cli

import (
	"strings"
	"testing"
)

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}

func TestRunningStatusSaysWhatIsHappening(t *testing.T) {
	if got := NewText(LangRU).Running(); got != "выполняется нагрузочное тестирование" {
		t.Errorf("Running() = %q", got)
	}
}
