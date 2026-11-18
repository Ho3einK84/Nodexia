// Package humanize contains shared presentation helpers for human-readable
// values shown across Nodexia's server-rendered and notification UIs.
package humanize

import (
	"fmt"
	"strconv"
)

// ByteUnit identifies one step in the base-1024 byte scale. The labels are the
// conventional short UI labels requested by Nodexia; the underlying division
// remains base 1024.
type ByteUnit uint8

const (
	B ByteUnit = iota
	KB
	MB
	GB
	TB
	PB
)

var byteUnitLabels = [...]string{"B", "KB", "MB", "GB", "TB", "PB"}

type bytesOptions struct {
	precision           int
	minimumUnit         ByteUnit
	integerBytes        bool
	formatNonPositive   bool
	nonPositiveFallback string
}

// BytesOption customizes presentation without duplicating the base-1024
// scaling logic at individual call sites.
type BytesOption func(*bytesOptions)

// WithPrecision sets the number of digits shown after the decimal point.
func WithPrecision(precision int) BytesOption {
	return func(options *bytesOptions) {
		if precision < 0 {
			precision = 0
		}
		options.precision = precision
	}
}

// WithMinimumUnit prevents values from being rendered below unit. It is useful
// for views such as system capacity, which have historically always displayed
// totals in GB or TB even when the value is smaller than one GB.
func WithMinimumUnit(unit ByteUnit) BytesOption {
	return func(options *bytesOptions) {
		if unit > PB {
			unit = PB
		}
		options.minimumUnit = unit
	}
}

// WithIntegerBytes renders values that remain in B without a decimal fraction.
func WithIntegerBytes() BytesOption {
	return func(options *bytesOptions) {
		options.integerBytes = true
	}
}

// WithNonPositiveFallback replaces zero and negative values with fallback.
func WithNonPositiveFallback(fallback string) BytesOption {
	return func(options *bytesOptions) {
		options.formatNonPositive = true
		options.nonPositiveFallback = fallback
	}
}

// WithoutNonPositiveFallback formats zero and negative values numerically.
func WithoutNonPositiveFallback() BytesOption {
	return func(options *bytesOptions) {
		options.formatNonPositive = false
	}
}

// Bytes renders a byte count using base-1024 scaling and the labels
// B/KB/MB/GB/TB/PB. By default it uses two decimal places and renders
// non-positive values as "0 B".
func Bytes(value int64, options ...BytesOption) string {
	config := bytesOptions{
		precision:           2,
		formatNonPositive:   true,
		nonPositiveFallback: "0 B",
	}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}

	if value <= 0 && config.formatNonPositive {
		return config.nonPositiveFallback
	}

	unit := config.minimumUnit
	size := float64(value)
	for i := ByteUnit(0); i < unit; i++ {
		size /= 1024
	}
	for size >= 1024 && unit < PB {
		size /= 1024
		unit++
	}

	if unit == B && config.integerBytes {
		return strconv.FormatInt(value, 10) + " B"
	}
	return fmt.Sprintf("%.*f %s", config.precision, size, byteUnitLabels[unit])
}
