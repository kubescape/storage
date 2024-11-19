package file

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeflateSortString(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "nil",
		},
		{
			name: "empty",
			in:   []string{},
			want: []string{},
		},
		{
			name: "single",
			in:   []string{"a"},
			want: []string{"a"},
		},
		{
			name: "single duplicates",
			in:   []string{"a", "a", "a"},
			want: []string{"a"},
		},
		{
			name: "multiple",
			in:   []string{"c", "a", "b"},
			want: []string{"a", "b", "c"},
		},
		{
			name: "multiple duplicates",
			in:   []string{"a", "c", "a", "b", "c", "b", "a"},
			want: []string{"a", "b", "c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, DeflateSortString(tt.in), "DeflateSortString(%v)", tt.in)
		})
	}
}
