package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserStatusCode_Enum(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "active", UserActive.String())
	assert.Equal(t, "disabled", UserDisabled.String())
	assert.Equal(t, "passwordExpired", UserPasswordExpired.String())
	assert.Equal(t, "passwordNotSet", UserPasswordNotSet.String())
	assert.Equal(t, "locked", UserLocked.String())
	assert.Equal(t, "statusCode(99)", UserStatusCode(99).String())

	// Parse
	code, err := ParseUserStatusCode("active")
	require.NoError(t, err)
	assert.Equal(t, UserActive, code)

	_, err = ParseUserStatusCode("5")
	assert.Error(t, err)

	_, err = ParseUserStatusCode("unknown")
	assert.Error(t, err)

	// JSON Marshal
	bytes, err := json.Marshal(UserActive)
	require.NoError(t, err)
	assert.Equal(t, `"active"`, string(bytes))

	// JSON Unmarshal from String
	var uStatus UserStatusCode
	err = json.Unmarshal([]byte(`"locked"`), &uStatus)
	require.NoError(t, err)
	assert.Equal(t, UserLocked, uStatus)

	// JSON Unmarshal from Int must fail (no legacy numeric fallback)
	err = json.Unmarshal([]byte(`2`), &uStatus)
	assert.Error(t, err)
}

func TestUserType_Enum(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "system", UserTypeSystem.String())
	assert.Equal(t, "app", UserTypeApp.String())

	tp, err := ParseUserType("app")
	require.NoError(t, err)
	assert.Equal(t, UserTypeApp, tp)

	bytes, err := json.Marshal(UserTypeSystem)
	require.NoError(t, err)
	assert.Equal(t, `"system"`, string(bytes))

	var uType UserType
	err = json.Unmarshal([]byte(`"app"`), &uType)
	require.NoError(t, err)
	assert.Equal(t, UserTypeApp, uType)

	// Numeric JSON should be rejected
	err = json.Unmarshal([]byte(`1`), &uType)
	assert.Error(t, err)
}

func TestNodeStatus_Enum(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "online", NodeStatusOnline.String())
	assert.Equal(t, "offline", NodeStatusOffline.String())
	assert.Equal(t, "pending", NodeStatusPending.String())
	assert.Equal(t, "provisioned", NodeStatusProvisioned.String())
	assert.Equal(t, "maintenance", NodeStatusMaintenance.String())

	ns, err := ParseNodeStatus("provisioned")
	require.NoError(t, err)
	assert.Equal(t, NodeStatusProvisioned, ns)

	bytes, err := json.Marshal(NodeStatusOnline)
	require.NoError(t, err)
	assert.Equal(t, `"online"`, string(bytes))

	var nodeSt NodeStatus
	err = json.Unmarshal([]byte(`"disabled"`), &nodeSt)
	require.Error(t, err)

	// Numeric JSON should be rejected
	err = json.Unmarshal([]byte(`2`), &nodeSt)
	assert.Error(t, err)
}

func TestConnectionStatus_And_Source(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "running", ConnectionRunning.String())
	assert.Equal(t, "stopped", ConnectionStopped.String())

	cs, err := ParseConnectionStatus("running")
	require.NoError(t, err)
	assert.Equal(t, ConnectionRunning, cs)

	bytes, err := json.Marshal(ConnectionStopped)
	require.NoError(t, err)
	assert.Equal(t, `"stopped"`, string(bytes))

	var connSt ConnectionStatus
	err = json.Unmarshal([]byte(`"running"`), &connSt)
	require.NoError(t, err)
	assert.Equal(t, ConnectionRunning, connSt)

	// Numeric JSON should be rejected
	err = json.Unmarshal([]byte(`1`), &connSt)
	assert.Error(t, err)

	// Source
	assert.Equal(t, "manual", ConnectionSourceManual.String())
	assert.Equal(t, "discovered", ConnectionSourceDiscovered.String())
	assert.Equal(t, "fixed", ConnectionSourceFixed.String())

	src, err := ParseConnectionSource("discovered")
	require.NoError(t, err)
	assert.Equal(t, ConnectionSourceDiscovered, src)

	src, err = ParseConnectionSource("fixed")
	require.NoError(t, err)
	assert.Equal(t, ConnectionSourceFixed, src)

	var connSrc ConnectionSource
	err = json.Unmarshal([]byte(`"manual"`), &connSrc)
	require.NoError(t, err)
	assert.Equal(t, ConnectionSourceManual, connSrc)

	err = json.Unmarshal([]byte(`"fixed"`), &connSrc)
	require.NoError(t, err)
	assert.Equal(t, ConnectionSourceFixed, connSrc)

	// Numeric JSON should be rejected
	err = json.Unmarshal([]byte(`1`), &connSrc)
	assert.Error(t, err)
}

func TestTicketEnums(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "bug", TicketTypeBug.String())
	assert.Equal(t, "feature", TicketTypeFeature.String())
	assert.Equal(t, "task", TicketTypeTask.String())

	assert.Equal(t, "low", TicketPriorityLow.String())
	assert.Equal(t, "high", TicketPriorityHigh.String())

	assert.Equal(t, "todo", TicketStatusToDo.String())
	assert.Equal(t, "inProgress", TicketStatusInProgress.String())

	var tType TicketType
	require.NoError(t, json.Unmarshal([]byte(`"bug"`), &tType))
	assert.Equal(t, TicketTypeBug, tType)

	var tPri TicketPriority
	require.NoError(t, json.Unmarshal([]byte(`"critical"`), &tPri))
	assert.Equal(t, TicketPriorityCritical, tPri)

	var tSt TicketStatus
	require.NoError(t, json.Unmarshal([]byte(`"inReview"`), &tSt))
	assert.Equal(t, TicketStatusInReview, tSt)

	// Numeric JSON should be rejected
	assert.Error(t, json.Unmarshal([]byte(`0`), &tType))
	assert.Error(t, json.Unmarshal([]byte(`0`), &tPri))
	assert.Error(t, json.Unmarshal([]byte(`0`), &tSt))
}
