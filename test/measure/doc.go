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

// Package measure checks that the report matches what the target actually did.
//
// The tests here are substitution tests: the target is a stand whose behavior
// is set by construction, so the expected numbers are known before the run and
// owe nothing to anyone's idea of how the generator should count. The whole
// path is exercised — scheduler, worker pool, gRPC sender, metrics — because a
// number is only honest end to end.
//
// Real time is what is being measured, so these tests are the one place where
// waiting is not a synchronisation shortcut. Upper bounds on latency carry
// slack for a loaded runner; lower bounds do not, since no scheduling delay
// can make a call finish before the stand answers it.
package measure
