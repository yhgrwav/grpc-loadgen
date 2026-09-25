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

import (
	"testing"
	"time"
)

// The names are what scripts read: renaming one after v0.1.0 breaks them.
func TestCategory_NamesAreTheContract(t *testing.T) {
	want := map[Category]string{
		CategoryUnknown:     "unclassified",
		CategorySuccess:     "success",
		CategoryClientFault: "request_error",
		CategoryOverload:    "overload",
		CategoryServerFault: "failure",
		CategoryTimeout:     "timed_out",
		CategoryCutOff:      "cut_off",
		CategoryUnreachable: "unreachable",
		CategoryClientError: "client_error",
		CategoryBadResponse: "bad_response",
		CategoryAborted:     "aborted",
	}

	seen := map[string]Category{}
	for c := CategoryUnknown; c <= CategoryBadResponse; c++ {
		name, ok := want[c]
		if !ok {
			t.Errorf("category %d has no agreed name", c)
			continue
		}
		if got := c.Name(); got != name {
			t.Errorf("%d.Name() = %q, want %q", c, got, name)
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("%q names both %d and %d", name, prev, c)
		}
		seen[name] = c
	}
	if len(want) != int(CategoryBadResponse)+1 {
		t.Errorf("the contract lists %d names for %d categories", len(want), int(CategoryBadResponse)+1)
	}
}

// Whether a category is in sent, failed or not sent. Unreachable has been
// "sent" since the start: the sender tried. client_error follows it: the
// sender tried and its own stack refused. Both are failed.
func TestCategory_EveryCategoryIsInSentAndFailedAsAgreed(t *testing.T) {
	for _, c := range []struct {
		category     Category
		sent, failed int
	}{
		{CategorySuccess, 1, 0},
		{CategoryClientFault, 1, 1},
		{CategoryOverload, 1, 1},
		{CategoryServerFault, 1, 1},
		{CategoryTimeout, 1, 1},
		{CategoryCutOff, 1, 1},
		{CategoryUnreachable, 1, 1},
		{CategoryClientError, 1, 1},
		{CategoryBadResponse, 1, 1},
		{CategoryAborted, 1, 0},
		{CategoryUnknown, 1, 1},
	} {
		report := reportOf([]finished{{method: "a", at: 1500 * time.Millisecond, category: c.category}}, time.Second)
		m := report.Methods[0]
		if m.Sent != c.sent || m.Failed != c.failed || m.NotSent != 0 {
			t.Errorf("%s: sent %d, failed %d, not sent %d; want %d, %d, 0",
				c.category.Name(), m.Sent, m.Failed, m.NotSent, c.sent, c.failed)
		}
	}
}

// Each category lands in its own field of the second it ended in.
func TestCategory_SecondsKeepEveryCategoryApart(t *testing.T) {
	var calls []finished
	for i, c := range []Category{CategoryOverload, CategoryServerFault, CategoryClientError, CategoryBadResponse} {
		for range i + 1 {
			calls = append(calls, finished{method: "a", at: 1500 * time.Millisecond, category: c})
		}
	}
	report := reportOf(calls, time.Second)

	var got Second
	for _, s := range report.Methods[0].Seconds {
		got.Overload += s.Overload
		got.Failure += s.Failure
		got.ClientError += s.ClientError
		got.BadResponse += s.BadResponse
		got.TargetFailed += s.TargetFailed
		got.Unclassified += s.Unclassified
	}
	if got.Overload != 1 || got.Failure != 2 || got.ClientError != 3 || got.BadResponse != 4 || got.Unclassified != 0 {
		t.Errorf("overload %d, failure %d, client error %d, bad response %d, unclassified %d; want 1, 2, 3, 4, 0",
			got.Overload, got.Failure, got.ClientError, got.BadResponse, got.Unclassified)
	}
}

// A bad response was answered: its latency is real. It is kept apart from
// the service time, as refusals are, not dropped like a call cut off.
func TestCategory_ABadResponseHasItsOwnLatency(t *testing.T) {
	report := reportOf([]finished{{method: "a", at: 1500 * time.Millisecond, category: CategoryBadResponse}}, time.Second)

	m := report.Methods[0]
	if m.BadResponse.Count != 1 || !m.BadResponse.P50.Defined {
		t.Errorf("bad response latency: count %d, p50 defined %v; want 1 and defined", m.BadResponse.Count, m.BadResponse.P50.Defined)
	}
	if m.Latencies != 0 {
		t.Errorf("%d observations in the service time, want none: a reply the client refused is not a served call", m.Latencies)
	}
}

// A run whose every measured call of a method failed the same way at any
// rate measured nothing about load there: the request was wrong, the client
// could not send it, or the client refused every reply.
func TestCategory_AMethodThatMeasuredNothingMakesTheRunInvalid(t *testing.T) {
	for _, c := range []struct {
		name  string
		calls []Category
		want  bool
	}{
		{"all request errors", []Category{CategoryClientFault, CategoryClientFault}, true},
		{"all client errors", []Category{CategoryClientError, CategoryClientError}, true},
		{"all bad responses", []Category{CategoryBadResponse, CategoryBadResponse}, true},
		{"a mix of the three", []Category{CategoryClientFault, CategoryClientError, CategoryBadResponse}, true},
		{"one success among them", []Category{CategoryClientError, CategorySuccess}, false},
		{"all overload", []Category{CategoryOverload, CategoryOverload}, false},
		{"all unreachable", []Category{CategoryUnreachable, CategoryUnreachable}, false},
	} {
		var calls []finished
		for _, cat := range c.calls {
			calls = append(calls, finished{method: "a", at: 1500 * time.Millisecond, category: cat})
		}
		if got := reportOf(calls, time.Second).RequestRejected; got != c.want {
			t.Errorf("%s: invalid %v, want %v", c.name, got, c.want)
		}
	}
}
