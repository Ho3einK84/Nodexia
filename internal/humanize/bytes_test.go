package humanize

import "testing"

func TestBytesUsesBase1024WithShortLabels(t *testing.T) {
	tests := []struct {
		name  string
		value int64
		want  string
	}{
		{name: "zero", value: 0, want: "0 B"},
		{name: "negative", value: -1, want: "0 B"},
		{name: "bytes", value: 512, want: "512.00 B"},
		{name: "kilobytes", value: 1024, want: "1.00 KB"},
		{name: "megabytes", value: 1024 * 1024, want: "1.00 MB"},
		{name: "gigabytes", value: 1024 * 1024 * 1024, want: "1.00 GB"},
		{name: "terabytes", value: 1024 * 1024 * 1024 * 1024, want: "1.00 TB"},
		{name: "petabytes", value: 1024 * 1024 * 1024 * 1024 * 1024, want: "1.00 PB"},
		{name: "fraction", value: 1536, want: "1.50 KB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Bytes(tt.value); got != tt.want {
				t.Fatalf("Bytes(%d) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestBytesPresentationOptions(t *testing.T) {
	tests := []struct {
		name    string
		value   int64
		options []BytesOption
		want    string
	}{
		{
			name:    "one decimal",
			value:   1536,
			options: []BytesOption{WithPrecision(1)},
			want:    "1.5 KB",
		},
		{
			name:    "integer byte count",
			value:   512,
			options: []BytesOption{WithPrecision(1), WithIntegerBytes()},
			want:    "512 B",
		},
		{
			name:    "dash fallback",
			value:   0,
			options: []BytesOption{WithNonPositiveFallback("-")},
			want:    "-",
		},
		{
			name:    "fixed zero",
			value:   0,
			options: []BytesOption{WithoutNonPositiveFallback()},
			want:    "0.00 B",
		},
		{
			name:    "minimum GB",
			value:   2 * 1024 * 1024 * 1024,
			options: []BytesOption{WithMinimumUnit(GB), WithPrecision(1)},
			want:    "2.0 GB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Bytes(tt.value, tt.options...); got != tt.want {
				t.Fatalf("Bytes(%d) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
