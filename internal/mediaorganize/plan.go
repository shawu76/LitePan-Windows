package mediaorganize

import "litepan/internal/mediaorganize/moplan"

const (
	ActionKindRelocate = moplan.ActionKindRelocate
)

type PlanAction = moplan.PlanAction
type Plan = moplan.Plan

func ParsePlan(data []byte) (*Plan, error) {
	return moplan.Parse(data)
}
