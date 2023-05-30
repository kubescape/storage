package apiserver

import (
	"k8s.io/apimachinery/pkg/runtime"
)

// NewNoProtobufSerializer returns a decorated originalSerializer that will reject the Protobuf content type.
func NewNoProtobufSerializer(originalSerializer runtime.NegotiatedSerializer) runtime.NegotiatedSerializer {
	return noProtobufNegotiatedSerializer{NegotiatedSerializer: originalSerializer}
}

// noProtobufNegotiatedSerializer is a negotiated seriazlier that rejects the Protobuf content type
type noProtobufNegotiatedSerializer struct {
	runtime.NegotiatedSerializer
}

func (s noProtobufNegotiatedSerializer) SupportedMediaTypes() []runtime.SerializerInfo {
	base := s.NegotiatedSerializer.SupportedMediaTypes()
	filtered := []runtime.SerializerInfo{}
	for _, info := range base {
		if info.MediaType != runtime.ContentTypeProtobuf {
			filtered = append(filtered, info)
		}
	}
	return filtered
}
