// Package poker holds the pure planning-poker domain logic: vote scales and
// consensus calculation. It has no dependency on Slack, Linear, or storage so
// it can be tested in isolation.
package poker

import (
	"math"
	"sort"
	"strconv"
)

// Scale is an ordered set of selectable vote labels. Non-numeric labels (such
// as "?") are valid votes but are ignored when computing consensus.
type Scale []string

// Fibonacci is the classic planning-poker scale with an "unsure" option.
var Fibonacci = Scale{"1", "2", "3", "5", "8", "13", "21", "?"}

// Contains reports whether label is a selectable value on the scale.
func (s Scale) Contains(label string) bool {
	for _, v := range s {
		if v == label {
			return true
		}
	}
	return false
}

// numericValues returns the parseable numeric points on the scale, ascending.
func (s Scale) numericValues() []float64 {
	out := make([]float64, 0, len(s))
	for _, v := range s {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			out = append(out, f)
		}
	}
	sort.Float64s(out)
	return out
}

// Consensus computes the recommended estimate from the cast votes. It takes the
// median of the numeric votes and snaps it to the nearest numeric point on the
// scale. Non-numeric votes (e.g. "?") are excluded. ok is false when there are
// no numeric votes to derive an estimate from.
func (s Scale) Consensus(votes map[string]string) (label string, value float64, ok bool) {
	nums := make([]float64, 0, len(votes))
	for _, v := range votes {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			nums = append(nums, f)
		}
	}
	if len(nums) == 0 {
		return "", 0, false
	}
	sort.Float64s(nums)

	var median float64
	mid := len(nums) / 2
	if len(nums)%2 == 1 {
		median = nums[mid]
	} else {
		median = (nums[mid-1] + nums[mid]) / 2
	}

	return s.snap(median)
}

// snap returns the scale point nearest to target. Ties resolve to the lower
// point. It returns ok=false only when the scale has no numeric points.
func (s Scale) snap(target float64) (label string, value float64, ok bool) {
	points := s.numericValues()
	if len(points) == 0 {
		return "", 0, false
	}
	best := points[0]
	bestDist := math.Abs(points[0] - target)
	for _, p := range points[1:] {
		if d := math.Abs(p - target); d < bestDist {
			best, bestDist = p, d
		}
	}
	return strconv.FormatFloat(best, 'f', -1, 64), best, true
}
