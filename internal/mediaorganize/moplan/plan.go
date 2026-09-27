package moplan

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ActionKindRelocate         = "relocate"
	ActionKindEnsureDir        = "ensure_dir"
	ActionKindMoveAndRenameDir = "move_and_rename_dir"
	ActionKindDeleteEmptyDir   = "delete_empty_dir"
)

type PlanAction struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	SourceID       string         `json:"source_id,omitempty"`
	SourceName     string         `json:"source_name,omitempty"`
	SourceParentID string         `json:"source_parent_id,omitempty"`
	TargetParentID string         `json:"target_parent_id,omitempty"`
	TargetName     string         `json:"target_name,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	Confidence     float64        `json:"confidence,omitempty"`
	DependsOn      []string       `json:"depends_on,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Status         string         `json:"status,omitempty"`
	Error          string         `json:"error,omitempty"`
	ResolvedID     string         `json:"resolved_id,omitempty"`
	ExecutedAt     string         `json:"executed_at,omitempty"`
}

type Plan struct {
	TaskID         string           `json:"task_id"`
	CreatedAt      string           `json:"created_at"`
	TargetRootID   string           `json:"target_root_id"`
	TargetParentID string           `json:"target_parent_id"`
	Actions        []PlanAction     `json:"actions,omitempty"`
	Skipped        []map[string]any `json:"skipped,omitempty"`
	Diagnostics    map[string]any   `json:"diagnostics,omitempty"`
}

func (p Plan) MarshalJSON() ([]byte, error) {
	type alias Plan
	if p.Actions == nil {
		p.Actions = []PlanAction{}
	}
	if p.Skipped == nil {
		p.Skipped = []map[string]any{}
	}
	if p.Diagnostics == nil {
		p.Diagnostics = map[string]any{}
	}
	return json.Marshal(alias(p))
}

func Parse(data []byte) (*Plan, error) {
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	if plan.Actions == nil {
		plan.Actions = []PlanAction{}
	}
	if plan.Skipped == nil {
		plan.Skipped = []map[string]any{}
	}
	if plan.Diagnostics == nil {
		plan.Diagnostics = map[string]any{}
	}
	NormalizeDiagnostics(plan.Diagnostics)
	return &plan, nil
}

func NormalizeDiagnostics(d map[string]any) {
	if d == nil {
		return
	}
	raw, ok := d["meta_followers"]
	if !ok || raw == nil {
		return
	}
	switch items := raw.(type) {
	case []map[string]any:
		for _, entry := range items {
			normalizeMetaFollowerEntry(entry)
		}
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			normalizeMetaFollowerEntry(entry)
			out = append(out, entry)
		}
		d["meta_followers"] = out
	}
}

func normalizeMetaFollowerEntry(entry map[string]any) {
	if raw, ok := entry["meta_exts"]; ok {
		entry["meta_exts"] = CoerceStringSlice(raw)
	}
	if raw, ok := entry["match_bases"]; ok {
		entry["match_bases"] = CoerceStringSlice(raw)
	}
}

func CoerceStringSlice(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
