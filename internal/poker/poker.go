// Package poker holds the pure planning-poker domain logic: the vote scale and
// consensus calculation. It has no dependency on Slack, Linear, or storage so
// it can be tested in isolation.
//
// A scale is a list of points. Each point has a display Label (what voters see,
// e.g. "5" or "M") and a numeric Value (what gets written to Linear). Scales are
// derived per-team from Linear's estimation settings rather than hard-coded, so
// a team using Fibonacci, exponential, linear, or T-shirt sizing all work.
package poker

import (
	"fmt"
	"strconv"
)

// Point is a single selectable estimate on a scale.
type Point struct {
	Label string  `json:"label"` // display label, e.g. "5" or "M"
	Value float64 `json:"value"` // numeric estimate written to Linear
}

// Scale is an ordered set of estimate points.
type Scale []Point

// Linear estimation type identifiers, as returned by the GraphQL API's
// Team.issueEstimationType field.
const (
	TypeExponential = "exponential"
	TypeFibonacci   = "fibonacci"
	TypeLinear      = "linear"
	TypeTShirt      = "tShirt"
	TypeNotUsed     = "notUsed"
)

// ScaleFor builds the estimate scale for a Linear team from its estimation
// settings. T-shirt sizes map to the Fibonacci numbers (per Linear). An error
// is returned when estimation is disabled for the team.
func ScaleFor(estimationType string, extended, allowZero bool) (Scale, error) {
	var labels []string // nil for numeric scales (label derived from value)
	var values []float64

	switch estimationType {
	case TypeExponential:
		values = []float64{1, 2, 4, 8, 16}
		if extended {
			values = append(values, 32, 64)
		}
	case TypeFibonacci:
		values = []float64{1, 2, 3, 5, 8}
		if extended {
			values = append(values, 13, 21)
		}
	case TypeLinear:
		values = []float64{1, 2, 3, 4, 5}
		if extended {
			values = append(values, 6, 7)
		}
	case TypeTShirt:
		labels = []string{"XS", "S", "M", "L", "XL"}
		values = []float64{1, 2, 3, 5, 8}
		if extended {
			labels = append(labels, "XXL", "XXXL")
			values = append(values, 13, 21)
		}
	case "", TypeNotUsed:
		return nil, fmt.Errorf("estimation is not enabled for this team")
	default:
		return nil, fmt.Errorf("unknown Linear estimation type %q", estimationType)
	}

	scale := make(Scale, 0, len(values)+1)
	if allowZero {
		scale = append(scale, Point{Label: "0", Value: 0})
	}
	for i, v := range values {
		label := strconv.FormatFloat(v, 'f', -1, 64)
		if labels != nil {
			label = labels[i]
		}
		scale = append(scale, Point{Label: label, Value: v})
	}
	return scale, nil
}

// Labels returns the display labels in scale order.
func (s Scale) Labels() []string {
	out := make([]string, len(s))
	for i, p := range s {
		out[i] = p.Label
	}
	return out
}

// Find returns the point with the given label.
func (s Scale) Find(label string) (Point, bool) {
	for _, p := range s {
		if p.Label == label {
			return p, true
		}
	}
	return Point{}, false
}

// Contains reports whether label is a selectable point on the scale.
func (s Scale) Contains(label string) bool {
	_, ok := s.Find(label)
	return ok
}

// Consensus computes the recommended estimate from the cast votes: the mode
// (the most-voted point). This always resolves to a value someone actually
// cast, unlike a median which can land on a value nobody voted for once
// snapped to the nearest point on a non-linear scale. Ties (including "every
// vote is different") break toward the higher point, so the tool overestimates
// rather than under. ok is false when no votes map onto the scale.
func (s Scale) Consensus(votes map[string]string) (Point, bool) {
	counts := make(map[string]int, len(votes))
	for _, label := range votes {
		if _, ok := s.Find(label); ok {
			counts[label]++
		}
	}
	if len(counts) == 0 {
		return Point{}, false
	}

	var best Point
	bestCount := -1
	for label, count := range counts {
		p, _ := s.Find(label)
		if count > bestCount || (count == bestCount && p.Value > best.Value) {
			best, bestCount = p, count
		}
	}
	return best, true
}
