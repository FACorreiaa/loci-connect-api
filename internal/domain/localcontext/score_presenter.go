package localcontext

import (
	lcv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/localcontext"
)

// ToGoScoreProto converts a scored verdict to its wire form.
//
// It lives beside the scorer rather than in a per-caller presenter because two
// services return the same score — CompareService on each column, and
// LocalContextService from GetGoScore — and they must not drift.
func ToGoScoreProto(s GoScore) *lcv1.GoScore {
	out := &lcv1.GoScore{
		Score:              int32(s.Score),
		Verdict:            s.Verdict,
		Summary:            s.Summary,
		HasEstimatedInputs: s.HasEstimatedInputs,
	}
	for _, f := range s.Factors {
		out.Factors = append(out.Factors, &lcv1.ScoreFactor{
			Label:           f.Label,
			Contribution:    int32(f.Contribution),
			MaxContribution: int32(f.MaxContribution),
			Detail:          f.Detail,
		})
	}
	return out
}
