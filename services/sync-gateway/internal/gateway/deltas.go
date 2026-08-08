package gateway

import (
	"context"
	"encoding/json"
	"fmt"

	syncv1 "github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1"
)

// defaultPullDeltasLimit caps how many mutations one PullDeltas call
// returns. A caller that needs more pages again with since set to the
// revision this call returned, following HasMore.
const defaultPullDeltasLimit = 500

// PullDeltas returns every mutation applied in workspaceID with a revision
// (sequence number) greater than since, ordered by revision, along with the
// revision cursor to resume from and whether more pages remain. This is the
// authoritative recovery path (spec/brief: "the socket is a latency
// optimization over the pull, never the only path") — the WebSocket fan-out
// in internal/realtime calls this same method rather than duplicating the
// decode logic.
func (g *Gateway) PullDeltas(ctx context.Context, workspaceID string, since int64, limit int32) (mutations []*syncv1.Mutation, revision int64, hasMore bool, captures []*syncv1.CaptureSyncState, err error) {
	if limit <= 0 {
		limit = defaultPullDeltasLimit
	}

	var rows []DeltaMutation
	var states []*syncv1.CaptureSyncState
	err = g.WithinTx(ctx, func(ctx context.Context, s Store) error {
		var e error
		rows, e = s.PullMutationsSince(ctx, workspaceID, since, limit+1)
		if e != nil {
			return e
		}
		if int32(len(rows)) > limit { //nolint:gosec // len(rows) <= limit+1, itself an int32 argument; never near MaxInt32
			hasMore = true
			rows = rows[:limit]
		}
		// One CaptureSyncState per distinct capture touched in this page,
		// in first-seen order — the only channel manifest_complete has to
		// reach the client (see Store.GetCaptureManifestState).
		seen := make(map[string]bool, len(rows))
		for _, row := range rows {
			if seen[row.CaptureID] {
				continue
			}
			seen[row.CaptureID] = true
			complete, rev, found, e := s.GetCaptureManifestState(ctx, row.CaptureID)
			if e != nil {
				return e
			}
			if !found {
				continue
			}
			states = append(states, &syncv1.CaptureSyncState{
				CaptureId:        row.CaptureID,
				ManifestComplete: complete,
				Revision:         rev,
			})
		}
		return nil
	})
	if err != nil {
		return nil, since, false, nil, fmt.Errorf("gateway: pull deltas: %w", err)
	}

	revision = since
	mutations = make([]*syncv1.Mutation, 0, len(rows))
	for _, row := range rows {
		m, decErr := decodePayload(row.Op, row.Payload)
		if decErr != nil {
			return nil, since, false, nil, fmt.Errorf("gateway: decode delta %s: %w", row.ID, decErr)
		}
		m.Id = row.ID
		m.CaptureId = row.CaptureID
		m.ClientT = row.ClientT
		mutations = append(mutations, m)
		revision = row.Seq
	}
	return mutations, revision, hasMore, states, nil
}

// decodePayload reverses decodeOp/opName: given the op name and payload
// bytes gateway.go wrote at apply time, it reconstructs the Mutation's
// oneof. Id/CaptureId/ClientT are filled in by the caller from the delta
// log row, not here.
func decodePayload(op string, payload []byte) (*syncv1.Mutation, error) {
	switch op {
	case "set_title":
		var v string
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("unmarshal set_title: %w", err)
		}
		return &syncv1.Mutation{Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: v}}}, nil
	case "set_summary":
		var v string
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("unmarshal set_summary: %w", err)
		}
		return &syncv1.Mutation{Op: &syncv1.Mutation_SetSummary{SetSummary: &syncv1.SetSummary{Summary: v}}}, nil
	case "set_tags":
		var v []string
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("unmarshal set_tags: %w", err)
		}
		return &syncv1.Mutation{Op: &syncv1.Mutation_SetTags{SetTags: &syncv1.SetTags{Tags: v}}}, nil
	case "assign":
		var v string
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("unmarshal assign: %w", err)
		}
		return &syncv1.Mutation{Op: &syncv1.Mutation_Assign{Assign: &syncv1.Assign{AssigneeUserId: v}}}, nil
	case "append_comment":
		var v commentValue
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("unmarshal append_comment: %w", err)
		}
		return &syncv1.Mutation{Op: &syncv1.Mutation_AppendComment{AppendComment: &syncv1.AppendComment{
			CommentId: v.ID,
			Body:      v.Body,
		}}}, nil
	default:
		return nil, fmt.Errorf("unknown mutation op %q", op)
	}
}
