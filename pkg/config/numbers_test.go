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

package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/yhgrwav/leettest/pkg/config"
)

const numbersHead = `app:
  target:
    ip: localhost
    port: 50051
load:
  calls:
    - method: wallet.v1.WalletService/GetBalance
      rps: 10
      duration: 1s
      data:
        account_id: `

// The YAML decoder reads 0123 as octal 83 and 08 as the string "08"; both
// reach the target as numbers the user did not write, without an error.
// Ground: contract — pkg/config is a library API; a number read other than written reaches the
// target silently.
func TestParse_LeadingZeroNumberIsRejected(t *testing.T) {
	for _, value := range []string{"0123", "-012", "+07", "08", "00", "0_1"} {
		t.Run(value, func(t *testing.T) {
			_, err := config.Parse([]byte(numbersHead + value + "\n"))

			if !errors.Is(err, config.ErrAmbiguousNumber) {
				t.Fatalf("Parse(%s) error = %v, want ErrAmbiguousNumber", value, err)
			}
			if !strings.Contains(err.Error(), value) || !strings.Contains(err.Error(), "line 11") {
				t.Errorf("error %q must name the value %s and line 11", err, value)
			}
		})
	}
}

// Ground: contract — pkg/config is a library API; a number read other than written reaches the
// target silently.
func TestParse_UnambiguousNumbersAreAccepted(t *testing.T) {
	for _, value := range []string{"0", "-0", "123", "0.5", "0x1F", `"0123"`, "'08'", "1_000"} {
		t.Run(value, func(t *testing.T) {
			if _, err := config.Parse([]byte(numbersHead + value + "\n")); err != nil {
				t.Fatalf("Parse(%s) error = %v, want nil", value, err)
			}
		})
	}
}

// Ground: contract — pkg/config is a library API; a number read other than written reaches the
// target silently.
func TestParse_EveryAmbiguousNumberIsReported(t *testing.T) {
	raw := numbersHead + "0123\n        wallet_id: 007\n"

	_, err := config.Parse([]byte(raw))

	if err == nil || !strings.Contains(err.Error(), "0123") || !strings.Contains(err.Error(), "007") {
		t.Fatalf("error %v must name both 0123 and 007", err)
	}
}
