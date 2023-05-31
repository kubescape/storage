package apiserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
)

type stubSerializer struct {
	supportedTypes []runtime.SerializerInfo
}

func newStubSerializer(supportedTypes []runtime.SerializerInfo) *stubSerializer {
	return &stubSerializer{supportedTypes: supportedTypes}
}

func (s *stubSerializer) SupportedMediaTypes() []runtime.SerializerInfo {
	return s.supportedTypes
}

func (s *stubSerializer) EncoderForVersion(serializer runtime.Encoder, gv runtime.GroupVersioner) runtime.Encoder {
	return nil
}

func (s *stubSerializer) DecoderToVersion(serializer runtime.Decoder, gv runtime.GroupVersioner) runtime.Decoder {
	return nil
}

func TestNoProtobufSerializerSupportedMediaTypes(t *testing.T) {
	tt := []struct {
		name                 string
		originalContentTypes []runtime.SerializerInfo
		wantContentTypes     []runtime.SerializerInfo
		wantStrictlyEqual    bool
	}{
		{
			name: "Wrapping an original Protobuf-only serializer should return no supported types",
			originalContentTypes: []runtime.SerializerInfo{
				{MediaType: runtime.ContentTypeProtobuf},
			},
			wantContentTypes: []runtime.SerializerInfo{},
		},
		{
			name: "Wrapping an original serializer should return original types",
			originalContentTypes: []runtime.SerializerInfo{
				{MediaType: runtime.ContentTypeProtobuf},
				{MediaType: runtime.ContentTypeJSON},
				{MediaType: runtime.ContentTypeYAML},
			},
			wantContentTypes: []runtime.SerializerInfo{
				{MediaType: runtime.ContentTypeJSON},
				{MediaType: runtime.ContentTypeYAML},
			},
		},
		{
			name: "Wrapping an original serializer with no Protobuf returns a slice matching the original",
			originalContentTypes: []runtime.SerializerInfo{
				{MediaType: runtime.ContentTypeJSON},
				{MediaType: runtime.ContentTypeYAML},
			},
			wantContentTypes: []runtime.SerializerInfo{
				{MediaType: runtime.ContentTypeJSON},
				{MediaType: runtime.ContentTypeYAML},
			},
			wantStrictlyEqual: false,
		},
		{
			name:                 "Wrapping an original serializer with no supported types returns an empty slice",
			originalContentTypes: []runtime.SerializerInfo{},
			wantContentTypes:     []runtime.SerializerInfo{},
			wantStrictlyEqual:    true,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			originalSerializer := newStubSerializer(tc.originalContentTypes)
			s := NewNoProtobufSerializer(originalSerializer)

			got := s.SupportedMediaTypes()

			assert.ElementsMatch(t, tc.wantContentTypes, got)
			if tc.wantStrictlyEqual {
				assert.Equal(t, tc.wantContentTypes, got)
			}
		})
	}
}
