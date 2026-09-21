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

package cli_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/yhgrwav/grpc-loadgen/internal/cli"
)

// testMessages builds the messages the hint tests run against. The names are
// the point: idX is a prefix of idXRay, id is spelled the same in JSON, and
// child_item points back at Item. No message in the dependencies has such a
// set, and the project generates no .proto of its own.
func testMessages(t *testing.T) (item, empty protoreflect.MessageDescriptor) {
	t.Helper()

	const (
		str = descriptorpb.FieldDescriptorProto_TYPE_STRING
		i32 = descriptorpb.FieldDescriptorProto_TYPE_INT32
		msg = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	)

	field := func(name string, num int32, kind descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
		f := &descriptorpb.FieldDescriptorProto{
			Name:   proto.String(name),
			Number: proto.Int32(num),
			Type:   kind.Enum(),
			Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		}
		if typeName != "" {
			f.TypeName = proto.String(typeName)
		}

		return f
	}

	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("loadgen/test/item.proto"),
		Package: proto.String("loadgen.test"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Item"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("id", 1, i32, ""),
					field("id_x", 2, str, ""),
					field("id_x_ray", 3, str, ""),
					field("count_total", 4, i32, ""),
					field("note_text", 5, str, ""),
					field("nested_item", 6, msg, ".loadgen.test.Inner"),
					field("child_item", 7, msg, ".loadgen.test.Item"),
				},
			},
			{
				Name:  proto.String("Inner"),
				Field: []*descriptorpb.FieldDescriptorProto{field("deep_value", 1, str, "")},
			},
			{Name: proto.String("Empty")},
		},
	}, nil)
	if err != nil {
		t.Fatalf("build the test file: %v", err)
	}

	return file.Messages().ByName("Item"), file.Messages().ByName("Empty")
}

func TestRequestBody_TheHintNamesTheFieldTheErrorIsAbout(t *testing.T) {
	item, _ := testMessages(t)

	tests := []struct {
		name string
		data map[string]any
		hint string
	}{
		{
			// protojson reports idXRay; idX is a prefix of it and belongs to
			// another field of the same message.
			name: "a name whose prefix is another field's name",
			data: map[string]any{"id_x_ray": 5},
			hint: "id_x_ray",
		},
		{
			name: "the name the config wrote, reported in its JSON form",
			data: map[string]any{"id_x": 5},
			hint: "id_x",
		},
		{
			// protojson prints the rejected value after the field name, so
			// data can name any field it likes.
			name: "a value that names another field",
			data: map[string]any{"count_total": "x field noteText: 1"},
			hint: "count_total",
		},
		{
			name: "a field of a nested message",
			data: map[string]any{"nested_item": map[string]any{"deep_value": 5}},
			hint: "deep_value",
		},
		{
			name: "a field of the message nested in itself",
			data: map[string]any{"child_item": map[string]any{"id_x_ray": 5}},
			hint: "id_x_ray",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cli.RequestBody(item, tt.data)
			if err == nil {
				t.Fatal("RequestBody accepted data that does not fit the message")
			}

			want := "(field " + tt.hint + " in the .proto)"
			if !strings.HasSuffix(err.Error(), want) {
				t.Errorf("error = %q, want it to end with %q", err, want)
			}
		})
	}
}

func TestRequestBody_NoHintWhenThereIsNothingToTranslate(t *testing.T) {
	item, _ := testMessages(t)

	tests := []struct {
		name string
		data map[string]any
	}{
		{
			// id is spelled the same in JSON: the hint would repeat the name
			// the error already carries.
			name: "a field whose JSON name is its .proto name",
			data: map[string]any{"id": "abc"},
		},
		{
			// protojson echoes an unknown key as the config wrote it.
			name: "an unknown field",
			data: map[string]any{"id_ray": 1},
		},
		{
			// deepValue belongs to the nested message, so it is unknown here.
			// Naming its .proto form would send the reader looking for a field
			// this message does not have.
			name: "an unknown field that another message does have",
			data: map[string]any{"deepValue": 1},
		},
		{
			name: "an error that names no field at all",
			data: map[string]any{"nested_item": 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cli.RequestBody(item, tt.data)
			if err == nil {
				t.Fatal("RequestBody accepted data that does not fit the message")
			}
			if strings.Contains(err.Error(), "in the .proto") {
				t.Errorf("error = %q, want no .proto hint in it", err)
			}
		})
	}
}

func TestRequestBody_AValueThatDoesNotEncodeIsReported(t *testing.T) {
	item, _ := testMessages(t)

	// YAML gives no such value, but the body must never come back empty and
	// without an error: an empty message would be sent as if it were the data.
	// The value is what failed, so that is what the error has to say — not the
	// parse error the empty JSON would give protojson further down.
	body, err := cli.RequestBody(item, map[string]any{"id_x": make(chan int)})
	if err == nil {
		t.Fatalf("RequestBody accepted a value it cannot encode, body = %d bytes", len(body))
	}

	var unsupported *json.UnsupportedTypeError
	if !errors.As(err, &unsupported) {
		t.Errorf("error = %q, want it to name the value that does not encode", err)
	}
}

func TestRequestBody_AMessageWithoutFieldsTakesNoData(t *testing.T) {
	_, empty := testMessages(t)

	body, err := cli.RequestBody(empty, nil)
	if err != nil {
		t.Fatalf("empty message with no data: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("body = %d bytes, want an empty message", len(body))
	}

	_, err = cli.RequestBody(empty, map[string]any{"id_x": 1})
	if err == nil {
		t.Fatal("RequestBody accepted data for a message without fields")
	}
	if strings.Contains(err.Error(), "in the .proto") {
		t.Errorf("error = %q, want no .proto hint in it", err)
	}
}
