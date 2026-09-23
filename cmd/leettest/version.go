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

package main

import "runtime/debug"

// version is set by the release build: -ldflags "-X main.version=v0.1.0".
var version string

// versionString names the build: the release's version, else the module
// version go install recorded, else the commit a checkout was built from.
func versionString(release string, info *debug.BuildInfo) string {
	if release != "" {
		return release
	}
	if info == nil {
		return "devel"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	var revision, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}

	if revision == "" {
		return "devel"
	}

	out := "devel " + revision[:min(12, len(revision))]
	if modified == "true" {
		out += "+dirty"
	}

	return out
}
