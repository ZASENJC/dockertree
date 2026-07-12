package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dockertree/internal/core"
	"dockertree/internal/docker"
)

func (s *Server) executeRecorded(ctx context.Context, cmd docker.Command, targetType, targetID, targetName, action string) (docker.Result, error) {
	result, err := s.execute(ctx, cmd)
	result.Command = cmd.RedactedString()
	s.recordOperation(core.OperationRecord{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Timestamp:  time.Now(),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		TargetName: targetName,
		Command:    cmd.RedactedString(),
		Output:     truncateOperationText(result.Output),
		ExitCode:   result.ExitCode,
		Success:    err == nil,
		Error:      truncateOperationText(result.Error),
	})
	return result, err
}

func (s *Server) recordOperation(record core.OperationRecord) {
	if s.operations == nil {
		return
	}
	_ = s.operations.Append(record)
}

func truncateOperationText(value string) string {
	const maxBytes = 4000
	if len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes] + "\n[truncated]"
}

func (s *Server) operationHistory(w http.ResponseWriter, r *http.Request) {
	if s.operations == nil {
		respond(w, []core.OperationRecord{}, nil)
		return
	}
	limit := 100
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 500 {
			badRequest(w, errText("limit must be between 1 and 500"))
			return
		}
		limit = parsed
	}
	failedOnly := false
	if value := strings.TrimSpace(r.URL.Query().Get("failed")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			badRequest(w, errText("failed must be true or false"))
			return
		}
		failedOnly = parsed
	}
	records, err := s.operations.List(limit, strings.TrimSpace(r.URL.Query().Get("targetId")), failedOnly)
	respond(w, records, err)
}

func (s *Server) containerInspect(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		badRequest(w, errText("container id is required"))
		return
	}
	info, err := s.exec.Inspect(r.Context(), id)
	respond(w, info, err)
}
