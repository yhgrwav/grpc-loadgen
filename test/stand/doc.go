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

// Package stand is a gRPC target whose behavior is set in advance.
//
// A stand told to hold every call for 200ms does not state an opinion about
// the right answer, it creates one: a tool reporting 5ms for that call is
// wrong, and nobody's judgement takes part in the conclusion. Tests that
// compare a report against a stand therefore check the tool, not the author's
// idea of how the tool should count.
//
// The stand knows nothing about the load generator — only the gRPC contract
// it serves, the health service — so it can be pointed at by another tool for
// a cross-check.
package stand
