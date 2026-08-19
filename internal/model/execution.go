// noinspection GoMixedReceiver
package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ExecutionStatus int

const (
	ExecutionStatusPending    ExecutionStatus = 1
	ExecutionStatusDispatched ExecutionStatus = 2
	ExecutionStatusSucceeded  ExecutionStatus = 3
	ExecutionStatusFailed     ExecutionStatus = 4
	ExecutionStatusTimedOut   ExecutionStatus = 5
	ExecutionStatusCancelled  ExecutionStatus = 6
)

func (s ExecutionStatus) String() string {
	switch s {
	case ExecutionStatusPending:
		return "pending"
	case ExecutionStatusDispatched:
		return "dispatched"
	case ExecutionStatusSucceeded:
		return "succeeded"
	case ExecutionStatusFailed:
		return "failed"
	case ExecutionStatusTimedOut:
		return "timedOut"
	case ExecutionStatusCancelled:
		return "cancelled"
	default:
		return fmt.Sprintf("executionStatus(%d)", s)
	}
}

func ParseExecutionStatus(s string) (ExecutionStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending":
		return ExecutionStatusPending, nil
	case "dispatched":
		return ExecutionStatusDispatched, nil
	case "succeeded":
		return ExecutionStatusSucceeded, nil
	case "failed":
		return ExecutionStatusFailed, nil
	case "timedout":
		return ExecutionStatusTimedOut, nil
	case "cancelled":
		return ExecutionStatusCancelled, nil
	default:
		return 0, fmt.Errorf("unknown execution status: %q", s)
	}
}

func (s ExecutionStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *ExecutionStatus) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid ExecutionStatus: %s", string(data))
	}
	parsed, err := ParseExecutionStatus(strVal)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

type ExecutionRecord struct {
	ID          uint            `gorm:"primarykey"`
	ExecutionID string          `gorm:"uniqueIndex;not null;type:varchar(128)"`
	ActionName  string          `gorm:"index;type:varchar(64)"`
	TraceID     string          `gorm:"uniqueIndex;not null;type:varchar(128)"`
	ServerNode  string          `gorm:"not null;type:varchar(64)"`
	TargetNode  string          `gorm:"type:varchar(64)"`
	Status      ExecutionStatus `gorm:"not null;index"`
	Success     bool
	ExitCode    int
	SystemError string `gorm:"type:text"`
	FinishedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
