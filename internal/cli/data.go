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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/yhgrwav/leettest/pkg/config"
	"github.com/yhgrwav/leettest/pkg/descriptor"
	"github.com/yhgrwav/leettest/pkg/engine"
)

// ErrRequestData says a call's data does not fit its method's request message.
var ErrRequestData = errors.New("request data does not fit the method")

// RequestBody builds a call's data into the wire form of message desc.
//
// The data goes YAML → JSON → protojson, so the type rules are protojson's:
// enums by name, well-known types in their JSON form, bytes in base64. No
// number passes through float64 on the way: the YAML decoder keeps integers as
// int64 or uint64 and encoding/json writes them digit for digit, so an ID above
// 2^53 arrives as written instead of rounded.
func RequestBody(desc protoreflect.MessageDescriptor, data any) ([]byte, error) {
	msg := dynamicpb.NewMessage(desc)

	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}

		// Strict: an unknown field is an error, not a silently dropped typo.
		if err := protojson.Unmarshal(raw, msg); err != nil {
			return nil, withProtoNames(err, desc)
		}
	}

	// Deterministic, so every run of the same config sends the same bytes.
	return proto.MarshalOptions{Deterministic: true}.Marshal(msg)
}

// AttachData resolves every method the run will call and fills in the payload
// of the calls that have data. It runs once, before the load: a method the
// target does not serve, or a body that does not fit its message, is a config
// error, and finding it out from a whole run of failures costs the run.
// Without reflection a method that has no data is only reported to warn, since
// it needs no schema to run.
//
// calls are the engine calls built from cfg, in the same order.
func AttachData(
	ctx context.Context, resolver descriptor.Resolver, cfg *config.MasterConfig,
	calls []engine.Call,
) (unchecked []string, err error) {
	var errs []error

	for i := range cfg.Load.Calls {
		call := &cfg.Load.Calls[i]

		method, resolveErr := resolver.Resolve(ctx, call.Method)

		switch {

		case errors.Is(resolveErr, descriptor.ErrReflectionUnsupported) && call.Data == nil:
			unchecked = append(unchecked, call.Method)

			continue
		case errors.Is(resolveErr, descriptor.ErrReflectionUnsupported):
			errs = append(errs, fmt.Errorf("%s: %w: the schema for its data comes from reflection; "+
				"without data the method runs with an empty message", call.Method, resolveErr))

			continue
		case resolveErr != nil:
			// Named here rather than left to the resolver: which method the
			// run cannot make is ours to say, whoever resolves it.
			errs = append(errs, fmt.Errorf("%s: %w", call.Method, resolveErr))

			continue
		}

		if call.Data == nil {
			continue
		}

		body, err := RequestBody(method.Input, call.Data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: %s: %w", ErrRequestData, call.Method, err))

			continue
		}

		calls[i].Payload = body
	}

	return unchecked, errors.Join(errs...)
}

// withProtoNames adds the .proto name of a field protojson names by its JSON
// name: the config is usually written with response_type, and an error about
// responseType reads like a different field.
func withProtoNames(err error, desc protoreflect.MessageDescriptor) error {
	jsonName := fieldInError(err.Error())
	if jsonName == "" {
		return err
	}

	// protojson names the field by JSON name alone: when fields of different
	// messages share it, any one name could send the user to a field that is fine.
	names := protoNames(desc, map[protoreflect.FullName]bool{}, map[string]map[string]bool{})[jsonName]
	if len(names) != 1 {
		return err
	}

	var name string
	for n := range names {
		name = n
	}
	if name == jsonName {
		return err
	}

	return fmt.Errorf("%w (field %s in the .proto)", err, name)
}

// fieldInError returns the JSON name protojson put in the text of an error, or
// "" if it named no field.
//
// protojson spells a field by its JSON name in exactly one message —
// "invalid value for <kind> field <jsonName>: <value>". On an unknown or a
// duplicate field it quotes the key as the config wrote it, and there is
// nothing to translate. The name is read out of that shape rather than looked
// up by substring: JSON names nest (idX inside idXRay), and the rejected value
// printed after the colon can name any field it likes.
func fieldInError(text string) string {
	_, rest, ok := strings.Cut(text, "invalid value for ")
	if !ok {
		return ""
	}

	_, rest, ok = strings.Cut(rest, " field ")
	if !ok {
		return ""
	}

	name, _, ok := strings.Cut(rest, ":")
	if !ok {
		return ""
	}

	return name
}

// protoNames collects, for the JSON name of every field reachable from desc,
// the .proto names spelled that way. seen stops at recursive messages.
func protoNames(desc protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool, names map[string]map[string]bool) map[string]map[string]bool {
	if seen[desc.FullName()] {
		return names
	}
	seen[desc.FullName()] = true

	fields := desc.Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if names[field.JSONName()] == nil {
			names[field.JSONName()] = map[string]bool{}
		}
		names[field.JSONName()][string(field.Name())] = true
		if inner := field.Message(); inner != nil {
			protoNames(inner, seen, names)
		}
	}

	return names
}
