package file

import (
	"fmt"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/storage"
)

// normalizeSelectionPredicate makes the zero value of SelectionPredicate safe.
func normalizeSelectionPredicate(
	p storage.SelectionPredicate,
) (storage.SelectionPredicate, error) {
	if p.Label == nil {
		p.Label = labels.Everything()
	}
	if p.Field == nil {
		p.Field = fields.Everything()
	}

	if !p.Empty() && p.GetAttrs == nil {
		return p, fmt.Errorf("selection predicate has a non-empty selector but GetAttrs is nil")
	}

	return p, nil
}
